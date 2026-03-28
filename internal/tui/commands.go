package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/archive"
	"github.com/eoghanhynes/ralph/internal/autofix"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	rexec "github.com/eoghanhynes/ralph/internal/exec"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/storystate"
	"github.com/eoghanhynes/ralph/internal/worker"
)

// safeCmd wraps a tea.Cmd so that panics in the command goroutine are recovered
// instead of corrupting the terminal. Bubble Tea only recovers panics from
// Update/View — panics in Cmd goroutines leave the terminal in a broken state.
func safeCmd(fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		defer func() {
			if r := recover(); r != nil {
				debuglog.Log("panic recovered in tea.Cmd: %v", r)
			}
		}()
		return fn()
	}
}

func fastTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return fastTickMsg{}
	})
}

func spriteTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spriteTickMsg{}
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func checkMemorySizeCmd(projectDir string) tea.Cmd {
	return safeCmd(func() tea.Msg {
		result, err := memory.CheckSize(projectDir)
		if err != nil {
			debuglog.Log("memory size check error: %v", err)
			return nil
		}
		debuglog.Log("memory size check: %d bytes, ~%d tokens, level=%s", result.TotalBytes, result.TokenEstimate, result.Level())
		msg := result.WarnMessage()
		if msg == "" {
			return nil
		}
		level := statusWarn
		if result.Level() == "crit" {
			level = statusError
		}
		return statusMsg{Text: msg, Level: statusLevel(level)}
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

func pollMemoryStatsCmd(ralphHome string) tea.Cmd {
	return func() tea.Msg {
		stats := memory.MemoryStats(ralphHome)
		return memoryStatsMsg{Stats: stats}
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
			Stories:        p.UserStories,
		}
	}
}

func planCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
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
  "constraints": [
    "Cross-cutting architectural decisions or constraints from the plan"
  ],
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
      "notes": "",
      "dependsOn": ["<ID of story this depends on>"],
      "approach": "Brief implementation strategy or approach hint"
    }
  ]
}

Story IDs should use a short prefix derived from the project name (e.g., "TP-001" for "Task Priority").
Priority numbers determine execution order: 1 runs first, 2 runs second, etc.
All stories must have "passes": false and "notes": "".
The "dependsOn" field must list IDs of stories that must complete first. Use [] for stories with no dependencies.
The "approach" field should capture the implementation strategy from the plan (e.g., "extend the middleware chain", "use the existing EventBus pattern").
The "constraints" array at the top level captures cross-cutting decisions that apply to all stories.

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
		result, err := runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)
		_ = result
		if err != nil {
			return planDoneMsg{Err: fmt.Errorf("claude plan generation failed: %w", err)}
		}

		// Verify prd.json was actually created
		if _, statErr := os.Stat(cfg.PRDFile); os.IsNotExist(statErr) {
			return planDoneMsg{Err: fmt.Errorf("claude did not generate prd.json")}
		}

		return planDoneMsg{}
	})
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
			debuglog.Log("findNextStory: prd load error: %v — signaling AllDone", err)
			return nextStoryMsg{AllDone: true}
		}
		if p.AllComplete() {
			debuglog.Log("findNextStory: all %d stories complete", p.TotalCount())
			return nextStoryMsg{AllDone: true}
		}
		next := p.NextIncompleteStory()
		if next == nil {
			debuglog.Log("findNextStory: AllComplete()=false but NextIncompleteStory()=nil (should not happen)")
			return nextStoryMsg{AllDone: true}
		}
		debuglog.Log("findNextStory: next story=%s (%d/%d complete)", next.ID, p.CompletedCount(), p.TotalCount())
		return nextStoryMsg{StoryID: next.ID, StoryTitle: next.Title}
	}
}

