package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

// MemoryRetriever abstracts semantic memory retrieval so callers can pass
// a real ChromaDB+embedder pair or nil to skip retrieval entirely.
type MemoryRetriever interface {
	RetrieveContext(ctx context.Context, storyTitle, storyDescription string, acceptanceCriteria []string, opts memory.RetrievalOptions) (memory.RetrievalResult, error)
}

// BuildPromptOpts holds optional parameters for BuildPrompt.
type BuildPromptOpts struct {
	Memory     MemoryRetriever
	MemoryOpts memory.RetrievalOptions
}

// BuildPrompt reads ralph-prompt.md, appends PRD context, story state, iteration constraint,
// judge feedback, event context, and semantic memory into the prompt.
func BuildPrompt(ralphHome, projectDir, storyID string, p *prd.PRD, opts ...BuildPromptOpts) (string, memory.RetrievalResult, error) {
	base, err := os.ReadFile(filepath.Join(ralphHome, "ralph-prompt.md"))
	if err != nil {
		return "", memory.RetrievalResult{}, fmt.Errorf("reading ralph-prompt.md: %w", err)
	}

	prompt := string(base)

	// Inject PRD context if provided
	var story *prd.UserStory
	if p != nil {
		story = p.FindStory(storyID)
		prompt += buildPRDContext(p, storyID, story)
		prompt += buildStoryStateContext(projectDir, storyID)
	}

	prompt += fmt.Sprintf(`

---
## THIS ITERATION
You MUST only work on story **%s**. Do NOT implement any other story. After completing %s, stop immediately.
If progress.md contains a [CONTEXT EXHAUSTED] entry for %s, continue from where it left off.`, storyID, storyID, storyID)

	// Inject judge feedback if present
	feedbackPath := filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-feedback-%s.md", storyID))
	if feedback, err := os.ReadFile(feedbackPath); err == nil && len(feedback) > 0 {
		prompt += "\n\n---\n## JUDGE FEEDBACK (MUST ADDRESS)\n" + string(feedback)
	}

	// Inject context from event log
	if evts, err := events.Load(projectDir); err == nil && len(evts) > 0 {
		if section := events.FormatContextSection(evts, storyID); section != "" {
			prompt += "\n\n---\n" + section
		}
	}

	// Inject semantic memory context (additive, after event context)
	var retrieval memory.RetrievalResult
	if len(opts) > 0 && opts[0].Memory != nil && story != nil {
		memCtx, err := opts[0].Memory.RetrieveContext(
			context.Background(),
			story.Title,
			story.Description,
			story.AcceptanceCriteria,
			opts[0].MemoryOpts,
		)
		if err != nil {
			debuglog.Log("warning: semantic memory retrieval failed: %v", err)
		} else {
			retrieval = memCtx
			if memCtx.Text != "" {
				prompt += "\n\n---\n" + memCtx.Text
			}
		}
	}

	return prompt, retrieval, nil
}

// buildPRDContext generates the YOUR STORY, PROJECT CONTEXT, and OTHER STORIES sections.
func buildPRDContext(p *prd.PRD, storyID string, story *prd.UserStory) string {
	var b strings.Builder

	// YOUR STORY section
	if story != nil {
		b.WriteString("\n\n---\n## YOUR STORY\n")
		b.WriteString(fmt.Sprintf("**%s: %s**\n\n", story.ID, story.Title))
		b.WriteString(story.Description + "\n\n")
		b.WriteString("### Acceptance Criteria\n")
		for _, ac := range story.AcceptanceCriteria {
			b.WriteString(fmt.Sprintf("- %s\n", ac))
		}
		if story.Notes != "" {
			b.WriteString(fmt.Sprintf("\n**Notes:** %s\n", story.Notes))
		}
	}

	// PROJECT CONTEXT section
	b.WriteString("\n\n---\n## PROJECT CONTEXT\n")
	b.WriteString(fmt.Sprintf("- **Project:** %s\n", p.Project))
	b.WriteString(fmt.Sprintf("- **Branch:** %s\n", p.BranchName))
	b.WriteString(fmt.Sprintf("- **Progress:** %d/%d stories complete\n", p.CompletedCount(), p.TotalCount()))

	// OTHER STORIES section
	b.WriteString("\n\n---\n## OTHER STORIES\n")
	for _, s := range p.UserStories {
		if s.ID == storyID {
			continue
		}
		status := "queued"
		if s.Passes {
			status = "✓"
		}
		b.WriteString(fmt.Sprintf("- %s: %s [%s]\n", s.ID, s.Title, status))
	}

	return b.String()
}

var validStoryID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidStoryID(id string) bool {
	return validStoryID.MatchString(id)
}

