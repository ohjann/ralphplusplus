package exec

import (
	"context"
	"os/exec"
	"strings"
	"time"
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

// JJDiff runs "jj diff --from <fromRev> --to <toRev> --git" and returns the output.
// If toRev is empty, it defaults to "@".
func JJDiff(ctx context.Context, dir, fromRev, toRev string) (string, error) {
	if toRev == "" {
		toRev = "@"
	}
	cmd := exec.CommandContext(ctx, "jj", "diff", "--from", fromRev, "--to", toRev, "--git")
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

// runGeminiOnce executes a single gemini invocation.
func runGeminiOnce(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "gemini", "-p", prompt, "-o", "text")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// RunGemini runs gemini with retry and exponential backoff (3 attempts, 2s/4s delays).
func RunGemini(ctx context.Context, prompt string) (string, error) {
	delays := []time.Duration{2 * time.Second, 4 * time.Second}
	var lastErr error
	var lastOut string

	for attempt := range 3 {
		out, err := runGeminiOnce(ctx, prompt)
		if err == nil && out != "" {
			return out, nil
		}
		lastErr = err
		lastOut = out

		if attempt < 2 {
			select {
			case <-ctx.Done():
				return lastOut, ctx.Err()
			case <-time.After(delays[attempt]):
			}
		}
	}

	if lastErr != nil {
		return lastOut, lastErr
	}
	return lastOut, nil
}

// GeminiAvailable checks whether the gemini CLI is on PATH.
func GeminiAvailable() bool {
	_, err := exec.LookPath("gemini")
	return err == nil
}
