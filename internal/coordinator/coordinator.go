package coordinator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ohjann/ralphplusplus/internal/debuglog"

	"github.com/ohjann/ralphplusplus/internal/checkpoint"
	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/dag"
	"github.com/ohjann/ralphplusplus/internal/fusion"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/notify"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/worker"
	"github.com/ohjann/ralphplusplus/internal/workspace"
)

const maxRetries = 3

// PlanQuality tracks metrics about how well the PRD plan translates to successful builds.
type PlanQuality struct {
	FirstPassCount int // stories that passed on first attempt (no retries)
	RetryCount     int // stories that needed retries before passing
	FailedCount    int // stories that ultimately failed
	AutoPassCount  int // stories that were auto-passed after max rejections
	TotalStories   int // total stories in the run
}

// Score returns a 0.0-1.0 quality score for the plan.
// First-pass successes score 1.0, retried successes score 0.5, failures score 0.0.
func (pq PlanQuality) Score() float64 {
	if pq.TotalStories == 0 {
		return 0
	}
	points := float64(pq.FirstPassCount) + float64(pq.RetryCount)*0.5
	return points / float64(pq.TotalStories)
}

type Coordinator struct {
	cfg        *config.Config
	dag        *dag.DAG
	maxWorkers int
	updateCh   chan worker.WorkerUpdate

	mu              sync.Mutex
	jjMu            sync.Mutex // serialises all jj operations against the main repo (workspace create/destroy, merge)
	paused          bool       // true when paused due to usage limit
	workers         map[worker.WorkerID]*worker.Worker
	completed       map[string]bool
	failed          map[string]bool
	failedErrors    map[string]string // last error message per failed story
	inProgress      map[string]worker.WorkerID
	retries         map[string]int // retry count per story (transient failures)
	storyRetries    map[string]int // retry count for stories that didn't pass
	nextID          worker.WorkerID
	stories         map[string]*prd.UserStory // story lookup
	prdHash         string                    // SHA-256 of prd.json, computed at init
	iterationCount  int                       // total iterations dispatched
	runCosting      *costs.RunCosting         // optional: for including cost data in checkpoints
	notifier        *notify.Notifier          // optional: for push notifications
	firstPass       map[string]bool           // tracks stories that passed on first attempt
	fusionGroups    map[string]*fusion.FusionGroup // active fusion groups by storyID
	complexityCache map[string]bool                // cached complexity assessment results
	fusionMetrics   costs.FusionMetrics            // running fusion outcome metrics for this run
	antiPatterns    []memory.AntiPattern           // detected at startup; injected into worker prompts
}

func New(cfg *config.Config, d *dag.DAG, maxWorkers int, stories []prd.UserStory) *Coordinator {
	storyMap := make(map[string]*prd.UserStory)
	for i := range stories {
		s := stories[i]
		storyMap[s.ID] = &s
	}

	// Compute PRD hash at initialization for reuse across checkpoint writes
	prdHash, err := checkpoint.ComputePRDHash(cfg.PRDFile)
	if err != nil {
		debuglog.Log("warning: could not compute PRD hash: %v", err)
	}

	antiPatterns, err := memory.DetectAntiPatterns(cfg.ProjectDir)
	if err != nil {
		debuglog.Log("warning: anti-pattern detection failed: %v", err)
	}

	return &Coordinator{
		cfg:          cfg,
		dag:          d,
		maxWorkers:   maxWorkers,
		updateCh:     make(chan worker.WorkerUpdate, maxWorkers*2),
		workers:      make(map[worker.WorkerID]*worker.Worker),
		completed:    make(map[string]bool),
		failed:       make(map[string]bool),
		failedErrors: make(map[string]string),
		inProgress:   make(map[string]worker.WorkerID),
		retries:      make(map[string]int),
		storyRetries: make(map[string]int),
		stories:      storyMap,
		prdHash:      prdHash,
		firstPass:       make(map[string]bool),
		fusionGroups:    make(map[string]*fusion.FusionGroup),
		complexityCache: make(map[string]bool),
		antiPatterns:    antiPatterns,
	}
}

// NewFromCheckpoint creates a Coordinator pre-seeded with state from a checkpoint.
func NewFromCheckpoint(
	cfg *config.Config, d *dag.DAG, maxWorkers int, stories []prd.UserStory,
	completedIDs []string, failedStories map[string]checkpoint.FailedStory, iterationCount int,
) *Coordinator {
	c := New(cfg, d, maxWorkers, stories)
	for _, id := range completedIDs {
		c.completed[id] = true
	}
	for id, fs := range failedStories {
		c.failed[id] = true
		c.failedErrors[id] = fs.LastError
	}
	c.iterationCount = iterationCount
	return c
}

// SetRunCosting sets the RunCosting reference so checkpoints include cost data.
func (c *Coordinator) SetRunCosting(rc *costs.RunCosting) {
	c.runCosting = rc
}

// SetNotifier configures optional push notification support.
func (c *Coordinator) SetNotifier(n *notify.Notifier) {
	c.notifier = n
}

