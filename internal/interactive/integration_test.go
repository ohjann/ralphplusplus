package interactive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eoghanhynes/ralph/internal/checkpoint"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/prd"
)

// TestFullLifecycle_CreateStory_DAG_Checkpoint exercises the complete interactive
// task lifecycle: create stories, verify DAG readiness, save checkpoint with
// interactive tasks, restore checkpoint, and verify round-trip integrity.
func TestFullLifecycle_CreateStory_DAG_Checkpoint(t *testing.T) {
	dir := t.TempDir()

	// Start with an empty PRD (simulates no prd.json / NoPRD mode)
	p := &prd.PRD{
		Project:     "test-project",
		UserStories: []prd.UserStory{},
	}
	d := dag.FromPRD(p.UserStories)
	sc := NewStoryCreator()

	// Create first interactive task (no clarification)
	s1 := sc.CreateAndAppend("Fix the login bug", "", p, d)
	if s1.ID != "T-001" {
		t.Fatalf("first story ID: got %s, want T-001", s1.ID)
	}

	// Create second interactive task (with clarification Q&A)
	qa := "Q: Which login page?\nA: The main OAuth flow"
	s2 := sc.CreateAndAppend("Add rate limiting to API", qa, p, d)
	if s2.ID != "T-002" {
		t.Fatalf("second story ID: got %s, want T-002", s2.ID)
	}

	// Verify both stories are in the PRD
	if len(p.UserStories) != 2 {
		t.Fatalf("PRD stories: got %d, want 2", len(p.UserStories))
	}

	// Verify both are immediately ready in the DAG (empty DependsOn)
	ready := d.Ready(map[string]bool{})
	if len(ready) != 2 {
		t.Fatalf("ready stories: got %d, want 2", len(ready))
	}

	// Mark first as completed
	completed := map[string]bool{"T-001": true}
	ready = d.Ready(completed)
	if len(ready) != 1 || ready[0] != "T-002" {
		t.Errorf("after completing T-001, ready: got %v, want [T-002]", ready)
	}

	// Save checkpoint with interactive tasks
	dagEdges := make(map[string][]string)
	for id, node := range d.Nodes {
		dagEdges[id] = node.DependsOn
	}
	cp := checkpoint.Checkpoint{
		PRDHash:          "test-hash",
		Phase:            "interactive",
		CompletedStories: []string{"T-001"},
		FailedStories:    map[string]FailedStory{},
		InProgress:       []string{"T-002"},
		DAG:              dagEdges,
		IterationCount:   1,
	}
	if err := checkpoint.Save(dir, cp); err != nil {
		t.Fatalf("checkpoint.Save: %v", err)
	}

	// Restore checkpoint
	loaded, exists, err := checkpoint.Load(dir)
	if err != nil {
		t.Fatalf("checkpoint.Load: %v", err)
	}
	if !exists {
		t.Fatal("checkpoint should exist after save")
	}

	// Verify interactive tasks survived round-trip
	if len(loaded.CompletedStories) != 1 || loaded.CompletedStories[0] != "T-001" {
		t.Errorf("completed stories: got %v, want [T-001]", loaded.CompletedStories)
	}
	if len(loaded.InProgress) != 1 || loaded.InProgress[0] != "T-002" {
		t.Errorf("in-progress: got %v, want [T-002]", loaded.InProgress)
	}
	if len(loaded.DAG) != 2 {
		t.Errorf("DAG edges: got %d entries, want 2", len(loaded.DAG))
	}
	for id, deps := range loaded.DAG {
		if len(deps) != 0 {
			t.Errorf("DAG[%s] deps: got %v, want empty", id, deps)
		}
	}

	// Reconstruct DAG from checkpoint and verify interactive tasks are still ready
	restored := dag.FromCheckpoint(loaded.DAG, p.UserStories)
	restoredReady := restored.Ready(map[string]bool{"T-001": true})
	if len(restoredReady) != 1 || restoredReady[0] != "T-002" {
		t.Errorf("restored DAG ready: got %v, want [T-002]", restoredReady)
	}

	// Save session and verify interactive tasks are persisted
	sessionPath, err := SaveSession(dir, p.UserStories)
	if err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if sessionPath == "" {
		t.Fatal("expected non-empty session path")
	}

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("reading session: %v", err)
	}
	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parsing session: %v", err)
	}
	if len(sf.Tasks) != 2 {
		t.Fatalf("session tasks: got %d, want 2", len(sf.Tasks))
	}
	if sf.Tasks[1].ClarificationQA != "Q: Which login page?\nA: The main OAuth flow" {
		t.Errorf("session task 2 clarification QA: got %q", sf.Tasks[1].ClarificationQA)
	}
}

