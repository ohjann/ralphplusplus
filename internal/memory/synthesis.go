package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ohjann/ralphplusplus/internal/assets"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/events"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

// injectLearnings loads project and PRD learnings and replaces placeholders in the prompt.
func injectLearnings(prompt, projectDir, ralphHome, learningsPlaceholder, prdLearningsPlaceholder string) string {
	learnings, _ := ReadLearnings(projectDir)
	if learnings == "" {
		learnings = "(none yet)"
	}
	prompt = strings.Replace(prompt, learningsPlaceholder, learnings, 1)

	prdLearnings, _ := ReadPRDLearnings(ralphHome)
	if prdLearnings == "" {
		prdLearnings = "(none yet)"
	}
	prompt = strings.Replace(prompt, prdLearningsPlaceholder, prdLearnings, 1)
	return prompt
}

// RunClaudeFunc is the signature for invoking Claude. This avoids an import
// cycle between memory and runner.
type RunClaudeFunc func(ctx context.Context, projectDir, prompt, logFilePath string) error

// SynthesizeRun invokes Claude to analyse the completed run and write
// cross-story learnings to the markdown memory files. It builds a prompt
// from the PRD, events, and existing memory, then lets Claude append
// new entries. Errors are returned but callers should log, not fail.
func SynthesizeRun(ctx context.Context, projectDir, ralphHome, logDir string, p *prd.PRD, runClaude RunClaudeFunc) error {
	prompt, err := buildSynthesisPrompt(projectDir, ralphHome, p)
	if err != nil {
		return fmt.Errorf("building synthesis prompt: %w", err)
	}

	logPath := filepath.Join(logDir, "synthesis.log")
	if err := runClaude(ctx, projectDir, prompt, logPath); err != nil {
		return fmt.Errorf("running synthesis claude: %w", err)
	}

	debuglog.Log("post-run synthesis completed")
	return nil
}

func buildSynthesisPrompt(projectDir, ralphHome string, p *prd.PRD) (string, error) {
	tmpl, err := assets.ReadPrompt("prompts/synthesis.md")
	if err != nil {
		return "", fmt.Errorf("reading synthesis prompt: %w", err)
	}

	prompt := string(tmpl)

	// Build run summary from PRD
	prompt = strings.Replace(prompt, "{{RUN_SUMMARY}}", buildRunSummary(p), 1)

	// Build key events from events.jsonl
	prompt = strings.Replace(prompt, "{{KEY_EVENTS}}", buildKeyEvents(projectDir), 1)

	prompt = injectLearnings(prompt, projectDir, ralphHome, "{{EXISTING_LEARNINGS}}", "{{EXISTING_PRD_LEARNINGS}}")

	// Append instructions to write directly to memory files
	prompt += fmt.Sprintf(`

## Output Instructions

Write your new general learnings by appending to the file at: %s
Write your new PRD learnings by appending to the file at: %s

Use the Write tool or Edit tool to append entries. Create the directories if needed.
For confirmation updates, read the existing file, find the entry, and increment its "Confirmed: N times" count.
`,
		filepath.Join(projectDir, ".ralph", "memory", "learnings.md"),
		filepath.Join(ralphHome, "memory", "prd-learnings.md"),
	)

	return prompt, nil
}

func buildRunSummary(p *prd.PRD) string {
	if p == nil {
		return "(no PRD data)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Project:** %s\n", p.Project)
	fmt.Fprintf(&b, "**Stories:** %d total, %d completed\n\n", p.TotalCount(), p.CompletedCount())

	b.WriteString("| ID | Title | Status |\n")
	b.WriteString("|---|---|---|\n")
	for _, s := range p.UserStories {
		status := "incomplete"
		if s.Passes {
			status = "complete"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", s.ID, s.Title, status)
	}

	return b.String()
}

func buildKeyEvents(projectDir string) string {
	evts, err := events.Load(projectDir)
	if err != nil || len(evts) == 0 {
		return "(no events recorded)"
	}

	var b strings.Builder
	count := 0
	for _, e := range evts {
		switch e.Type {
		case events.EventStuck, events.EventStoryFailed, events.EventJudgeResult, events.EventContextExhausted:
			fmt.Fprintf(&b, "- [%s] **%s** %s: %s\n", e.Timestamp.Format("15:04"), e.Type, e.StoryID, e.Summary)
			if len(e.Errors) > 0 {
				for _, err := range e.Errors {
					fmt.Fprintf(&b, "  - Error: %s\n", err)
				}
			}
			count++
		}
	}

	if count == 0 {
		return "(no notable events — clean run)"
	}
	return b.String()
}
