package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ohjann/ralphplusplus/internal/archive"
	"github.com/ohjann/ralphplusplus/internal/autofix"
	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/coordinator"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/daemon"
	"github.com/ohjann/ralphplusplus/internal/dag"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	rexec "github.com/ohjann/ralphplusplus/internal/exec"
	"github.com/ohjann/ralphplusplus/internal/fusion"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/quality"
	"github.com/ohjann/ralphplusplus/internal/retro"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/storystate"
	"github.com/ohjann/ralphplusplus/internal/summary"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// panicRecoveredMsg is returned when a tea.Cmd panics. This prevents the TUI
// from hanging forever waiting for a message that will never arrive.
type panicRecoveredMsg struct {
	Value string
	Stack string
}

// safeCmd wraps a tea.Cmd so that panics in the command goroutine are recovered
// instead of corrupting the terminal. Bubble Tea only recovers panics from
// Update/View — panics in Cmd goroutines leave the terminal in a broken state.
func safeCmd(fn func() tea.Msg) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				stack := string(buf[:n])
				debuglog.Log("panic recovered in tea.Cmd: %v\n%s", r, stack)
				msg = panicRecoveredMsg{
					Value: fmt.Sprintf("%v", r),
					Stack: stack,
				}
			}
		}()
		return fn()
	}
}

// scheduleReadyCmd runs ScheduleReady off the main thread so fusion complexity
// LLM calls don't block the TUI event loop.
func scheduleReadyCmd(ctx context.Context, coord *coordinator.Coordinator) tea.Cmd {
	return safeCmd(func() tea.Msg {
		n := coord.ScheduleReady(ctx)
		return scheduleReadyDoneMsg{Launched: n}
	})
}

