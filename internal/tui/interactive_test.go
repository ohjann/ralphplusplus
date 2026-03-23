package tui

import (
	"testing"
)

func TestBuildClarifyDescription_SingleQuestion(t *testing.T) {
	m := &Model{
		clarifyingTask:   "Add a REST endpoint",
		clarifyQuestions: []string{"What HTTP method?"},
		clarifyAnswers:   []string{"POST"},
	}

	got := m.buildClarifyDescription()
	want := "Task: Add a REST endpoint\n\nQ: What HTTP method?\nA: POST"
	if got != want {
		t.Errorf("buildClarifyDescription() =\n%q\nwant:\n%q", got, want)
	}
}

func TestBuildClarifyDescription_MultipleQuestions(t *testing.T) {
	m := &Model{
		clarifyingTask:   "Build a widget",
		clarifyQuestions: []string{"What color?", "What size?", "Material?"},
		clarifyAnswers:   []string{"Blue", "Large", "Metal"},
	}

	got := m.buildClarifyDescription()
	want := "Task: Build a widget\n\nQ: What color?\nA: Blue\n\nQ: What size?\nA: Large\n\nQ: Material?\nA: Metal"
	if got != want {
		t.Errorf("buildClarifyDescription() =\n%q\nwant:\n%q", got, want)
	}
}

func TestBuildClarifyDescription_PartialAnswers(t *testing.T) {
	m := &Model{
		clarifyingTask:   "Fix the bug",
		clarifyQuestions: []string{"Which module?", "Priority?"},
		clarifyAnswers:   []string{"auth"},
		clarifyIndex:     1,
	}

	got := m.buildClarifyDescription()
	// Second answer should be empty since it's not yet provided
	want := "Task: Fix the bug\n\nQ: Which module?\nA: auth\n\nQ: Priority?\nA: "
	if got != want {
		t.Errorf("buildClarifyDescription() =\n%q\nwant:\n%q", got, want)
	}
}

func TestIsClarifying(t *testing.T) {
	m := &Model{}
	if m.isClarifying() {
		t.Error("should not be clarifying with no questions")
	}

	m.clarifyQuestions = []string{"Q1"}
	if !m.isClarifying() {
		t.Error("should be clarifying with questions set")
	}
}

func TestClearClarifyState(t *testing.T) {
	m := &Model{
		clarifyingTask:   "task",
		clarifyQuestions: []string{"q1", "q2"},
		clarifyAnswers:   []string{"a1"},
		clarifyIndex:     1,
	}

	m.clearClarifyState()

	if m.clarifyingTask != "" {
		t.Errorf("clarifyingTask not cleared: %q", m.clarifyingTask)
	}
	if m.clarifyQuestions != nil {
		t.Errorf("clarifyQuestions not cleared: %v", m.clarifyQuestions)
	}
	if m.clarifyAnswers != nil {
		t.Errorf("clarifyAnswers not cleared: %v", m.clarifyAnswers)
	}
	if m.clarifyIndex != 0 {
		t.Errorf("clarifyIndex not cleared: %d", m.clarifyIndex)
	}
}
