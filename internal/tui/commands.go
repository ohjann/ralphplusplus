package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/archive"
	"github.com/eoghanhynes/ralph/internal/autofix"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/dag"
	rexec "github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/worker"
)

func fastTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return fastTickMsg{}
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func pollProgressCmd(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return progressContentMsg{}
		}
		return progressContentMsg{Content: string(data)}
	}
}

func pollActivityCmd(activityPath string) tea.Cmd {
	return func() tea.Msg {
		content := runner.ReadActivityContent(activityPath)
		return claudeActivityMsg{Content: content}
	}
}

func pollWorktreeCmd(ctx context.Context, dir string) tea.Cmd {
	return func() tea.Msg {
		out, _ := rexec.JJStatus(ctx, dir)
		return worktreeMsg{Content: out}
	}
}

func reloadPRDCmd(path string) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(path)
		if err != nil {
			return prdReloadedMsg{}
		}
		next := p.NextIncompleteStory()
		storyID := ""
		if next != nil {
			storyID = next.ID
		}
		return prdReloadedMsg{
			CompletedCount: p.CompletedCount(),
			TotalCount:     p.TotalCount(),
			AllComplete:    p.AllComplete(),
			CurrentStoryID: storyID,
		}
	}
}

func planCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		planContent, err := os.ReadFile(cfg.PlanFile)
		if err != nil {
			return planDoneMsg{Err: fmt.Errorf("reading plan file: %w", err)}
		}

		prompt := fmt.Sprintf(`You are generating a prd.json file from a plan. Read the plan below, explore the codebase for context, then generate prd.json.

CRITICAL: Write prd.json to the current working directory using the Write tool. Do NOT write it anywhere else. The file MUST be named exactly "prd.json".

## prd.json Format

The file must be valid JSON with this exact structure:

{
  "project": "<short project name>",
  "branchName": "<kebab-case branch name for this work>",
  "description": "<one-line description of the work>",
  "userStories": [
    {
      "id": "<PREFIX-001>",
      "title": "<short title>",
      "description": "<As a [user], I want [feature] so that [benefit]>",
      "acceptanceCriteria": [
        "Specific verifiable criterion",
        "Another criterion",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": ""
    }
  ]
}

Story IDs should use a short prefix derived from the project name (e.g., "TP-001" for "Task Priority").
Priority numbers determine execution order: 1 runs first, 2 runs second, etc.
All stories must have "passes": false and "notes": "".

## Story Sizing Rules
- Each story must be completable in ONE Claude Code context window (one focused session)
- Right-sized examples: add a DB column, add a UI component, update server logic, add an API endpoint
- Too big (MUST split): "build entire dashboard", "add authentication", "create full CRUD" — split these into smaller stories
- When in doubt, make stories smaller rather than larger

## Story Ordering
- Schema/database changes first, then backend/API, then UI/frontend
- Earlier stories must NOT depend on later ones
- Each story should be independently testable after completion

## Acceptance Criteria
- Must be specific and verifiable, not vague
- BAD: "Works correctly", "Is fast", "Looks good"
- GOOD: "Returns 200 with JSON body containing user object", "Button shows confirmation dialog before deleting"
- Always include "Typecheck passes" for every story
- UI stories: always include "Verify in browser"

## The Plan

%s
`, string(planContent))

		// Ensure log directory exists
		_ = os.MkdirAll(cfg.LogDir, 0o755)

		logPath := filepath.Join(cfg.LogDir, "plan.log")
		err = runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)
		if err != nil {
			return planDoneMsg{Err: fmt.Errorf("claude plan generation failed: %w", err)}
		}

		// Verify prd.json was actually created
		if _, statErr := os.Stat(cfg.PRDFile); os.IsNotExist(statErr) {
			return planDoneMsg{Err: fmt.Errorf("claude did not generate prd.json")}
		}

		return planDoneMsg{}
	}
}

func archiveCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		archived, _ := archive.CheckAndArchive(
			cfg.ProjectDir, cfg.LastBranchFile, cfg.ArchiveDir, cfg.PRDFile, cfg.ProgressFile,
		)
		_ = archive.TrackBranch(cfg.PRDFile, cfg.LastBranchFile)
		_ = archive.EnsureProgressFile(cfg.ProgressFile)
		return archiveDoneMsg{Archived: archived}
	}
}

func findNextStoryCmd(prdPath string) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(prdPath)
		if err != nil {
			return nextStoryMsg{AllDone: true}
		}
		if p.AllComplete() {
			return nextStoryMsg{AllDone: true}
		}
		next := p.NextIncompleteStory()
		if next == nil {
			return nextStoryMsg{AllDone: true}
		}
		return nextStoryMsg{StoryID: next.ID, StoryTitle: next.Title}
	}
}