func listenUpdateCmd(coord *coordinator.Coordinator) tea.Cmd {
	return func() tea.Msg {
		u := <-coord.UpdateCh()
		return workerUpdateMsg{Update: u}
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

// usageLimitResumeCmd waits until the given time (plus a small buffer) then
// fires a usageLimitResumeMsg to auto-resume paused workers.
func usageLimitResumeCmd(resetsAt time.Time) tea.Cmd {
	wait := time.Until(resetsAt) + 30*time.Second // 30s buffer for safety
	if wait < 10*time.Second {
		wait = 10 * time.Second
	}
	return tea.Tick(wait, func(time.Time) tea.Msg {
		return usageLimitResumeMsg{}
	})
}

func checkMemorySizeCmd(projectDir, ralphHome string) tea.Cmd {
	return safeCmd(func() tea.Msg {
		result, err := memory.CheckSize(projectDir, ralphHome)
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

func pollMemoryStatsCmd(projectDir, ralphHome string) tea.Cmd {
	return func() tea.Msg {
		stats := memory.MemoryStats(projectDir, ralphHome)
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
		result, err := runner.RunClaudeForIteration(ctx, cfg, cfg.ProjectDir, prompt, logPath, runner.IterationOpts{
			StoryID: "_plan",
			Role:    "plan-generation",
			Iter:    1,
		})
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

			archParts, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID, p, runner.BuildPromptOpts{Role: roles.RoleArchitect, MemoryDisabled: cfg.Memory.Disabled})
			if err != nil {
				return claudeDoneMsg{Err: fmt.Errorf("architect prompt: %w", err), Role: roles.RoleArchitect}
			}

			logPath := runner.LogFilePath(cfg.LogDir, iteration) + ".architect"
			result, err := runner.RunClaudeForIteration(ctx, cfg, cfg.ProjectDir, archParts.UserMessage, logPath, runner.IterationOpts{
				StoryID:      storyID,
				Role:         roles.RoleArchitect,
				Iter:         iteration,
				SystemAppend: archParts.SystemAppend,
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

		implParts, err := runner.BuildPrompt(cfg.RalphHome, cfg.ProjectDir, storyID, p, runner.BuildPromptOpts{Role: implRole, MemoryDisabled: cfg.Memory.Disabled})
		if err != nil {
			return claudeDoneMsg{Err: err, TokenUsage: totalUsage, Role: implRole}
		}

		logPath := runner.LogFilePath(cfg.LogDir, iteration)
		result, err := runner.RunClaudeForIteration(ctx, cfg, cfg.ProjectDir, implParts.UserMessage, logPath, runner.IterationOpts{
			StoryID:      storyID,
			Role:         implRole,
			Iter:         iteration,
			SystemAppend: implParts.SystemAppend,
		})

		if result != nil {
			totalUsage = costs.CombineUsage(totalUsage, result.TokenUsage)
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

		var incomplete []prd.UserStory
		for _, s := range p.UserStories {
			if !s.Passes {
				incomplete = append(incomplete, s)
			}
		}

		if len(incomplete) == 0 {
			return coordinator.DAGAnalyzedMsg{Err: fmt.Errorf("no incomplete stories")}
		}

		d := dag.BuildDAG(ctx, cfg.ProjectDir, p, incomplete, cfg.UtilityModel)
		return coordinator.DAGAnalyzedMsg{DAG: d}
	})
}

// mergeBackCmd is no longer used — the daemon handles merges autonomously and
// broadcasts MergeResultEvent via SSE. The TUI receives these in handleDaemonMergeResult.

func fusionCompareCmd(ctx context.Context, coord *coordinator.Coordinator, storyID string, fg *fusion.FusionGroup) tea.Cmd {
	return safeCmd(func() tea.Msg {
		story := coord.GetStory(storyID)
		workers := coord.Workers()

		getDiff := func(wID worker.WorkerID) (string, error) {
			w := workers[wID]
			if w == nil || w.Workspace == "" {
				return "", fmt.Errorf("worker %d has no workspace", wID)
			}
			for _, r := range fg.Results {
				if r.WorkerID == wID {
					return rexec.JJDiff(ctx, w.Workspace, w.BaseChangeID, r.ChangeID)
				}
			}
			return "", fmt.Errorf("worker %d not in fusion group", wID)
		}

		cr := fusion.RunCompare(ctx, story, fg, getDiff)
		return coordinator.FusionCompareDoneMsg{
			StoryID:        storyID,
			WinnerWorkerID: cr.WinnerWorkerID,
			WinnerChangeID: cr.WinnerChangeID,
			LoserWorkerIDs: cr.LoserWorkerIDs,
			LoserChangeIDs: cr.LoserChangeIDs,
			Reason:         cr.Reason,
			Passed:         cr.Passed,
			MultiplePassed: cr.MultiplePassed,
			WasFirstPasser: cr.WasFirstPasser,
			Err:            cr.Err,
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
		manifest, err := quality.GetDiffManifest(ctx, cfg.ProjectDir, cfg.PRDFile)
		if err != nil || manifest == "" {
			return qualityReviewDoneMsg{Err: fmt.Errorf("no changes to review: %v", err)}
		}

		lenses := quality.DefaultLenses()
		results := quality.RunReviewsParallel(ctx, cfg, cfg.ProjectDir, cfg.LogDir, lenses, manifest, iteration, cfg.QualityWorkers)
		assessment := quality.MergeAssessment(results, iteration)

		_ = quality.WriteAssessment(cfg.ProjectDir, assessment)

		return qualityReviewDoneMsg{Assessment: assessment}
	})
}

func qualityFixCmd(ctx context.Context, cfg *config.Config, assessment quality.Assessment, iteration int) tea.Cmd {
	return safeCmd(func() tea.Msg {
		// Drop findings that reference deleted/renamed files since the review ran
		if dropped := quality.FilterStaleFindings(cfg.ProjectDir, &assessment); dropped > 0 {
			debuglog.Log("quality fix: dropped %d stale findings (files no longer exist)", dropped)
		}
		err := quality.RunFix(ctx, cfg, cfg.ProjectDir, cfg.LogDir, assessment, iteration)
		return qualityFixDoneMsg{Err: err}
	})
}

func generateSummaryCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		content, err := summary.Generate(ctx, cfg)
		return summaryDoneMsg{Content: content, Err: err}
	})
}

func retroCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		result, err := retro.RunRetrospective(ctx, cfg, cfg.ProjectDir, cfg.LogDir, cfg.PRDFile, cfg.UtilityModel)
		return retroDoneMsg{Result: result, Err: err}
	})
}

func synthesisCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		p, _ := prd.Load(cfg.PRDFile)
		err := memory.SynthesizeRun(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, p, runner.UtilityClaude(cfg))
		return synthesisDoneMsg{Err: err}
	})
}