// recordStoryFinal persists a terminal story status on the active history
// manifest when one is attached via cfg.HistoryRun. Best-effort: failures
// are logged, not returned, so persistence never blocks the coordinator's
// critical path. Called with c.mu held — SetStoryFinal takes its own lock
// on the Run so there's no deadlock (no shared mutex between the two).
func (c *Coordinator) recordStoryFinal(storyID, status string) {
	if c.cfg == nil || c.cfg.HistoryRun == nil {
		return
	}
	if err := c.cfg.HistoryRun.SetStoryFinal(storyID, status); err != nil {
		debuglog.Log("history: SetStoryFinal(%s,%s) failed: %v", storyID, status, err)
	}
}

// SetDAG installs the dependency DAG after construction. Used when DAG
// analysis is deferred so the daemon API can start serving before the
// (potentially slow) LLM-based dependency analysis completes.
func (c *Coordinator) SetDAG(d *dag.DAG) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dag = d
}

// Notifier returns the coordinator's notifier (may be nil).
func (c *Coordinator) Notifier() *notify.Notifier {
	return c.notifier
}

// AddStory registers a dynamically created story (e.g. an interactive task)
// so that ScheduleReady can create workers for it. The story must already be
// added to the DAG via dag.AddNode before calling this method.
func (c *Coordinator) AddStory(story *prd.UserStory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stories[story.ID] = story
}

// ScheduleReady launches workers for stories whose dependencies are met.
// Returns the number of new workers launched.
//
// Reads cfg.Workers / cfg.NoFusion / cfg.FusionWorkers through a fresh
// Config snapshot per tick so live settings reload is respected.
func (c *Coordinator) ScheduleReady(ctx context.Context) int {
	snap := c.cfg.Snapshot()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.paused || c.dag == nil {
		return 0
	}

	ready := c.dag.Ready(c.completed)

	// Filter out in-progress and failed
	var available []string
	for _, id := range ready {
		if _, inProg := c.inProgress[id]; inProg {
			continue
		}
		if c.failed[id] {
			continue
		}
		available = append(available, id)
	}

	// Limit to available worker slots. The per-tick Workers read lets live
	// settings reload (RV-202) flow into dispatch decisions immediately.
	workerCap := snap.Workers
	if workerCap < 1 {
		workerCap = 1
	}
	slots := workerCap - len(c.inProgress)
	if slots <= 0 {
		return 0
	}
	if len(available) < slots {
		slots = len(available)
	}

	launched := 0
	for i := 0; i < len(available) && slots > 0; i++ {
		storyID := available[i]
		story := c.stories[storyID]
		if story == nil {
			continue
		}

		// Check if this story should use fusion mode (competing implementations)
		useFusion := false
		if !snap.NoFusion && snap.FusionWorkers >= 2 && slots >= snap.FusionWorkers {
			useFusion = c.shouldUseFusionLocked(ctx, story, snap.UtilityModel)
		}

		if useFusion {
			fg := &fusion.FusionGroup{
				StoryID:  storyID,
				Expected: snap.FusionWorkers,
			}
			c.fusionGroups[storyID] = fg
			c.fusionMetrics.GroupsCreated++
			c.inProgress[storyID] = 0 // reserve slot while architect runs
			slots -= snap.FusionWorkers
			launched += snap.FusionWorkers
			c.iterationCount += snap.FusionWorkers
			go c.runFusionArchitectThenSpawn(ctx, storyID, story, fg)
			debuglog.Log("fusion: launching shared architect then %d workers for %s", snap.FusionWorkers, storyID)
		} else {
			w := c.spawnWorkerLocked(ctx, storyID, story, "", snap.StoryTimeout)
			go worker.Run(w, c.cfg, c.updateCh)
			launched++
			c.iterationCount++
			slots--
		}
	}

	return launched
}

// spawnWorkerLocked creates and registers a new worker. Must be called with c.mu held.
// storyTimeoutMinutes is the caller-supplied StoryTimeout from a fresh Config
// snapshot — passed in so spawn shares the same per-tick snapshot as the
// scheduler that decided to launch this worker.
func (c *Coordinator) spawnWorkerLocked(ctx context.Context, storyID string, story *prd.UserStory, fusionSuffix string, storyTimeoutMinutes int) *worker.Worker {
	var wCtx context.Context
	var wCancel context.CancelFunc
	if storyTimeoutMinutes > 0 {
		wCtx, wCancel = context.WithTimeout(ctx, time.Duration(storyTimeoutMinutes)*time.Minute)
	} else {
		wCtx, wCancel = context.WithCancel(ctx)
	}
	c.nextID++
	w := &worker.Worker{
		ID:           c.nextID,
		StoryID:      storyID,
		StoryTitle:   story.Title,
		State:        worker.WorkerIdle,
		Iteration:    int(c.nextID),
		FusionSuffix: fusionSuffix,
		JJMu:         &c.jjMu,
		Ctx:          wCtx,
		Cancel:       wCancel,
		AntiPatterns: c.antiPatterns,
	}
	c.workers[w.ID] = w
	c.inProgress[storyID] = w.ID
	return w
}