func runClaudeCmd(ctx context.Context, cfg *config.Config, storyID string, iteration int) tea.Cmd {
	return func() tea.Msg {
		prompt, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID)
		if err != nil {
			return claudeDoneMsg{Err: err}
		}

		logPath := runner.LogFilePath(cfg.LogDir, iteration)
		err = runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath, runner.RunClaudeOpts{
			Iteration: iteration,
			StoryID:   storyID,
		})

		completeSignal := runner.LogContainsComplete(logPath)

		return claudeDoneMsg{Err: err, CompleteSignal: completeSignal}
	}
}

func generateFixStoryCmd(ctx context.Context, cfg *config.Config, info runner.StuckInfo) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}
		original := p.FindStory(info.StoryID)
		if original == nil {
			return fixStoryGeneratedMsg{Err: fmt.Errorf("story %s not found", info.StoryID)}
		}

		activityPath := runner.ActivityFilePath(cfg.LogDir, info.Iteration)
		activityTail := runner.ReadLogTail(activityPath, 50)

		fix, err := autofix.GenerateFixStory(ctx, info, *original, activityTail)
		if err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}

		if err := autofix.InsertFixStory(cfg.PRDFile, fix, info.StoryID); err != nil {
			return fixStoryGeneratedMsg{Err: err}
		}

		return fixStoryGeneratedMsg{StoryID: fix.ID}
	}
}

func pollStuckCmd(projectDir string, iteration int) tea.Cmd {
	return func() tea.Msg {
		info := runner.ReadStuckInfo(projectDir, iteration)
		if info != nil {
			return stuckDetectedMsg{Info: *info}
		}
		return nil
	}
}

func runJudgeCmd(ctx context.Context, cfg *config.Config, storyID string, preRevs []judge.DirRev) tea.Cmd {
	return func() tea.Msg {
		result := judge.RunJudge(ctx, cfg.RalphHome, cfg.ProjectDir, cfg.PRDFile, storyID, preRevs)
		return judgeDoneMsg{Result: result}
	}
}

func captureRevsCmd(ctx context.Context, dirs []string) []judge.DirRev {
	var revs []judge.DirRev
	for _, dir := range dirs {
		rev, _ := rexec.JJCurrentRev(ctx, dir)
		revs = append(revs, judge.DirRev{Dir: dir, Rev: rev})
	}
	return revs
}

func dagAnalyzeCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return coordinator.DAGAnalyzedMsg{Err: err}
		}

		// Only analyze incomplete stories
		var incomplete []prd.UserStory
		for _, s := range p.UserStories {
			if !s.Passes {
				incomplete = append(incomplete, s)
			}
		}

		if len(incomplete) == 0 {
			return coordinator.DAGAnalyzedMsg{Err: fmt.Errorf("no incomplete stories")}
		}

		d, err := dag.Analyze(ctx, cfg.ProjectDir, incomplete)
		if err != nil {
			// Fallback to linear
			d = dag.LinearFallback(incomplete)
			return coordinator.DAGAnalyzedMsg{DAG: d}
		}

		// Validate
		ids := make([]string, len(incomplete))
		for i, s := range incomplete {
			ids[i] = s.ID
		}
		if err := d.Validate(ids); err != nil {
			d = dag.LinearFallback(incomplete)
		}

		return coordinator.DAGAnalyzedMsg{DAG: d}
	}
}

func mergeBackCmd(ctx context.Context, coord *coordinator.Coordinator, u worker.WorkerUpdate) tea.Cmd {
	return func() tea.Msg {
		err := coord.MergeAndSync(ctx, u)
		return coordinator.MergeCompleteMsg{
			StoryID:  u.StoryID,
			WorkerID: u.WorkerID,
			Err:      err,
		}
	}
}

func pollWorkerActivityCmd(wID worker.WorkerID, activityPath string) tea.Cmd {
	return func() tea.Msg {
		content := runner.ReadActivityContent(activityPath)
		return coordinator.WorkerActivityMsg{
			WorkerID: wID,
			Content:  content,
		}
	}
}

func qualityReviewCmd(ctx context.Context, cfg *config.Config, iteration int) tea.Cmd {
	return func() tea.Msg {
		manifest, err := quality.GetDiffManifest(ctx, cfg.ProjectDir)
		if err != nil || manifest == "" {
			return qualityReviewDoneMsg{Err: fmt.Errorf("no changes to review: %v", err)}
		}

		lenses := quality.DefaultLenses()
		results := quality.RunReviewsParallel(ctx, cfg.ProjectDir, cfg.LogDir, lenses, manifest, iteration, cfg.QualityWorkers)
		assessment := quality.MergeAssessment(results, iteration)

		_ = quality.WriteAssessment(cfg.ProjectDir, assessment)

		return qualityReviewDoneMsg{Assessment: assessment}
	}
}

func qualityFixCmd(ctx context.Context, cfg *config.Config, assessment quality.Assessment, iteration int) tea.Cmd {
	return func() tea.Msg {
		err := quality.RunFix(ctx, cfg.ProjectDir, cfg.LogDir, assessment, iteration)
		return qualityFixDoneMsg{Err: err}
	}
}
