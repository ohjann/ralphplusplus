package interactive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eoghanhynes/ralph/internal/prd"
)

func TestSaveSession_InteractiveTasksOnly(t *testing.T) {
	dir := t.TempDir()
	stories := []prd.UserStory{
		{ID: "P1-001", Title: "PRD story", Description: "not interactive"},
		{ID: "T-001", Title: "Fix the bug", Description: "Fix the login bug", Passes: true},
		{ID: "T-002", Title: "Add feature", Description: "Add dark mode", Passes: false},
	}

	path, err := SaveSession(dir, stories)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading session file: %v", err)
	}

	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parsing session file: %v", err)
	}

	if len(sf.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(sf.Tasks))
	}
	if sf.Tasks[0].ID != "T-001" || !sf.Tasks[0].Completed {
		t.Errorf("task 0: got %+v", sf.Tasks[0])
	}
	if sf.Tasks[1].ID != "T-002" || sf.Tasks[1].Completed {
		t.Errorf("task 1: got %+v", sf.Tasks[1])
	}
}

func TestSaveSession_WithClarificationQA(t *testing.T) {
	dir := t.TempDir()
	stories := []prd.UserStory{
		{
			ID:          "T-001",
			Title:       "Build widget",
			Description: "Build a widget\n\n## Clarification Q&A\nQ: What color?\nA: Blue",
		},
	}

	path, err := SaveSession(dir, stories)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}

	data, _ := os.ReadFile(path)
	var sf SessionFile
	_ = json.Unmarshal(data, &sf)

	if sf.Tasks[0].Description != "Build a widget" {
		t.Errorf("description: got %q, want %q", sf.Tasks[0].Description, "Build a widget")
	}
	if sf.Tasks[0].ClarificationQA != "Q: What color?\nA: Blue" {
		t.Errorf("clarification QA: got %q", sf.Tasks[0].ClarificationQA)
	}
}

func TestSaveSession_NoInteractiveTasks(t *testing.T) {
	dir := t.TempDir()
	stories := []prd.UserStory{
		{ID: "P1-001", Title: "PRD story"},
	}

	path, err := SaveSession(dir, stories)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path for no interactive tasks, got %s", path)
	}
}

func TestSaveSession_EmptyStories(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveSession(dir, nil)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %s", path)
	}
}

func TestSaveSession_FileLocation(t *testing.T) {
	dir := t.TempDir()
	stories := []prd.UserStory{
		{ID: "T-001", Title: "task"},
	}

	path, err := SaveSession(dir, stories)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}

	// Verify file is in .ralph/ directory
	relPath, _ := filepath.Rel(dir, path)
	if !strings.HasPrefix(relPath, ".ralph/session-") {
		t.Errorf("unexpected path: %s", relPath)
	}
	if !strings.HasSuffix(relPath, ".json") {
		t.Errorf("expected .json suffix: %s", relPath)
	}
}