// shouldUseFusionLocked checks whether a story is complex enough for fusion mode.
// Uses cached results when available. Must be called with c.mu held.
// utilityModel is passed in from the caller's Config snapshot.
func (c *Coordinator) shouldUseFusionLocked(ctx context.Context, story *prd.UserStory, utilityModel string) bool {
	if cached, ok := c.complexityCache[story.ID]; ok {
		return cached
	}

	// Release lock for the LLM call (can take a few seconds)
	c.mu.Unlock()
	complex, reason, err := fusion.AssessComplexity(ctx, story, utilityModel)
	c.mu.Lock()

	if err != nil {
		debuglog.Log("fusion: complexity assessment failed for %s: %v", story.ID, err)
		c.complexityCache[story.ID] = false
		return false
	}

	c.complexityCache[story.ID] = complex
	debuglog.Log("fusion: %s assessed as complex=%v (%s)", story.ID, complex, reason)
	return complex
}

// runFusionArchitectThenSpawn runs a shared architect phase for fusion workers
// and exports its plan.md to the main project dir, then spawns fusion
// implementer workers. Each worker inherits the plan via the normal
// workspace.CopyState, so shouldRunArchitect returns false and no per-worker
// architect is run. If the architect produced nothing, workers fall back to
// running their own architects.
func (c *Coordinator) runFusionArchitectThenSpawn(ctx context.Context, storyID string, story *prd.UserStory, fg *fusion.FusionGroup) {
	snap := c.cfg.Snapshot()
	if !snap.NoArchitect {
		c.runSharedArchitect(ctx, storyID)
	}

	// Spawn fusion workers
	c.mu.Lock()
	delete(c.inProgress, storyID) // remove placeholder
	for fi := 0; fi < snap.FusionWorkers; fi++ {
		suffix := fmt.Sprintf("-f%d", fi)
		w := c.spawnWorkerLocked(ctx, storyID, story, suffix, snap.StoryTimeout)
		fg.Workers = append(fg.Workers, w.ID)
		go worker.Run(w, c.cfg, c.updateCh)
	}
	c.mu.Unlock()
}

