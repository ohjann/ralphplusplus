package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/prd"
)

// CompareCandidate represents one competing implementation for comparison.
type CompareCandidate struct {
	Index    int    // 0-based index
	ChangeID string // jj change ID
	Diff     string // the implementation diff
}

// CompareResult holds the outcome of a fusion comparison.
type CompareResult struct {
	WinnerIndex int              // 0-based index of the winning candidate
	Reason      string           // explanation of why this candidate won
	TokenUsage  costs.TokenUsage // token usage for the comparison call
}

type compareVerdict struct {
	Winner int    `json:"winner"`
	Reason string `json:"reason"`
}

// RunComparison evaluates multiple competing implementations of the same story
// and picks the best one based on correctness, quality, simplicity, and maintainability.
func RunComparison(ctx context.Context, story *prd.UserStory, candidates []CompareCandidate) (CompareResult, error) {
	if len(candidates) == 0 {
		return CompareResult{}, fmt.Errorf("no candidates to compare")
	}
	if len(candidates) == 1 {
		return CompareResult{WinnerIndex: 0, Reason: "single candidate"}, nil
	}

	criteria := strings.Join(story.AcceptanceCriteria, "\n- ")

	var diffsSection strings.Builder
	for _, c := range candidates {
		fmt.Fprintf(&diffsSection, "\n## Implementation %d\n```diff\n%s\n```\n", c.Index+1, c.Diff)
	}

	prompt := fmt.Sprintf(`You are comparing %d competing implementations of the same coding task. Each was implemented independently.

**Story:** %s
**Description:** %s
**Acceptance Criteria:**
- %s

%s

Evaluate each implementation against the acceptance criteria. Pick the best one considering:
1. **Correctness** — Does it fully satisfy the acceptance criteria?
2. **Code quality** — Is it clean, well-structured, and idiomatic?
3. **Simplicity** — Is it the simplest solution that works?
4. **Maintainability** — Will it be easy to understand and modify?

Return ONLY valid JSON: {"winner": <0-based index>, "reason": "brief explanation"}`,
		len(candidates), story.Title, story.Description, criteria, diffsSection.String())

	out, usage, err := exec.RunClaudeJudge(ctx, prompt)
	if err != nil {
		return CompareResult{}, fmt.Errorf("comparison judge: %w", err)
	}

	// Parse verdict
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "```json")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)

	var verdict compareVerdict
	if err := json.Unmarshal([]byte(out), &verdict); err != nil {
		// If we can't parse, default to first candidate
		return CompareResult{WinnerIndex: 0, Reason: "comparison parse failed, defaulting to first", TokenUsage: usage}, nil
	}

	// Bounds check
	if verdict.Winner < 0 || verdict.Winner >= len(candidates) {
		verdict.Winner = 0
		verdict.Reason = fmt.Sprintf("invalid winner index, defaulting to first (original reason: %s)", verdict.Reason)
	}

	return CompareResult{
		WinnerIndex: verdict.Winner,
		Reason:      verdict.Reason,
		TokenUsage:  usage,
	}, nil
}
