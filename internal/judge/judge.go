package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	rexec "github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/prd"
)

type Verdict struct {
	Verdict        string   `json:"verdict"`
	CriteriaMet    []string `json:"criteria_met"`
	CriteriaFailed []string `json:"criteria_failed"`
	Reason         string   `json:"reason"`
	Suggestion     string   `json:"suggestion"`
}

type Result struct {
	Passed  bool
	Reason  string
	Warning string // non-empty if we fell through to PASS due to error
}

// RunJudge performs the full judge flow for a story.
func RunJudge(ctx context.Context, ralphHome, projectDir, prdFile, storyID, preRev string) Result {
	p, err := prd.Load(prdFile)
	if err != nil {
		return Result{Passed: true, Warning: fmt.Sprintf("could not load prd.json: %v", err)}
	}

	story := p.FindStory(storyID)
	if story == nil {
		return Result{Passed: true, Warning: fmt.Sprintf("story %s not found in prd.json", storyID)}
	}

	// Build acceptance criteria string
	var criteria []string
	for _, c := range story.AcceptanceCriteria {
		criteria = append(criteria, "- "+c)
	}
	criteriaStr := strings.Join(criteria, "\n")
	if len(criteria) == 0 {
		criteriaStr = "No acceptance criteria specified"
	}

	// Get diff
	diff := getDiff(ctx, projectDir, preRev)
	if diff == "" {
		return Result{Passed: true, Warning: "no diff available"}
	}

	// Build prompt from template
	template, err := os.ReadFile(filepath.Join(ralphHome, "judge-prompt.md"))
	if err != nil {
		return Result{Passed: true, Warning: fmt.Sprintf("could not read judge-prompt.md: %v", err)}
	}

	prompt := string(template)
	prompt = strings.ReplaceAll(prompt, "{{STORY_ID}}", storyID)
	prompt = strings.ReplaceAll(prompt, "{{STORY_TITLE}}", story.Title)
	prompt = strings.ReplaceAll(prompt, "{{STORY_DESCRIPTION}}", story.Description)
	prompt = strings.ReplaceAll(prompt, "{{ACCEPTANCE_CRITERIA}}", criteriaStr)
	prompt = strings.ReplaceAll(prompt, "{{DIFF}}", diff)

	// Run gemini
	output, err := rexec.RunGemini(ctx, prompt)
	if err != nil || output == "" {
		return Result{Passed: true, Warning: "gemini returned empty output or error"}
	}

	// Parse verdict
	verdict := parseVerdict(output)
	if verdict == nil {
		return Result{Passed: true, Warning: "could not parse judge verdict JSON"}
	}

	if verdict.Verdict == "PASS" {
		return Result{Passed: true, Reason: verdict.Reason}
	}

	if verdict.Verdict == "FAIL" {
		// Write feedback file
		writeFeedback(projectDir, storyID, verdict)

		// Revert passes to false in prd.json
		p.SetPasses(storyID, false)
		_ = prd.Save(prdFile, p)

		return Result{
			Passed: false,
			Reason: verdict.Reason,
		}
	}

	return Result{Passed: true, Warning: fmt.Sprintf("unknown verdict %q", verdict.Verdict)}
}

// GetRejectionCount reads the rejection count for a story.
func GetRejectionCount(projectDir, storyID string) int {
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-rejections-%s.count", storyID))
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

// IncrementRejectionCount increments the rejection counter.
func IncrementRejectionCount(projectDir, storyID string) {
	current := GetRejectionCount(projectDir, storyID)
	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-rejections-%s.count", storyID))
	_ = os.WriteFile(path, []byte(strconv.Itoa(current+1)), 0o644)
}

// ClearRejectionCount removes the rejection counter and feedback file.
func ClearRejectionCount(projectDir, storyID string) {
	os.Remove(filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-rejections-%s.count", storyID)))
	os.Remove(filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-feedback-%s.md", storyID)))
}

// AppendAutoPass adds an auto-pass note to progress.txt.
func AppendAutoPass(progressFile, storyID string, rejectionCount int) {
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n## [Judge] %s auto-passed after %d rejections [HUMAN REVIEW NEEDED]\n---\n", storyID, rejectionCount)
}

func getDiff(ctx context.Context, dir, preRev string) string {
	if preRev != "" {
		if diff, err := rexec.JJDiff(ctx, dir, preRev); err == nil && diff != "" {
			return diff
		}
	}
	if diff, err := rexec.GitDiff(ctx, dir); err == nil && diff != "" {
		return diff
	}
	return ""
}

func parseVerdict(output string) *Verdict {
	// Strip markdown fences
	output = strings.TrimSpace(output)
	output = strings.TrimPrefix(output, "```json")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	// Try to extract JSON block
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	jsonStr := output[start : end+1]

	var v Verdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil
	}
	return &v
}

func writeFeedback(projectDir, storyID string, v *Verdict) {
	content := fmt.Sprintf(`# Judge Feedback for %s

## Verdict: FAIL

## Reason
%s

## Failed Criteria
%s

## Suggestion
%s

## Instructions
Address the failed criteria above. Do not repeat the same approach that was rejected.
`, storyID, v.Reason, strings.Join(v.CriteriaFailed, ", "), v.Suggestion)

	path := filepath.Join(projectDir, ".ralph", fmt.Sprintf("judge-feedback-%s.md", storyID))
	_ = os.WriteFile(path, []byte(content), 0o644)
}