// FailedStory is a local alias for checkpoint.FailedStory to avoid import cycle confusion.
type FailedStory = checkpoint.FailedStory

// TestNoPRD_EmptyPRDStartsInteractiveReady verifies that an empty PRD
// (as created by main.go when no prd.json exists) results in a DAG
// that has no nodes and no ready stories — until an interactive task is added.
func TestNoPRD_EmptyPRDStartsInteractiveReady(t *testing.T) {
	p := &prd.PRD{
		Project:     "my-project",
		UserStories: []prd.UserStory{},
	}
	d := dag.FromPRD(p.UserStories)

	// Empty DAG has nothing ready
	ready := d.Ready(map[string]bool{})
	if len(ready) != 0 {
		t.Errorf("empty DAG should have no ready stories, got %v", ready)
	}

	// Add an interactive task — should become immediately ready
	sc := NewStoryCreator()
	s := sc.CreateAndAppend("Write tests", "", p, d)

	ready = d.Ready(map[string]bool{})
	if len(ready) != 1 || ready[0] != s.ID {
		t.Errorf("after adding task, ready: got %v, want [%s]", ready, s.ID)
	}
}

// TestConcurrentStoryCreation verifies that concurrent CreateAndAppend calls
// produce unique sequential IDs without data races.
func TestConcurrentStoryCreation(t *testing.T) {
	sc := NewStoryCreator()
	p := &prd.PRD{UserStories: []prd.UserStory{}}
	d := dag.FromPRD(p.UserStories)

	const n = 20
	var wg sync.WaitGroup
	stories := make([]prd.UserStory, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stories[idx] = sc.CreateAndAppend("task", "", p, d)
		}(i)
	}
	wg.Wait()

	// All IDs should be unique
	seen := make(map[string]bool)
	for _, s := range stories {
		if seen[s.ID] {
			t.Errorf("duplicate ID: %s", s.ID)
		}
		seen[s.ID] = true
	}

	// PRD and DAG should have all n stories
	if len(p.UserStories) != n {
		t.Errorf("PRD stories: got %d, want %d", len(p.UserStories), n)
	}
	if len(d.Nodes) != n {
		t.Errorf("DAG nodes: got %d, want %d", len(d.Nodes), n)
	}

	// All should be ready (no dependencies)
	ready := d.Ready(map[string]bool{})
	if len(ready) != n {
		t.Errorf("ready stories: got %d, want %d", len(ready), n)
	}
}

