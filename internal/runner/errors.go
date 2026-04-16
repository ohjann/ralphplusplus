package runner

import (
	"fmt"
	"strings"
)

// ClaudeExitError wraps a non-zero claude CLI exit with the trailing
// stderr output that usually contains the actual reason. Unwrap exposes
// the underlying *exec.ExitError for callers that need exit-code details.
type ClaudeExitError struct {
	Err    error
	Stderr string
}

func (e *ClaudeExitError) Error() string {
	if e.Stderr == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%v: %s", e.Err, e.Stderr)
}

func (e *ClaudeExitError) Unwrap() error { return e.Err }

// StderrTail returns the last N bytes of stderr with internal whitespace
// collapsed to single spaces so a multi-line Claude error fits on one log
// line. Empty input returns empty.
func StderrTail(stderr string, max int) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return ""
	}
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return strings.Join(strings.Fields(s), " ")
}

// UsageLimitError indicates the Claude CLI failed due to a usage/rate limit.
type UsageLimitError struct {
	Stderr string
}

func (e *UsageLimitError) Error() string {
	return "claude usage limit: " + e.Stderr
}

var usageLimitPatterns = []string{
	"rate limit",
	"usage limit",
	"token limit",
	"too many requests",
	"overloaded",
	"429",
}

// IsUsageLimitError checks whether stderr output indicates a usage limit error.
func IsUsageLimitError(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, pat := range usageLimitPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}
