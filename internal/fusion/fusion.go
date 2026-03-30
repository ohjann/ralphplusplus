package fusion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/worker"
)

// FusionGroup tracks competing implementations of the same story.
type FusionGroup struct {
	StoryID  string
	Workers  []worker.WorkerID
	Results  []FusionResult
	Expected int
}

// FusionResult holds the outcome of a single fusion worker.
type FusionResult struct {
	WorkerID    worker.WorkerID
	ChangeID    string
	Passed      bool
	JudgeResult *judge.Result
	TokenUsage  *costs.TokenUsage
}

// AllDone returns true when all expected workers have reported results.
func (fg *FusionGroup) AllDone() bool {
	return len(fg.Results) >= fg.Expected
}

// PassingResults returns only the results where the judge passed.
func (fg *FusionGroup) PassingResults() []FusionResult {
	var passing []FusionResult
	for _, r := range fg.Results {
		if r.Passed {
			passing = append(passing, r)
		}
	}
	return passing
}

// complexityAssessment is the JSON response from the Haiku complexity call.
type complexityAssessment struct {
	Complex bool   `json:"complex"`
	Reason  string `json:"reason"`
}

// AssessComplexity uses a lightweight LLM call to determine if a story is
// complex enough to benefit from fusion mode (multiple competing implementations).
func AssessComplexity(ctx context.Context, story *prd.UserStory, utilityModel string) (bool, string, error) {
	criteria := strings.Join(story.AcceptanceCriteria, "\n- ")
	prompt := fmt.Sprintf(`Assess if this coding task would benefit from multiple competing implementations.

**Complex** = architectural decisions with multiple valid approaches, touches many files, integration-heavy, design trade-offs.
**Simple** = straightforward implementation, single obvious approach, few files, routine CRUD/config changes.

Story: %s
Description: %s
Acceptance Criteria:
- %s

Return ONLY valid JSON: {"complex": true/false, "reason": "one sentence"}`, story.Title, story.Description, criteria)

	out, _, err := exec.RunClaudeUtility(ctx, prompt, utilityModel)
	if err != nil {
		return false, "", fmt.Errorf("complexity assessment: %w", err)
	}

	// Parse JSON from response
	out = strings.TrimSpace(out)
	// Strip markdown fences if present
	out = strings.TrimPrefix(out, "```json")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)

	var assessment complexityAssessment
	if err := json.Unmarshal([]byte(out), &assessment); err != nil {
		return false, "", fmt.Errorf("parse complexity assessment: %w (raw: %s)", err, out)
	}

	return assessment.Complex, assessment.Reason, nil
}