// TestCheckpointRoundTrip_MixedPRDAndInteractive verifies that a checkpoint
// with both PRD stories and interactive tasks survives save/load.
func TestCheckpointRoundTrip_MixedPRDAndInteractive(t *testing.T) {
	dir := t.TempDir()

	cp := checkpoint.Checkpoint{
		PRDHash:          "abc123",
		Phase:            "parallel",
		CompletedStories: []string{"P1-001", "T-001"},
		FailedStories:    map[string]FailedStory{},
		InProgress:       []string{"P1-002", "T-002"},
		DAG: map[string][]string{
			"P1-001": {},
			"P1-002": {"P1-001"},
			"T-001":  {},
			"T-002":  {},
		},
		IterationCount: 3,
	}

	if err := checkpoint.Save(dir, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, exists, err := checkpoint.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !exists {
		t.Fatal("checkpoint should exist")
	}

	// Verify both PRD and interactive tasks present
	if len(loaded.CompletedStories) != 2 {
		t.Errorf("completed: got %d, want 2", len(loaded.CompletedStories))
	}
	if len(loaded.InProgress) != 2 {
		t.Errorf("in-progress: got %d, want 2", len(loaded.InProgress))
	}
	if len(loaded.DAG) != 4 {
		t.Errorf("DAG: got %d, want 4", len(loaded.DAG))
	}

	// Reconstruct DAG and verify mixed readiness
	stories := []prd.UserStory{
		{ID: "P1-001", Priority: 1},
		{ID: "P1-002", Priority: 2, DependsOn: []string{"P1-001"}},
		{ID: "T-001", Priority: 0},
		{ID: "T-002", Priority: 0},
	}
	restored := dag.FromCheckpoint(loaded.DAG, stories)
	completedSet := map[string]bool{"P1-001": true, "T-001": true}

	ready := restored.Ready(completedSet)
	// P1-002 depends on P1-001 (done) → ready. T-002 has no deps → ready.
	if len(ready) != 2 {
		t.Errorf("ready after restore: got %v, want 2 stories", ready)
	}
	readySet := map[string]bool{}
	for _, id := range ready {
		readySet[id] = true
	}
	if !readySet["T-002"] {
		t.Error("T-002 should be ready (no deps)")
	}
	if !readySet["P1-002"] {
		t.Error("P1-002 should be ready (P1-001 completed)")
	}
}

// TestSessionSaveRestore_RoundTrip verifies that session save captures
// interactive tasks and the data can be loaded back correctly.
func TestSessionSaveRestore_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	sc := NewStoryCreator()
	p := &prd.PRD{
		Project: "test",
		UserStories: []prd.UserStory{
			{ID: "P1-001", Title: "PRD story", Description: "existing"},
		},
	}
	d := dag.FromPRD(p.UserStories)

	// Create interactive tasks
	sc.CreateAndAppend("Build auth system", "Q: OAuth or JWT?\nA: JWT", p, d)
	sc.CreateAndAppend("Add logging", "", p, d)

	// Mark one as complete
	p.UserStories[1].Passes = true // T-001

	// Save session
	path, err := SaveSession(dir, p.UserStories)
	if err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Load and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading session: %v", err)
	}
	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parsing session: %v", err)
	}

	// Only interactive tasks (T-prefix) should be in session
	if len(sf.Tasks) != 2 {
		t.Fatalf("session tasks: got %d, want 2", len(sf.Tasks))
	}

	// First interactive task (T-001) should be complete with clarification
	if sf.Tasks[0].ID != "T-001" {
		t.Errorf("task 0 ID: got %s, want T-001", sf.Tasks[0].ID)
	}
	if !sf.Tasks[0].Completed {
		t.Error("T-001 should be marked completed")
	}
	if sf.Tasks[0].ClarificationQA != "Q: OAuth or JWT?\nA: JWT" {
		t.Errorf("T-001 clarification QA: got %q", sf.Tasks[0].ClarificationQA)
	}
	if sf.Tasks[0].Description != "Build auth system" {
		t.Errorf("T-001 description: got %q, want task text only", sf.Tasks[0].Description)
	}

	// Second interactive task (T-002) should be incomplete, no clarification
	if sf.Tasks[1].ID != "T-002" {
		t.Errorf("task 1 ID: got %s, want T-002", sf.Tasks[1].ID)
	}
	if sf.Tasks[1].Completed {
		t.Error("T-002 should not be completed")
	}
	if sf.Tasks[1].ClarificationQA != "" {
		t.Errorf("T-002 should have no clarification QA, got %q", sf.Tasks[1].ClarificationQA)
	}
}

// TestDAG_InteractiveTasksAlwaysReady verifies that interactive tasks
// (empty DependsOn) are always in the ready set regardless of other stories.
func TestDAG_InteractiveTasksAlwaysReady(t *testing.T) {
	stories := []prd.UserStory{
		{ID: "P1-001", Priority: 1, DependsOn: []string{}},
		{ID: "P1-002", Priority: 2, DependsOn: []string{"P1-001"}},
		{ID: "P1-003", Priority: 3, DependsOn: []string{"P1-002"}},
	}
	d := dag.FromPRD(stories)

	// Add interactive tasks dynamically
	d.AddNode("T-001", nil, 0)
	d.AddNode("T-002", nil, 0)

	// With nothing completed, only P1-001 (no deps) and both T- tasks should be ready
	ready := d.Ready(map[string]bool{})
	readySet := map[string]bool{}
	for _, id := range ready {
		readySet[id] = true
	}
	if !readySet["T-001"] || !readySet["T-002"] {
		t.Errorf("interactive tasks should be ready, got %v", ready)
	}
	if !readySet["P1-001"] {
		t.Error("P1-001 (no deps) should be ready")
	}
	if readySet["P1-002"] || readySet["P1-003"] {
		t.Error("P1-002 and P1-003 should NOT be ready (deps not met)")
	}

	// Even with P1-001 completed, interactive tasks remain ready
	completed := map[string]bool{"P1-001": true}
	ready = d.Ready(completed)
	readySet = map[string]bool{}
	for _, id := range ready {
		readySet[id] = true
	}
	if !readySet["T-001"] || !readySet["T-002"] {
		t.Errorf("interactive tasks should still be ready, got %v", ready)
	}
}

