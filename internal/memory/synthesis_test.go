package memory

import (
	"context"
	"fmt"
	"strings"
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
	], "prd_lessons": [
		{
			"category": "sizing",
			"pattern": "Story P5-003 was too large",
			"evidence": "P5-003 required 5 iterations and got stuck twice",
			"recommendation": "Split synthesis into prompt-building and response-parsing stories",
			"confidence": 0.85
		}
	]}`

	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
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

	result, err := synthesizeWithRunner(
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

	if len(result.Lessons) != 2 {
		t.Fatalf("expected 2 lessons, got %d", len(result.Lessons))
	}

	// Verify first lesson
	if result.Lessons[0].Category != "tooling" {
		t.Errorf("lesson[0].Category = %q, want %q", result.Lessons[0].Category, "tooling")
	}
	if result.Lessons[0].Pattern != "Agents repeatedly edited wrong file" {
		t.Errorf("lesson[0].Pattern = %q", result.Lessons[0].Pattern)
	}
	if result.Lessons[0].Confidence != 0.9 {
		t.Errorf("lesson[0].Confidence = %f, want 0.9", result.Lessons[0].Confidence)
	}
	if result.Lessons[0].TimesConfirmed != 1 {
		t.Errorf("lesson[0].TimesConfirmed = %d, want 1", result.Lessons[0].TimesConfirmed)
	}
	if result.Lessons[0].ID != "L-001" {
		t.Errorf("lesson[0].ID = %q, want %q", result.Lessons[0].ID, "L-001")
	}

	// Verify second lesson
	if result.Lessons[1].Category != "criteria" {
		t.Errorf("lesson[1].Category = %q, want %q", result.Lessons[1].Category, "criteria")
	}
	if result.Lessons[1].Confidence != 0.75 {
		t.Errorf("lesson[1].Confidence = %f, want 0.75", result.Lessons[1].Confidence)
	}

	// Verify PRD lessons
	if len(result.PRDLessons) != 1 {
		t.Fatalf("expected 1 PRD lesson, got %d", len(result.PRDLessons))
	}
	if result.PRDLessons[0].Category != "sizing" {
		t.Errorf("prd_lesson[0].Category = %q, want %q", result.PRDLessons[0].Category, "sizing")
	}
	if result.PRDLessons[0].ID != "PL-001" {
		t.Errorf("prd_lesson[0].ID = %q, want %q", result.PRDLessons[0].ID, "PL-001")
	}
	if result.PRDLessons[0].Confidence != 0.85 {
		t.Errorf("prd_lesson[0].Confidence = %f, want 0.85", result.PRDLessons[0].Confidence)
	}
}

func TestSynthesizeRunLessons_EmptyResponse(t *testing.T) {
	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		return `{"lessons": [], "prd_lessons": []}`, costs.TokenUsage{}, nil
	}

	result, err := synthesizeWithRunner(
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
	if len(result.Lessons) != 0 {
		t.Errorf("expected empty lessons, got %d", len(result.Lessons))
	}
	if len(result.PRDLessons) != 0 {
		t.Errorf("expected empty PRD lessons, got %d", len(result.PRDLessons))
	}
}

func TestSynthesizeRunLessons_MarkdownCodeBlock(t *testing.T) {
	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		return "```json\n{\"lessons\": [{\"category\": \"testing\", \"pattern\": \"test pattern\", \"evidence\": \"test\", \"recommendation\": \"test\", \"confidence\": 0.8}], \"prd_lessons\": []}\n```", costs.TokenUsage{}, nil
	}

	result, err := synthesizeWithRunner(
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
	if len(result.Lessons) != 1 {
		t.Fatalf("expected 1 lesson, got %d", len(result.Lessons))
	}
	if result.Lessons[0].Category != "testing" {
		t.Errorf("category = %q, want %q", result.Lessons[0].Category, "testing")
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
		"PRD-Quality Lessons",
		"Story sizing issues",
		"Criteria quality",
		"Ordering issues",
		"Missing criteria patterns",
		"prd_lessons",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}
}