// buildStoryStateContext loads story state, plan, and decisions and formats them.
func buildStoryStateContext(projectDir, storyID string) string {
	state, err := storystate.Load(projectDir, storyID)
	if err != nil || state.IterationCount == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("\n\n---\n## Story State\n")
	b.WriteString(fmt.Sprintf("- **Status:** %s\n", state.Status))
	b.WriteString(fmt.Sprintf("- **Iteration:** %d\n", state.IterationCount))
	b.WriteString(fmt.Sprintf("- **Last Updated:** %s\n", state.LastUpdated.Format("2006-01-02 15:04:05")))

	if len(state.FilesTouched) > 0 {
		b.WriteString("- **Files Touched:** " + strings.Join(state.FilesTouched, ", ") + "\n")
	}
	if len(state.Subtasks) > 0 {
		b.WriteString("\n### Subtasks\n")
		for _, st := range state.Subtasks {
			check := "[ ]"
			if st.Done {
				check = "[x]"
			}
			b.WriteString(fmt.Sprintf("- %s %s\n", check, st.Description))
		}
	}
	if len(state.ErrorsEncountered) > 0 {
		b.WriteString("\n### Errors Encountered\n")
		for _, e := range state.ErrorsEncountered {
			b.WriteString(fmt.Sprintf("- **Error:** %s\n  **Resolution:** %s\n", e.Error, e.Resolution))
		}
	}
	if len(state.JudgeFeedback) > 0 {
		b.WriteString("\n### Judge Feedback\n")
		for _, jf := range state.JudgeFeedback {
			b.WriteString(fmt.Sprintf("- %s\n", jf))
		}
	}

	// Plan
	if plan, err := storystate.LoadPlan(projectDir, storyID); err == nil && plan != "" {
		b.WriteString("\n### Implementation Plan\n")
		b.WriteString(plan + "\n")
	}

	// Decisions
	if decisions, err := storystate.LoadDecisions(projectDir, storyID); err == nil && decisions != "" {
		b.WriteString("\n### Key Decisions\n")
		b.WriteString(decisions + "\n")
	}

	return b.String()
}

// RunClaudeOpts contains optional parameters for RunClaude.
type RunClaudeOpts struct {
	Iteration int
	StoryID   string
}

// RunClaude executes claude with streaming JSON output, parsing events into
// a human-readable activity file for the TUI to display. Raw JSON is written
// to the log file for debugging. Returns accumulated token usage from the
// streaming response alongside any error.
func RunClaude(ctx context.Context, projectDir, prompt, logFilePath string, opts ...RunClaudeOpts) (*costs.TokenUsage, error) {
	logFile, err := os.Create(logFilePath)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()

	activityPath := activityPathFromLog(logFilePath)
	activityFile, err := os.Create(activityPath)
	if err != nil {
		return nil, fmt.Errorf("creating activity file: %w", err)
	}
	defer activityFile.Close()

	cmd := exec.CommandContext(ctx, "claude",
		"--dangerously-skip-permissions",
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	)
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(prompt)
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(logFile, &stderrBuf)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	proc := &streamProcessor{activityFile: activityFile, projectDir: projectDir}
	if len(opts) > 0 {
		proc.iteration = opts[0].Iteration
		proc.storyID = opts[0].StoryID
		proc.isFixStory = strings.HasPrefix(opts[0].StoryID, "FIX-")
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // up to 10MB lines
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(logFile, line)
		proc.processLine(line)
	}

	usage := proc.tokenUsage()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return &usage, ctx.Err()
		}
		if IsUsageLimitError(stderrBuf.String()) {
			return &usage, &UsageLimitError{Stderr: strings.TrimSpace(stderrBuf.String())}
		}
		return &usage, err
	}
	return &usage, nil
}

// LogFilePath returns the log file path for a given iteration.
func LogFilePath(logDir string, iteration int) string {
	return filepath.Join(logDir, fmt.Sprintf("iteration-%d.log", iteration))
}

// ActivityFilePath returns the activity file path for a given iteration.
func ActivityFilePath(logDir string, iteration int) string {
	return filepath.Join(logDir, fmt.Sprintf("iteration-%d-activity.log", iteration))
}

// ReadLogTail reads the last n lines of a file. Returns empty string if file doesn't exist.
func ReadLogTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ReadActivityContent reads the activity file, keeping the tail if it's too large.
func ReadActivityContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	const maxSize = 256 * 1024
	if len(content) > maxSize {
		content = content[len(content)-maxSize:]
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
	}
	return content
}

// LogContainsComplete checks if the output contains the completion signal.
func LogContainsComplete(logPath string) bool {
	// Check activity file first (continuous text, most reliable)
	activityPath := activityPathFromLog(logPath)
	if data, err := os.ReadFile(activityPath); err == nil {
		if strings.Contains(string(data), "<promise>COMPLETE</promise>") {
			return true
		}
	}
	// Fallback: check raw log
	if data, err := os.ReadFile(logPath); err == nil {
		if strings.Contains(string(data), "<promise>COMPLETE</promise>") {
			return true
		}
	}
	return false
}