// TestClarificationQA_BundledIntoDescription verifies that clarification Q&A
// is correctly concatenated into the story description and can be split back.
func TestClarificationQA_BundledIntoDescription(t *testing.T) {
	sc := NewStoryCreator()

	tests := []struct {
		name     string
		task     string
		qa       string
		wantDesc string
		wantSplit struct {
			desc string
			qa   string
		}
	}{
		{
			name:     "no clarification",
			task:     "Fix the bug",
			qa:       "",
			wantDesc: "Fix the bug",
			wantSplit: struct {
				desc string
				qa   string
			}{"Fix the bug", ""},
		},
		{
			name:     "with clarification",
			task:     "Add caching",
			qa:       "Q: Redis or memcached?\nA: Redis",
			wantDesc: "Add caching\n\n## Clarification Q&A\nQ: Redis or memcached?\nA: Redis",
			wantSplit: struct {
				desc string
				qa   string
			}{"Add caching", "Q: Redis or memcached?\nA: Redis"},
		},
		{
			name:     "multi-question clarification",
			task:     "Build dashboard",
			qa:       "Q: Which metrics?\nA: CPU and memory\nQ: Time range?\nA: Last 24h",
			wantDesc: "Build dashboard\n\n## Clarification Q&A\nQ: Which metrics?\nA: CPU and memory\nQ: Time range?\nA: Last 24h",
			wantSplit: struct {
				desc string
				qa   string
			}{"Build dashboard", "Q: Which metrics?\nA: CPU and memory\nQ: Time range?\nA: Last 24h"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sc.CreateStory(tt.task, tt.qa)
			if s.Description != tt.wantDesc {
				t.Errorf("description:\ngot:  %q\nwant: %q", s.Description, tt.wantDesc)
			}

			// Verify SaveSession can split it back correctly
			dir := t.TempDir()
			stories := []prd.UserStory{s}
			path, err := SaveSession(dir, stories)
			if err != nil {
				t.Fatalf("SaveSession: %v", err)
			}

			data, _ := os.ReadFile(path)
			var sf SessionFile
			_ = json.Unmarshal(data, &sf)

			if sf.Tasks[0].Description != tt.wantSplit.desc {
				t.Errorf("split desc: got %q, want %q", sf.Tasks[0].Description, tt.wantSplit.desc)
			}
			if sf.Tasks[0].ClarificationQA != tt.wantSplit.qa {
				t.Errorf("split qa: got %q, want %q", sf.Tasks[0].ClarificationQA, tt.wantSplit.qa)
			}
		})
	}
}

// TestConfigNoPRD_PRDFileAbsent verifies that when no prd.json exists,
// an empty PRD with the project name works correctly with the interactive system.
func TestConfigNoPRD_PRDFileAbsent(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.json")

	// Verify prd.json doesn't exist
	if _, err := os.Stat(prdPath); !os.IsNotExist(err) {
		t.Fatal("prd.json should not exist initially")
	}

	// Simulate what main.go does: create empty PRD
	emptyPRD := &prd.PRD{
		Project:     "test-project",
		UserStories: []prd.UserStory{},
	}

	// Save it (as main.go does)
	if err := prd.Save(prdPath, emptyPRD); err != nil {
		t.Fatalf("prd.Save: %v", err)
	}

	// Load it back (as TUI Init does)
	loaded, err := prd.Load(prdPath)
	if err != nil {
		t.Fatalf("prd.Load: %v", err)
	}
	if loaded.Project != "test-project" {
		t.Errorf("project: got %q, want test-project", loaded.Project)
	}
	if len(loaded.UserStories) != 0 {
		t.Errorf("stories: got %d, want 0", len(loaded.UserStories))
	}

	// Create DAG from empty PRD — should have no nodes
	d := dag.FromPRD(loaded.UserStories)
	if len(d.Nodes) != 0 {
		t.Errorf("empty PRD DAG should have 0 nodes, got %d", len(d.Nodes))
	}

	// Add interactive task — should work correctly
	sc := NewStoryCreator()
	s := sc.CreateAndAppend("First task", "", loaded, d)

	if s.ID != "T-001" {
		t.Errorf("first task ID: got %s, want T-001", s.ID)
	}
	if len(loaded.UserStories) != 1 {
		t.Errorf("stories after task: got %d, want 1", len(loaded.UserStories))
	}

	ready := d.Ready(map[string]bool{})
	if len(ready) != 1 || ready[0] != "T-001" {
		t.Errorf("ready: got %v, want [T-001]", ready)
	}
}
