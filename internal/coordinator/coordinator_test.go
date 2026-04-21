package coordinator

import (
	"context"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/checkpoint"
	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/dag"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

func TestNewFromCheckpoint(t *testing.T) {
	d := &dag.DAG{Nodes: map[string]*dag.StoryNode{
		"S-001": {StoryID: "S-001"},
		"S-002": {StoryID: "S-002", DependsOn: []string{"S-001"}},
		"S-003": {StoryID: "S-003"},
	}}

	stories := []prd.UserStory{
		{ID: "S-002", Title: "Story 2"},
		{ID: "S-003", Title: "Story 3"},
	}

	completedIDs := []string{"S-001"}
	failedStories := map[string]checkpoint.FailedStory{
		"S-003": {Retries: 2, LastError: "timeout"},
	}

	c := NewFromCheckpoint(
		&config.Config{PRDFile: "/dev/null"},
		d, 2, stories, completedIDs, failedStories, 5,
	)

	if !c.completed["S-001"] {
		t.Error("S-001 should be marked completed")
	}
	if !c.failed["S-003"] {
		t.Error("S-003 should be marked failed")
	}
	if c.failedErrors["S-003"] != "timeout" {
		t.Errorf("S-003 error: want 'timeout', got %q", c.failedErrors["S-003"])
	}
	if c.iterationCount != 5 {
		t.Errorf("iterationCount: want 5, got %d", c.iterationCount)
	}
}

func TestNewFromCheckpoint_ScheduleSkipsCompletedAndFailed(t *testing.T) {
	// DAG: S-001 (completed), S-002 depends on S-001, S-003 (failed)
	d := &dag.DAG{Nodes: map[string]*dag.StoryNode{
		"S-001": {StoryID: "S-001"},
		"S-002": {StoryID: "S-002", DependsOn: []string{"S-001"}},
		"S-003": {StoryID: "S-003"},
	}}

	stories := []prd.UserStory{
		{ID: "S-002", Title: "Story 2"},
		{ID: "S-003", Title: "Story 3"},
	}

	c := NewFromCheckpoint(
		&config.Config{PRDFile: "/dev/null"},
		d, 3, stories,
		[]string{"S-001"},
		map[string]checkpoint.FailedStory{"S-003": {Retries: 1, LastError: "err"}},
		2,
	)

	// ScheduleReady should only launch S-002 (S-001 completed, S-003 failed)
	// We can't actually launch workers without a real config, but we can verify
	// the ready list filtering by checking state.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so workers don't actually run

	launched := c.ScheduleReady(ctx)

	// S-002's deps are met (S-001 completed), S-003 is failed, S-001 is completed
	// So only S-002 should be scheduled. But since ctx is cancelled, the worker
	// goroutine will exit quickly. We just verify the count.
	if launched != 1 {
		t.Errorf("expected 1 worker launched, got %d", launched)
	}

	// Verify S-002 is in progress
	if _, ok := c.inProgress["S-002"]; !ok {
		t.Error("S-002 should be in progress")
	}
	// Verify S-001 and S-003 are NOT in progress
	if _, ok := c.inProgress["S-001"]; ok {
		t.Error("S-001 (completed) should not be in progress")
	}
	if _, ok := c.inProgress["S-003"]; ok {
		t.Error("S-003 (failed) should not be in progress")
	}
}

func TestAddStory_SchedulesInteractiveTask(t *testing.T) {
	// Start with an empty DAG and no stories
	d := &dag.DAG{Nodes: make(map[string]*dag.StoryNode)}
	c := New(&config.Config{PRDFile: "/dev/null", Workers: 2}, d, 2, nil)

	// Dynamically add an interactive task story
	story := &prd.UserStory{
		ID:          "T-001",
		Title:       "Fix the login page",
		Description: "Fix the login page styling",
		DependsOn:   []string{},
		Priority:    0,
	}
	d.AddNode(story.ID, nil, 0)
	c.AddStory(story)

	// ScheduleReady should pick it up
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	launched := c.ScheduleReady(ctx)
	if launched != 1 {
		t.Errorf("expected 1 worker launched for interactive task, got %d", launched)
	}
	if _, ok := c.inProgress["T-001"]; !ok {
		t.Error("T-001 should be in progress")
	}
}

func TestAddStory_MultipleInteractiveTasksParallel(t *testing.T) {
	d := &dag.DAG{Nodes: make(map[string]*dag.StoryNode)}
	c := New(&config.Config{PRDFile: "/dev/null", Workers: 3}, d, 3, nil)

	// Add multiple interactive tasks
	for _, id := range []string{"T-001", "T-002", "T-003"} {
		story := &prd.UserStory{ID: id, Title: id, DependsOn: []string{}, Priority: 0}
		d.AddNode(id, nil, 0)
		c.AddStory(story)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	launched := c.ScheduleReady(ctx)
	if launched != 3 {
		t.Errorf("expected 3 workers launched in parallel, got %d", launched)
	}
}

func TestAddStory_WithoutDAGNode_NotScheduled(t *testing.T) {
	d := &dag.DAG{Nodes: make(map[string]*dag.StoryNode)}
	c := New(&config.Config{PRDFile: "/dev/null", Workers: 2}, d, 2, nil)

	// Add story to coordinator but NOT to DAG
	story := &prd.UserStory{ID: "T-001", Title: "Test", DependsOn: []string{}, Priority: 0}
	c.AddStory(story)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	launched := c.ScheduleReady(ctx)
	if launched != 0 {
		t.Errorf("expected 0 workers (not in DAG), got %d", launched)
	}
}
