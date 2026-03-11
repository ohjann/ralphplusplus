package storystate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveCreatesDirectoryAndValidJSON(t *testing.T) {
	dir := t.TempDir()
	state := StoryState{
		StoryID:        "TEST-001",
		Status:         StatusInProgress,
		IterationCount: 1,
		FilesTouched:   []string{"file1.go"},
		Subtasks:       []Subtask{{Description: "task1", Done: false}},
		ErrorsEncountered: []ErrorEntry{{Error: "err1", Resolution: "fix1"}},
		JudgeFeedback:  []string{"feedback1"},
		LastUpdated:    time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC),
	}

	if err := Save(dir, state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify directory structure
	stateFile := filepath.Join(dir, ".ralph", "stories", "TEST-001", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state.json not created: %v", err)
	}

	// Verify valid JSON with snake_case keys
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"story_id", "status", "iteration_count", "files_touched", "subtasks", "errors_encountered", "judge_feedback", "last_updated"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected JSON key: %s", key)
		}
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	original := StoryState{
		StoryID:        "TEST-002",
		Status:         StatusComplete,
		IterationCount: 3,
		FilesTouched:   []string{"a.go", "b.go"},
		Subtasks: []Subtask{
			{Description: "sub1", Done: true},
			{Description: "sub2", Done: false},
		},
		ErrorsEncountered: []ErrorEntry{
			{Error: "compile error", Resolution: "fixed import"},
			{Error: "test failure", Resolution: "updated assertion"},
		},
		JudgeFeedback: []string{"looks good", "needs refactor"},
		LastUpdated:   now,
	}

	if err := Save(dir, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(dir, "TEST-002")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Compare all fields
	if loaded.StoryID != original.StoryID {
		t.Errorf("StoryID: got %q, want %q", loaded.StoryID, original.StoryID)
	}
	if loaded.Status != original.Status {
		t.Errorf("Status: got %q, want %q", loaded.Status, original.Status)
	}
	if loaded.IterationCount != original.IterationCount {
		t.Errorf("IterationCount: got %d, want %d", loaded.IterationCount, original.IterationCount)
	}
	if len(loaded.FilesTouched) != len(original.FilesTouched) {
		t.Fatalf("FilesTouched length: got %d, want %d", len(loaded.FilesTouched), len(original.FilesTouched))
	}
	for i, f := range loaded.FilesTouched {
		if f != original.FilesTouched[i] {
			t.Errorf("FilesTouched[%d]: got %q, want %q", i, f, original.FilesTouched[i])
		}
	}
	if len(loaded.Subtasks) != len(original.Subtasks) {
		t.Fatalf("Subtasks length: got %d, want %d", len(loaded.Subtasks), len(original.Subtasks))
	}
	for i, s := range loaded.Subtasks {
		if s.Description != original.Subtasks[i].Description || s.Done != original.Subtasks[i].Done {
			t.Errorf("Subtasks[%d]: got %+v, want %+v", i, s, original.Subtasks[i])
		}
	}
	if len(loaded.ErrorsEncountered) != len(original.ErrorsEncountered) {
		t.Fatalf("ErrorsEncountered length: got %d, want %d", len(loaded.ErrorsEncountered), len(original.ErrorsEncountered))
	}
	for i, e := range loaded.ErrorsEncountered {
		if e.Error != original.ErrorsEncountered[i].Error || e.Resolution != original.ErrorsEncountered[i].Resolution {
			t.Errorf("ErrorsEncountered[%d]: got %+v, want %+v", i, e, original.ErrorsEncountered[i])
		}
	}
	if len(loaded.JudgeFeedback) != len(original.JudgeFeedback) {
		t.Fatalf("JudgeFeedback length: got %d, want %d", len(loaded.JudgeFeedback), len(original.JudgeFeedback))
	}
	for i, f := range loaded.JudgeFeedback {
		if f != original.JudgeFeedback[i] {
			t.Errorf("JudgeFeedback[%d]: got %q, want %q", i, f, original.JudgeFeedback[i])
		}
	}
	if !loaded.LastUpdated.Equal(original.LastUpdated) {
		t.Errorf("LastUpdated: got %v, want %v", loaded.LastUpdated, original.LastUpdated)
	}
}

func TestLoadNonExistentReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	state, err := Load(dir, "NONEXISTENT")
	if err != nil {
		t.Fatalf("Load() should not error for missing file: %v", err)
	}
	if state.StoryID != "" || state.Status != "" || state.IterationCount != 0 ||
		len(state.FilesTouched) != 0 || len(state.Subtasks) != 0 ||
		len(state.ErrorsEncountered) != 0 || len(state.JudgeFeedback) != 0 ||
		!state.LastUpdated.IsZero() {
		t.Errorf("expected zero-value StoryState, got %+v", state)
	}
}

func TestLoadPlanNonExistentReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	plan, err := LoadPlan(dir, "NONEXISTENT")
	if err != nil {
		t.Fatalf("LoadPlan() should not error for missing file: %v", err)
	}
	if plan != "" {
		t.Errorf("expected empty string, got %q", plan)
	}
}

func TestLoadDecisionsNonExistentReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	decisions, err := LoadDecisions(dir, "NONEXISTENT")
	if err != nil {
		t.Fatalf("LoadDecisions() should not error for missing file: %v", err)
	}
	if decisions != "" {
		t.Errorf("expected empty string, got %q", decisions)
	}
}

func TestLoadPlanReturnsContents(t *testing.T) {
	dir := t.TempDir()
	storyDir := filepath.Join(dir, ".ralph", "stories", "TEST-003")
	if err := os.MkdirAll(storyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# Implementation Plan\n\n1. Do thing A\n2. Do thing B\n"
	if err := os.WriteFile(filepath.Join(storyDir, "plan.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := LoadPlan(dir, "TEST-003")
	if err != nil {
		t.Fatalf("LoadPlan() error: %v", err)
	}
	if plan != content {
		t.Errorf("LoadPlan() got %q, want %q", plan, content)
	}
}

func TestSaveAllStatusValues(t *testing.T) {
	statuses := []string{
		StatusInProgress,
		StatusBlocked,
		StatusContextExhausted,
		StatusComplete,
		StatusFailed,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			state := StoryState{
				StoryID: "STATUS-" + status,
				Status:  status,
			}
			if err := Save(dir, state); err != nil {
				t.Fatalf("Save() error for status %q: %v", status, err)
			}

			loaded, err := Load(dir, "STATUS-"+status)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if loaded.Status != status {
				t.Errorf("Status: got %q, want %q", loaded.Status, status)
			}
		})
	}
}

func TestSubtasksAndErrorsSerialization(t *testing.T) {
	dir := t.TempDir()
	state := StoryState{
		StoryID: "MULTI-001",
		Status:  StatusInProgress,
		Subtasks: []Subtask{
			{Description: "Setup database", Done: true},
			{Description: "Write migrations", Done: true},
			{Description: "Add API endpoints", Done: false},
			{Description: "Write tests", Done: false},
		},
		ErrorsEncountered: []ErrorEntry{
			{Error: "connection refused", Resolution: "started database"},
			{Error: "missing column", Resolution: "added migration"},
			{Error: "timeout", Resolution: "increased limit"},
		},
	}

	if err := Save(dir, state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(dir, "MULTI-001")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded.Subtasks) != 4 {
		t.Fatalf("Subtasks: got %d, want 4", len(loaded.Subtasks))
	}
	if !loaded.Subtasks[0].Done || !loaded.Subtasks[1].Done {
		t.Error("first two subtasks should be done")
	}
	if loaded.Subtasks[2].Done || loaded.Subtasks[3].Done {
		t.Error("last two subtasks should not be done")
	}

	if len(loaded.ErrorsEncountered) != 3 {
		t.Fatalf("ErrorsEncountered: got %d, want 3", len(loaded.ErrorsEncountered))
	}
	if loaded.ErrorsEncountered[2].Error != "timeout" {
		t.Errorf("ErrorsEncountered[2].Error: got %q, want %q", loaded.ErrorsEncountered[2].Error, "timeout")
	}
}
