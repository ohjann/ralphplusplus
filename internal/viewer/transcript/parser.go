// Package transcript converts a Claude stream-json jsonl log plus the prompt
// file that seeded it into a typed, server-side sequence of Turns.
//
// The Turn/Block schema is intended to be stable wire output (TurnV1) — the
// SPA consumes this via NDJSON, so extending existing fields is allowed but
// renaming or changing their shape is a breaking change.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"time"
)

// Turn is one logical conversation turn reconstructed from the stream.
// StartedAt is a pointer so an unset zero time is omitted from wire output
// (Claude stream-json does not carry per-event timestamps today).
type Turn struct {
	Index      int        `json:"index"`
	Role       string     `json:"role"`
	Blocks     []Block    `json:"blocks"`
	StopReason string     `json:"stop_reason,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
}

// Block is one content block inside a Turn. Kind determines which fields are
// populated: "text" and "thinking" use Text; "tool_use" uses ToolName,
// ToolUseID, Input; "tool_result" uses ToolUseID, Output, IsError.
type Block struct {
	Kind      string          `json:"kind"`
	Text      string          `json:"text,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Usage mirrors the subset of Anthropic's usage object that callers care
// about. Fields are additive as new ones appear; omitempty keeps wire output
// compact.
type Usage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

const scannerMaxBuf = 4 * 1024 * 1024

// ParseFile opens the prompt and jsonl files and returns a lazy sequence of
// Turns. The underlying file handle is closed when iteration ends (either by
// the caller breaking or by reaching EOF).
//
// Turn 0 is synthesised from the prompt file as a single user text block.
// Subsequent turns come from the stream-json events: one assistant turn per
// message_start→message_stop envelope, and one user turn per top-level
// `user` event carrying tool_result payloads.
func ParseFile(promptPath, jsonlPath string) (iter.Seq2[Turn, error], error) {
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read prompt: %w", err)
	}
	if _, err := os.Stat(jsonlPath); err != nil {
		return nil, fmt.Errorf("stat jsonl: %w", err)
	}
	prompt := string(promptBytes)

	return func(yield func(Turn, error) bool) {
		turn0 := Turn{
			Index:  0,
			Role:   "user",
			Blocks: []Block{{Kind: "text", Text: prompt}},
		}
		if !yield(turn0, nil) {
			return
		}

		f, err := os.Open(jsonlPath)
		if err != nil {
			yield(Turn{}, fmt.Errorf("open jsonl: %w", err))
			return
		}
		defer f.Close()

		p := newParser(f, 1)
		p.run(yield)
	}, nil
}

// parser holds the per-iteration state machine. It is not safe for
// concurrent use but one instance is scoped to a single ParseFile call.
type parser struct {
	sc       *bufio.Scanner
	idx      int
	pending  *Turn
	curBlock *Block
	inputBuf strings.Builder
}

func newParser(r io.Reader, startIdx int) *parser {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), scannerMaxBuf)
	return &parser{sc: sc, idx: startIdx}
}

// streamLine is the outer wrapper used by the Claude Code CLI. Only the
// fields we inspect are unmarshaled — everything else is ignored via
// RawMessage indirection.
type streamLine struct {
	Type    string          `json:"type"`
	Event   json.RawMessage `json:"event"`
	Message json.RawMessage `json:"message"`
}

// apiEvent is an unwrapped raw Anthropic API event (the contents of
// streamLine.Event when Type == "stream_event").
type apiEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message"`
	Delta        json.RawMessage `json:"delta"`
	ContentBlock json.RawMessage `json:"content_block"`
	Usage        json.RawMessage `json:"usage"`
}

func (p *parser) run(yield func(Turn, error) bool) {
	for p.sc.Scan() {
		line := p.sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var outer streamLine
		if err := json.Unmarshal(line, &outer); err != nil {
			// Malformed line — skip rather than aborting the stream. A single
			// corrupt line shouldn't poison the rest of the transcript.
			continue
		}

		switch outer.Type {
		case "system", "result", "rate_limit_event", "assistant":
			// Metadata-only or redundant top-level envelopes. Explicitly
			// absorbed per RV-004 acceptance criteria.
			continue
		case "user":
			if t, ok := p.buildUserTurn(outer.Message); ok {
				if !yield(t, nil) {
					return
				}
				p.idx++
			}
			continue
		case "stream_event":
			var ev apiEvent
			if err := json.Unmarshal(outer.Event, &ev); err != nil {
				continue
			}
			if t, emit, stop := p.handleAPIEvent(ev); stop {
				return
			} else if emit {
				if !yield(t, nil) {
					return
				}
				p.idx++
			}
		default:
			// Unknown top-level type — skip without erroring.
			continue
		}
	}

	if err := p.sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		yield(Turn{}, fmt.Errorf("scan transcript: %w", err))
	}
}

