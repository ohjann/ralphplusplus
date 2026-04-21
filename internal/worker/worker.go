package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/storystate"
	"github.com/ohjann/ralphplusplus/internal/workspace"
)

type WorkerID int

type WorkerState int

const (
	WorkerIdle WorkerState = iota
	WorkerSetup
	WorkerRunning
	WorkerSimplifying
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
	case WorkerSimplifying:
		return "Simplifying"
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
	FusionSuffix string // non-empty for fusion workers (e.g., "-f0", "-f1")
	SessionID    string // captured from Claude stream for kill+resume
	ResumeHint   string // if set after cancel, resume with this hint instead of treating as failure
	IsResumed    bool   // true when current run is a resume (for logging/diagnostics)
	JJMu           *sync.Mutex // serialises jj operations against the main repo (shared with coordinator)
	Ctx            context.Context
	Cancel         context.CancelFunc
	AntiPatterns   []memory.AntiPattern // injected into architect/implementer prompts
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

// ResolveModel determines the model to use for a given role by applying the
// override precedence: config role-specific override > config global override > role default.
func ResolveModel(role roles.Role, cfg *config.Config) string {
	snap := cfg.Snapshot()
	// Check role-specific CLI overrides first
	switch role {
	case roles.RoleArchitect:
		if snap.ArchitectModel != "" {
			return snap.ArchitectModel
		}
	case roles.RoleImplementer:
		if snap.ImplementerModel != "" {
			return snap.ImplementerModel
		}
	}

	// Then global override
	if snap.ModelOverride != "" {
		return snap.ModelOverride
	}

	// Fall back to role default
	return roles.DefaultConfig(role).Model
}

// shouldRunSimplify returns true when the simplify phase should run for a story.
// Skips for FIX- stories (targeted fixes) and when disabled via config.
func shouldRunSimplify(storyID string, cfg *config.Config) bool {
	if cfg.Snapshot().NoSimplify {
		return false
	}
	if strings.HasPrefix(storyID, "FIX-") {
		return false
	}
	return true
}

// AppendParallelMode adds the parallel mode stop condition to a prompt.
func AppendParallelMode(prompt, storyID string) string {
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
	snap := cfg.Snapshot()
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

	// 1. Create workspace (serialised via JJMu to prevent concurrent jj sibling operations)
	send(WorkerSetup, "", nil, false, "")
	if w.JJMu != nil {
		w.JJMu.Lock()
	}
	ws, err := workspace.Create(w.Ctx, snap.ProjectDir, w.StoryID, snap.WorkspaceBase, w.FusionSuffix)
	if w.JJMu != nil {
		w.JJMu.Unlock()
	}
	if err != nil {
		send(WorkerFailed, "", fmt.Errorf("workspace create: %w", err), false, "")
		return
	}
	w.Workspace = ws.Dir
	w.WorkspaceName = workspace.WorkspaceName(w.StoryID) + w.FusionSuffix
	w.BaseChangeID = ws.BaseChangeID

	// Copy state files into workspace
	if err := workspace.CopyState(snap.ProjectDir, ws.Dir, w.StoryID); err != nil {
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

	// 2. Architect phase: run architect agent if applicable.
	// shouldRunArchitect returns false when plan.md already exists in the
	// workspace — fusion workers inherit the shared architect's plan via
	// CopyState, so this naturally skips the per-worker architect.
	if !snap.NoArchitect && shouldRunArchitect(w.StoryID, w.Iteration, ws.Dir, wsPRD) {
		send(WorkerRunning, roles.RoleArchitect, nil, false, "")

		archParts, err := runner.BuildPrompt(snap.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: roles.RoleArchitect, MemoryDisabled: snap.Memory.Disabled, AntiPatterns: w.AntiPatterns})
		if err != nil {
			send(WorkerFailed, roles.RoleArchitect, fmt.Errorf("build architect prompt: %w", err), false, "")
			return
		}
		archParts.UserMessage = AppendParallelMode(archParts.UserMessage, w.StoryID)

		archLogPath := runner.LogFilePath(wsLogDir, w.Iteration) + ".architect"
		archResult, err := runner.RunClaudeForIteration(w.Ctx, cfg, ws.Dir, archParts.UserMessage, archLogPath, runner.IterationOpts{
			StoryID:      w.StoryID,
			Role:         roles.RoleArchitect,
			Iter:         w.Iteration,
			Model:        ResolveModel(roles.RoleArchitect, cfg),
			SystemAppend: archParts.SystemAppend,
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

		// LLM-based plan quality gate: ensure the plan names files and addresses criteria.
		// On infra error we log and proceed (don't block on flakes). On a real FAIL we
		// re-run the architect once with the rejection reason appended; if still FAIL,
		// the worker fails.
		storyForGate := wsPRD.FindStory(w.StoryID)
		pass, reason, gateErr := validatePlan(w.Ctx, plan, storyForGate, snap.UtilityModel)
		if gateErr != nil {
			debuglog.Log("plan gate error for %s: %v — allowing through", w.StoryID, gateErr)
		} else if !pass {
			debuglog.Log("plan gate FAIL for %s: %s — re-running architect", w.StoryID, reason)
			retryParts, err := runner.BuildPrompt(snap.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: roles.RoleArchitect, MemoryDisabled: snap.Memory.Disabled, AntiPatterns: w.AntiPatterns})
			if err != nil {
				send(WorkerFailed, roles.RoleArchitect, fmt.Errorf("build architect retry prompt: %w", err), false, "")
				return
			}
			retryParts.UserMessage = AppendParallelMode(retryParts.UserMessage, w.StoryID) +
				"\n\n## Plan Gate Rejection (previous attempt)\n" +
				"The previous plan was rejected: " + reason + "\n" +
				"Produce a revised plan that names specific files to modify and addresses each acceptance criterion.\n"
			retryLogPath := runner.LogFilePath(wsLogDir, w.Iteration) + ".architect.retry"
			retryResult, err := runner.RunClaudeForIteration(w.Ctx, cfg, ws.Dir, retryParts.UserMessage, retryLogPath, runner.IterationOpts{
				StoryID:      w.StoryID,
				Role:         roles.RoleArchitect,
				Iter:         w.Iteration,
				Model:        ResolveModel(roles.RoleArchitect, cfg),
				SystemAppend: retryParts.SystemAppend,
			})
			if retryResult != nil {
				claudeUsage = retryResult.TokenUsage
				if retryResult.RateLimitInfo != nil {
					latestRateLimit = retryResult.RateLimitInfo
				}
			}
			if err != nil {
				if w.Ctx.Err() != nil {
					send(WorkerFailed, roles.RoleArchitect, w.Ctx.Err(), false, "")
					return
				}
				sendRetryable(fmt.Errorf("architect retry run: %w", err))
				return
			}
			plan, _ = storystate.LoadPlan(ws.Dir, w.StoryID)
			pass2, reason2, gateErr2 := validatePlan(w.Ctx, plan, storyForGate, snap.UtilityModel)
			if gateErr2 != nil {
				debuglog.Log("plan gate retry error for %s: %v — allowing through", w.StoryID, gateErr2)
			} else if !pass2 {
				send(WorkerFailed, roles.RoleArchitect, fmt.Errorf("plan gate rejected after retry: %s", reason2), false, "")
				return
			}
		}
	}

	// 3. Build prompt and run implementer (or debugger if stuck)
	implRole := roles.RoleImplementer
	if runner.HasStuckInfo(ws.Dir, w.StoryID) {
		implRole = roles.RoleDebugger
	}
	send(WorkerRunning, implRole, nil, false, "")

	implParts, err := runner.BuildPrompt(snap.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: implRole, MemoryDisabled: snap.Memory.Disabled, AntiPatterns: w.AntiPatterns})
	if err != nil {
		send(WorkerFailed, implRole, fmt.Errorf("build prompt: %w", err), false, "")
		return
	}
	implParts.UserMessage = AppendParallelMode(implParts.UserMessage, w.StoryID)

	logPath := runner.LogFilePath(wsLogDir, w.Iteration)
	implOpts := runner.IterationOpts{
		StoryID:      w.StoryID,
		Role:         implRole,
		Iter:         w.Iteration,
		Model:        ResolveModel(implRole, cfg),
		SystemAppend: implParts.SystemAppend,
	}
	implResult, err := runner.RunClaudeForIteration(w.Ctx, cfg, ws.Dir, implParts.UserMessage, logPath, implOpts)
	if implResult != nil {
		claudeUsage = costs.CombineUsage(claudeUsage, implResult.TokenUsage)
		if implResult.RateLimitInfo != nil {
			latestRateLimit = implResult.RateLimitInfo
		}
		// Capture session ID for kill+resume support
		if implResult.SessionID != "" {
			w.SessionID = implResult.SessionID
			// Persist to story state so TUI can read it
			if ss, loadErr := storystate.Load(ws.Dir, w.StoryID); loadErr == nil {
				ss.SessionID = implResult.SessionID
				if saveErr := storystate.Save(ws.Dir, ss); saveErr != nil {
					debuglog.Log("worker: failed to save story state for session ID: %v", saveErr)
				}
			}
		}
	}
	if err != nil {
		if w.Ctx.Err() != nil {
			// Check if this cancellation was for a hint-resume
			if w.ResumeHint != "" {
				resumeHint := w.ResumeHint
				w.ResumeHint = "" // consume the hint

				if w.SessionID != "" {
					// Resume with hint: relaunch with --resume
					w.IsResumed = true
					activityPath := runner.ActivityFilePath(wsLogDir, w.Iteration)

					// Check for partial tool call and warn
					rawLogPath := runner.LogFilePath(wsLogDir, w.Iteration)
					if runner.CheckPartialToolCall(rawLogPath) {
						_ = runner.AppendActivityMarker(activityPath,
							"\n--- WARNING: resumed from partial tool call; tool_use had no tool_result ---")
					}

					// Write user guidance marker
					_ = runner.AppendActivityMarker(activityPath,
						"\n--- USER GUIDANCE (resumed session) ---\n"+resumeHint)

					resumePrompt := resumeHint + "\n\nResume directly from where you left off. Do not recap or acknowledge this guidance — just act on it."

					// Create a fresh context for the resumed run
					resumeCtx, resumeCancel := context.WithCancel(context.Background())
					w.Ctx = resumeCtx
					w.Cancel = resumeCancel

					resumeResult, resumeErr := runner.RunClaudeForIteration(resumeCtx, cfg, ws.Dir, resumePrompt, logPath, runner.IterationOpts{
						StoryID:         w.StoryID,
						Role:            implRole,
						Iter:            w.Iteration,
						Model:           ResolveModel(implRole, cfg),
						ResumeSessionID: w.SessionID,
					})
					if resumeResult != nil {
						claudeUsage = costs.CombineUsage(claudeUsage, resumeResult.TokenUsage)
						if resumeResult.RateLimitInfo != nil {
							latestRateLimit = resumeResult.RateLimitInfo
						}
						if resumeResult.SessionID != "" {
							w.SessionID = resumeResult.SessionID
						}
					}
					if resumeErr != nil {
						if resumeCtx.Err() != nil {
							send(WorkerFailed, implRole, resumeCtx.Err(), false, "")
							return
						}
						sendRetryable(fmt.Errorf("claude resume: %w", resumeErr))
						return
					}
					// Resume succeeded — fall through to simplify/commit
				} else {
					// No session ID — fall back to hint.md + retry
					_ = storystate.SaveHint(ws.Dir, w.StoryID, resumeHint)
					sendRetryable(fmt.Errorf("hint resume: no session ID available, saved hint for next iteration"))
					return
				}
			} else {
				send(WorkerFailed, implRole, w.Ctx.Err(), false, "")
				return
			}
		} else {
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
	}

	// 3b. Simplify phase: quick code quality pass before commit
	if shouldRunSimplify(w.StoryID, cfg) {
		send(WorkerSimplifying, roles.RoleSimplify, nil, false, "")

		simplifyParts, err := runner.BuildPrompt(snap.RalphHome, ws.Dir, w.StoryID, wsPRD, runner.BuildPromptOpts{Role: roles.RoleSimplify, MemoryDisabled: snap.Memory.Disabled})
		if err != nil {
			// Non-fatal: skip simplify if prompt build fails
			w.State = WorkerRunning
		} else {
			simplifyParts.UserMessage = AppendParallelMode(simplifyParts.UserMessage, w.StoryID)
			simplifyLogPath := runner.LogFilePath(wsLogDir, w.Iteration) + ".simplify"
			simplifyResult, simplifyErr := runner.RunClaudeForIteration(w.Ctx, cfg, ws.Dir, simplifyParts.UserMessage, simplifyLogPath, runner.IterationOpts{
				StoryID:      w.StoryID,
				Role:         roles.RoleSimplify,
				Iter:         w.Iteration,
				Model:        ResolveModel(roles.RoleSimplify, cfg),
				SystemAppend: simplifyParts.SystemAppend,
			})
			if simplifyResult != nil {
				claudeUsage = costs.CombineUsage(claudeUsage, simplifyResult.TokenUsage)
				if simplifyResult.RateLimitInfo != nil {
					latestRateLimit = simplifyResult.RateLimitInfo
				}
			}
			// Simplify errors are non-fatal — log but continue to commit+judge
			if simplifyErr != nil {
				if w.Ctx.Err() != nil {
					send(WorkerFailed, roles.RoleSimplify, w.Ctx.Err(), false, "")
					return
				}
				var usageErr *runner.UsageLimitError
				if errors.As(simplifyErr, &usageErr) {
					w.State = WorkerFailed
					updateCh <- WorkerUpdate{
						WorkerID:      w.ID,
						StoryID:       w.StoryID,
						State:         WorkerFailed,
						Role:          roles.RoleSimplify,
						Err:           simplifyErr,
						UsageLimit:    true,
						TokenUsage:    claudeUsage,
						RateLimitInfo: latestRateLimit,
					}
					return
				}
				// Other errors: just skip simplify, continue to commit
			}
		}
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
	if snap.JudgeEnabled && passed {
		send(WorkerJudging, "", nil, false, changeID)

		// Capture revs for judge: diff from base to squashed commit
		preRevs := []judge.DirRev{{Dir: ws.Dir, Rev: w.BaseChangeID, ToRev: changeID}}
		result := judge.RunJudge(w.Ctx, snap.RalphHome, ws.Dir, filepath.Join(ws.Dir, "prd.json"), w.StoryID, preRevs)
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
