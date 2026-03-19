package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/storystate"
)

// synthesisResponse is the expected JSON structure from the Gemini synthesis prompt.
type synthesisResponse struct {
	Lessons    []synthesisLesson `json:"lessons"`
	PRDLessons []synthesisLesson `json:"prd_lessons"`
}

// SynthesisResult holds both general lessons and PRD-specific lessons.
type SynthesisResult struct {
	Lessons    []Lesson
	PRDLessons []Lesson
}

type synthesisLesson struct {
	Category       string  `json:"category"`
	Pattern        string  `json:"pattern"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
}

// GeminiRunner abstracts the Gemini invocation for testability.
type GeminiRunner func(ctx context.Context, prompt string) (string, costs.TokenUsage, error)

// SynthesizeRunLessons analyzes a completed PRD run using Gemini and extracts
// both cross-story lessons and PRD-specific lessons (about story sizing,
// criteria quality, ordering issues, and missing criteria patterns).
//
// Returns a SynthesisResult with both general and PRD lessons.
func SynthesizeRunLessons(
	ctx context.Context,
	projectDir string,
	runSummary costs.RunSummary,
	storyStates []storystate.StoryState,
	evts []events.Event,
) (SynthesisResult, error) {
	return synthesizeWithRunner(ctx, projectDir, runSummary, storyStates, evts, exec.RunGemini)
}

// synthesizeWithRunner is the internal implementation that accepts a GeminiRunner
// for testability.
func synthesizeWithRunner(
	ctx context.Context,
	projectDir string,
	runSummary costs.RunSummary,
	storyStates []storystate.StoryState,
	evts []events.Event,
	runner GeminiRunner,
) (SynthesisResult, error) {
	prompt := buildSynthesisPrompt(runSummary, storyStates, evts)

	output, _, err := runner(ctx, prompt)
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("gemini synthesis: %w", err)
	}

	result, err := parseSynthesisResponse(output)
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("parse synthesis response: %w", err)
	}

	return result, nil
}

// buildSynthesisPrompt constructs a concise prompt for the synthesis model.
func buildSynthesisPrompt(
	runSummary costs.RunSummary,
	storyStates []storystate.StoryState,
	evts []events.Event,
) string {
	var b strings.Builder

	b.WriteString("Analyze this completed PRD run and identify cross-story lessons.\n\n")

	// Run summary stats
	b.WriteString("## Run Summary\n")
	fmt.Fprintf(&b, "- PRD: %s\n", runSummary.PRD)
	fmt.Fprintf(&b, "- Stories: %d total, %d completed, %d failed\n",
		runSummary.StoriesTotal, runSummary.StoriesCompleted, runSummary.StoriesFailed)
	fmt.Fprintf(&b, "- Total iterations: %d (avg %.1f per story)\n",
		runSummary.TotalIterations, runSummary.AvgIterationsPerStory)
	fmt.Fprintf(&b, "- Stuck count: %d\n", runSummary.StuckCount)
	fmt.Fprintf(&b, "- Judge rejection rate: %.0f%%\n", runSummary.JudgeRejectionRate*100)
	fmt.Fprintf(&b, "- Duration: %.1f minutes, Cost: $%.2f\n\n",
		runSummary.DurationMinutes, runSummary.TotalCost)

	// Per-story summaries
	b.WriteString("## Per-Story Summaries\n")
	for _, ss := range storyStates {
		fmt.Fprintf(&b, "### %s (status: %s, iterations: %d)\n",
			ss.StoryID, ss.Status, ss.IterationCount)
		if len(ss.ErrorsEncountered) > 0 {
			b.WriteString("Errors:\n")
			for _, e := range ss.ErrorsEncountered {
				fmt.Fprintf(&b, "- %s → %s\n", e.Error, e.Resolution)
			}
		}
		if len(ss.JudgeFeedback) > 0 {
			b.WriteString("Judge feedback:\n")
			for _, f := range ss.JudgeFeedback {
				fmt.Fprintf(&b, "- %s\n", f)
			}
		}
		b.WriteString("\n")
	}

	// Event highlights (stuck and judge events)
	var highlights []string
	for _, ev := range evts {
		switch ev.Type {
		case events.EventStuck:
			highlights = append(highlights, fmt.Sprintf("[STUCK] %s: %s", ev.StoryID, ev.Summary))
		case events.EventJudgeResult:
			highlights = append(highlights, fmt.Sprintf("[JUDGE] %s: %s", ev.StoryID, ev.Summary))
		case events.EventStoryFailed:
			highlights = append(highlights, fmt.Sprintf("[FAILED] %s: %s", ev.StoryID, ev.Summary))
		}
	}
	if len(highlights) > 0 {
		b.WriteString("## Event Highlights\n")
		for _, h := range highlights {
			fmt.Fprintf(&b, "- %s\n", h)
		}
		b.WriteString("\n")
	}

	// Instructions for the model
	b.WriteString(`## Instructions
Identify cross-story lessons from this run. Produce TWO separate lists:

### General Lessons
Focus on:
1. Stories that required retries and why
2. Stuck patterns (what caused agents to get stuck)
3. Judge rejection patterns (common reasons for rejection)
4. Cross-story patterns that individual story analysis would miss

### PRD-Quality Lessons
Focus specifically on PRD authoring issues:
1. Story sizing issues — stories that were too big (many iterations, got stuck) or too small (trivial, should have been combined)
2. Criteria quality — vague acceptance criteria that led to judge rejections or rework
3. Ordering issues — stories blocked by missing dependencies from other stories
4. Missing criteria patterns — criteria that should have been specified but weren't, leading to ambiguity

Respond with ONLY a JSON object in this format:
{"lessons": [{"category": "<testing|architecture|sizing|ordering|criteria|tooling>", "pattern": "<what happened>", "evidence": "<which stories and data support this>", "recommendation": "<what to do differently>", "confidence": <0.0-1.0>}], "prd_lessons": [{"category": "<sizing|criteria|ordering|missing_criteria>", "pattern": "<what happened>", "evidence": "<which stories and data support this>", "recommendation": "<what to do differently>", "confidence": <0.0-1.0>}]}

If no meaningful lessons are found for either list, use an empty array.
`)

	return b.String()
}

// parseSynthesisResponse extracts both general and PRD lessons from the Gemini JSON response.
func parseSynthesisResponse(output string) (SynthesisResult, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return SynthesisResult{}, nil
	}

	// Extract JSON from potential markdown code blocks
	jsonStr := extractJSON(output)

	var resp synthesisResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return SynthesisResult{}, fmt.Errorf("unmarshal synthesis response: %w", err)
	}

	now := time.Now()
	lessons := convertLessons(resp.Lessons, "L", now)
	prdLessons := convertLessons(resp.PRDLessons, "PL", now)

	return SynthesisResult{
		Lessons:    lessons,
		PRDLessons: prdLessons,
	}, nil
}

// convertLessons transforms raw synthesis lessons into Lesson structs with the given ID prefix.
func convertLessons(raw []synthesisLesson, prefix string, now time.Time) []Lesson {
	lessons := make([]Lesson, 0, len(raw))
	for i, sl := range raw {
		if sl.Pattern == "" {
			continue
		}
		confidence := sl.Confidence
		if confidence <= 0 || confidence > 1.0 {
			confidence = 0.5
		}
		lessons = append(lessons, Lesson{
			ID:             fmt.Sprintf("%s-%03d", prefix, i+1),
			Category:       sl.Category,
			Pattern:        sl.Pattern,
			Evidence:       sl.Evidence,
			Recommendation: sl.Recommendation,
			Confidence:     confidence,
			TimesConfirmed: 1,
			CreatedAt:      now,
		})
	}
	return lessons
}

// extractJSON finds the first JSON object in the output, handling markdown
// code blocks and surrounding text.
func extractJSON(s string) string {
	// Try stripping markdown code fences
	if idx := strings.Index(s, "```json"); idx >= 0 {
		s = s[idx+7:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
		return strings.TrimSpace(s)
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx+3:]
		if end := strings.Index(s, "```"); end >= 0 {
			s = s[:end]
		}
		return strings.TrimSpace(s)
	}

	// Find first { and last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}

	return s
}
