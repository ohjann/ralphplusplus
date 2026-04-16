package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rexec "github.com/ohjann/ralphplusplus/internal/exec"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

// planGateVerdict is the JSON shape returned by the utility model.
type planGateVerdict struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

// validatePlan asks the utility model whether the architect's plan addresses
// the story's acceptance criteria and names specific files.
//
// Returns (pass, reason, err). err is non-nil only on infrastructure failures
// (LLM call failed, unparseable output) — callers should treat err as "skip
// the gate" rather than "plan failed", to avoid blocking on transient issues.
func validatePlan(ctx context.Context, plan string, story *prd.UserStory, utilityModel string) (bool, string, error) {
	if story == nil {
		return true, "no story to validate against", nil
	}

	criteria := strings.Join(story.AcceptanceCriteria, "\n- ")
	if len(story.AcceptanceCriteria) == 0 {
		criteria = "(none specified)"
	}

	prompt := fmt.Sprintf(`Evaluate whether this implementation plan is concrete enough to execute.

A good plan:
1. Names specific files to create or modify (not just "update the relevant module").
2. Addresses every acceptance criterion (does not silently skip any).
3. Is actionable — describes what to do, not just restates the story.

Story: %s
Description: %s
Acceptance Criteria:
- %s

Plan:
%s

Return ONLY valid JSON: {"pass": true/false, "reason": "one sentence"}`,
		story.Title, story.Description, criteria, plan)

	out, _, err := rexec.RunClaudeUtility(ctx, prompt, utilityModel)
	if err != nil {
		return false, "", fmt.Errorf("plan gate LLM call: %w", err)
	}

	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "```json")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)

	var v planGateVerdict
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return false, "", fmt.Errorf("parse plan gate verdict: %w (raw: %s)", err, out)
	}

	return v.Pass, v.Reason, nil
}