// runSharedArchitect creates a temporary workspace, runs the architect phase,
// and exports the resulting plan.md (plus any other files the architect wrote
// under .ralph/stories/<id>/) back to the main project dir. Fusion workers
// then inherit the plan via the normal workspace.CopyState propagation.
//
// The architect's session is not resumed by subsequent fusion workers: Claude
// CLI scopes session lookup by cwd, and the workers run from different
// workspaces than the -arch dir. Sharing the plan.md artifact is both simpler
// and robust to Claude CLI storage-format changes.
func (c *Coordinator) runSharedArchitect(ctx context.Context, storyID string) {
	snap := c.cfg.Snapshot()
	c.jjMu.Lock()
	ws, err := workspace.Create(ctx, snap.ProjectDir, storyID, snap.WorkspaceBase, "-arch")
	c.jjMu.Unlock()
	if err != nil {
		debuglog.Log("fusion: architect workspace create failed for %s: %v", storyID, err)
		return
	}
	defer func() {
		wsName := workspace.WorkspaceName(storyID) + "-arch"
		c.jjMu.Lock()
		_ = workspace.Destroy(ctx, snap.ProjectDir, wsName, ws.Dir)
		c.jjMu.Unlock()
	}()

	if err := workspace.CopyState(snap.ProjectDir, ws.Dir, storyID); err != nil {
		debuglog.Log("fusion: architect copy state failed for %s: %v", storyID, err)
		return
	}
	if _, err := workspace.RunSetup(ctx, ws.Dir); err != nil {
		debuglog.Log("fusion: architect workspace setup failed for %s: %v", storyID, err)
		return
	}

	wsPRD, _ := prd.Load(filepath.Join(ws.Dir, "prd.json"))

	archParts, err := runner.BuildPrompt(snap.RalphHome, ws.Dir, storyID, wsPRD, runner.BuildPromptOpts{
		Role:           roles.RoleArchitect,
		MemoryDisabled: snap.Memory.Disabled,
	})
	if err != nil {
		debuglog.Log("fusion: architect prompt build failed for %s: %v", storyID, err)
		return
	}
	archParts.UserMessage = worker.AppendParallelMode(archParts.UserMessage, storyID)

	logDir := filepath.Join(ws.Dir, ".ralph", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := runner.LogFilePath(logDir, 0) + ".architect"

	if _, err := runner.RunClaudeForIteration(ctx, c.cfg, ws.Dir, archParts.UserMessage, logPath, runner.IterationOpts{
		StoryID:      storyID,
		Role:         roles.RoleArchitect,
		Iter:         0,
		Model:        worker.ResolveModel(roles.RoleArchitect, c.cfg),
		SystemAppend: archParts.SystemAppend,
	}); err != nil {
		debuglog.Log("fusion: architect run failed for %s: %v", storyID, err)
	}

	if err := workspace.ExportStoryState(ws.Dir, snap.ProjectDir, storyID); err != nil {
		debuglog.Log("fusion: architect export state failed for %s: %v", storyID, err)
	}
}

// HandleUpdate processes a worker update.
// Returns true if the story should be retried (transient failure with retries remaining).
func (c *Coordinator) HandleUpdate(u worker.WorkerUpdate) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if w, ok := c.workers[u.WorkerID]; ok {
		w.State = u.State
		w.Role = u.Role
	}

	shouldRetry := false
	switch u.State {
	case worker.WorkerDone:
		// Check if this worker is part of a fusion group
		if fg, ok := c.fusionGroups[u.StoryID]; ok {
			fg.Results = append(fg.Results, fusion.FusionResult{
				WorkerID:    u.WorkerID,
				ChangeID:    u.ChangeID,
				Passed:      u.Passed,
				JudgeResult: u.JudgeResult,
				TokenUsage:  u.TokenUsage,
			})
			delete(c.inProgress, u.StoryID)
			// Don't complete/retry yet — wait for all fusion workers
			break
		}

		delete(c.inProgress, u.StoryID)
		if u.Passed {
			c.completed[u.StoryID] = true
			// Track first-pass success: no retries were needed
			if c.retries[u.StoryID] == 0 && c.storyRetries[u.StoryID] == 0 {
				c.firstPass[u.StoryID] = true
			}
			c.recordStoryFinal(u.StoryID, history.StatusComplete)
		} else {
			// Always retry — the judge auto-pass (--judge-max-rejections) is the
			// safety valve that guarantees eventual completion.
			errMsg := "story did not pass"
			if u.Err != nil {
				errMsg = u.Err.Error()
			}
			c.storyRetries[u.StoryID]++
			c.failedErrors[u.StoryID] = errMsg
			shouldRetry = true
		}
	case worker.WorkerFailed:
		delete(c.inProgress, u.StoryID)
		errMsg := ""
		if u.Err != nil {
			errMsg = u.Err.Error()
		}
		// context.Canceled is never the story's fault — it means we pulled
		// the rug on the worker (user pause, daemon shutdown, ctrl+C). The
		// story goes back to the ready pool for the next schedule cycle so
		// resume picks it up. Not retry-counted (the worker didn't get a
		// fair swing), not persisted as failed.
		cancelled := u.Err != nil && errors.Is(u.Err, context.Canceled)
		if u.UsageLimit {
			// Cancel all workers — nothing can proceed until the limit resets
			for _, w := range c.workers {
				if w.Cancel != nil {
					w.Cancel()
				}
			}
			c.paused = true
			c.failedErrors[u.StoryID] = errMsg
			// Don't mark as failed — will retry after user resumes
		} else if cancelled {
			// User pause / daemon shutdown / ctrl+C — don't mark failed,
			// don't increment retries. Story sits in limbo until resume
			// or next daemon start pulls it back through ScheduleReady.
			c.failedErrors[u.StoryID] = errMsg
		} else if u.Retryable && c.retries[u.StoryID] < maxRetries {
			c.retries[u.StoryID]++
			c.failedErrors[u.StoryID] = errMsg
			// Don't mark as failed — leave it available for ScheduleReady
			shouldRetry = true
		} else {
			c.failed[u.StoryID] = true
			c.failedErrors[u.StoryID] = errMsg
			c.recordStoryFinal(u.StoryID, history.StatusFailed)
		}
	}

	// Write checkpoint after state change
	c.writeCheckpointLocked()

	return shouldRetry
}

// writeCheckpointLocked writes the current state as a checkpoint. Must be called with c.mu held.
func (c *Coordinator) writeCheckpointLocked() {
	// Build in-progress list
	inProgress := make([]string, 0, len(c.inProgress))
	for id := range c.inProgress {
		inProgress = append(inProgress, id)
	}
	sort.Strings(inProgress)

	// Build completed list
	completedStories := make([]string, 0, len(c.completed))
	for id := range c.completed {
		completedStories = append(completedStories, id)
	}
	sort.Strings(completedStories)

	// Build failed stories map
	failedStories := make(map[string]checkpoint.FailedStory)
	for id := range c.failed {
		failedStories[id] = checkpoint.FailedStory{
			Retries:   c.retries[id] + c.storyRetries[id],
			LastError: c.failedErrors[id],
		}
	}

	// Build DAG map from the DAG's nodes
	dagMap := make(map[string][]string)
	if c.dag != nil {
		for id, node := range c.dag.Nodes {
			dagMap[id] = node.DependsOn
		}
	}

	snap := c.cfg.Snapshot()
	// Recompute PRD hash from current file (it may have been modified during the run)
	if hash, err := checkpoint.ComputePRDHash(snap.PRDFile); err == nil {
		c.prdHash = hash
	}

	cp := checkpoint.Checkpoint{
		PRDHash:          c.prdHash,
		Phase:            "parallel",
		CompletedStories: completedStories,
		FailedStories:    failedStories,
		InProgress:       inProgress,
		DAG:              dagMap,
		IterationCount:   c.iterationCount,
	}

	if c.runCosting != nil {
		snap := c.runCosting.Snapshot()
		cp.CostData = &snap
	}

	if err := checkpoint.Save(snap.ProjectDir, cp); err != nil {
		debuglog.Log("warning: failed to write checkpoint: %v", err)
	}
}