// handleAPIEvent advances the state machine for one unwrapped stream event.
// Returns (turn, emit, stop):
//   - emit=true → caller should yield `turn` as the next Turn
//   - stop=true → iteration should halt immediately (unused here but reserved)
func (p *parser) handleAPIEvent(ev apiEvent) (Turn, bool, bool) {
	switch ev.Type {
	case "message_start":
		var msg struct {
			Role  string          `json:"role"`
			Usage json.RawMessage `json:"usage"`
		}
		_ = json.Unmarshal(ev.Message, &msg)
		role := msg.Role
		if role == "" {
			role = "assistant"
		}
		p.pending = &Turn{Index: p.idx, Role: role}
		if u := parseUsage(msg.Usage); u != nil {
			p.pending.Usage = u
		}
		p.curBlock = nil
		p.inputBuf.Reset()

	case "content_block_start":
		if p.pending == nil {
			return Turn{}, false, false
		}
		var cb struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(ev.ContentBlock, &cb)
		p.inputBuf.Reset()
		var nb Block
		switch cb.Type {
		case "text":
			nb = Block{Kind: "text", Text: cb.Text}
		case "thinking":
			nb = Block{Kind: "thinking"}
		case "tool_use":
			nb = Block{Kind: "tool_use", ToolName: cb.Name, ToolUseID: cb.ID}
		default:
			// Unknown block type — don't push a block, and leave curBlock nil
			// so subsequent deltas are ignored.
			p.curBlock = nil
			return Turn{}, false, false
		}
		p.pending.Blocks = append(p.pending.Blocks, nb)
		p.curBlock = &p.pending.Blocks[len(p.pending.Blocks)-1]

	case "content_block_delta":
		if p.pending == nil || p.curBlock == nil {
			return Turn{}, false, false
		}
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		}
		_ = json.Unmarshal(ev.Delta, &d)
		switch d.Type {
		case "text_delta":
			p.curBlock.Text += d.Text
		case "thinking_delta":
			p.curBlock.Text += d.Thinking
		case "input_json_delta":
			p.inputBuf.WriteString(d.PartialJSON)
		}

	case "content_block_stop":
		if p.pending != nil && p.curBlock != nil && p.curBlock.Kind == "tool_use" {
			raw := p.inputBuf.String()
			if strings.TrimSpace(raw) == "" {
				raw = "{}"
			}
			var probe any
			if json.Unmarshal([]byte(raw), &probe) == nil {
				p.curBlock.Input = json.RawMessage(raw)
			} else {
				// Input was truncated or malformed — fall back to empty object
				// so consumers still get valid JSON.
				p.curBlock.Input = json.RawMessage("{}")
			}
		}
		p.inputBuf.Reset()
		p.curBlock = nil

	case "message_delta":
		if p.pending == nil {
			return Turn{}, false, false
		}
		var d struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(ev.Delta, &d)
		if d.StopReason != "" {
			p.pending.StopReason = d.StopReason
		}
		if u := parseUsage(ev.Usage); u != nil {
			if p.pending.Usage != nil {
				// Preserve prefill tokens reported in message_start even if
				// message_delta omits them.
				if u.CacheCreationInputTokens == 0 {
					u.CacheCreationInputTokens = p.pending.Usage.CacheCreationInputTokens
				}
				if u.CacheReadInputTokens == 0 {
					u.CacheReadInputTokens = p.pending.Usage.CacheReadInputTokens
				}
				if u.InputTokens == 0 {
					u.InputTokens = p.pending.Usage.InputTokens
				}
			}
			p.pending.Usage = u
		}

	case "message_stop":
		if p.pending != nil {
			t := *p.pending
			p.pending = nil
			p.curBlock = nil
			p.inputBuf.Reset()
			return t, true, false
		}
	}
	return Turn{}, false, false
}

// buildUserTurn converts a top-level `user` event's message.content into a
// Turn. Returns (turn, true) if the event produced any blocks.
func (p *parser) buildUserTurn(msgRaw json.RawMessage) (Turn, bool) {
	if len(msgRaw) == 0 {
		return Turn{}, false
	}
	var msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil || len(msg.Content) == 0 {
		return Turn{}, false
	}
	turn := Turn{Index: p.idx, Role: "user"}

	var arr []json.RawMessage
	if err := json.Unmarshal(msg.Content, &arr); err != nil {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			turn.Blocks = []Block{{Kind: "text", Text: s}}
			return turn, true
		}
		return Turn{}, false
	}

	for _, bRaw := range arr {
		var b struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if err := json.Unmarshal(bRaw, &b); err != nil {
			continue
		}
		switch b.Type {
		case "tool_result":
			block := Block{Kind: "tool_result", ToolUseID: b.ToolUseID, IsError: b.IsError}
			if len(b.Content) > 0 {
				var s string
				if err := json.Unmarshal(b.Content, &s); err == nil {
					block.Output = s
				} else {
					block.Output = string(compactJSON(b.Content))
				}
			}
			turn.Blocks = append(turn.Blocks, block)
		case "text":
			turn.Blocks = append(turn.Blocks, Block{Kind: "text", Text: b.Text})
		}
	}

	if len(turn.Blocks) == 0 {
		return Turn{}, false
	}
	return turn, true
}

func parseUsage(raw json.RawMessage) *Usage {
	if len(raw) == 0 {
		return nil
	}
	var u struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
		return nil
	}
	return &Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}