// needsArchitect determines whether the architect phase should run for a story.
// It returns false (skip architect) when:
//   - The story is a FIX- story
//   - The story description is too short (< 50 words)
//   - A plan already exists from a previous iteration
func needsArchitect(projectDir, storyID string, story *prd.UserStory) bool {
	if story == nil {
		return false
	}

	// FIX- stories always skip architect
	if strings.HasPrefix(storyID, "FIX-") {
		return false
	}

	// If a plan already exists, skip architect (subsequent iteration)
	plan, err := storystate.LoadPlan(projectDir, storyID)
	if err == nil && len(strings.TrimSpace(plan)) >= 50 {
		return false
	}

	// Use the roles package to check word count threshold
	wordCount := len(strings.Fields(story.Description))
	return !roles.ShouldSkipArchitect(storyID, wordCount)
}

// combineTokenUsage merges two token usage values, summing all fields.
func combineTokenUsage(a, b *costs.TokenUsage) *costs.TokenUsage {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &costs.TokenUsage{
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		CacheRead:    a.CacheRead + b.CacheRead,
		CacheWrite:   a.CacheWrite + b.CacheWrite,
		Model:        b.Model, // use the later model (implementer)
		Provider:     b.Provider,
		NumTurns:     a.NumTurns + b.NumTurns,
		DurationMS:   a.DurationMS + b.DurationMS,
	}
}

func runClaudeCmd(ctx context.Context, cfg *config.Config, storyID string, iteration int) tea.Cmd {
	return safeCmd(func() tea.Msg {
		p, err := prd.Load(cfg.PRDFile)
		if err != nil {
			return claudeDoneMsg{Err: fmt.Errorf("loading PRD: %w", err)}
		}

		story := p.FindStory(storyID)

		// Determine if we need the architect phase
		runArchitect := !cfg.NoArchitect && needsArchitect(cfg.ProjectDir, storyID, story)

		var totalUsage *costs.TokenUsage
		var latestRateLimit *costs.RateLimitInfo

		// --- Architect phase ---
		if runArchitect {
			debuglog.Log("runClaudeCmd: running architect phase for story=%s", storyID)

			prompt, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID, p, runner.BuildPromptOpts{Role: roles.RoleArchitect, MemoryDisabled: cfg.Memory.Disabled})
			if err != nil {
				return claudeDoneMsg{Err: fmt.Errorf("architect prompt: %w", err), Role: roles.RoleArchitect}
			}

			logPath := runner.LogFilePath(cfg.LogDir, iteration) + ".architect"
			result, err := runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath, runner.RunClaudeOpts{
				Iteration: iteration,
				StoryID:   storyID,
				Role:      roles.RoleArchitect,
			})
			if result != nil {
				totalUsage = result.TokenUsage
				if result.RateLimitInfo != nil {
					latestRateLimit = result.RateLimitInfo
				}
			}

			if err != nil {
				return claudeDoneMsg{Err: fmt.Errorf("architect failed: %w", err), TokenUsage: totalUsage, RateLimitInfo: latestRateLimit, Role: roles.RoleArchitect}
			}

			// Validate that plan.md was created and is non-empty (>= 50 bytes)
			planContent, planErr := storystate.LoadPlan(cfg.ProjectDir, storyID)
			if planErr != nil || len(strings.TrimSpace(planContent)) < 50 {
				return claudeDoneMsg{
					Err:        fmt.Errorf("architect did not produce a valid plan (plan.md missing or < 50 bytes), retrying"),
					TokenUsage: totalUsage,
					Role:       roles.RoleArchitect,
				}
			}

			debuglog.Log("runClaudeCmd: architect phase complete, plan validated (%d bytes)", len(planContent))
		}

		// --- Implementer / Debugger phase ---
		implRole := roles.RoleImplementer
		if runner.HasStuckInfo(cfg.ProjectDir, storyID) {
			implRole = roles.RoleDebugger
			debuglog.Log("runClaudeCmd: stuck info found, using debugger role for story=%s", storyID)
		}
		debuglog.Log("runClaudeCmd: running %s phase for story=%s", implRole, storyID)

		prompt, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID, p, runner.BuildPromptOpts{Role: implRole, MemoryDisabled: cfg.Memory.Disabled})
		if err != nil {
			return claudeDoneMsg{Err: err, TokenUsage: totalUsage, Role: implRole}
		}

		logPath := runner.LogFilePath(cfg.LogDir, iteration)
		result, err := runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath, runner.RunClaudeOpts{
			Iteration: iteration,
			StoryID:   storyID,
			Role:      implRole,
		})

		if result != nil {
			totalUsage = combineTokenUsage(totalUsage, result.TokenUsage)
			if result.RateLimitInfo != nil {
				latestRateLimit = result.RateLimitInfo
			}
		}
		completeSignal := runner.LogContainsComplete(logPath)

		return claudeDoneMsg{
			Err:            err,
			CompleteSignal: completeSignal,
			TokenUsage:     totalUsage,
			Role:           implRole,
			RateLimitInfo:  latestRateLimit,
		}
	})
}

