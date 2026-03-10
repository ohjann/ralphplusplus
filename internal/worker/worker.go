package worker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/runner"
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
	Workspace      string // path to workspace dir
	WorkspaceName  string // jj workspace name (for forget)
	BaseChangeID   string // jj change ID of the commit the workspace branched from
	LogDir         string
	Iteration      int
	Ctx            context.Context
	Cancel         context.CancelFunc
}

type WorkerUpdate struct {
	WorkerID  WorkerID
	StoryID   string
	State     WorkerState
	Err       error
	Passed    bool
	ChangeID  string // jj change_id of committed work, for rebase
	Retryable bool   // true for transient errors (rate limits, timeouts)
}

// Run executes the full worker lifecycle in the workspace.
func Run(w *Worker, cfg *config.Config, updateCh chan<- WorkerUpdate) {
	send := func(state WorkerState, err error, passed bool, changeID string) {
		w.State = state
		updateCh <- WorkerUpdate{
			WorkerID: w.ID,
			StoryID:  w.StoryID,
			State:    state,
			Err:      err,
			Passed:   passed,
			ChangeID: changeID,
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
	send(WorkerSetup, nil, false, "")
	ws, err := workspace.Create(w.Ctx, cfg.ProjectDir, w.StoryID, cfg.WorkspaceBase)
	if err != nil {
		send(WorkerFailed, fmt.Errorf("workspace create: %w", err), false, "")
		return
	}
	w.Workspace = ws.Dir
	w.WorkspaceName = workspace.WorkspaceName(w.StoryID)
	w.BaseChangeID = ws.BaseChangeID

	// Copy state files into workspace
	if err := workspace.CopyState(cfg.ProjectDir, ws.Dir); err != nil {
		send(WorkerFailed, fmt.Errorf("copy state: %w", err), false, "")
		return
	}

	// Ensure log directory exists in workspace
	wsLogDir := filepath.Join(ws.Dir, ".ralph", "logs")
	w.LogDir = wsLogDir

	// 2. Build prompt and run Claude
	send(WorkerRunning, nil, false, "")
	prompt, err := runner.BuildPrompt(cfg.RalphHome, ws.Dir, w.StoryID)
	if err != nil {
		send(WorkerFailed, fmt.Errorf("build prompt: %w", err), false, "")
		return
	}

	// In parallel mode, override the stop condition — this worker only handles one story
	prompt += fmt.Sprintf(`

---
## PARALLEL MODE
You are running as a parallel worker. Other workers are handling other stories simultaneously.
You are ONLY responsible for story **%s**. After completing it, stop immediately.
Do NOT check if all stories are complete. Do NOT emit the COMPLETE signal.
Just implement your story, commit, set passes: true, update progress.md, and stop.`, w.StoryID)

	logPath := runner.LogFilePath(wsLogDir, w.Iteration)
	err = runner.RunClaude(w.Ctx, ws.Dir, prompt, logPath, runner.RunClaudeOpts{
		Iteration: w.Iteration,
		StoryID:   w.StoryID,
	})
	if err != nil {
		if w.Ctx.Err() != nil {
			send(WorkerFailed, w.Ctx.Err(), false, "")
			return
		}
		// Claude CLI failures are likely transient (rate limits, network, etc.)
		sendRetryable(fmt.Errorf("claude run: %w", err))
		return
	}

	// 3. Check if story passed
	p, err := prd.Load(filepath.Join(ws.Dir, "prd.json"))
	if err != nil {
		send(WorkerFailed, fmt.Errorf("load prd: %w", err), false, "")
		return
	}
	story := p.FindStory(w.StoryID)
	passed := story != nil && story.Passes

	// 4. Commit workspace changes
	changeID, err := workspace.CommitWorkspace(w.Ctx, ws.Dir, w.StoryID, w.StoryTitle, w.BaseChangeID)
	if err != nil {
		send(WorkerFailed, fmt.Errorf("commit workspace: %w", err), false, "")
		return
	}

	// 5. Run judge if enabled and story passed
	if cfg.JudgeEnabled && passed {
		send(WorkerJudging, nil, false, changeID)

		// Capture revs for judge
		preRevs := []judge.DirRev{{Dir: ws.Dir, Rev: changeID}}
		result := judge.RunJudge(w.Ctx, cfg.RalphHome, ws.Dir, filepath.Join(ws.Dir, "prd.json"), w.StoryID, preRevs)
		if !result.Passed {
			passed = false
		}
	}

	// 6. Send done
	send(WorkerDone, nil, passed, changeID)
}
