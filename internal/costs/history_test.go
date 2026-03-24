package costs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHistory_NoFile(t *testing.T) {
	dir := t.TempDir()
	h, err := LoadHistory(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Runs) != 0 {
		t.Fatalf("expected empty runs, got %d", len(h.Runs))
	}
}

func TestSaveAndLoadHistory(t *testing.T) {
	dir := t.TempDir()
	h := RunHistory{
		Runs: []RunSummary{
			{
				PRD:              "test-project",
				Date:             "2026-03-13T10:00:00Z",
				StoriesTotal:     5,
				StoriesCompleted: 4,
				StoriesFailed:    1,
				TotalCost:        1.23,
				DurationMinutes:  45.5,
				TotalIterations:  12,
				AvgIterationsPerStory: 3.0,
				StuckCount:       1,
				JudgeRejectionRate: 0.25,
			},
		},
	}

	if err := SaveHistory(dir, h); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadHistory(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(loaded.Runs))
	}
	r := loaded.Runs[0]
	if r.PRD != "test-project" {
		t.Errorf("PRD = %q, want %q", r.PRD, "test-project")
	}
	if r.StoriesTotal != 5 {
		t.Errorf("StoriesTotal = %d, want 5", r.StoriesTotal)
	}
	if r.StoriesCompleted != 4 {
		t.Errorf("StoriesCompleted = %d, want 4", r.StoriesCompleted)
	}
	if r.JudgeRejectionRate != 0.25 {
		t.Errorf("JudgeRejectionRate = %f, want 0.25", r.JudgeRejectionRate)
	}

	// Verify the file is indented JSON
	data, _ := os.ReadFile(filepath.Join(dir, ".ralph", "run-history.json"))
	if len(data) == 0 {
		t.Fatal("file is empty")
	}
	// Indented JSON should contain newlines
	if !containsByte(data, '\n') {
		t.Error("expected indented JSON with newlines")
	}
}

func TestAppendRun(t *testing.T) {
	dir := t.TempDir()

	s1 := RunSummary{PRD: "proj1", Date: "2026-03-13T10:00:00Z", StoriesTotal: 3, StoriesCompleted: 3}
	s2 := RunSummary{PRD: "proj1", Date: "2026-03-13T11:00:00Z", StoriesTotal: 5, StoriesCompleted: 4, StoriesFailed: 1}

	if err := AppendRun(dir, s1); err != nil {
		t.Fatalf("first append error: %v", err)
	}
	if err := AppendRun(dir, s2); err != nil {
		t.Fatalf("second append error: %v", err)
	}

	h, err := LoadHistory(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(h.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(h.Runs))
	}
	if h.Runs[0].StoriesTotal != 3 {
		t.Errorf("first run StoriesTotal = %d, want 3", h.Runs[0].StoriesTotal)
	}
	if h.Runs[1].StoriesFailed != 1 {
		t.Errorf("second run StoriesFailed = %d, want 1", h.Runs[1].StoriesFailed)
	}
}

func TestBackwardCompatibility_OldHistoryFormat(t *testing.T) {
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a history file with only the original fields (no new fields)
	oldJSON := `{
  "runs": [
    {
      "prd": "old-project",
      "date": "2026-01-01T00:00:00Z",
      "stories_total": 3,
      "stories_completed": 2,
      "stories_failed": 1,
      "total_cost": 0,
      "duration_minutes": 10.5,
      "total_iterations": 4,
      "avg_iterations_per_story": 2.0,
      "stuck_count": 0,
      "judge_rejection_rate": 0.1
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(ralphDir, "run-history.json"), []byte(oldJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := LoadHistory(dir)
	if err != nil {
		t.Fatalf("failed to load old history format: %v", err)
	}
	if len(h.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(h.Runs))
	}
	r := h.Runs[0]
	if r.PRD != "old-project" {
		t.Errorf("PRD = %q, want %q", r.PRD, "old-project")
	}
	// New fields should be zero values
	if r.FirstPassRate != 0 {
		t.Errorf("FirstPassRate = %f, want 0", r.FirstPassRate)
	}
	if len(r.ModelsUsed) != 0 {
		t.Errorf("ModelsUsed = %v, want empty", r.ModelsUsed)
	}
	if r.TotalInputTokens != 0 {
		t.Errorf("TotalInputTokens = %d, want 0", r.TotalInputTokens)
	}
	if len(r.StoryDetails) != 0 {
		t.Errorf("StoryDetails = %v, want empty", r.StoryDetails)
	}
}

func TestNewFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h := RunHistory{
		Runs: []RunSummary{
			{
				PRD:               "test",
				Date:              "2026-03-25T10:00:00Z",
				StoriesTotal:      5,
				StoriesCompleted:  4,
				StoriesFailed:     1,
				FirstPassRate:     0.75,
				ModelsUsed:        []string{"claude-opus-4-6", "claude-sonnet-4-6"},
				TotalInputTokens:  100000,
				TotalOutputTokens: 50000,
				CacheHitRate:      0.42,
				StoryDetails: []StorySummary{
					{StoryID: "S-001", Title: "Auth", Iterations: 2, Passed: true, JudgeRejects: 1, Model: "claude-opus-4-6"},
					{StoryID: "S-002", Title: "API", Iterations: 1, Passed: true, JudgeRejects: 0, Model: "claude-sonnet-4-6"},
				},
			},
		},
	}

	if err := SaveHistory(dir, h); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadHistory(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	r := loaded.Runs[0]
	if r.FirstPassRate != 0.75 {
		t.Errorf("FirstPassRate = %f, want 0.75", r.FirstPassRate)
	}
	if len(r.ModelsUsed) != 2 {
		t.Errorf("ModelsUsed length = %d, want 2", len(r.ModelsUsed))
	}
	if r.TotalInputTokens != 100000 {
		t.Errorf("TotalInputTokens = %d, want 100000", r.TotalInputTokens)
	}
	if r.CacheHitRate != 0.42 {
		t.Errorf("CacheHitRate = %f, want 0.42", r.CacheHitRate)
	}
	if len(r.StoryDetails) != 2 {
		t.Fatalf("StoryDetails length = %d, want 2", len(r.StoryDetails))
	}
	if r.StoryDetails[0].JudgeRejects != 1 {
		t.Errorf("StoryDetails[0].JudgeRejects = %d, want 1", r.StoryDetails[0].JudgeRejects)
	}
	if r.StoryDetails[1].Model != "claude-sonnet-4-6" {
		t.Errorf("StoryDetails[1].Model = %q, want %q", r.StoryDetails[1].Model, "claude-sonnet-4-6")
	}
}

func containsByte(data []byte, b byte) bool {
	for _, c := range data {
		if c == b {
			return true
		}
	}
	return false
}
