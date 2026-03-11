package coordinator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/worker"
	"github.com/eoghanhynes/ralph/internal/workspace"
)

const maxRetries = 3

type Coordinator struct {
	cfg        *config.Config
	dag        *dag.DAG
	maxWorkers int
	updateCh   chan worker.WorkerUpdate

	mu         sync.Mutex
	workers    map[worker.WorkerID]*worker.Worker
	completed  map[string]bool
	failed     map[string]bool
	inProgress map[string]worker.WorkerID
	retries    map[string]int // retry count per story
	nextID     worker.WorkerID
	stories    map[string]*prd.UserStory // story lookup
}

func New(cfg *config.Config, d *dag.DAG, maxWorkers int, stories []prd.UserStory) *Coordinator {
	storyMap := make(map[string]*prd.UserStory)
	for i := range stories {
		s := stories[i]
		storyMap[s.ID] = &s
	}
	return &Coordinator{
		cfg:        cfg,
		dag:        d,
		maxWorkers: maxWorkers,
		updateCh:   make(chan worker.WorkerUpdate, maxWorkers*2),
		workers:    make(map[worker.WorkerID]*worker.Worker),
		completed:  make(map[string]bool),
		failed:     make(map[string]bool),
		inProgress: make(map[string]worker.WorkerID),
		retries:    make(map[string]int),
		stories:    storyMap,
	}
}

// ScheduleReady launches workers for stories whose dependencies are met.
// Returns the number of new workers launched.
func (c *Coordinator) ScheduleReady(ctx context.Context) int {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	// Limit to available worker slots
	slots := c.maxWorkers - len(c.inProgress)
	if slots <= 0 {
		return 0
	}
	if len(available) < slots {
		slots = len(available)
	}

	launched := 0
	for i := 0; i < slots; i++ {
		storyID := available[i]
		story := c.stories[storyID]
		if story == nil {
			continue
		}

		wCtx, wCancel := context.WithCancel(ctx)
		c.nextID++
		w := &worker.Worker{
			ID:        c.nextID,
			StoryID:   storyID,
			StoryTitle: story.Title,
			State:     worker.WorkerIdle,
			Iteration: int(c.nextID), // use worker ID as iteration for unique log paths
			Ctx:       wCtx,
			Cancel:    wCancel,
		}
		c.workers[w.ID] = w
		c.inProgress[storyID] = w.ID

		go worker.Run(w, c.cfg, c.updateCh)
		launched++
	}

	return launched
}

// HandleUpdate processes a worker update.
// Returns true if the story should be retried (transient failure with retries remaining).
func (c *Coordinator) HandleUpdate(u worker.WorkerUpdate) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if w, ok := c.workers[u.WorkerID]; ok {
		w.State = u.State
	}

	switch u.State {
	case worker.WorkerDone:
		delete(c.inProgress, u.StoryID)
		if u.Passed {
			c.completed[u.StoryID] = true
		} else {
			c.failed[u.StoryID] = true
		}
	case worker.WorkerFailed:
		delete(c.inProgress, u.StoryID)
		if u.Retryable && c.retries[u.StoryID] < maxRetries {
			c.retries[u.StoryID]++
			// Don't mark as failed — leave it available for ScheduleReady
			return true
		}
		c.failed[u.StoryID] = true
	}
	return false
}

// MergeAndSync rebases the worker's changes onto main, syncs prd.json and progress.md.
// If the rebase produces conflicts, it runs Claude to resolve them before advancing.
// Returns true if conflicts were resolved during the merge.
func (c *Coordinator) MergeAndSync(ctx context.Context, u worker.WorkerUpdate) (conflictsResolved bool, err error) {
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
	result, mergeErr := workspace.MergeBack(ctx, c.cfg.ProjectDir, u.ChangeID)
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

	// Sync prd.json: read workspace's prd.json and update main's
	wsPRD, prdErr := prd.Load(filepath.Join(w.Workspace, "prd.json"))
	if prdErr == nil {
		mainPRD, mainPrdErr := prd.Load(c.cfg.PRDFile)
		if mainPrdErr == nil {
			// Update the specific story's passes status
			wsStory := wsPRD.FindStory(u.StoryID)
			if wsStory != nil {
				mainPRD.SetPasses(u.StoryID, wsStory.Passes)
				_ = prd.Save(c.cfg.PRDFile, mainPRD)
			}
		}
	}

	// Append workspace progress.md entries to main
	wsProgress := filepath.Join(w.Workspace, "progress.md")
	if data, readErr := os.ReadFile(wsProgress); readErr == nil && len(data) > 0 {
		f, openErr := os.OpenFile(c.cfg.ProgressFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if openErr == nil {
			f.Write(data)
			f.Close()
		}
	}

	// Append workspace events.jsonl to main
	wsEvents := filepath.Join(w.Workspace, ".ralph", "events.jsonl")
	if data, readErr := os.ReadFile(wsEvents); readErr == nil && len(data) > 0 {
		f, openErr := os.OpenFile(
			filepath.Join(c.cfg.ProjectDir, ".ralph", "events.jsonl"),
			os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644,
		)
		if openErr == nil {
			f.Write(data)
			f.Close()
		}
	}

	// Sync story state directory from workspace to main
	wsStoryDir := filepath.Join(w.Workspace, ".ralph", "stories", u.StoryID)
	if info, statErr := os.Stat(wsStoryDir); statErr == nil && info.IsDir() {
		mainStoryDir := filepath.Join(c.cfg.ProjectDir, ".ralph", "stories", u.StoryID)
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

	logDir := filepath.Join(c.cfg.ProjectDir, ".ralph", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, fmt.Sprintf("conflict-resolution-%s.log", storyID))

	if err := runner.RunClaude(ctx, c.cfg.ProjectDir, prompt, logPath); err != nil {
		return fmt.Errorf("claude conflict resolution: %w", err)
	}

	// Advance @ past the now-resolved commit
	return workspace.AdvanceAfterResolve(ctx, c.cfg.ProjectDir)
}

// CleanupWorker destroys the workspace for a completed/failed worker.
func (c *Coordinator) CleanupWorker(ctx context.Context, workerID worker.WorkerID) {
	c.mu.Lock()
	w, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok || w.Workspace == "" {
		return
	}

	_ = workspace.Destroy(ctx, c.cfg.ProjectDir, w.WorkspaceName, w.Workspace)
}

// ListenCmd returns a tea.Cmd that waits for the next worker update.
func (c *Coordinator) ListenCmd() tea.Cmd {
	return func() tea.Msg {
		u := <-c.updateCh
		return WorkerUpdateMsg{Update: u}
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

// CleanupAll destroys all workspaces.
func (c *Coordinator) CleanupAll(ctx context.Context) {
	c.mu.Lock()
	workers := make([]*worker.Worker, 0, len(c.workers))
	for _, w := range c.workers {
		workers = append(workers, w)
	}
	c.mu.Unlock()

	for _, w := range workers {
		if w.Workspace != "" {
			_ = workspace.Destroy(ctx, c.cfg.ProjectDir, w.WorkspaceName, w.Workspace)
		}
	}
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

// WorkerUpdateMsg wraps a worker update for the TUI message system.
type WorkerUpdateMsg struct {
	Update worker.WorkerUpdate
}

// MergeCompleteMsg signals that a merge-back operation completed.
type MergeCompleteMsg struct {
	StoryID           string
	WorkerID          worker.WorkerID
	Err               error
	ConflictsResolved bool
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
