package exec

import (
	"context"
	"os/exec"
	"strings"
)

// JJStatus runs "jj st" in the given directory and returns the output.
func JJStatus(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "jj", "st")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// JJDiff runs "jj diff --from <rev> --to @ --git" and returns the output.
func JJDiff(ctx context.Context, dir, fromRev string) (string, error) {
	cmd := exec.CommandContext(ctx, "jj", "diff", "--from", fromRev, "--to", "@", "--git")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// JJCurrentRev returns the current change_id from "jj log -r @ --no-graph -T change_id".
func JJCurrentRev(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "jj", "log", "-r", "@", "--no-graph", "-T", "change_id")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GitDiff runs "git diff HEAD~1" as a fallback.
func GitDiff(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "HEAD~1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// RunGemini runs "gemini -p <prompt> -o text" and returns the output.
func RunGemini(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "gemini", "-p", prompt, "-o", "text")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// GeminiAvailable checks whether the gemini CLI is on PATH.
func GeminiAvailable() bool {
	_, err := exec.LookPath("gemini")
	return err == nil
}