func generateFixStoryCmd(ctx context.Context, cfg *config.Config, info runner.StuckInfo) tea.Cmd {
	return safeCmd(func() tea.Msg {
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

		// Build rich escalation context from story state
		esc := autofix.EscalationContext{ActivityTail: activityTail}
		if plan, err := storystate.LoadPlan(cfg.ProjectDir, info.StoryID); err == nil && plan != "" {
			esc.Plan = plan
		}
		if state, err := storystate.Load(cfg.ProjectDir, info.StoryID); err == nil {
			if len(state.FilesTouched) > 0 {
				esc.FilesTouched = strings.Join(state.FilesTouched, "\n")
			}
			var errLines []string
			for _, e := range state.ErrorsEncountered {
				errLines = append(errLines, fmt.Sprintf("- %s → %s", e.Error, e.Resolution))
			}
			if len(errLines) > 0 {
				esc.Errors = strings.Join(errLines, "\n")
			}
			var taskLines []string
			for _, t := range state.Subtasks {
				status := "[ ]"
				if t.Done {
					status = "[x]"
				}
				taskLines = append(taskLines, fmt.Sprintf("%s %s", status, t.Description))
			}
			if len(taskLines) > 0 {
				esc.Subtasks = strings.Join(taskLines, "\n")
			}
		}

		fix, tokenUsage, err := autofix.GenerateFixStory(ctx, info, *original, esc)
		if err != nil {
			return fixStoryGeneratedMsg{Err: err, TokenUsage: tokenUsage}
		}

		if err := autofix.InsertFixStory(cfg.PRDFile, fix, info.StoryID); err != nil {
			return fixStoryGeneratedMsg{Err: err, TokenUsage: tokenUsage}
		}

		return fixStoryGeneratedMsg{StoryID: fix.ID, TokenUsage: tokenUsage}
	})
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
	return safeCmd(func() tea.Msg {
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

		// Use PRD-provided dependencies if available, skipping the Claude analysis call
		if p.HasExplicitDependencies() {
			debuglog.Log("dagAnalyze: using PRD-provided dependsOn fields (skipping Claude analysis)")
			d := dag.FromPRD(incomplete)

			ids := make([]string, len(incomplete))
			for i, s := range incomplete {
				ids[i] = s.ID
			}
			if err := d.Validate(ids); err != nil {
				debuglog.Log("dagAnalyze: PRD dependencies invalid (%v), falling back to Claude analysis", err)
			} else {
				return coordinator.DAGAnalyzedMsg{DAG: d}
			}
		}

		d, err := dag.Analyze(ctx, cfg.ProjectDir, incomplete, cfg.UtilityModel)
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
	})
}

