package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyState_SelectiveStoryCopy(t *testing.T) {
	// Setup: create a fake main dir with .ralph/stories for multiple stories
	mainDir := t.TempDir()
	wsDir := t.TempDir()

	// Create prd.json
	if err := os.WriteFile(filepath.Join(mainDir, "prd.json"), []byte(`{"stories":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create progress.md
	if err := os.WriteFile(filepath.Join(mainDir, "progress.md"), []byte("# Progress"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create story state for P1-005 (the target story)
	story5Dir := filepath.Join(mainDir, ".ralph", "stories", "P1-005")
	if err := os.MkdirAll(story5Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(story5Dir, "state.json"), []byte(`{"story_id":"P1-005"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(story5Dir, "plan.md"), []byte("# Plan for P1-005"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create story state for P1-003 (should NOT be copied)
	story3Dir := filepath.Join(mainDir, ".ralph", "stories", "P1-003")
	if err := os.MkdirAll(story3Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(story3Dir, "state.json"), []byte(`{"story_id":"P1-003"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a non-story file in .ralph/ (should be copied)
	if err := os.WriteFile(filepath.Join(mainDir, ".ralph", "checkpoint.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := CopyState(mainDir, wsDir, "P1-005"); err != nil {
		t.Fatalf("CopyState failed: %v", err)
	}

	// Assert: prd.json copied
	if _, err := os.Stat(filepath.Join(wsDir, "prd.json")); err != nil {
		t.Error("prd.json was not copied")
	}

	// Assert: progress.md copied
	if _, err := os.Stat(filepath.Join(wsDir, "progress.md")); err != nil {
		t.Error("progress.md was not copied")
	}

	// Assert: P1-005 state was copied
	if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "stories", "P1-005", "state.json")); err != nil {
		t.Error("P1-005/state.json was not copied")
	}
	if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "stories", "P1-005", "plan.md")); err != nil {
		t.Error("P1-005/plan.md was not copied")
	}

	// Assert: P1-003 state was NOT copied
	if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "stories", "P1-003")); !os.IsNotExist(err) {
		t.Error("P1-003 story state should not have been copied, but it exists")
	}

	// Assert: non-story .ralph files were copied
	if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "checkpoint.json")); err != nil {
		t.Error("checkpoint.json was not copied")
	}
}

func TestCopyState_NoRalphDir(t *testing.T) {
	// Setup: main dir with no .ralph/ directory at all
	mainDir := t.TempDir()
	wsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(mainDir, "prd.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act: should not error when .ralph/ doesn't exist
	if err := CopyState(mainDir, wsDir, "P1-005"); err != nil {
		t.Fatalf("CopyState should handle missing .ralph/ gracefully, got: %v", err)
	}

	// Assert: prd.json still copied
	if _, err := os.Stat(filepath.Join(wsDir, "prd.json")); err != nil {
		t.Error("prd.json was not copied")
	}
}

func TestCopyState_NoStoriesDir(t *testing.T) {
	// Setup: .ralph/ exists but no stories/ subdirectory
	mainDir := t.TempDir()
	wsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(mainDir, "prd.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ralphDir := filepath.Join(mainDir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ralphDir, "checkpoint.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := CopyState(mainDir, wsDir, "P1-005"); err != nil {
		t.Fatalf("CopyState should handle missing stories/ gracefully, got: %v", err)
	}

	// Assert: .ralph/checkpoint.json copied
	if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "checkpoint.json")); err != nil {
		t.Error("checkpoint.json was not copied")
	}
}

func TestCopyState_NoStoryIDDir(t *testing.T) {
	// Setup: .ralph/stories/ exists but the specific story dir doesn't
	mainDir := t.TempDir()
	wsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(mainDir, "prd.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	storiesDir := filepath.Join(mainDir, ".ralph", "stories")
	if err := os.MkdirAll(storiesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act: story P1-099 doesn't exist yet
	if err := CopyState(mainDir, wsDir, "P1-099"); err != nil {
		t.Fatalf("CopyState should handle missing story dir gracefully, got: %v", err)
	}
}

func TestCopyState_EmptyStoryID(t *testing.T) {
	// When storyID is empty, all stories should be copied (backwards compat)
	mainDir := t.TempDir()
	wsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(mainDir, "prd.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create two story dirs
	for _, id := range []string{"P1-001", "P1-002"} {
		dir := filepath.Join(mainDir, ".ralph", "stories", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Act: empty storyID means copy all
	if err := CopyState(mainDir, wsDir, ""); err != nil {
		t.Fatalf("CopyState failed: %v", err)
	}

	// Assert: both stories copied
	for _, id := range []string{"P1-001", "P1-002"} {
		if _, err := os.Stat(filepath.Join(wsDir, ".ralph", "stories", id, "state.json")); err != nil {
			t.Errorf("%s/state.json was not copied", id)
		}
	}
}
