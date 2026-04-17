package costs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ohjann/ralphplusplus/internal/userdata"
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
	RunID                 string         `json:"run_id,omitempty"`  // cross-links to internal/history manifest
	Kind                  string         `json:"kind,omitempty"`    // matches manifest Kind (daemon|retro|memory-consolidate|ad-hoc)
	StoriesTotal          int            `json:"stories_total"`
	StoriesCompleted      int            `json:"stories_completed"`
	StoriesFailed         int            `json:"stories_failed"`
	TotalCost             float64        `json:"total_cost"`
	DurationMinutes       float64        `json:"duration_minutes"`
	TotalIterations       int            `json:"total_iterations"`
	AvgIterationsPerStory float64        `json:"avg_iterations_per_story"`
	StuckCount            int            `json:"stuck_count"`
	JudgeRejectionRate    float64        `json:"judge_rejection_rate"`
	FirstPassRate         float64        `json:"first_pass_rate"`         // fraction of stories that passed on first attempt
	ModelsUsed            []string       `json:"models_used,omitempty"`   // distinct models used in this run
	TotalInputTokens      int            `json:"total_input_tokens,omitempty"`
	TotalOutputTokens     int            `json:"total_output_tokens,omitempty"`
	CacheHitRate          float64        `json:"cache_hit_rate,omitempty"`
	StoryDetails          []StorySummary `json:"story_details,omitempty"` // per-story breakdown
	Workers               int            `json:"workers,omitempty"`       // number of parallel workers used
	NoArchitect           bool           `json:"no_architect,omitempty"`
	NoFusion              bool           `json:"no_fusion,omitempty"`
	NoSimplify            bool           `json:"no_simplify,omitempty"`
	QualityReview         bool           `json:"quality_review,omitempty"`
	FusionWorkers         int            `json:"fusion_workers,omitempty"`
	FusionMetrics         *FusionMetrics `json:"fusion_metrics,omitempty"`
}

// IsDaemon reports whether this summary is a daemon run. Legacy pre-migration
// entries with empty Kind are grandfathered as daemon so --history still shows
// them in the default view.
func (r RunSummary) IsDaemon() bool {
	return r.Kind == "" || r.Kind == "daemon"
}

// RunHistory holds a list of run summaries.
type RunHistory struct {
	Runs []RunSummary `json:"runs"`
}

// historyPath returns <userdata>/ralph/repos/<fp>/run-history.json.
func historyPath(fp string) (string, error) {
	rd, err := userdata.RepoDir(fp)
	if err != nil {
		return "", err
	}
	return filepath.Join(rd, historyFileName), nil
}

// LegacyHistoryPath returns the pre-IH-005 location (<projectDir>/.ralph/run-history.json).
// Exposed for migration only.
func LegacyHistoryPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", historyFileName)
}

// LoadHistory reads <userdata>/ralph/repos/<fp>/run-history.json. Returns empty
// history if the file doesn't exist.
func LoadHistory(fp string) (RunHistory, error) {
	path, err := historyPath(fp)
	if err != nil {
		return RunHistory{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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

// SaveHistory writes the run history to <userdata>/ralph/repos/<fp>/run-history.json
// with JSON indentation.
func SaveHistory(fp string, history RunHistory) error {
	path, err := historyPath(fp)
	if err != nil {
		return err
	}
	if err := userdata.EnsureDirs(filepath.Dir(path)); err != nil {
		return fmt.Errorf("creating repo dir: %w", err)
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
func AppendRun(fp string, summary RunSummary) error {
	h, err := LoadHistory(fp)
	if err != nil {
		return fmt.Errorf("loading history for append: %w", err)
	}
	h.Runs = append(h.Runs, summary)
	return SaveHistory(fp, h)
}

// MigrateLegacyHistory copies <projectDir>/.ralph/run-history.json into the
// user-level location for fp when the legacy file exists and the user-level
// file does not. It leaves the legacy file in place (for rollback) and writes
// a sibling <legacy>.migrated marker so the copy is only ever performed once.
// If both files already exist, the user-level file wins and this is a no-op.
func MigrateLegacyHistory(projectDir, fp string) error {
	legacy := LegacyHistoryPath(projectDir)
	marker := legacy + ".migrated"

	legacyData, err := os.ReadFile(legacy)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading legacy history: %w", err)
	}

	userPath, err := historyPath(fp)
	if err != nil {
		return err
	}
	if _, err := os.Stat(userPath); err == nil {
		// Both files exist: user-level already wins, skip.
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat user-level history: %w", err)
	}

	if err := userdata.EnsureDirs(filepath.Dir(userPath)); err != nil {
		return fmt.Errorf("creating user repo dir: %w", err)
	}
	if err := os.WriteFile(userPath, legacyData, 0o644); err != nil {
		return fmt.Errorf("writing user-level history: %w", err)
	}
	if err := os.WriteFile(marker, []byte("migrated\n"), 0o644); err != nil {
		return fmt.Errorf("writing migration marker: %w", err)
	}
	return nil
}

// LoadAllHistory aggregates run-history.json files across every fingerprint
// directory under <userdata>/ralph/repos/. Returns a concatenated list of
// RunSummary entries in directory-walk order; no repos.json index is involved.
func LoadAllHistory() (RunHistory, error) {
	reposDir, err := userdata.ReposDir()
	if err != nil {
		return RunHistory{}, err
	}
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RunHistory{}, nil
		}
		return RunHistory{}, fmt.Errorf("read repos dir: %w", err)
	}
	var agg RunHistory
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		h, err := LoadHistory(e.Name())
		if err != nil {
			// Skip unreadable entries; aggregation must not fail on one bad file.
			continue
		}
		agg.Runs = append(agg.Runs, h.Runs...)
	}
	return agg, nil
}