// MergeAndSync rebases the worker's changes onto main, syncs prd.json and progress.md.
// If the rebase produces conflicts, it runs Claude to resolve them before advancing.
// Returns true if conflicts were resolved during the merge.
//
// This method is serialised via jjMu so that concurrent goroutines
// cannot run overlapping jj operations, which would create divergent
// sibling operations and corrupt the working copy state.
func (c *Coordinator) MergeAndSync(ctx context.Context, u worker.WorkerUpdate) (conflictsResolved bool, err error) {
	// Serialise all merge-back operations. Multiple workers can finish around
	// the same time and the caller dispatches each merge as a concurrent
	// goroutine. Without this lock, two rebases would both target the
	// same @- and create divergent sibling commits instead of a linear chain.
	c.jjMu.Lock()
	defer c.jjMu.Unlock()

	c.mu.Lock()
	w, ok := c.workers[u.WorkerID]
	c.mu.Unlock()
	if !ok {
		return false, fmt.Errorf("worker %d not found", u.WorkerID)
	}

	if u.ChangeID == "" || w.Workspace == "" {
		return false, nil
	}

	// Rebase onto main
	result, mergeErr := workspace.MergeBack(ctx, c.cfg.Snapshot().ProjectDir, u.ChangeID)
	if mergeErr != nil {
		return false, fmt.Errorf("rebase: %w", mergeErr)
	}

	// If conflicts, run Claude to resolve them in-place
	if result.HasConflict {
		if resolveErr := c.resolveConflicts(ctx, u.StoryID, result.ConflictedFiles); resolveErr != nil {
			return false, fmt.Errorf("conflict resolution: %w", resolveErr)
		}
		conflictsResolved = true
	}

	snap := c.cfg.Snapshot()

	// Sync prd.json: read workspace's prd.json and update main's
	wsPRD, prdErr := prd.Load(filepath.Join(w.Workspace, "prd.json"))
	if prdErr == nil {
		mainPRD, mainPrdErr := prd.Load(snap.PRDFile)
		if mainPrdErr == nil {
			// Update the specific story's passes status
			wsStory := wsPRD.FindStory(u.StoryID)
			if wsStory != nil {
				mainPRD.SetPasses(u.StoryID, wsStory.Passes)
				_ = prd.Save(snap.PRDFile, mainPRD)
			}
		}
	}

	// Append workspace progress.md entries to main
	wsProgress := filepath.Join(w.Workspace, "progress.md")
	if data, readErr := os.ReadFile(wsProgress); readErr == nil && len(data) > 0 {
		if f, openErr := os.OpenFile(snap.ProgressFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); openErr == nil {
			defer f.Close()
			if _, writeErr := f.Write(data); writeErr != nil {
				debuglog.Log("coordinator: failed to append progress: %v", writeErr)
			}
		}
	}

	// Append workspace events.jsonl to main
	wsEvents := filepath.Join(w.Workspace, ".ralph", "events.jsonl")
	if data, readErr := os.ReadFile(wsEvents); readErr == nil && len(data) > 0 {
		if f, openErr := os.OpenFile(
			filepath.Join(snap.ProjectDir, ".ralph", "events.jsonl"),
			os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644,
		); openErr == nil {
			defer f.Close()
			if _, writeErr := f.Write(data); writeErr != nil {
				debuglog.Log("coordinator: failed to append events: %v", writeErr)
			}
		}
	}

	// Sync story state directory from workspace to main
	wsStoryDir := filepath.Join(w.Workspace, ".ralph", "stories", u.StoryID)
	if info, statErr := os.Stat(wsStoryDir); statErr == nil && info.IsDir() {
		mainStoryDir := filepath.Join(snap.ProjectDir, ".ralph", "stories", u.StoryID)
		_ = os.MkdirAll(mainStoryDir, 0o755)
		entries, readErr := os.ReadDir(wsStoryDir)
		if readErr == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				data, fileErr := os.ReadFile(filepath.Join(wsStoryDir, entry.Name()))
				if fileErr == nil {
					_ = os.WriteFile(filepath.Join(mainStoryDir, entry.Name()), data, 0o644)
				}
			}
		}
	}

	// Re-write the checkpoint so the stored PRD hash reflects the post-merge
	// state of prd.json (which was just updated with passes=true). Otherwise
	// the next TUI startup sees a hash mismatch and silently discards the
	// checkpoint, treating a mid-run restart as a fresh start.
	c.mu.Lock()
	c.writeCheckpointLocked()
	c.mu.Unlock()

	return conflictsResolved, nil
}

