package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ohjann/ralphplusplus/internal/assets"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	rexec "github.com/ohjann/ralphplusplus/internal/exec"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

// DirRev associates a directory with revisions for diffing.
// Rev is the "from" revision. ToRev, if set, is the "to" revision (otherwise defaults to @).
type DirRev struct {
	Dir   string
	Rev   string
	ToRev string
}

type Verdict struct {
	Verdict        string   `json:"verdict"`
	CriteriaMet    []string `json:"criteria_met"`
	CriteriaFailed []string `json:"criteria_failed"`
	Reason         string   `json:"reason"`
	Suggestion     string   `json:"suggestion"`
}

type Result struct {
	Passed         bool
	Reason         string
	Warning        string // non-empty if we fell through to PASS due to error
	CriteriaMet    []string
	CriteriaFailed []string
	Suggestion     string
	TokenUsage     costs.TokenUsage
}

// RunJudge performs the full judge flow for a story.
func RunJudge(ctx context.Context, ralphHome, projectDir, prdFile, storyID string, preRevs []DirRev) Result {
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

	// Pre-judge compilation gate: fast local check before expensive judge call
	if buildErr := preBuildCheck(ctx, projectDir, p.BuildCommand); buildErr != "" {
		debuglog.Log("judge pre-build failed for %s: %s", storyID, buildErr)
		writeFeedback(projectDir, storyID, &Verdict{
			Verdict:        "FAIL",
			CriteriaFailed: []string{"Code compiles successfully"},
			Reason:         "Pre-judge build check failed: " + buildErr,
			Suggestion:     "Fix the compilation error before the judge can review your changes.",
		})
		p.SetPasses(storyID, false)
		_ = prd.Save(prdFile, p)
		return Result{
			Passed:         false,
			Reason:         "Pre-judge build check failed: " + buildErr,
			CriteriaFailed: []string{"Code compiles successfully"},
			Suggestion:     "Fix the compilation error before the judge can review your changes.",
		}
	}

	// Get diffs from all repos
	diff := getDiffs(ctx, preRevs)
	if diff == "" {
		return Result{
			Passed:         false,
			Reason:         "No code changes were produced for this story",
			CriteriaFailed: []string{"Implementation produces code changes"},
			Suggestion:     "The worker did not produce any diff. The story needs to be re-attempted.",
		}
	}

	// Build prompt from template
	template, err := assets.ReadPrompt("judge-prompt.md")
	if err != nil {
		return Result{Passed: true, Warning: fmt.Sprintf("could not read judge-prompt.md: %v", err)}
	}

	prompt := string(template)
	prompt = strings.ReplaceAll(prompt, "{{STORY_ID}}", storyID)
	prompt = strings.ReplaceAll(prompt, "{{STORY_TITLE}}", story.Title)
	prompt = strings.ReplaceAll(prompt, "{{STORY_DESCRIPTION}}", story.Description)
	prompt = strings.ReplaceAll(prompt, "{{ACCEPTANCE_CRITERIA}}", criteriaStr)
	prompt = strings.ReplaceAll(prompt, "{{DIFF}}", diff)

	// Run Claude judge
	output, tokenUsage, err := rexec.RunClaudeJudge(ctx, prompt)
	if err != nil || output == "" {
		return Result{Passed: true, Warning: "claude judge returned empty output or error", TokenUsage: tokenUsage}
	}

	// Parse verdict
	verdict, parseErr := parseVerdict(output)
	if parseErr != nil {
		return Result{Passed: true, Warning: parseErr.Error(), TokenUsage: tokenUsage}
	}

	if verdict.Verdict == "PASS" {
		return Result{
			Passed:      true,
			Reason:      verdict.Reason,
			CriteriaMet: verdict.CriteriaMet,
			Suggestion:  verdict.Suggestion,
			TokenUsage:  tokenUsage,
		}
	}

	if verdict.Verdict == "FAIL" {
		// Write feedback file
		writeFeedback(projectDir, storyID, verdict)

		// Revert passes to false in prd.json
		p.SetPasses(storyID, false)
		_ = prd.Save(prdFile, p)

		return Result{
			Passed:         false,
			Reason:         verdict.Reason,
			CriteriaMet:    verdict.CriteriaMet,
			CriteriaFailed: verdict.CriteriaFailed,
			Suggestion:     verdict.Suggestion,
			TokenUsage:     tokenUsage,
		}
	}

	return Result{Passed: true, Warning: fmt.Sprintf("unknown verdict %q", verdict.Verdict), TokenUsage: tokenUsage}
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

// AppendAutoPass adds an auto-pass note to progress.md.
func AppendAutoPass(progressFile, storyID string, rejectionCount int) {
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n## [Judge] %s auto-passed after %d rejections [HUMAN REVIEW NEEDED]\n---\n", storyID, rejectionCount)
}

// AppendJudgeResult writes the judge result to progress.md for persistence.
func AppendJudgeResult(progressFile, storyID string, r Result) {
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprint(f, FormatResult(storyID, r))
	fmt.Fprintln(f, "---")
}

func getDiffs(ctx context.Context, preRevs []DirRev) string {
	var parts []string
	for _, dr := range preRevs {
		var diff string
		if dr.Rev != "" {
			if d, err := rexec.JJDiff(ctx, dr.Dir, dr.Rev, dr.ToRev); err == nil && d != "" {
				diff = d
			}
		}
		if diff == "" {
			if d, err := rexec.GitDiff(ctx, dr.Dir); err == nil && d != "" {
				diff = d
			}
		}
		if diff != "" {
			if len(preRevs) > 1 {
				parts = append(parts, fmt.Sprintf("## Repo: %s\n%s", dr.Dir, diff))
			} else {
				parts = append(parts, diff)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// parseVerdictError describes why verdict parsing failed.
type parseVerdictError struct {
	Reason    string // "no JSON found", "unmarshal failed", etc.
	RawOutput string // truncated raw output for debugging
	JSONStr   string // extracted JSON string (if any)
	Err       error  // underlying error (if any)
}

func (e *parseVerdictError) Error() string {
	msg := fmt.Sprintf("could not parse judge verdict JSON: %s", e.Reason)
	if e.Err != nil {
		msg += fmt.Sprintf(" (%v)", e.Err)
	}
	if e.JSONStr != "" {
		excerpt := e.JSONStr
		if len(excerpt) > 200 {
			excerpt = excerpt[:200] + "..."
		}
		msg += fmt.Sprintf("\n  extracted JSON: %s", excerpt)
	} else if e.RawOutput != "" {
		excerpt := e.RawOutput
		if len(excerpt) > 200 {
			excerpt = excerpt[:200] + "..."
		}
		msg += fmt.Sprintf("\n  raw output: %s", excerpt)
	}
	return msg
}

func parseVerdict(output string) (*Verdict, error) {
	// Strip markdown fences
	output = strings.TrimSpace(output)
	output = strings.TrimPrefix(output, "```json")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	if output == "" {
		return nil, &parseVerdictError{Reason: "empty output after stripping markdown fences"}
	}

	// Try to extract JSON block
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < 0 || end <= start {
		return nil, &parseVerdictError{Reason: "no JSON object found in output", RawOutput: output}
	}
	jsonStr := output[start : end+1]

	var v Verdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, &parseVerdictError{Reason: "unmarshal failed", JSONStr: jsonStr, Err: err}
	}
	return &v, nil
}

// FormatResult returns a human-readable summary of a judge result.
func FormatResult(storyID string, r Result) string {
	var sb strings.Builder

	if r.Warning != "" {
		sb.WriteString(fmt.Sprintf("── Judge: %s ── AUTO-PASS ──\n", storyID))
		sb.WriteString(fmt.Sprintf("  Warning: %s\n", r.Warning))
		return sb.String()
	}

	verdict := "PASS"
	if !r.Passed {
		verdict = "FAIL"
	}
	sb.WriteString(fmt.Sprintf("── Judge: %s ── %s ──\n", storyID, verdict))

	if r.Reason != "" && r.Passed {
		sb.WriteString(fmt.Sprintf("  Reason: %s\n", r.Reason))
	}

	if len(r.CriteriaMet) > 0 {
		sb.WriteString("  Criteria met:\n")
		for _, c := range r.CriteriaMet {
			sb.WriteString(fmt.Sprintf("    ✓ %s\n", c))
		}
	}

	if len(r.CriteriaFailed) > 0 {
		sb.WriteString("  Criteria failed:\n")
		for _, c := range r.CriteriaFailed {
			sb.WriteString(fmt.Sprintf("    ✗ %s\n", c))
		}
		if r.Reason != "" {
			sb.WriteString("      ┆ Reason: ")
			sb.WriteString(indentMultiline(r.Reason, "      ┆ "))
			sb.WriteString("\n")
		}
		if r.Suggestion != "" {
			sb.WriteString("      ┆ Suggestion: ")
			sb.WriteString(indentMultiline(r.Suggestion, "      ┆ "))
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// indentMultiline indents continuation lines of a multi-line string.
// The first line is returned as-is; subsequent lines are prefixed with indent.
func indentMultiline(s, indent string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
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

// preBuildCheck runs a fast compilation check before invoking the judge.
// Returns empty string on success, or a trimmed error message on failure.
// Uses buildCommand from prd.json if set, otherwise detects from marker files.
// If no build command can be determined, the check is skipped (returns success).
func preBuildCheck(ctx context.Context, projectDir, buildCommand string) string {
	name, args := parseBuildCommand(buildCommand)
	if name == "" {
		debuglog.Log("preBuildCheck: no build command configured or detected in %s, skipping", projectDir)
		return ""
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		// Truncate to keep feedback concise
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		if msg == "" {
			msg = err.Error()
		}
		return msg
	}
	return ""
}

// parseBuildCommand splits the buildCommand from prd.json into name and args.
// Returns empty name if buildCommand is not set.
func parseBuildCommand(buildCommand string) (string, []string) {
	if buildCommand == "" {
		return "", nil
	}
	parts := strings.Fields(buildCommand)
	return parts[0], parts[1:]
}