func mergeBackCmd(ctx context.Context, coord *coordinator.Coordinator, u worker.WorkerUpdate) tea.Cmd {
	return safeCmd(func() tea.Msg {
		conflictsResolved, err := coord.MergeAndSync(ctx, u)
		return coordinator.MergeCompleteMsg{
			StoryID:           u.StoryID,
			WorkerID:          u.WorkerID,
			ChangeID:          u.ChangeID,
			Err:               err,
			ConflictsResolved: conflictsResolved,
		}
	})
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
	return safeCmd(func() tea.Msg {
		manifest, err := quality.GetDiffManifest(ctx, cfg.ProjectDir)
		if err != nil || manifest == "" {
			return qualityReviewDoneMsg{Err: fmt.Errorf("no changes to review: %v", err)}
		}

		lenses := quality.DefaultLenses()
		results := quality.RunReviewsParallel(ctx, cfg.ProjectDir, cfg.LogDir, lenses, manifest, iteration, cfg.QualityWorkers)
		assessment := quality.MergeAssessment(results, iteration)

		_ = quality.WriteAssessment(cfg.ProjectDir, assessment)

		return qualityReviewDoneMsg{Assessment: assessment}
	})
}

func qualityFixCmd(ctx context.Context, cfg *config.Config, assessment quality.Assessment, iteration int) tea.Cmd {
	return safeCmd(func() tea.Msg {
		err := quality.RunFix(ctx, cfg.ProjectDir, cfg.LogDir, assessment, iteration)
		return qualityFixDoneMsg{Err: err}
	})
}

func generateSummaryCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		// Remove stale SUMMARY.md so Claude generates a fresh one from current state
		summaryPath := filepath.Join(cfg.ProjectDir, "SUMMARY.md")
		_ = os.Remove(summaryPath)

		// Read PRD for context
		prdData, _ := os.ReadFile(cfg.PRDFile)
		// Read progress for context
		progressData, _ := os.ReadFile(cfg.ProgressFile)

		prompt := fmt.Sprintf(`You have just completed implementing all stories in a project. Generate a comprehensive summary of everything that was done.

Write this summary to a file called SUMMARY.md in the current working directory using the Write tool.

The summary should include:
1. **Overview** - What was built/changed (one paragraph)
2. **Stories Completed** - Brief summary of each story and what it involved
3. **Files Changed** - Key files that were added or modified (explore the recent changes)
4. **Configuration** - Any new configuration, environment variables, or setup needed
5. **Build & Run** - How to build and run the project (check for Makefile, package.json, etc.)
6. **Testing** - How to run tests, any new test files added
7. **Notes** - Any caveats, known issues, or things that need human review

Be concise but thorough. Focus on actionable information the developer needs to know.

## PRD (what was planned)
%s

## Progress Log
%s
`, string(prdData), string(progressData))

		logPath := filepath.Join(cfg.LogDir, "summary.log")
		result, err := runner.RunClaude(ctx, cfg.ProjectDir, prompt, logPath)
		_ = result

		// Read the generated summary
		content, _ := os.ReadFile(summaryPath)

		return summaryDoneMsg{Content: string(content), Err: err}
	})
}

func synthesisCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		p, _ := prd.Load(cfg.PRDFile)
		runClaude := func(ctx context.Context, projectDir, prompt, logFilePath string) error {
			_, err := runner.RunClaude(ctx, projectDir, prompt, logFilePath, runner.RunClaudeOpts{Model: cfg.UtilityModel})
			return err
		}
		err := memory.SynthesizeRun(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, p, runClaude)
		return synthesisDoneMsg{Err: err}
	})
}

func dreamCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		runClaude := func(ctx context.Context, projectDir, prompt, logFilePath string) error {
			_, err := runner.RunClaude(ctx, projectDir, prompt, logFilePath, runner.RunClaudeOpts{Model: cfg.UtilityModel})
			return err
		}
		err := memory.RunDream(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, cfg.Memory.MaxEntries, cfg.Memory.DreamEveryNRuns, runClaude)
		return dreamDoneMsg{Err: err}
	})
}

