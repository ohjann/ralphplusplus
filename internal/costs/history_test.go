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
				PRD:                   "test-project",
				Date:                  "2026-03-13T10:00:00Z",
				StoriesTotal:          5,
				StoriesCompleted:      4,
				StoriesFailed:         1,
				TotalCost:             1.23,
				DurationMinutes:       45.5,
				TotalIterations:       12,
				AvgIterationsPerStory: 3.0,
				StuckCount:            1,
				JudgeRejectionRate:    0.25,
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

	// Verify the file is indented JSON
	data, _ := os.ReadFile(filepath.Join(dir, ".ralph", "run-history.json"))
	if len(data) == 0 {
		t.Fatal("file is empty")
	}
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

func containsByte(data []byte, b byte) bool {
	for _, c := range data {
		if c == b {
			return true
		}
	}
	return false
}
