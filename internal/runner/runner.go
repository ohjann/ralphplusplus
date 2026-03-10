package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eoghanhynes/ralph/internal/events"
)

// BuildPrompt reads ralph-prompt.md, appends iteration constraint, and injects judge feedback if present.
func BuildPrompt(ralphHome, projectDir, storyID string) (string, error) {
	base, err := os.ReadFile(filepath.Join(ralphHome, "ralph-prompt.md"))
	if err != nil {
		return "", fmt.Errorf("reading ralph-prompt.md: %w", err)
	}

	prompt := string(base) + fmt.Sprintf(`

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

	return prompt, nil
}

// RunClaudeOpts contains optional parameters for RunClaude.
type RunClaudeOpts struct {
	Iteration int
	StoryID   string
}

// RunClaude executes claude with streaming JSON output, parsing events into
// a human-readable activity file for the TUI to display. Raw JSON is written
// to the log file for debugging.
func RunClaude(ctx context.Context, projectDir, prompt, logFilePath string, opts ...RunClaudeOpts) error {
	logFile, err := os.Create(logFilePath)
	if err != nil {
		return fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()

	activityPath := activityPathFromLog(logFilePath)
	activityFile, err := os.Create(activityPath)
	if err != nil {
		return fmt.Errorf("creating activity file: %w", err)
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
	cmd.Stderr = logFile

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting claude: %w", err)
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

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
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
	const maxSize = 64 * 1024
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
}

func (sp *streamProcessor) processLine(line string) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}

	// The stream-json format wraps API events in a "stream_event" envelope:
	//   {"type":"stream_event","event":{"type":"content_block_delta",...}}
	// Unwrap the inner event if present; also handle bare events for compatibility.
	event := raw
	topType, _ := raw["type"].(string)
	if topType == "stream_event" {
		inner, ok := raw["event"].(map[string]interface{})
		if !ok {
			return
		}
		event = inner
	}

	eventType, _ := event["type"].(string)

	switch eventType {
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
