package costs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const historyFileName = "run-history.json"

// StorySummary holds per-story metrics for a completed run.
type StorySummary struct {
	StoryID      string `json:"story_id"`
	Title        string `json:"title"`
	Iterations   int    `json:"iterations"`
	Passed       bool   `json:"passed"`
	JudgeRejects int    `json:"judge_rejects"`
	Model        string `json:"model,omitempty"` // primary model used for this story
}

// FusionMetrics tracks fusion-mode outcomes for a run.
type FusionMetrics struct {
	GroupsCreated    int `json:"groups_created"`              // fusion groups created
	MultiplesPassed  int `json:"multiples_passed,omitempty"`  // groups where 2+ implementations passed
	ComparisonPicked int `json:"comparison_picked,omitempty"` // groups where the comparison judge picked a non-first-passer
}

// RunSummary holds the summary of a single completed PRD run.
type RunSummary struct {
	PRD                   string         `json:"prd"`
	Date                  string         `json:"date"`
	StoriesTotal          int            `json:"stories_total"`
	StoriesCompleted      int            `json:"stories_completed"`
	StoriesFailed         int            `json:"stories_failed"`
	TotalCost             float64        `json:"total_cost"`
	DurationMinutes       float64        `json:"duration_minutes"`
	TotalIterations       int            `json:"total_iterations"`
	AvgIterationsPerStory float64        `json:"avg_iterations_per_story"`
	StuckCount            int            `json:"stuck_count"`
	JudgeRejectionRate    float64        `json:"judge_rejection_rate"`
	FirstPassRate         float64        `json:"first_pass_rate"`                // fraction of stories that passed on first attempt
	ModelsUsed            []string       `json:"models_used,omitempty"`          // distinct models used in this run
	TotalInputTokens      int            `json:"total_input_tokens,omitempty"`
	TotalOutputTokens     int            `json:"total_output_tokens,omitempty"`
	CacheHitRate          float64        `json:"cache_hit_rate,omitempty"`
	StoryDetails          []StorySummary `json:"story_details,omitempty"`        // per-story breakdown
	Workers               int            `json:"workers,omitempty"`              // number of parallel workers used
	NoArchitect           bool           `json:"no_architect,omitempty"`
	NoFusion              bool           `json:"no_fusion,omitempty"`
	NoSimplify            bool           `json:"no_simplify,omitempty"`
	QualityReview         bool           `json:"quality_review,omitempty"`
	FusionWorkers         int            `json:"fusion_workers,omitempty"`
	FusionMetrics         *FusionMetrics `json:"fusion_metrics,omitempty"`
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
