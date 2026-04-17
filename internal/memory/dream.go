package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohjann/ralphplusplus/internal/assets"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// RunMeta tracks run count and last dream consolidation time.
type RunMeta struct {
	RunCount  int    `json:"run_count"`
	LastDream string `json:"last_dream,omitempty"`
}

func runMetaPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", "run-meta.json")
}

// LoadRunMeta reads .ralph/run-meta.json. Returns zero-value RunMeta if the file doesn't exist.
func LoadRunMeta(projectDir string) (RunMeta, error) {
	data, err := os.ReadFile(runMetaPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return RunMeta{}, nil
		}
		return RunMeta{}, fmt.Errorf("reading run-meta.json: %w", err)
	}
	var m RunMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return RunMeta{}, fmt.Errorf("parsing run-meta.json: %w", err)
	}
	return m, nil
}

// SaveRunMeta writes .ralph/run-meta.json.
func SaveRunMeta(projectDir string, meta RunMeta) error {
	path := runMetaPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating .ralph directory: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling run-meta.json: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// IncrementRunCount increments the run counter and returns the new count.
func IncrementRunCount(projectDir string) (int, error) {
	meta, err := LoadRunMeta(projectDir)
	if err != nil {
		meta = RunMeta{}
	}
	meta.RunCount++
	if err := SaveRunMeta(projectDir, meta); err != nil {
		return meta.RunCount, err
	}
	return meta.RunCount, nil
}

// ShouldDream returns true if the run count has reached the dream threshold.
func ShouldDream(projectDir string, dreamEveryNRuns int) bool {
	if dreamEveryNRuns <= 0 {
		return false
	}
	meta, err := LoadRunMeta(projectDir)
	if err != nil {
		return false
	}
	return meta.RunCount >= dreamEveryNRuns
}

// RunDream invokes Claude to consolidate the memory files. It builds the dream
// prompt with current memory contents and recent run history, then lets Claude
// rewrite the files. Errors are returned but callers should log, not fail.
func RunDream(ctx context.Context, projectDir, ralphHome, logDir string, maxEntries, dreamEveryNRuns int, runClaude RunClaudeFunc) error {
	prompt, err := buildDreamPrompt(projectDir, ralphHome, maxEntries, dreamEveryNRuns)
	if err != nil {
		return fmt.Errorf("building dream prompt: %w", err)
	}

	logPath := filepath.Join(logDir, "dream.log")
	if err := runClaude(ctx, projectDir, prompt, logPath); err != nil {
		return fmt.Errorf("running dream claude: %w", err)
	}

	// Reset run counter and update last dream time
	meta := RunMeta{
		RunCount:  0,
		LastDream: time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveRunMeta(projectDir, meta); err != nil {
		return fmt.Errorf("resetting run-meta after dream: %w", err)
	}

	debuglog.Log("dream consolidation completed")
	return nil
}

func buildDreamPrompt(projectDir, ralphHome string, maxEntries, lastNRuns int) (string, error) {
	tmpl, err := assets.ReadPrompt("prompts/dream.md")
	if err != nil {
		return "", fmt.Errorf("reading dream prompt: %w", err)
	}

	prompt := string(tmpl)

	prompt = injectLearnings(prompt, projectDir, ralphHome, "{{LEARNINGS}}", "{{PRD_LEARNINGS}}")

	// Inject recent run summaries
	prompt = strings.Replace(prompt, "{{RUN_SUMMARIES}}", buildRecentRunSummaries(projectDir, lastNRuns), 1)

	// Inject max entries
	prompt = strings.Replace(prompt, "{{MAX_ENTRIES}}", fmt.Sprintf("%d", maxEntries), 1)

	// Append output instructions telling Claude where to write
	prompt += fmt.Sprintf(`

## Output Instructions

Write the consolidated versions of the memory files using the Write tool. These are FULL replacements, not appends.

- Learnings file (project-specific): %s
- PRD learnings file (global): %s

Create the directories if they don't exist.
Write each file in its entirety — the old content will be replaced.
`, filepath.Join(projectDir, ".ralph", "memory", "learnings.md"),
		filepath.Join(ralphHome, "memory", "prd-learnings.md"))

	return prompt, nil
}

func buildRecentRunSummaries(projectDir string, lastN int) string {
	fp, err := userdata.Fingerprint(projectDir)
	if err != nil {
		return "(no run history)"
	}
	h, err := costs.LoadHistory(fp)
	if err != nil || len(h.Runs) == 0 {
		return "(no run history)"
	}

	runs := h.Runs
	if lastN > 0 && len(runs) > lastN {
		runs = runs[len(runs)-lastN:]
	}

	var b strings.Builder
	for i, r := range runs {
		fmt.Fprintf(&b, "### Run %d: %s (%s)\n", i+1, r.PRD, r.Date)
		fmt.Fprintf(&b, "- Stories: %d/%d completed, %d failed\n", r.StoriesCompleted, r.StoriesTotal, r.StoriesFailed)
		fmt.Fprintf(&b, "- Iterations: %d total, %.1f avg/story\n", r.TotalIterations, r.AvgIterationsPerStory)
		fmt.Fprintf(&b, "- Stuck count: %d, Judge rejection rate: %.0f%%\n", r.StuckCount, r.JudgeRejectionRate*100)
		if len(r.StoryDetails) > 0 {
			for _, s := range r.StoryDetails {
				status := "passed"
				if !s.Passed {
					status = "failed"
				}
				fmt.Fprintf(&b, "  - %s: %s (%d iterations, %d judge rejects)\n", s.StoryID, status, s.Iterations, s.JudgeRejects)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}
