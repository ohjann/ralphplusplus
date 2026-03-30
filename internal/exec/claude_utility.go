package exec

import (
	"context"
	"os/exec"
	"strings"

	"github.com/eoghanhynes/ralph/internal/costs"
)

// RunClaudeUtility runs a lightweight Claude CLI call for utility tasks
// (complexity assessment, DAG analysis, etc.) using the specified model.
func RunClaudeUtility(ctx context.Context, prompt, model string) (string, costs.TokenUsage, error) {
	if model == "" {
		model = "haiku"
	}

	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--model", model,
		"--output-format", "text",
	)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), costs.TokenUsage{}, err
	}

	result := strings.TrimSpace(string(out))
	usage := costs.TokenUsage{
		InputTokens:  estimateTokens(prompt),
		OutputTokens: estimateTokens(result),
		Model:        model,
		Provider:     "claude",
	}
	return result, usage, nil
}