// resolveConflicts runs Claude to resolve merge conflicts in the main workspace.
// MergeBack has already switched @ to the conflicted commit via jj edit.
func (c *Coordinator) resolveConflicts(ctx context.Context, storyID, conflictedFiles string) error {
	prompt := fmt.Sprintf(`You are resolving merge conflicts in a jj (Jujutsu) repository.

After rebasing story %s, the following files have conflicts:
%s

INSTRUCTIONS:
1. Read each conflicted file — they contain jj conflict markers (lines with <<<<<<<, >>>>>>>  etc.)
2. Resolve ALL conflicts by editing the files to combine both sides correctly
3. Preserve the intent of BOTH sides — do not simply pick one side
4. Remove ALL conflict markers
5. Verify the result compiles / has valid syntax
6. Do NOT commit or run jj commands — jj auto-snapshots your edits

Be concise. Just fix the conflicts and stop.`, storyID, conflictedFiles)

	snap := c.cfg.Snapshot()
	logDir := filepath.Join(snap.ProjectDir, ".ralph", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, fmt.Sprintf("conflict-resolution-%s.log", storyID))

	if _, err := runner.RunClaudeForIteration(ctx, c.cfg, snap.ProjectDir, prompt, logPath, runner.IterationOpts{
		StoryID: storyID,
		Role:    "conflict-resolution",
		Iter:    0,
	}); err != nil {
		return fmt.Errorf("claude conflict resolution: %w", err)
	}

	// Advance @ past the now-resolved commit
	return workspace.AdvanceAfterResolve(ctx, snap.ProjectDir)
}

// FusionGroupReady returns the fusion group for a story if all workers have reported.
// Returns nil if the story is not a fusion story or not all workers are done yet.
func (c *Coordinator) FusionGroupReady(storyID string) *fusion.FusionGroup {
	c.mu.Lock()
	defer c.mu.Unlock()
	fg, ok := c.fusionGroups[storyID]
	if !ok {
		return nil
	}
	if !fg.AllDone() {
		return nil
	}
	return fg
}

// CompleteFusion marks a fusion story as completed with the winning worker's result.
// Callers should merge the winner and abandon losers before calling this.
func (c *Coordinator) CompleteFusion(storyID string, passed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if passed {
		c.completed[storyID] = true
		if c.retries[storyID] == 0 && c.storyRetries[storyID] == 0 {
			c.firstPass[storyID] = true
		}
		c.recordStoryFinal(storyID, history.StatusComplete)
	} else {
		c.storyRetries[storyID]++
		c.failedErrors[storyID] = "all fusion implementations failed"
	}
	delete(c.fusionGroups, storyID)
	c.writeCheckpointLocked()
}

// IsFusionStory returns true if the story is currently in fusion mode.
func (c *Coordinator) IsFusionStory(storyID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.fusionGroups[storyID]
	return ok
}

// FusionProgress returns (completed, total) worker counts for a fusion story.
func (c *Coordinator) FusionProgress(storyID string) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fg, ok := c.fusionGroups[storyID]
	if !ok {
		return 0, 0
	}
	return len(fg.Results), fg.Expected
}

// GetStory returns a story by ID.
func (c *Coordinator) GetStory(storyID string) *prd.UserStory {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stories[storyID]
}

// CleanupWorker destroys the workspace for a completed/failed worker.
// ResumeWorkerWithHint sets a resume hint on the worker and cancels its context,
// triggering the worker's run loop to relaunch Claude with --resume and the hint.
func (c *Coordinator) ResumeWorkerWithHint(workerID worker.WorkerID, hint string) {
	c.mu.Lock()
	w, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok {
		return
	}
	w.ResumeHint = hint
	w.Cancel()
}

func (c *Coordinator) CleanupWorker(ctx context.Context, workerID worker.WorkerID) {
	c.mu.Lock()
	w, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok || w.Workspace == "" {
		return
	}

	projectDir := c.cfg.Snapshot().ProjectDir
	c.jjMu.Lock()
	_ = workspace.Destroy(ctx, projectDir, w.WorkspaceName, w.Workspace)
	c.jjMu.Unlock()
}

// AbandonChange serialises jj abandon through jjMu to prevent concurrent
// jj operations from creating sibling operations.
func (c *Coordinator) AbandonChange(ctx context.Context, changeID string) {
	projectDir := c.cfg.Snapshot().ProjectDir
	c.jjMu.Lock()
	_ = workspace.AbandonChange(ctx, projectDir, changeID)
	c.jjMu.Unlock()
}

// PreserveFailedLogs copies the worker's activity log to the main project's
// .ralph/logs/worker-<storyID>-failed.log for post-mortem debugging.
func (c *Coordinator) PreserveFailedLogs(storyID string, workerID worker.WorkerID) {
	c.preserveWorkerLogs(storyID, workerID, "failed")
}

// PreserveWorkerLogs copies the worker's activity log to the main project's
// .ralph/logs/worker-<storyID>.log so output survives workspace cleanup.
func (c *Coordinator) PreserveWorkerLogs(storyID string, workerID worker.WorkerID) {
	c.preserveWorkerLogs(storyID, workerID, "")
}

func (c *Coordinator) preserveWorkerLogs(storyID string, workerID worker.WorkerID, suffix string) {
	c.mu.Lock()
	w, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok || w.LogDir == "" {
		return
	}

	srcPath := filepath.Join(w.LogDir, fmt.Sprintf("iteration-%d-activity.log", w.Iteration))
	data, err := os.ReadFile(srcPath)
	if err != nil || len(data) == 0 {
		return
	}

	dstDir := filepath.Join(c.cfg.Snapshot().ProjectDir, ".ralph", "logs")
	_ = os.MkdirAll(dstDir, 0o755)
	filename := fmt.Sprintf("worker-%s.log", storyID)
	if suffix != "" {
		filename = fmt.Sprintf("worker-%s-%s.log", storyID, suffix)
	}
	_ = os.WriteFile(filepath.Join(dstDir, filename), data, 0o644)
}