// TestSynthesizePRDLessons_SizingAndCriteria verifies PRD lesson extraction
// from a mock run with known sizing and criteria issues.
func TestSynthesizePRDLessons_SizingAndCriteria(t *testing.T) {
	mockResponse := `{
		"lessons": [
			{
				"category": "architecture",
				"pattern": "Inconsistent error handling across stories",
				"evidence": "S-001, S-002, S-003 all had different error patterns",
				"recommendation": "Establish error handling convention before implementation",
				"confidence": 0.7
			}
		],
		"prd_lessons": [
			{
				"category": "sizing",
				"pattern": "Story S-002 was too large requiring 8 iterations",
				"evidence": "S-002 had 8 iterations, got stuck 3 times, and was eventually split manually",
				"recommendation": "Stories requiring more than 3 files should be split into smaller units",
				"confidence": 0.9
			},
			{
				"category": "criteria",
				"pattern": "Vague criteria 'handle errors properly' caused repeated judge rejections",
				"evidence": "S-001 rejected twice by judge for error handling that didn't match unstated expectations",
				"recommendation": "Specify exact error types and expected behavior in acceptance criteria",
				"confidence": 0.85
			},
			{
				"category": "ordering",
				"pattern": "S-003 depended on S-004 types but was ordered before it",
				"evidence": "S-003 failed because types from S-004 did not exist yet",
				"recommendation": "Add explicit dependency declarations between stories",
				"confidence": 0.95
			},
			{
				"category": "missing_criteria",
				"pattern": "No criteria specified for backward compatibility of SaveLessons",
				"evidence": "S-005 broke existing callers because criteria did not mention preserving existing signature",
				"recommendation": "Include backward compatibility requirements when modifying shared interfaces",
				"confidence": 0.8
			}
		]
	}`

	runner := func(ctx context.Context, prompt string) (string, costs.TokenUsage, error) {
		return mockResponse, costs.TokenUsage{}, nil
	}

	runSummary := costs.RunSummary{
		PRD:                   "test-sizing-prd",
		StoriesTotal:          5,
		StoriesCompleted:      3,
		StoriesFailed:         2,
		TotalIterations:       20,
		AvgIterationsPerStory: 4.0,
		StuckCount:            4,
		JudgeRejectionRate:    0.35,
		DurationMinutes:       90.0,
		TotalCost:             3.50,
	}

	storyStates := []storystate.StoryState{
		{
			StoryID:        "S-001",
			Status:         "complete",
			IterationCount: 4,
			JudgeFeedback:  []string{"error handling incomplete", "error handling still wrong"},
		},
		{
			StoryID:        "S-002",
			Status:         "blocked",
			IterationCount: 8,
			ErrorsEncountered: []storystate.ErrorEntry{
				{Error: "stuck editing same file", Resolution: "manual intervention"},
			},
		},
		{
			StoryID:        "S-003",
			Status:         "failed",
			IterationCount: 3,
			ErrorsEncountered: []storystate.ErrorEntry{
				{Error: "missing type from S-004", Resolution: "none"},
			},
		},
	}

	evts := []events.Event{
		{Type: events.EventStuck, StoryID: "S-002", Summary: "stuck 3 times on same file"},
		{Type: events.EventJudgeResult, StoryID: "S-001", Summary: "rejected for error handling"},
		{Type: events.EventStoryFailed, StoryID: "S-003", Summary: "missing dependency types"},
	}

	result, err := synthesizeWithRunner(
		context.Background(),
		t.TempDir(),
		runSummary,
		storyStates,
		evts,
		runner,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify general lessons
	if len(result.Lessons) != 1 {
		t.Fatalf("expected 1 general lesson, got %d", len(result.Lessons))
	}
	if result.Lessons[0].Category != "architecture" {
		t.Errorf("general lesson category = %q, want %q", result.Lessons[0].Category, "architecture")
	}

	// Verify PRD lessons
	if len(result.PRDLessons) != 4 {
		t.Fatalf("expected 4 PRD lessons, got %d", len(result.PRDLessons))
	}

	// Check categories
	expectedCategories := []string{"sizing", "criteria", "ordering", "missing_criteria"}
	for i, expected := range expectedCategories {
		if result.PRDLessons[i].Category != expected {
			t.Errorf("prd_lesson[%d].Category = %q, want %q", i, result.PRDLessons[i].Category, expected)
		}
	}

	// PRD lessons should have PL- prefix IDs
	for i, pl := range result.PRDLessons {
		expectedID := fmt.Sprintf("PL-%03d", i+1)
		if pl.ID != expectedID {
			t.Errorf("prd_lesson[%d].ID = %q, want %q", i, pl.ID, expectedID)
		}
		if pl.TimesConfirmed != 1 {
			t.Errorf("prd_lesson[%d].TimesConfirmed = %d, want 1", i, pl.TimesConfirmed)
		}
	}

	// Verify specific confidence values
	if result.PRDLessons[0].Confidence != 0.9 {
		t.Errorf("sizing lesson confidence = %f, want 0.9", result.PRDLessons[0].Confidence)
	}
	if result.PRDLessons[2].Confidence != 0.95 {
		t.Errorf("ordering lesson confidence = %f, want 0.95", result.PRDLessons[2].Confidence)
	}
}

// TestSaveLessonsWithPRDLessons verifies that SaveLessons persists prd_lessons key.
func TestSaveLessonsWithPRDLessons(t *testing.T) {
	dir := t.TempDir()
	lf := LessonsFile{
		Lessons: []Lesson{
			{ID: "L-001", Category: "testing", Pattern: "general lesson"},
		},
		PRDLessons: []Lesson{
			{ID: "PL-001", Category: "sizing", Pattern: "story too large"},
			{ID: "PL-002", Category: "criteria", Pattern: "vague criteria"},
		},
	}

	if err := SaveLessons(dir, lf); err != nil {
		t.Fatalf("SaveLessons: %v", err)
	}

	loaded, err := LoadLessons(dir)
	if err != nil {
		t.Fatalf("LoadLessons: %v", err)
	}

	if len(loaded.Lessons) != 1 {
		t.Errorf("expected 1 lesson, got %d", len(loaded.Lessons))
	}
	if len(loaded.PRDLessons) != 2 {
		t.Errorf("expected 2 PRD lessons, got %d", len(loaded.PRDLessons))
	}
	if loaded.PRDLessons[0].Category != "sizing" {
		t.Errorf("prd_lesson[0].Category = %q, want %q", loaded.PRDLessons[0].Category, "sizing")
	}
	if loaded.PRDLessons[1].Pattern != "vague criteria" {
		t.Errorf("prd_lesson[1].Pattern = %q, want %q", loaded.PRDLessons[1].Pattern, "vague criteria")
	}
}
