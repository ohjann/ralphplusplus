package runner

import (
	"context"
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

// RunClaude executes "claude --dangerously-skip-permissions --print" with the given prompt,
// writing stdout/stderr to logFile. Returns when the process exits.
func RunClaude(ctx context.Context, projectDir, prompt, logFilePath string) error {
	logFile, err := os.Create(logFilePath)
	if err != nil {
		return fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.CommandContext(ctx, "claude", "--dangerously-skip-permissions", "--print")
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Run(); err != nil {
		// Context cancellation is expected on quit
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

// LogContainsComplete checks if the log output contains the completion signal.
func LogContainsComplete(logPath string) bool {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "<promise>COMPLETE</promise>")
}
