package interactive

import (
	"testing"

	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/prd"
)

func TestCreateStory_IDAutoIncrement(t *testing.T) {
	sc := NewStoryCreator()
	s1 := sc.CreateStory("first task", "")
	s2 := sc.CreateStory("second task", "")
	s3 := sc.CreateStory("third task", "")

	if s1.ID != "T-001" {
		t.Errorf("expected T-001, got %s", s1.ID)
	}
	if s2.ID != "T-002" {
		t.Errorf("expected T-002, got %s", s2.ID)
	}
	if s3.ID != "T-003" {
		t.Errorf("expected T-003, got %s", s3.ID)
	}
}

func TestCreateStory_TitleTruncation(t *testing.T) {
	sc := NewStoryCreator()
	longText := "This is a very long task description that exceeds eighty characters and should be truncated for the title field"
	s := sc.CreateStory(longText, "")

	if len(s.Title) > 80 {
		t.Errorf("title should be at most 80 chars, got %d", len(s.Title))
	}
	if s.Description != longText {
		t.Error("description should be the full task text")
	}
}

func TestCreateStory_ClarificationQA(t *testing.T) {
	sc := NewStoryCreator()
	s := sc.CreateStory("build a widget", "Q: What color?\nA: Blue")

	expected := "build a widget\n\n## Clarification Q&A\nQ: What color?\nA: Blue"
	if s.Description != expected {
		t.Errorf("description mismatch:\ngot:  %q\nwant: %q", s.Description, expected)
	}
}

func TestCreateStory_Defaults(t *testing.T) {
	sc := NewStoryCreator()
	s := sc.CreateStory("do something", "")

	if s.Priority != 0 {
		t.Errorf("expected priority 0, got %d", s.Priority)
	}
	if len(s.DependsOn) != 0 {
		t.Errorf("expected empty DependsOn, got %v", s.DependsOn)
	}
	if s.Passes {
		t.Error("expected Passes to be false")
	}
}

func TestCreateAndAppend_PRDAndDAG(t *testing.T) {
	sc := NewStoryCreator()
	p := &prd.PRD{
		Project:     "test",
		UserStories: []prd.UserStory{},
	}
	d := dag.FromPRD(p.UserStories)

	s := sc.CreateAndAppend("build feature X", "", p, d)

	// Verify story appended to PRD
	if len(p.UserStories) != 1 {
		t.Fatalf("expected 1 story in PRD, got %d", len(p.UserStories))
	}
	if p.UserStories[0].ID != s.ID {
		t.Errorf("PRD story ID mismatch: got %s, want %s", p.UserStories[0].ID, s.ID)
	}

	// Verify story added to DAG
	if _, ok := d.Nodes[s.ID]; !ok {
		t.Error("story not found in DAG nodes")
	}

	// Verify ScheduleReady picks it up
	ready := d.Ready(map[string]bool{})
	if len(ready) != 1 || ready[0] != s.ID {
		t.Errorf("expected story %s to be ready, got %v", s.ID, ready)
	}
}

func TestCreateAndAppend_MultipleStories(t *testing.T) {
	sc := NewStoryCreator()
	p := &prd.PRD{UserStories: []prd.UserStory{}}
	d := dag.FromPRD(p.UserStories)

	sc.CreateAndAppend("task 1", "", p, d)
	sc.CreateAndAppend("task 2", "", p, d)

	if len(p.UserStories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(p.UserStories))
	}

	// Both should be ready (no dependencies)
	ready := d.Ready(map[string]bool{})
	if len(ready) != 2 {
		t.Errorf("expected 2 ready stories, got %d", len(ready))
	}
}