// UpdateCh returns a receive-only channel of worker updates. Callers (the
// daemon event loop or the TUI) read from this channel to drive coordination.
func (c *Coordinator) UpdateCh() <-chan worker.WorkerUpdate {
	return c.updateCh
}

// DrainUpdates non-blockingly reads all pending updates from the update channel
// and processes them via HandleUpdate. This prevents stale updates from
// accumulating when workers are cancelled (e.g. on usage limit).
func (c *Coordinator) DrainUpdates() int {
	drained := 0
	for {
		select {
		case u := <-c.updateCh:
			c.HandleUpdate(u)
			drained++
		default:
			return drained
		}
	}
}

// ActiveCount returns the number of workers currently in progress.
func (c *Coordinator) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inProgress)
}

// AllDone returns true if all stories are completed or failed and none are in progress.
func (c *Coordinator) AllDone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inProgress) > 0 {
		return false
	}
	// Fusion groups still collecting results are not done
	if len(c.fusionGroups) > 0 {
		return false
	}
	if c.dag == nil {
		// DAG not yet built (daemon still in Prepare). If any story is
		// still pending, the run is NOT done — it just hasn't started yet.
		// Without this guard, the TUI fires a premature completion report
		// as soon as it connects to a daemon that is mid-Prepare.
		for id := range c.stories {
			if !c.completed[id] && !c.failed[id] {
				return false
			}
		}
		return true
	}
	for id := range c.dag.Nodes {
		if !c.completed[id] && !c.failed[id] {
			// Check if it could still be scheduled (deps met)
			allMet := true
			for _, dep := range c.dag.Nodes[id].DependsOn {
				if !c.completed[dep] {
					allMet = false
					break
				}
			}
			if allMet {
				return false // still schedulable
			}
		}
	}
	return true
}

// CompletedCount returns how many stories completed successfully.
func (c *Coordinator) CompletedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.completed)
}

// FailedCount returns how many stories failed.
func (c *Coordinator) FailedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.failed)
}

// IterationCount returns total iterations dispatched.
func (c *Coordinator) IterationCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.iterationCount
}

// Workers returns a snapshot of all workers for TUI display.
func (c *Coordinator) Workers() map[worker.WorkerID]*worker.Worker {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[worker.WorkerID]*worker.Worker)
	for k, v := range c.workers {
		result[k] = v
	}
	return result
}

// IsPaused returns true if the coordinator is paused due to a usage limit.
func (c *Coordinator) IsPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// Resume unpauses the coordinator so scheduling can continue.
func (c *Coordinator) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = false
}

// CancelAll cancels all active workers.
func (c *Coordinator) CancelAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.workers {
		if w.Cancel != nil {
			w.Cancel()
		}
	}
}

// Pause flips the paused flag and cancels any active workers. ScheduleReady
// (line 174) short-circuits while paused, so no new work dispatches until
// Resume is called. User-initiated pauses go through here; usage-limit
// pauses also set the same flag at line 436.
func (c *Coordinator) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = true
	for _, w := range c.workers {
		if w.Cancel != nil {
			w.Cancel()
		}
	}
}

// CleanupAll destroys all workspaces.
func (c *Coordinator) CleanupAll(ctx context.Context) {
	c.mu.Lock()
	workers := make([]*worker.Worker, 0, len(c.workers))
	for _, w := range c.workers {
		workers = append(workers, w)
	}
	c.mu.Unlock()

	projectDir := c.cfg.Snapshot().ProjectDir
	for _, w := range workers {
		if w.Workspace != "" {
			c.jjMu.Lock()
			_ = workspace.Destroy(ctx, projectDir, w.WorkspaceName, w.Workspace)
			c.jjMu.Unlock()
		}
	}
}

// IsWorkerActive returns true if the worker exists and is still running (not done/failed).
func (c *Coordinator) IsWorkerActive(wID worker.WorkerID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, ok := c.workers[wID]
	if !ok {
		return false
	}
	return w.State != worker.WorkerDone && w.State != worker.WorkerFailed
}

// GetWorkerActivityPath returns the activity log path for a given worker.
func (c *Coordinator) GetWorkerActivityPath(wID worker.WorkerID) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, ok := c.workers[wID]
	if !ok || w.LogDir == "" {
		return ""
	}
	return filepath.Join(w.LogDir, fmt.Sprintf("iteration-%d-activity.log", w.Iteration))
}

