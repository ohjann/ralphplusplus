package retro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ohjann/ralphplusplus/internal/quality"
	"github.com/ohjann/ralphplusplus/internal/runner"
)

// Improvement represents a single design improvement opportunity.
type Improvement struct {
	Title              string   `json:"title"`
	Category           string   `json:"category"`           // architecture, resilience, api-design, dx, performance, observability
	Severity           string   `json:"severity"`           // high, medium, low
	Description        string   `json:"description"`
	Rationale          string   `json:"rationale"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	AffectedFiles      []string `json:"affected_files"`
}

// RetroResult is the full retrospective output.
type RetroResult struct {
	Timestamp    time.Time     `json:"timestamp"`
	PRDProject   string        `json:"prd_project"`
	Improvements []Improvement `json:"improvements"`
	Summary      string        `json:"summary"`
}

// RunRetrospective executes a post-implementation design retrospective.
// It reads the PRD and SUMMARY.md, gets a file manifest, spawns a Claude
// session to analyze the code holistically, and returns improvement suggestions.
func RunRetrospective(ctx context.Context, projectDir, logDir, prdFile, model string) (*RetroResult, error) {
	// Read PRD
	prdData, err := os.ReadFile(prdFile)
	if err != nil {
		return nil, fmt.Errorf("read prd.json: %w", err)
	}

	// Read SUMMARY.md (optional)
	summaryContent := "No summary available."
	summaryPath := filepath.Join(projectDir, "SUMMARY.md")
	if data, err := os.ReadFile(summaryPath); err == nil {
		summaryContent = string(data)
	}

	// Get file manifest (--stat output, no diff content)
	manifest, err := quality.GetDiffManifest(ctx, projectDir, prdFile)
	if err != nil {
		manifest = "(could not determine changed files)"
	}

	// Build prompt
	prompt := fmt.Sprintf(retroPrompt, string(prdData), summaryContent, manifest)

	// Ensure directories exist
	retroDir := filepath.Join(projectDir, ".ralph", "retro")
	if err := os.MkdirAll(retroDir, 0o755); err != nil {
		return nil, fmt.Errorf("create retro dir: %w", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	// Run Claude
	logPath := filepath.Join(logDir, "retro.log")
	_, err = runner.RunClaude(ctx, projectDir, prompt, logPath, runner.RunClaudeOpts{Model: model})
	if err != nil {
		return nil, fmt.Errorf("claude retrospective: %w", err)
	}

	// Parse improvements from activity log
	activityPath := strings.TrimSuffix(logPath, ".log") + "-activity.log"
	improvements, _ := parseImprovementsFromActivity(activityPath)
	summary := parseSummaryFromActivity(activityPath)

	// Extract project name from PRD
	var prd struct {
		Project string `json:"project"`
	}
	_ = json.Unmarshal(prdData, &prd)

	result := &RetroResult{
		Timestamp:    time.Now(),
		PRDProject:   prd.Project,
		Improvements: improvements,
		Summary:      summary,
	}

	// Save result
	ts := time.Now().Format("2006-01-02T15-04-05")
	resultPath := filepath.Join(retroDir, fmt.Sprintf("retro-%s.json", ts))
	data, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		_ = os.WriteFile(resultPath, data, 0o644)
	}

	return result, nil
}

// parseImprovementsFromActivity extracts improvements from <improvements> tags in the activity log.
func parseImprovementsFromActivity(activityPath string) ([]Improvement, bool) {
	data, err := os.ReadFile(activityPath)
	if err != nil {
		return nil, false
	}
	content := string(data)

	start := strings.LastIndex(content, "<improvements>")
	if start < 0 {
		return nil, false
	}
	start += len("<improvements>")
	end := strings.LastIndex(content, "</improvements>")
	if end < 0 || end <= start {
		return nil, false
	}

	jsonStr := strings.TrimSpace(content[start:end])
	if jsonStr == "" || jsonStr == "[]" {
		return nil, true // parsed successfully, no improvements needed
	}

	var improvements []Improvement
	if err := json.Unmarshal([]byte(jsonStr), &improvements); err != nil {
		return nil, false
	}

	return improvements, true
}

// parseSummaryFromActivity extracts the summary from <summary> tags in the activity log.
func parseSummaryFromActivity(activityPath string) string {
	data, err := os.ReadFile(activityPath)
	if err != nil {
		return ""
	}
	content := string(data)

	start := strings.LastIndex(content, "<summary>")
	if start < 0 {
		return ""
	}
	start += len("<summary>")
	end := strings.LastIndex(content, "</summary>")
	if end < 0 || end <= start {
		return ""
	}

	return strings.TrimSpace(content[start:end])
}

// FormatSummary produces a human-readable summary of retrospective results.
func FormatSummary(result *RetroResult) string {
	var b strings.Builder

	b.WriteString("── Design Retrospective ──\n")
	if result.PRDProject != "" {
		b.WriteString(fmt.Sprintf("  Project: %s\n", result.PRDProject))
	}
	b.WriteString(fmt.Sprintf("  Date: %s\n", result.Timestamp.Format("2006-01-02 15:04")))

	if len(result.Improvements) == 0 {
		b.WriteString("\n  No improvements identified.\n")
		if result.Summary != "" {
			b.WriteString(fmt.Sprintf("\n  %s\n", result.Summary))
		}
		return b.String()
	}

	// Count by severity
	counts := map[string]int{}
	for _, imp := range result.Improvements {
		counts[imp.Severity]++
	}
	b.WriteString(fmt.Sprintf("\n  Found %d improvement(s)", len(result.Improvements)))
	parts := []string{}
	if n := counts["high"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d high", n))
	}
	if n := counts["medium"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d medium", n))
	}
	if n := counts["low"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d low", n))
	}
	if len(parts) > 0 {
		b.WriteString(fmt.Sprintf(" (%s)", strings.Join(parts, ", ")))
	}
	b.WriteString("\n")

	// Group by category, sort by severity within each group
	categories := groupByCategory(result.Improvements)
	for _, cat := range categories {
		b.WriteString(fmt.Sprintf("\n  [%s]\n", cat.name))
		for _, imp := range cat.items {
			sev := imp.Severity
			if sev == "high" {
				sev = "HIGH"
			}
			b.WriteString(fmt.Sprintf("    %s  %s\n", sev, imp.Title))
			b.WriteString(fmt.Sprintf("         %s\n", imp.Description))
		}
	}

	if result.Summary != "" {
		b.WriteString(fmt.Sprintf("\n  Summary: %s\n", result.Summary))
	}

	return b.String()
}

type categoryGroup struct {
	name  string
	items []Improvement
}

func groupByCategory(improvements []Improvement) []categoryGroup {
	m := map[string][]Improvement{}
	order := []string{}
	for _, imp := range improvements {
		if _, exists := m[imp.Category]; !exists {
			order = append(order, imp.Category)
		}
		m[imp.Category] = append(m[imp.Category], imp)
	}

	sevOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
	groups := make([]categoryGroup, 0, len(order))
	for _, cat := range order {
		items := m[cat]
		sort.Slice(items, func(i, j int) bool {
			return sevOrder[items[i].Severity] < sevOrder[items[j].Severity]
		})
		groups = append(groups, categoryGroup{name: cat, items: items})
	}
	return groups
}
