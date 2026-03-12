package dag

import (
	"testing"

	"github.com/eoghanhynes/ralph/internal/prd"
)

func TestFromCheckpoint(t *testing.T) {
	edges := map[string][]string{
		"S-001": {},
		"S-002": {"S-001"},
		"S-003": {"S-001", "S-002"},
	}
	stories := []prd.UserStory{
		{ID: "S-001", Priority: 1},
		{ID: "S-002", Priority: 2},
		{ID: "S-003", Priority: 3},
	}

	d := FromCheckpoint(edges, stories)

	if len(d.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(d.Nodes))
	}

	// Check S-001 has no deps
	if len(d.Nodes["S-001"].DependsOn) != 0 {
		t.Errorf("S-001 should have no deps, got %v", d.Nodes["S-001"].DependsOn)
	}

	// Check S-002 depends on S-001
	if len(d.Nodes["S-002"].DependsOn) != 1 || d.Nodes["S-002"].DependsOn[0] != "S-001" {
		t.Errorf("S-002 should depend on S-001, got %v", d.Nodes["S-002"].DependsOn)
	}

	// Check S-003 depends on S-001 and S-002
	if len(d.Nodes["S-003"].DependsOn) != 2 {
		t.Errorf("S-003 should have 2 deps, got %v", d.Nodes["S-003"].DependsOn)
	}

	// Check priorities
	if d.Nodes["S-001"].Priority != 1 {
		t.Errorf("S-001 priority: want 1, got %d", d.Nodes["S-001"].Priority)
	}
	if d.Nodes["S-002"].Priority != 2 {
		t.Errorf("S-002 priority: want 2, got %d", d.Nodes["S-002"].Priority)
	}
}

func TestFromCheckpoint_MissingPriority(t *testing.T) {
	edges := map[string][]string{
		"S-001": {},
		"S-099": {"S-001"}, // not in stories list
	}
	stories := []prd.UserStory{
		{ID: "S-001", Priority: 5},
	}

	d := FromCheckpoint(edges, stories)

	if d.Nodes["S-099"].Priority != 0 {
		t.Errorf("unknown story should get priority 0, got %d", d.Nodes["S-099"].Priority)
	}
	if d.Nodes["S-001"].Priority != 5 {
		t.Errorf("S-001 priority: want 5, got %d", d.Nodes["S-001"].Priority)
	}
}
