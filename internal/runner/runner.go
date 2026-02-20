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
)

// BuildPrompt reads ralph-prompt.md and appends the iteration constraint.
func BuildPrompt(ralphHome, storyID string) (string, error) {
	base, err := os.ReadFile(filepath.Join(ralphHome, "ralph-prompt.md"))
	if err != nil {
		return "", fmt.Errorf("reading ralph-prompt.md: %w", err)
	}

	prompt := string(base) + fmt.Sprintf(`

---
## THIS ITERATION
You MUST only work on story **%s**. Do NOT implement any other story. After completing %s, stop immediately.
If progress.txt contains a [CONTEXT EXHAUSTED] entry for %s, continue from where it left off.`, storyID, storyID, storyID)

	return prompt, nil
}

// RunClaude executes claude with streaming JSON output, parsing events into
// a human-readable activity file for the TUI to display. Raw JSON is written
// to the log file for debugging.
func RunClaude(ctx context.Context, projectDir, prompt, logFilePath string) error {
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

	proc := &streamProcessor{activityFile: activityFile}

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

// streamProcessor parses streaming JSON events from Claude and writes
// human-readable activity to a file.
type streamProcessor struct {
	activityFile *os.File
	currentBlock string // "thinking", "tool_use", "text"
	currentTool  string
	inputBuf     strings.Builder // accumulates tool input JSON
}

func (sp *streamProcessor) processLine(line string) {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
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
			sp.writeToolSummary(sp.inputBuf.String())
			sp.inputBuf.Reset()
		}
		sp.currentBlock = ""
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
	// Sync periodically on newlines for real-time display
	if strings.Contains(text, "\n") {
		sp.activityFile.Sync()
	}
}
