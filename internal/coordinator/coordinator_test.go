package coordinator

import (
	"context"
	"testing"

	"github.com/eoghanhynes/ralph/internal/checkpoint"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/prd"
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
