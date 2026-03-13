package costs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const historyFileName = "run-history.json"

// RunSummary holds the summary of a single completed PRD run.
type RunSummary struct {
	PRD                  string  `json:"prd"`
	Date                 string  `json:"date"`
	StoriesTotal         int     `json:"stories_total"`
	StoriesCompleted     int     `json:"stories_completed"`
	StoriesFailed        int     `json:"stories_failed"`
	TotalCost            float64 `json:"total_cost"`
	DurationMinutes      float64 `json:"duration_minutes"`
	TotalIterations      int     `json:"total_iterations"`
	AvgIterationsPerStory float64 `json:"avg_iterations_per_story"`
	StuckCount           int     `json:"stuck_count"`
	JudgeRejectionRate   float64 `json:"judge_rejection_rate"`
}

// RunHistory holds a list of run summaries.
type RunHistory struct {
	Runs []RunSummary `json:"runs"`
}

func historyPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", historyFileName)
}

// LoadHistory reads .ralph/run-history.json. Returns empty history if the file doesn't exist.
func LoadHistory(projectDir string) (RunHistory, error) {
	path := historyPath(projectDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RunHistory{}, nil
		}
		return RunHistory{}, fmt.Errorf("reading run history: %w", err)
	}
	var h RunHistory
	if err := json.Unmarshal(data, &h); err != nil {
		return RunHistory{}, fmt.Errorf("parsing run history: %w", err)
	}
	return h, nil
}

// SaveHistory writes the run history to .ralph/run-history.json with JSON indentation.
func SaveHistory(projectDir string, history RunHistory) error {
	path := historyPath(projectDir)
	// Ensure the .ralph directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating .ralph directory: %w", err)
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling run history: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing run history: %w", err)
	}
	return nil
}

// AppendRun loads the existing history, appends the summary, and saves.
func AppendRun(projectDir string, summary RunSummary) error {
	h, err := LoadHistory(projectDir)
	if err != nil {
		return fmt.Errorf("loading history for append: %w", err)
	}
	h.Runs = append(h.Runs, summary)
	return SaveHistory(projectDir, h)
}
