package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadLessonsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	original := LessonsFile{
		Lessons: []Lesson{
			{
				ID:             "L-001",
				Category:       "testing",
				Pattern:        "Mocked DB tests miss migration bugs",
				Evidence:       "P3-005 failed in prod due to mock/prod divergence",
				Recommendation: "Use integration tests with real DB for migration-sensitive code",
				Confidence:     0.85,
				TimesConfirmed: 2,
				CreatedAt:      now,
			},
			{
				ID:             "L-002",
				Category:       "architecture",
				Pattern:        "Large PRs increase review latency",
				Evidence:       "P2-003 took 5 days to review",
				Recommendation: "Split PRs over 400 lines into smaller units",
				Confidence:     0.7,
				TimesConfirmed: 1,
				CreatedAt:      now,
			},
		},
	}

	if err := SaveLessons(dir, original); err != nil {
		t.Fatalf("SaveLessons: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, ".ralph", "lessons.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lessons.json not created: %v", err)
	}

	loaded, err := LoadLessons(dir)
	if err != nil {
		t.Fatalf("LoadLessons: %v", err)
	}

	if len(loaded.Lessons) != len(original.Lessons) {
		t.Fatalf("expected %d lessons, got %d", len(original.Lessons), len(loaded.Lessons))
	}

	for i, got := range loaded.Lessons {
		want := original.Lessons[i]
		if got.ID != want.ID {
			t.Errorf("lesson[%d].ID = %q, want %q", i, got.ID, want.ID)
		}
		if got.Category != want.Category {
			t.Errorf("lesson[%d].Category = %q, want %q", i, got.Category, want.Category)
		}
		if got.Pattern != want.Pattern {
			t.Errorf("lesson[%d].Pattern = %q, want %q", i, got.Pattern, want.Pattern)
		}
		if got.Evidence != want.Evidence {
			t.Errorf("lesson[%d].Evidence = %q, want %q", i, got.Evidence, want.Evidence)
		}
		if got.Recommendation != want.Recommendation {
			t.Errorf("lesson[%d].Recommendation = %q, want %q", i, got.Recommendation, want.Recommendation)
		}
		if got.Confidence != want.Confidence {
			t.Errorf("lesson[%d].Confidence = %f, want %f", i, got.Confidence, want.Confidence)
		}
		if got.TimesConfirmed != want.TimesConfirmed {
			t.Errorf("lesson[%d].TimesConfirmed = %d, want %d", i, got.TimesConfirmed, want.TimesConfirmed)
		}
		if !got.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("lesson[%d].CreatedAt = %v, want %v", i, got.CreatedAt, want.CreatedAt)
		}
	}
}

func TestLoadLessonsFileNotExist(t *testing.T) {
	dir := t.TempDir()

	lf, err := LoadLessons(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(lf.Lessons) != 0 {
		t.Errorf("expected empty lessons, got %d", len(lf.Lessons))
	}
}

func TestSaveLessonsEmpty(t *testing.T) {
	dir := t.TempDir()

	if err := SaveLessons(dir, LessonsFile{}); err != nil {
		t.Fatalf("SaveLessons with empty: %v", err)
	}

	loaded, err := LoadLessons(dir)
	if err != nil {
		t.Fatalf("LoadLessons: %v", err)
	}
	if loaded.Lessons != nil && len(loaded.Lessons) != 0 {
		t.Errorf("expected nil or empty lessons, got %d", len(loaded.Lessons))
	}
}