func activityPathFromLog(logPath string) string {
	return strings.TrimSuffix(logPath, ".log") + "-activity.log"
}

// ToolRecord tracks a tool invocation for stuck detection.
type ToolRecord struct {
	Tool     string
	Command  string
	FilePath string
}

// StuckInfo describes a detected stuck pattern.
type StuckInfo struct {
	Pattern   string   `json:"pattern"`
	Commands  []string `json:"repeated_commands"`
	Count     int      `json:"count"`
	Iteration int      `json:"iteration"`
	StoryID   string   `json:"story_id"`
}

// ReadStuckInfo reads a stuck info file for a given iteration.
func ReadStuckInfo(projectDir string, iteration int) *StuckInfo {
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("stuck-%d.json", iteration))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var info StuckInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}

func writeStuckInfo(projectDir string, info StuckInfo) {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("stuck-%d.json", info.Iteration))
	_ = os.WriteFile(path, data, 0o644)
}

// streamProcessor parses streaming JSON events from Claude and writes
// human-readable activity to a file.
type streamProcessor struct {
	activityFile *os.File
	currentBlock string // "thinking", "tool_use", "text"
	currentTool  string
	inputBuf     strings.Builder // accumulates tool input JSON
	syncCount    int             // counter for periodic syncing

	// Stuck detection
	recentTools []ToolRecord // ring buffer, cap 10
	projectDir  string
	iteration   int
	storyID     string
	isFixStory  bool

	// Token usage accumulation
	model        string
	inputTokens  int
	outputTokens int
	cacheRead    int
	cacheWrite   int
	numTurns     int
	durationMS   int
}

// tokenUsage returns the accumulated token usage as a costs.TokenUsage.
func (sp *streamProcessor) tokenUsage() costs.TokenUsage {
	return costs.TokenUsage{
		InputTokens:  sp.inputTokens,
		OutputTokens: sp.outputTokens,
		CacheRead:    sp.cacheRead,
		CacheWrite:   sp.cacheWrite,
		Model:        sp.model,
		Provider:     "claude",
		NumTurns:     sp.numTurns,
		DurationMS:   sp.durationMS,
	}
}

// parseUsage extracts token counts from a usage object in streaming events.
func (sp *streamProcessor) parseUsage(usage map[string]interface{}) {
	if v, ok := usage["input_tokens"].(float64); ok {
		sp.inputTokens += int(v)
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		sp.outputTokens += int(v)
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		sp.cacheWrite += int(v)
	}
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		sp.cacheRead += int(v)
	}
}

func (sp *streamProcessor) processLine(line string) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}

	// Claude Code CLI emits several top-level event types:
	//   "stream_event" — wraps raw API events (message_start, content_block_delta, etc.)
	//   "system" — init, hooks
	//   "assistant" — complete assistant message with usage
	//   "result" — final result with aggregated usage, num_turns, duration
	topType, _ := raw["type"].(string)

	// Handle top-level Claude Code CLI events that carry usage/metadata
	switch topType {
	case "result":
		if usage, ok := raw["usage"].(map[string]interface{}); ok {
			sp.parseUsage(usage)
		}
		if numTurns, ok := raw["num_turns"].(float64); ok {
			sp.numTurns = int(numTurns)
		}
		if durMS, ok := raw["duration_ms"].(float64); ok {
			sp.durationMS = int(durMS)
		}
		if model, ok := raw["model"].(string); ok && sp.model == "" {
			sp.model = model
		}
		return

	case "assistant":
		if msg, ok := raw["message"].(map[string]interface{}); ok {
			if model, ok := msg["model"].(string); ok && model != "<synthetic>" && sp.model == "" {
				sp.model = model
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				sp.parseUsage(usage)
			}
		}
		return

	case "system":
		if model, ok := raw["model"].(string); ok && sp.model == "" {
			sp.model = model
		}
		return
	}

	// Unwrap stream_event envelope for raw API events
	event := raw
	if topType == "stream_event" {
		inner, ok := raw["event"].(map[string]interface{})
		if !ok {
			return
		}
		event = inner
	}

	eventType, _ := event["type"].(string)

	switch eventType {
	case "message_start":
		// Extract model and initial usage from the message object
		if msg, ok := event["message"].(map[string]interface{}); ok {
			if model, ok := msg["model"].(string); ok {
				sp.model = model
			}
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				sp.parseUsage(usage)
			} else {
				debuglog.Log("warning: message_start event missing usage field")
			}
		}

	case "message_delta":
		// Extract incremental usage from delta
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			sp.parseUsage(usage)
		} else {
			debuglog.Log("warning: message_delta event missing usage field")
		}

	case "content_block_start":
		block, ok := event["content_block"].(map[string]interface{})
		if !ok {
			return
		}
		blockType, _ := block["type"].(string)
		sp.currentBlock = blockType
		sp.inputBuf.Reset()

		switch blockType {
		case "thinking":
			sp.writeLine("\n── Thinking ─────────────────────")
		case "tool_use":
			name, _ := block["name"].(string)
			sp.currentTool = name
			sp.writeLine(fmt.Sprintf("\n── Tool: %s ─────────────────────", name))
		case "text":
			sp.writeLine("\n── Response ─────────────────────")
		}

	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "thinking_delta":
			if text, ok := delta["thinking"].(string); ok && text != "" {
				sp.writeRaw(text)
			}
		case "text_delta":
			if text, ok := delta["text"].(string); ok && text != "" {
				sp.writeRaw(text)
			}
		case "input_json_delta":
			if partial, ok := delta["partial_json"].(string); ok {
				sp.inputBuf.WriteString(partial)
			}
		}

	case "content_block_stop":
		if sp.currentBlock == "tool_use" {
			inputJSON := sp.inputBuf.String()
			sp.writeToolSummary(inputJSON)
			sp.recordTool(inputJSON)
			sp.inputBuf.Reset()
		}
		sp.currentBlock = ""
	}
}

