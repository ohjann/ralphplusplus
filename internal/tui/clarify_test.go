package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/eoghanhynes/ralph/internal/prd"
)

func TestBuildStorySummary_Empty(t *testing.T) {
	result := buildStorySummary(nil)
	if result != "(none)" {
		t.Errorf("expected (none) for nil stories, got %q", result)
	}

	result = buildStorySummary([]prd.UserStory{})
	if result != "(none)" {
		t.Errorf("expected (none) for empty stories, got %q", result)
	}
}

func TestBuildStorySummary_WithStories(t *testing.T) {
	stories := []prd.UserStory{
		{ID: "T-001", Title: "Fix login bug", Passes: true},
		{ID: "T-002", Title: "Add dark mode", Passes: false},
	}

	result := buildStorySummary(stories)

	if !strings.Contains(result, "[done] T-001") {
		t.Errorf("expected completed story to show [done], got %q", result)
	}
	if !strings.Contains(result, "[queued] T-002") {
		t.Errorf("expected incomplete story to show [queued], got %q", result)
	}
	if !strings.Contains(result, "Fix login bug") {
		t.Errorf("expected title in summary, got %q", result)
	}
}

func TestBuildStorySummary_CapsAt10(t *testing.T) {
	stories := make([]prd.UserStory, 15)
	for i := range stories {
		stories[i] = prd.UserStory{
			ID:    fmt.Sprintf("T-%03d", i+1),
			Title: fmt.Sprintf("Task %d", i+1),
		}
	}

	result := buildStorySummary(stories)

	if !strings.Contains(result, "... and 5 more") {
		t.Errorf("expected truncation message, got %q", result)
	}
	// Should contain first 10 but not the 11th
	if !strings.Contains(result, "T-010") {
		t.Errorf("expected T-010 in summary")
	}
	if strings.Contains(result, "T-011") {
		t.Errorf("T-011 should not appear in truncated summary")
	}
}

func TestClarifyPromptTemplate_ContainsExpectedSections(t *testing.T) {
	stories := []prd.UserStory{
		{ID: "P1-001", Title: "Existing feature", Passes: true},
	}
	summary := buildStorySummary(stories)
	prompt := fmt.Sprintf(clarifyPromptTemplate, "MyProject", summary, "Add a new endpoint")

	if !strings.Contains(prompt, "Project: MyProject") {
		t.Error("prompt should contain project name")
	}
	if !strings.Contains(prompt, "Add a new endpoint") {
		t.Error("prompt should contain task text")
	}
	if !strings.Contains(prompt, "[done] P1-001") {
		t.Error("prompt should contain story summary")
	}
	if !strings.Contains(prompt, "READY") {
		t.Error("prompt should mention READY response option")
	}
	if !strings.Contains(prompt, "Q: ") {
		t.Error("prompt should mention Q: prefix for questions")
	}
}

func TestClarifyPromptTemplate_EmptyStories(t *testing.T) {
	summary := buildStorySummary(nil)
	prompt := fmt.Sprintf(clarifyPromptTemplate, "TestProj", summary, "Do something")

	if !strings.Contains(prompt, "(none)") {
		t.Error("prompt with no stories should show (none)")
	}
}