func dreamCmd(ctx context.Context, cfg *config.Config) tea.Cmd {
	return safeCmd(func() tea.Msg {
		err := memory.RunDream(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, cfg.Memory.MaxEntries, cfg.Memory.DreamEveryNRuns, runner.UtilityClaude(cfg))
		return dreamDoneMsg{Err: err}
	})
}

// --- Daemon client commands ---

// connectDaemonSSECmd establishes the SSE connection and returns a
// daemonConnectedMsg with the event channel, or daemonDisconnectedMsg on error.
func connectDaemonSSECmd(client *daemon.DaemonClient, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		eventCh := client.StreamEvents(ctx)
		return daemonConnectedMsg{EventCh: eventCh}
	}
}

// listenDaemonEventCmd reads the next event from the SSE channel and decodes it
// into a typed daemonEventMsg. If the channel closes, it returns daemonDisconnectedMsg.
func listenDaemonEventCmd(eventCh <-chan daemon.DaemonEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-eventCh
		if !ok {
			return daemonDisconnectedMsg{Err: nil}
		}

		msg := daemonEventMsg{}
		switch evt.Type {
		case daemon.EventDaemonState:
			var s daemon.DaemonStateEvent
			if err := json.Unmarshal(evt.Data, &s); err == nil {
				msg.State = &s
			}
		case daemon.EventWorkerLog:
			var l daemon.WorkerLogEvent
			if err := json.Unmarshal(evt.Data, &l); err == nil {
				msg.WorkerLog = &l
			}
		case daemon.EventLogLine:
			var l daemon.LogLineEvent
			if err := json.Unmarshal(evt.Data, &l); err == nil {
				msg.LogLine = &l
			}
		case daemon.EventMergeResult:
			var r daemon.MergeResultEvent
			if err := json.Unmarshal(evt.Data, &r); err == nil {
				msg.MergeResult = &r
			}
		case daemon.EventStuckAlert:
			var a daemon.StuckAlertEvent
			if err := json.Unmarshal(evt.Data, &a); err == nil {
				msg.StuckAlert = &a
			}
		case "error":
			return daemonDisconnectedMsg{Err: fmt.Errorf("daemon SSE error")}
		}
		return msg
	}
}

// daemonQuitCmd sends POST /api/quit to the daemon.
func daemonQuitCmd(client *daemon.DaemonClient) tea.Cmd {
	return func() tea.Msg {
		err := client.Quit()
		return daemonQuitDoneMsg{Err: err}
	}
}

// daemonResumeCmd sends POST /api/resume to the daemon.
func daemonResumeCmd(client *daemon.DaemonClient) tea.Cmd {
	return safeCmd(func() tea.Msg {
		if err := client.Resume(); err != nil {
			return statusMsg{Text: fmt.Sprintf("Resume failed: %v", err), Level: statusError}
		}
		return statusMsg{Text: "Resumed", Level: statusInfo}
	})
}

// daemonPauseCmd sends POST /api/pause to the daemon.
func daemonPauseCmd(client *daemon.DaemonClient) tea.Cmd {
	return safeCmd(func() tea.Msg {
		if err := client.Pause(); err != nil {
			return statusMsg{Text: fmt.Sprintf("Pause failed: %v", err), Level: statusError}
		}
		return statusMsg{Text: "Paused", Level: statusInfo}
	})
}

// daemonHintCmd sends a hint to a specific worker via the daemon.
func daemonHintCmd(client *daemon.DaemonClient, workerID worker.WorkerID, text string) tea.Cmd {
	return safeCmd(func() tea.Msg {
		err := client.SendHint(workerID, text)
		if err != nil {
			return statusMsg{Text: fmt.Sprintf("Hint failed: %v", err), Level: statusError}
		}
		return statusMsg{Text: fmt.Sprintf("Hint sent to worker %d", workerID), Level: statusInfo}
	})
}

// daemonTaskCmd submits an ad-hoc task to the daemon.
func daemonTaskCmd(client *daemon.DaemonClient, description string) tea.Cmd {
	return safeCmd(func() tea.Msg {
		err := client.SubmitTask(description)
		if err != nil {
			return statusMsg{Text: fmt.Sprintf("Task failed: %v", err), Level: statusError}
		}
		return statusMsg{Text: "Task submitted", Level: statusInfo}
	})
}

