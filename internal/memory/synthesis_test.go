package memory

import (
	"context"
	"testing"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

func TestSynthesizeRunLessons_MockResponse(t *testing.T) {
	mockResponse := `{"lessons": [
		{
			"category": "tooling",
			"pattern": "Agents repeatedly edited wrong file",
			"evidence": "P5-002 and P5-003 both got stuck editing runner.go instead of target files",
			"recommendation": "Validate file path against plan before editing",
			"confidence": 0.9
		},
		{
			"category": "criteria",
			"pattern": "Vague acceptance criteria led to rework",
			"evidence": "P5-001 required 3 iterations due to unclear collection naming",
			"recommendation": "Include exact function signatures in acceptance criteria",
			"confidence": 0.75
		}
	]}`

	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		// Verify the prompt contains expected sections
		if len(prompt) == 0 {
			t.Error("prompt should not be empty")
		}
		return mockResponse, costs.TokenUsage{}, nil
	}

	runSummary := costs.RunSummary{
		PRD:                   "test-prd",
		StoriesTotal:          5,
		StoriesCompleted:      3,
		StoriesFailed:         2,
		TotalIterations:       12,
		AvgIterationsPerStory: 2.4,
		StuckCount:            3,
		JudgeRejectionRate:    0.2,
		DurationMinutes:       45.0,
		TotalCost:             1.50,
	}

	storyStates := []storystate.StoryState{
		{
			StoryID:        "P5-001",
			Status:         "complete",
			IterationCount: 3,
			ErrorsEncountered: []storystate.ErrorEntry{
				{Error: "type mismatch", Resolution: "fixed import"},
			},
			JudgeFeedback: []string{"Collection name must match spec"},
		},
		{
			StoryID:        "P5-002",
			Status:         "complete",
			IterationCount: 2,
		},
	}

	evts := []events.Event{
		{
			Type:    events.EventStuck,
			StoryID: "P5-003",
			Summary: "Repeated edits to runner.go",
		},
		{
			Type:    events.EventJudgeResult,
			StoryID: "P5-001",
			Summary: "Missing collection definition",
		},
	}

	lessons, err := synthesizeWithRunner(
		context.Background(),
		t.TempDir(),
		runSummary,
		storyStates,
		evts,
		runner,
	)
	if err != nil {
		t.Fatalf("SynthesizeRunLessons: %v", err)
	}

	if len(lessons) != 2 {
		t.Fatalf("expected 2 lessons, got %d", len(lessons))
	}

	// Verify first lesson
	if lessons[0].Category != "tooling" {
		t.Errorf("lesson[0].Category = %q, want %q", lessons[0].Category, "tooling")
	}
	if lessons[0].Pattern != "Agents repeatedly edited wrong file" {
		t.Errorf("lesson[0].Pattern = %q", lessons[0].Pattern)
	}
	if lessons[0].Confidence != 0.9 {
		t.Errorf("lesson[0].Confidence = %f, want 0.9", lessons[0].Confidence)
	}
	if lessons[0].TimesConfirmed != 1 {
		t.Errorf("lesson[0].TimesConfirmed = %d, want 1", lessons[0].TimesConfirmed)
	}
	if lessons[0].ID != "L-001" {
		t.Errorf("lesson[0].ID = %q, want %q", lessons[0].ID, "L-001")
	}

	// Verify second lesson
	if lessons[1].Category != "criteria" {
		t.Errorf("lesson[1].Category = %q, want %q", lessons[1].Category, "criteria")
	}
	if lessons[1].Confidence != 0.75 {
		t.Errorf("lesson[1].Confidence = %f, want 0.75", lessons[1].Confidence)
	}
}

func TestSynthesizeRunLessons_EmptyResponse(t *testing.T) {
	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		return `{"lessons": []}`, costs.TokenUsage{}, nil
	}

	lessons, err := synthesizeWithRunner(
		context.Background(),
		t.TempDir(),
		costs.RunSummary{},
		nil,
		nil,
		runner,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lessons) != 0 {
		t.Errorf("expected empty lessons, got %d", len(lessons))
	}
}

func TestSynthesizeRunLessons_MarkdownCodeBlock(t *testing.T) {
	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		return "```json\n{\"lessons\": [{\"category\": \"testing\", \"pattern\": \"test pattern\", \"evidence\": \"test\", \"recommendation\": \"test\", \"confidence\": 0.8}]}\n```", costs.TokenUsage{}, nil
	}

	lessons, err := synthesizeWithRunner(
		context.Background(),
		t.TempDir(),
		costs.RunSummary{},
		nil,
		nil,
		runner,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("expected 1 lesson, got %d", len(lessons))
	}
	if lessons[0].Category != "testing" {
		t.Errorf("category = %q, want %q", lessons[0].Category, "testing")
	}
}

func TestBuildSynthesisPrompt(t *testing.T) {
	runSummary := costs.RunSummary{
		PRD:                   "test-prd",
		StoriesTotal:          3,
		StoriesCompleted:      2,
		StoriesFailed:         1,
		TotalIterations:       8,
		AvgIterationsPerStory: 2.7,
		StuckCount:            1,
		JudgeRejectionRate:    0.15,
	}

	storyStates := []storystate.StoryState{
		{
			StoryID:        "S-001",
			Status:         "complete",
			IterationCount: 2,
			JudgeFeedback:  []string{"missing error handling"},
		},
	}

	evts := []events.Event{
		{
			Type:    events.EventStuck,
			StoryID: "S-002",
			Summary: "repeated failure",
		},
	}

	prompt := buildSynthesisPrompt(runSummary, storyStates, evts)

	// Verify prompt contains key sections
	checks := []string{
		"Run Summary",
		"test-prd",
		"3 total, 2 completed, 1 failed",
		"Per-Story Summaries",
		"S-001",
		"missing error handling",
		"Event Highlights",
		"[STUCK] S-002",
		"Instructions",
		"cross-story lessons",
	}
	for _, check := range checks {
		if !synthContains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}
}

func synthContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && synthContainsSubstr(s, substr))
}

func synthContainsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
