package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/storystate"
	"github.com/eoghanhynes/ralph/internal/workspace"
)

type WorkerID int

type WorkerState int

const (
	WorkerIdle WorkerState = iota
	WorkerSetup
	WorkerRunning
	WorkerJudging
	WorkerDone
	WorkerFailed
)

func (s WorkerState) String() string {
	switch s {
	case WorkerIdle:
		return "Idle"
	case WorkerSetup:
		return "Setup"
	case WorkerRunning:
		return "Running"
	case WorkerJudging:
		return "Judging"
	case WorkerDone:
		return "Done"
	case WorkerFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

type Worker struct {
	ID             WorkerID
	StoryID        string
	StoryTitle     string
	State          WorkerState
	Role           roles.Role // current agent role (architect/implementer/debugger)
	Workspace      string     // path to workspace dir
	WorkspaceName  string     // jj workspace name (for forget)
	BaseChangeID   string     // jj change ID of the commit the workspace branched from
	LogDir         string
	Iteration      int
	Ctx            context.Context
	Cancel         context.CancelFunc
}

type WorkerUpdate struct {
	WorkerID      WorkerID
	StoryID       string
	State         WorkerState
	Role          roles.Role // current agent role for TUI display
	Err           error
	Passed        bool
	ChangeID      string // jj change_id of committed work, for rebase
	Retryable     bool   // true for transient errors (network, timeouts)
	UsageLimit    bool   // true when Claude hit usage/rate limit
	JudgeResult   *judge.Result
	TokenUsage    *costs.TokenUsage    // token/turn usage from Claude run
	RateLimitInfo *costs.RateLimitInfo // latest rate limit info from Claude CLI
	StatusText    string               // optional status message for TUI status bar
	StatusWarn    bool                 // true if status is a warning
}

// shouldRunArchitect determines if the architect agent should run before the implementer.
// Returns true when this is the first iteration, the story doesn't match skip criteria,
// and no plan already exists in the workspace.
func shouldRunArchitect(storyID string, iteration int, workspaceDir string, p *prd.PRD) bool {
	// Only on first iteration
	if iteration != 1 {
		return false
	}

	// FIX- stories skip architect
	if strings.HasPrefix(storyID, "FIX-") {
		return false
	}

	// Check description word count via ShouldSkipArchitect
	descWordCount := 0
	if p != nil {
		if story := p.FindStory(storyID); story != nil {
			descWordCount = len(strings.Fields(story.Description))
		}
	}
	if roles.ShouldSkipArchitect(storyID, descWordCount) {
		return false
	}

	// Skip if plan already exists
	plan, _ := storystate.LoadPlan(workspaceDir, storyID)
	if strings.TrimSpace(plan) != "" {
		return false
	}

	return true
}

// accumulateUsage merges token usage from two runs, summing token counts.
// If either is nil, the other is returned. Duration and turns are summed.
func accumulateUsage(a, b *costs.TokenUsage) *costs.TokenUsage {
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
		Model:        a.Model, // use first model (architect)
		Provider:     a.Provider,
		NumTurns:     a.NumTurns + b.NumTurns,
		DurationMS:   a.DurationMS + b.DurationMS,
	}
}

// appendParallelMode adds the parallel mode stop condition to a prompt.
func appendParallelMode(prompt, storyID string) string {
	return prompt + fmt.Sprintf(`

---
## PARALLEL MODE
You are running as a parallel worker. Other workers are handling other stories simultaneously.
You are ONLY responsible for story **%s**. After completing it, stop immediately.
Do NOT check if all stories are complete. Do NOT emit the COMPLETE signal.
Just implement your story, commit, update progress.md, and stop.`, storyID)
}

// Run executes the full worker lifecycle in the workspace.
func Run(w *Worker, cfg *config.Config, updateCh chan<- WorkerUpdate) {
	var claudeUsage *costs.TokenUsage       // captured from RunClaude, forwarded in updates
	var latestRateLimit *costs.RateLimitInfo // latest rate limit info from Claude CLI

	send := func(state WorkerState, role roles.Role, err error, passed bool, changeID string) {
		w.State = state
		updateCh <- WorkerUpdate{
			WorkerID:      w.ID,
			StoryID:       w.StoryID,
			State:         state,
			Role:          role,
			Err:           err,
			Passed:        passed,
			ChangeID:      changeID,
			TokenUsage:    claudeUsage,
			RateLimitInfo: latestRateLimit,
		}
	}

	sendRetryable := func(err error) {
		w.State = WorkerFailed
		updateCh <- WorkerUpdate{
			WorkerID:  w.ID,
			StoryID:   w.StoryID,
			State:     WorkerFailed,
			Err:       err,
			Retryable: true,
		}
	}

	// 1. Create workspace
	send(WorkerSetup, "", nil, false, "")
	ws, err := workspace.Create(w.Ctx, cfg.ProjectDir, w.StoryID, cfg.WorkspaceBase)
	if err != nil {
		send(WorkerFailed, "", fmt.Errorf("workspace create: %w", err), false, "")
		return
	}
	w.Workspace = ws.Dir
	w.WorkspaceName = workspace.WorkspaceName(w.StoryID)
	w.BaseChangeID = ws.BaseChangeID

	// Copy state files into workspace
	if err := workspace.CopyState(cfg.ProjectDir, ws.Dir, w.StoryID); err != nil {
		send(WorkerFailed, "", fmt.Errorf("copy state: %w", err), false, "")
		return
	}

	// Run project-specific workspace setup (.ralph/workspace-setup.sh)
	setupResult, err := workspace.RunSetup(w.Ctx, ws.Dir)
	if err != nil {
		send(WorkerFailed, "", fmt.Errorf("workspace setup: %w", err), false, "")
		return
	}
	if setupResult.Warning != "" {
		w.State = WorkerSetup
		updateCh <- WorkerUpdate{
			WorkerID:   w.ID,
			StoryID:    w.StoryID,
			State:      WorkerSetup,
			StatusText: setupResult.Warning,
			StatusWarn: true,
		}
	}

	// Ensure log directory exists in workspace
	wsLogDir := filepath.Join(ws.Dir, ".ralph", "logs")
	w.LogDir = wsLogDir

	// Load PRD for prompt building
	wsPRD, _ := prd.Load(filepath.Join(ws.Dir, "prd.json"))

	// 2. Architect phase: run architect agent if applicable
	if !cfg.NoArchitect && shouldRunArchitect(w.StoryID, w.Iteration, ws.Dir, wsPRD) {
		send(WorkerRunning, roles.RoleArchitect, nil, false, "")

		archPrompt, err := runner.BuildPrompt(cfg.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: roles.RoleArchitect})
		if err != nil {
			send(WorkerFailed, roles.RoleArchitect, fmt.Errorf("build architect prompt: %w", err), false, "")
			return
		}
		archPrompt = appendParallelMode(archPrompt, w.StoryID)

		archLogPath := runner.LogFilePath(wsLogDir, w.Iteration) + ".architect"
		archResult, err := runner.RunClaude(w.Ctx, ws.Dir, archPrompt, archLogPath, runner.RunClaudeOpts{
			Iteration: w.Iteration,
			StoryID:   w.StoryID,
			Role:      roles.RoleArchitect,
		})
		if archResult != nil {
			claudeUsage = archResult.TokenUsage
			if archResult.RateLimitInfo != nil {
				latestRateLimit = archResult.RateLimitInfo
			}
		}
		if err != nil {
			if w.Ctx.Err() != nil {
				send(WorkerFailed, roles.RoleArchitect, w.Ctx.Err(), false, "")
				return
			}
			var usageErr *runner.UsageLimitError
			if errors.As(err, &usageErr) {
				w.State = WorkerFailed
				updateCh <- WorkerUpdate{
					WorkerID:      w.ID,
					StoryID:       w.StoryID,
					State:         WorkerFailed,
					Role:          roles.RoleArchitect,
					Err:           err,
					UsageLimit:    true,
					TokenUsage:    claudeUsage,
					RateLimitInfo: latestRateLimit,
				}
				return
			}
			sendRetryable(fmt.Errorf("architect run: %w", err))
			return
		}

		// Validate that the architect produced a plan (must be >= 50 bytes, consistent with serial mode)
		plan, _ := storystate.LoadPlan(ws.Dir, w.StoryID)
		if len(strings.TrimSpace(plan)) < 50 {
			send(WorkerFailed, roles.RoleArchitect, fmt.Errorf("architect produced insufficient plan for %s (%d bytes)", w.StoryID, len(plan)), false, "")
			return
		}
	}

	// 3. Build prompt and run implementer (or debugger if stuck)
	implRole := roles.RoleImplementer
	if runner.HasStuckInfo(ws.Dir, w.StoryID) {
		implRole = roles.RoleDebugger
	}
	send(WorkerRunning, implRole, nil, false, "")

	prompt, err := runner.BuildPrompt(cfg.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: implRole})
	if err != nil {
		send(WorkerFailed, implRole, fmt.Errorf("build prompt: %w", err), false, "")
		return
	}
	prompt = appendParallelMode(prompt, w.StoryID)

	logPath := runner.LogFilePath(wsLogDir, w.Iteration)
	implResult, err := runner.RunClaude(w.Ctx, ws.Dir, prompt, logPath, runner.RunClaudeOpts{
		Iteration: w.Iteration,
		StoryID:   w.StoryID,
		Role:      implRole,
	})
	if implResult != nil {
		claudeUsage = accumulateUsage(claudeUsage, implResult.TokenUsage)
		if implResult.RateLimitInfo != nil {
			latestRateLimit = implResult.RateLimitInfo
		}
	}
	if err != nil {
		if w.Ctx.Err() != nil {
			send(WorkerFailed, implRole, w.Ctx.Err(), false, "")
			return
		}
		// Usage limit — signal to pause, don't retry automatically
		var usageErr *runner.UsageLimitError
		if errors.As(err, &usageErr) {
			w.State = WorkerFailed
			updateCh <- WorkerUpdate{
				WorkerID:      w.ID,
				StoryID:       w.StoryID,
				State:         WorkerFailed,
				Role:          implRole,
				Err:           err,
				UsageLimit:    true,
				TokenUsage:    claudeUsage,
				RateLimitInfo: latestRateLimit,
			}
			return
		}
		// Other Claude CLI failures are likely transient (network, etc.)
		sendRetryable(fmt.Errorf("claude run: %w", err))
		return
	}

	// 4. Mark story as passed in workspace prd.json
	// The system owns the passes field — the agent no longer sets it.
	// If the judge is enabled, it will revert passes to false on failure.
	p, err := prd.Load(filepath.Join(ws.Dir, "prd.json"))
	if err != nil {
		send(WorkerFailed, "", fmt.Errorf("load prd: %w", err), false, "")
		return
	}
	p.SetPasses(w.StoryID, true)
	if err := prd.Save(filepath.Join(ws.Dir, "prd.json"), p); err != nil {
		send(WorkerFailed, "", fmt.Errorf("save prd: %w", err), false, "")
		return
	}
	passed := true

	// 5. Commit workspace changes
	changeID, err := workspace.CommitWorkspace(w.Ctx, ws.Dir, w.StoryID, w.StoryTitle, w.BaseChangeID)
	if err != nil {
		send(WorkerFailed, "", fmt.Errorf("commit workspace: %w", err), false, "")
		return
	}

	// 6. Run judge if enabled and story passed
	if cfg.JudgeEnabled && passed {
		send(WorkerJudging, "", nil, false, changeID)

		// Capture revs for judge: diff from base to squashed commit
		preRevs := []judge.DirRev{{Dir: ws.Dir, Rev: w.BaseChangeID, ToRev: changeID}}
		result := judge.RunJudge(w.Ctx, cfg.RalphHome, ws.Dir, filepath.Join(ws.Dir, "prd.json"), w.StoryID, preRevs)
		if !result.Passed {
			passed = false
		}
		// Send done with judge result
		w.State = WorkerDone
		updateCh <- WorkerUpdate{
			WorkerID:      w.ID,
			StoryID:       w.StoryID,
			State:         WorkerDone,
			Passed:        passed,
			ChangeID:      changeID,
			JudgeResult:   &result,
			TokenUsage:    claudeUsage,
			RateLimitInfo: latestRateLimit,
		}
		return
	}

	// 7. Send done
	w.State = WorkerDone
	updateCh <- WorkerUpdate{
		WorkerID:      w.ID,
		StoryID:       w.StoryID,
		State:         WorkerDone,
		Passed:        passed,
		ChangeID:      changeID,
		TokenUsage:    claudeUsage,
		RateLimitInfo: latestRateLimit,
	}
}