// ActiveStoryIDs returns the story IDs currently being worked on, sorted for stable display.
func (c *Coordinator) ActiveStoryIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for id := range c.inProgress {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// MergeCompleteMsg signals that a merge-back operation completed.
type MergeCompleteMsg struct {
	StoryID           string
	WorkerID          worker.WorkerID
	ChangeID          string // jj change ID, needed for cleanup on merge failure
	Err               error
	ConflictsResolved bool
}

// FusionCompareDoneMsg signals that a fusion comparison is complete.
type FusionCompareDoneMsg struct {
	StoryID        string
	WinnerWorkerID worker.WorkerID
	WinnerChangeID string
	LoserWorkerIDs []worker.WorkerID
	LoserChangeIDs []string
	Reason         string
	Passed         bool // true if at least one candidate passed and a winner was selected
	MultiplePassed bool // true if 2+ candidates passed (the comparison judge actually ran)
	WasFirstPasser bool // true if winner is the lowest-WorkerID passer (proxy for first to pass)
	Err            error
}

// DAGAnalyzedMsg signals that DAG analysis is complete.
type DAGAnalyzedMsg struct {
	DAG *dag.DAG
	Err error
}

// WorkerActivityMsg carries activity content for a specific worker.
type WorkerActivityMsg struct {
	WorkerID worker.WorkerID
	Content  string
}

// workerStuckMsg carries a stuck event for all active workers.
type WorkerStuckMsg struct {
	WorkerID worker.WorkerID
	StoryID  string
}

// StoryTitle returns the title of a story by ID.
func (c *Coordinator) StoryTitle(storyID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.stories[storyID]; ok {
		return s.Title
	}
	return storyID
}

// IsInProgress returns true if the story is currently being worked on by a worker.
func (c *Coordinator) IsInProgress(storyID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.inProgress[storyID]
	return ok
}

// IsCompleted returns true if the story was completed by a worker.
func (c *Coordinator) IsCompleted(storyID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.completed[storyID]
}

// IsFailed returns true if the story failed in a worker.
func (c *Coordinator) IsFailed(storyID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed[storyID]
}

// FailedError returns the last error message for a failed story.
func (c *Coordinator) FailedError(storyID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failedErrors[storyID]
}

// IsBlockedByFailure returns true and the blocking dependency ID if the story
// cannot be scheduled because one of its dependencies failed (directly or transitively).
func (c *Coordinator) IsBlockedByFailure(storyID string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	visited := make(map[string]bool)
	return c.isBlockedByFailureLocked(storyID, visited)
}

// isBlockedByFailureLocked checks transitively whether a story is blocked by any failed dependency.
// Must be called with c.mu held.
func (c *Coordinator) isBlockedByFailureLocked(storyID string, visited map[string]bool) (bool, string) {
	if visited[storyID] {
		return false, ""
	}
	visited[storyID] = true

	node, ok := c.dag.Nodes[storyID]
	if !ok {
		return false, ""
	}
	for _, dep := range node.DependsOn {
		if c.failed[dep] {
			return true, dep
		}
		// Check if the dependency is itself blocked by a failure upstream
		if !c.completed[dep] {
			if blocked, blocker := c.isBlockedByFailureLocked(dep, visited); blocked {
				return true, blocker
			}
		}
	}
	return false, ""
}

// RecordFusionOutcome updates fusion metrics after a comparison completes.
// multiplePassed indicates 2+ implementations passed; pickedNonFirst indicates
// the comparison judge picked a non-first-to-pass implementation.
func (c *Coordinator) RecordFusionOutcome(multiplePassed, pickedNonFirst bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if multiplePassed {
		c.fusionMetrics.MultiplesPassed++
		if pickedNonFirst {
			c.fusionMetrics.ComparisonPicked++
		}
	}
}

// GetFusionMetrics returns a snapshot of fusion metrics for this run.
func (c *Coordinator) GetFusionMetrics() costs.FusionMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fusionMetrics
}

// GetAntiPatterns returns the anti-patterns detected at startup.
func (c *Coordinator) GetAntiPatterns() []memory.AntiPattern {
	return c.antiPatterns
}

// GetPlanQuality returns plan quality metrics based on the current run state.
func (c *Coordinator) GetPlanQuality() PlanQuality {
	c.mu.Lock()
	defer c.mu.Unlock()
	pq := PlanQuality{
		TotalStories: len(c.stories),
		FailedCount:  len(c.failed),
	}
	for id := range c.completed {
		if c.firstPass[id] {
			pq.FirstPassCount++
		} else {
			pq.RetryCount++
		}
	}
	return pq
}

// FormatWorkerStates returns a display string of worker states.
func FormatWorkerStates(workers map[worker.WorkerID]*worker.Worker) string {
	if len(workers) == 0 {
		return ""
	}
	var parts []string
	for _, w := range workers {
		if w.State == worker.WorkerIdle {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", w.StoryID, w.State))
	}
	return strings.Join(parts, " ")
}

// RegisterTestWorker injects a worker into the coordinator for testing.
// The worker is registered as active (in-progress) with the given state.
func (c *Coordinator) RegisterTestWorker(w *worker.Worker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workers[w.ID] = w
	c.inProgress[w.StoryID] = w.ID
	if w.ID > c.nextID {
		c.nextID = w.ID
	}
}

// MarkCompleteForTest marks a story as completed (test helper only).
func (c *Coordinator) MarkCompleteForTest(storyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.completed[storyID] = true
	delete(c.inProgress, storyID)
}