func (sp *streamProcessor) recordTool(inputJSON string) {
	if inputJSON == "" || sp.projectDir == "" {
		return
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return
	}

	rec := ToolRecord{Tool: sp.currentTool}
	if cmd, ok := input["command"].(string); ok {
		rec.Command = cmd
	}
	if fp, ok := input["file_path"].(string); ok {
		rec.FilePath = fp
	}

	// Ring buffer of 10
	if len(sp.recentTools) >= 10 {
		sp.recentTools = sp.recentTools[1:]
	}
	sp.recentTools = append(sp.recentTools, rec)

	sp.checkStuck()
}

func (sp *streamProcessor) checkStuck() {
	if len(sp.recentTools) < 3 {
		return
	}

	// Thresholds depend on story type
	bashThreshold := 3
	editThreshold := 4
	if sp.isFixStory {
		bashThreshold = 5
		editThreshold = 6
	}

	// Check for repeated bash commands
	cmdCounts := make(map[string]int)
	for _, r := range sp.recentTools {
		if r.Tool == "Bash" && r.Command != "" {
			// Normalize: trim to first 100 chars for comparison
			key := r.Command
			if len(key) > 100 {
				key = key[:100]
			}
			cmdCounts[key]++
		}
	}
	for cmd, count := range cmdCounts {
		if count >= bashThreshold {
			info := StuckInfo{
				Pattern:   "repeated_bash_command",
				Commands:  []string{cmd},
				Count:     count,
				Iteration: sp.iteration,
				StoryID:   sp.storyID,
			}
			writeStuckInfo(sp.projectDir, info)
			return
		}
	}

	// Check for repeated file edits
	fileCounts := make(map[string]int)
	for _, r := range sp.recentTools {
		if (r.Tool == "Edit" || r.Tool == "Write") && r.FilePath != "" {
			fileCounts[r.FilePath]++
		}
	}
	for fp, count := range fileCounts {
		if count >= editThreshold {
			info := StuckInfo{
				Pattern:   "repeated_file_edit",
				Commands:  []string{fp},
				Count:     count,
				Iteration: sp.iteration,
				StoryID:   sp.storyID,
			}
			writeStuckInfo(sp.projectDir, info)
			return
		}
	}
}

func (sp *streamProcessor) writeToolSummary(inputJSON string) {
	if inputJSON == "" {
		return
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return
	}
	if fp, ok := input["file_path"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → %s", fp))
	}
	if cmd, ok := input["command"].(string); ok {
		if len(cmd) > 200 {
			cmd = cmd[:200] + "..."
		}
		sp.writeLine(fmt.Sprintf("  $ %s", cmd))
	}
	if pattern, ok := input["pattern"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → pattern: %s", pattern))
	}
	if query, ok := input["query"].(string); ok {
		sp.writeLine(fmt.Sprintf("  → %s", query))
	}
	if oldStr, ok := input["old_string"].(string); ok {
		preview := oldStr
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		sp.writeLine(fmt.Sprintf("  → replacing: %s", preview))
	}
}

func (sp *streamProcessor) writeLine(line string) {
	fmt.Fprintln(sp.activityFile, line)
	sp.activityFile.Sync()
}

func (sp *streamProcessor) writeRaw(text string) {
	fmt.Fprint(sp.activityFile, text)
	sp.syncCount++
	// Sync every 5 writes or on newlines for real-time display
	if sp.syncCount >= 5 || strings.Contains(text, "\n") {
		sp.activityFile.Sync()
		sp.syncCount = 0
	}
}
