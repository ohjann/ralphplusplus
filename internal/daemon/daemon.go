package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/coordinator"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/events"
	rexec "github.com/ohjann/ralphplusplus/internal/exec"
	"github.com/ohjann/ralphplusplus/internal/fusion"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/lockfile"
	"github.com/ohjann/ralphplusplus/internal/notify"
	"github.com/ohjann/ralphplusplus/internal/postcompletion"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// AlreadyRunningError is returned by Daemon.Run when another daemon holds
// the per-repo singleton lock. The second invocation is expected to print
// a friendly message and exit 0 (the race was resolved, not a failure).
type AlreadyRunningError struct {
	PID       int
	StartedAt time.Time
}

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("already running at pid %d (started %s)", e.PID, e.StartedAt.Format(time.RFC3339))
}

// IsAlreadyRunning reports whether err wraps AlreadyRunningError.
func IsAlreadyRunning(err error) bool {
	var e *AlreadyRunningError
	return errors.As(err, &e)
}

// Daemon owns the coordinator and runs the coordination event loop,
// replacing the TUI's Update() handler for worker/merge messages.
type Daemon struct {
	Coord      *coordinator.Coordinator
	Cfg        *config.Config
	Notifier   *notify.Notifier
	RunCosting *costs.RunCosting
	Version    string
	prepare    func(ctx context.Context) error

	// SSE broadcaster for daemon events
	sseMu       sync.Mutex
	sseClients  []chan DaemonEvent
	clientCount atomic.Int32

	// HTTP API server over Unix socket
	apiServer *APIServer

	// Lifecycle
	ctx          context.Context
	cancel       context.CancelFunc
	pidFile      string
	socketPath   string
	lockPath     string
	singleton    *lockfile.Handle
	startTime    time.Time

	// Client connect/disconnect notifications for idle timer
	clientNotifyCh chan struct{}

	// Coordination state
	totalStories     int
	completedStories int

	// postCompletionRan guards runPostCompletion so it fires at most
	// once per daemon lifetime. The on-disk sentinel
	// (.ralph/post-run-complete.json) provides additional
	// cross-restart guarding.
	postCompletionRan atomic.Bool
}

// DaemonOpts holds optional configuration for the daemon.
type DaemonOpts struct {
	Notifier     *notify.Notifier
	RunCosting   *costs.RunCosting
	Version      string
	TotalStories int
	// Prepare runs after the API socket is serving but before the event
	// loop starts. Use it for slow setup (e.g. LLM-based DAG analysis) so
	// clients can attach during the wait instead of timing out.
	Prepare func(ctx context.Context) error
}

// New creates a new Daemon.
func New(cfg *config.Config, coord *coordinator.Coordinator, opts DaemonOpts) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		Coord:        coord,
		Cfg:          cfg,
		Notifier:     opts.Notifier,
		RunCosting:   opts.RunCosting,
		Version:      opts.Version,
		prepare:      opts.Prepare,
		ctx:          ctx,
		cancel:       cancel,
		pidFile:      filepath.Join(cfg.ProjectDir, ".ralph", "daemon.pid"),
		socketPath:   filepath.Join(cfg.ProjectDir, ".ralph", "daemon.sock"),
		lockPath:     filepath.Join(cfg.ProjectDir, ".ralph", "daemon.lock"),
		startTime:      time.Now(),
		totalStories:   opts.TotalStories,
		sseClients:     make([]chan DaemonEvent, 0),
		clientNotifyCh: make(chan struct{}, 1),
	}
}

// Run starts the daemon: acquires the per-repo singleton lock, writes PID
// file, installs signal handlers, starts the status page, and enters the
// coordination event loop. It blocks until shutdown is triggered.
//
// If a live daemon already holds the singleton lock, Run returns an
// *AlreadyRunningError without touching any other state — the existing
// daemon keeps running untouched.
func (d *Daemon) Run() error {
	handle, existing, err := lockfile.TryAcquire(d.lockPath)
	if err == lockfile.ErrLocked {
		return &AlreadyRunningError{PID: existing.PID, StartedAt: existing.StartedAt}
	}
	if err != nil {
		return fmt.Errorf("acquire daemon lock: %w", err)
	}
	d.singleton = handle
	if err := d.singleton.WriteInfo(lockfile.Info{PID: os.Getpid(), StartedAt: d.startTime}); err != nil {
		d.singleton.Release()
		d.singleton = nil
		return fmt.Errorf("write daemon lock payload: %w", err)
	}

	if err := d.writePIDFile(); err != nil {
		d.singleton.Release()
		d.singleton = nil
		return fmt.Errorf("write PID file: %w", err)
	}
	defer d.cleanup()

	d.installSignalHandler()

	// Start the HTTP API over Unix socket
	apiServer, err := d.StartAPI()
	if err != nil {
		debuglog.Log("daemon: failed to start API server: %v", err)
		// Non-fatal — daemon can still operate without the API
	} else {
		d.apiServer = apiServer
	}

	// Run any slow setup (e.g. DAG analysis) now that the API is serving.
	// Clients attached during this window see /api/state and can wait.
	if d.prepare != nil {
		if err := d.prepare(d.ctx); err != nil {
			return fmt.Errorf("daemon prepare: %w", err)
		}
	}

	// Initial scheduling (skip if no stories, e.g. --idle with all complete)
	if d.totalStories > 0 {
		d.Coord.ScheduleReady(d.ctx)
	}

	// Enter the coordination event loop
	d.eventLoop()
	return nil
}

// Shutdown triggers graceful shutdown of the daemon.
func (d *Daemon) Shutdown() {
	d.cancel()
}

// ApplySettings validates + applies the incoming settings to the live Config,
// persists the result to .ralph/config.toml, and broadcasts a fresh
// daemon_state event so SSE subscribers see the new values.
func (d *Daemon) ApplySettings(tc *config.TomlConfig) (applied []string, fieldErrs map[string]string, err error) {
	if fe := tc.Validate(); len(fe) > 0 {
		return nil, fe, nil
	}
	applied = d.Cfg.ApplySettings(tc)
	if saveErr := d.Cfg.SaveConfig(); saveErr != nil {
		return applied, nil, fmt.Errorf("save config: %w", saveErr)
	}
	debuglog.Log("daemon: applied settings %v", applied)
	d.broadcast(d.buildStateEvent())
	return applied, nil, nil
}

// Context returns the daemon's context.
func (d *Daemon) Context() context.Context {
	return d.ctx
}

// SocketPath returns the Unix socket path for the daemon API.
func (d *Daemon) SocketPath() string {
	return d.socketPath
}

// Subscribe returns a channel that receives daemon events (SSE).
// The caller must call Unsubscribe when done.
func (d *Daemon) Subscribe() chan DaemonEvent {
	ch := make(chan DaemonEvent, 64)
	d.sseMu.Lock()
	d.sseClients = append(d.sseClients, ch)
	d.sseMu.Unlock()
	d.clientCount.Add(1)
	// Notify event loop to re-check idle state
	select {
	case d.clientNotifyCh <- struct{}{}:
	default:
	}
	return ch
}

// ClientCount returns the number of connected SSE clients on the daemon API.
func (d *Daemon) ClientCount() int {
	return int(d.clientCount.Load())
}

// Unsubscribe removes a previously subscribed SSE channel.
func (d *Daemon) Unsubscribe(ch chan DaemonEvent) {
	d.sseMu.Lock()
	defer d.sseMu.Unlock()
	for i, c := range d.sseClients {
		if c == ch {
			d.sseClients = append(d.sseClients[:i], d.sseClients[i+1:]...)
			close(ch)
			d.clientCount.Add(-1)
			// Notify event loop to re-check idle state
			select {
			case d.clientNotifyCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

// TotalConnectedClients returns the total number of connected clients
// on the daemon API.
func (d *Daemon) TotalConnectedClients() int {
	return d.ClientCount()
}

// broadcast sends a DaemonEvent to all SSE subscribers.
func (d *Daemon) broadcast(evt DaemonEvent) {
	d.sseMu.Lock()
	defer d.sseMu.Unlock()
	for _, ch := range d.sseClients {
		select {
		case ch <- evt:
		default:
			// Drop if client is slow
		}
	}
}

// eventLoop is the main coordination loop. It reads from the coordinator's
// update channel and handles worker completions, merges, scheduling, and
// checkpoints — the same work that model.go's Update() did for
// workerUpdateMsg, MergeCompleteMsg, and FusionCompareDoneMsg.
func (d *Daemon) eventLoop() {
	// Internal channels for merge and fusion results
	mergeCh := make(chan coordinator.MergeCompleteMsg, 8)
	fusionCh := make(chan coordinator.FusionCompareDoneMsg, 8)

	// Idle timeout: auto-shutdown when all work done and no clients connected.
	// A nil channel blocks forever in select, which is the desired default.
	var idleTimer *time.Timer
	var idleCh <-chan time.Time

	checkIdle := func() {
		snap := d.Cfg.Snapshot()
		if snap.IdleTimeout <= 0 {
			return
		}
		if d.Coord.AllDone() && d.TotalConnectedClients() == 0 {
			if idleTimer == nil {
				debuglog.Log("daemon: idle timeout started (%v)", snap.IdleTimeout)
				idleTimer = time.NewTimer(snap.IdleTimeout)
				idleCh = idleTimer.C
			}
		} else if idleTimer != nil {
			idleTimer.Stop()
			idleTimer = nil
			idleCh = nil
			debuglog.Log("daemon: idle timeout cancelled (work or clients present)")
		}
	}

	// Periodic idle check (covers cases where no events flow through the loop)
	idleCheck := time.NewTicker(5 * time.Second)
	defer idleCheck.Stop()

	// Run initial idle check
	checkIdle()

	for {
		select {
		case <-d.ctx.Done():
			debuglog.Log("daemon: context cancelled, exiting event loop")
			if idleTimer != nil {
				idleTimer.Stop()
			}
			return

		case u := <-d.Coord.UpdateCh():
			d.handleWorkerUpdate(u, mergeCh, fusionCh)
			checkIdle()

		case msg := <-mergeCh:
			d.handleMergeComplete(msg)
			checkIdle()

		case msg := <-fusionCh:
			d.handleFusionComplete(msg, mergeCh)
			checkIdle()

		case <-d.clientNotifyCh:
			checkIdle()

		case <-idleCheck.C:
			checkIdle()

		case <-idleCh:
			debuglog.Log("daemon: idle timeout expired, shutting down")
			d.cancel()
			return
		}
	}
}

// handleWorkerUpdate processes a worker update from the coordinator's channel.
func (d *Daemon) handleWorkerUpdate(u worker.WorkerUpdate, mergeCh chan coordinator.MergeCompleteMsg, fusionCh chan coordinator.FusionCompareDoneMsg) {
	willRetry := d.Coord.HandleUpdate(u)

	// Track cost data
	if u.TokenUsage != nil && d.RunCosting != nil {
		d.RunCosting.AddIteration(u.StoryID, *u.TokenUsage, 0)
	}

	// Usage limit — nothing more to do (daemon stays alive, workers are paused)
	if u.UsageLimit {
		debuglog.Log("daemon: usage limit hit for %s, workers paused", u.StoryID)
		d.broadcastLogLine(fmt.Sprintf("Usage limit hit (%s) — workers paused", u.StoryID))
		return
	}

	// Judge result broadcast
	if u.JudgeResult != nil {
		judge.AppendJudgeResult(d.Cfg.Snapshot().ProgressFile, u.StoryID, *u.JudgeResult)
	}

	switch u.State {
	case worker.WorkerDone:
		d.Coord.PreserveWorkerLogs(u.StoryID, u.WorkerID)

		// Fusion group handling
		if fg := d.Coord.FusionGroupReady(u.StoryID); fg != nil {
			debuglog.Log("daemon: fusion %s all implementations complete — comparing", u.StoryID)
			d.broadcastLogLine(fmt.Sprintf("Fusion %s: all implementations complete — comparing", u.StoryID))
			go d.fusionCompare(u.StoryID, fg, fusionCh)
			return
		} else if d.Coord.IsFusionStory(u.StoryID) {
			done, total := d.Coord.FusionProgress(u.StoryID)
			debuglog.Log("daemon: fusion %s: worker %d done (%d/%d)", u.StoryID, u.WorkerID, done, total)
			d.broadcastLogLine(fmt.Sprintf("Fusion %s: worker %d done (%d/%d) — waiting", u.StoryID, u.WorkerID, done, total))
			return
		}

		if u.Passed && u.ChangeID != "" {
			d.notifyStoryComplete(u.StoryID)
			go d.mergeBack(u, mergeCh)
		} else {
			if u.ChangeID != "" {
				d.Coord.AbandonChange(d.ctx, u.ChangeID)
			}
			d.Coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
			go d.Coord.CleanupWorker(d.ctx, u.WorkerID)
			if willRetry {
				debuglog.Log("daemon: worker %d (%s) did not pass — retrying", u.WorkerID, u.StoryID)
				d.broadcastLogLine(fmt.Sprintf("Worker %d (%s): did not pass — retrying", u.WorkerID, u.StoryID))
			} else {
				errMsg := "story did not pass"
				if u.Err != nil {
					errMsg = u.Err.Error()
				}
				if d.Notifier != nil {
					d.Notifier.StoryFailed(d.ctx, u.StoryID, errMsg)
				}
			}
			d.scheduleMore()
		}

	case worker.WorkerFailed:
		d.Coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
		go d.Coord.CleanupWorker(d.ctx, u.WorkerID)
		if willRetry {
			debuglog.Log("daemon: worker %d failed (%s): %v — retrying", u.WorkerID, u.StoryID, u.Err)
			d.broadcastLogLine(fmt.Sprintf("Worker %d failed (%s): %v — retrying", u.WorkerID, u.StoryID, u.Err))
		} else {
			errMsg := "unknown error"
			if u.Err != nil {
				errMsg = u.Err.Error()
			}
			if d.Notifier != nil {
				d.Notifier.StoryFailed(d.ctx, u.StoryID, errMsg)
			}
			debuglog.Log("daemon: worker %d failed (%s): %s", u.WorkerID, u.StoryID, errMsg)
		}
		d.scheduleMore()
	}

	d.checkCompletion()
}

// handleMergeComplete processes a merge result.
func (d *Daemon) handleMergeComplete(msg coordinator.MergeCompleteMsg) {

	if msg.Err != nil {
		if msg.ChangeID != "" {
			d.Coord.AbandonChange(d.ctx, msg.ChangeID)
		}
		debuglog.Log("daemon: merge failed (%s): %v", msg.StoryID, msg.Err)
		d.broadcastLogLine(fmt.Sprintf("Merge failed (%s): %v", msg.StoryID, msg.Err))
		d.broadcastMergeResult(msg.StoryID, false, msg.Err.Error())
	} else {
		if msg.ConflictsResolved {
			debuglog.Log("daemon: merged %s into main (conflicts resolved)", msg.StoryID)
			d.broadcastLogLine(fmt.Sprintf("Merged %s into main (conflicts resolved)", msg.StoryID))
		} else {
			debuglog.Log("daemon: merged %s into main", msg.StoryID)
			d.broadcastLogLine(fmt.Sprintf("Merged %s into main", msg.StoryID))
		}
		d.completedStories = d.Coord.CompletedCount()
		d.broadcastMergeResult(msg.StoryID, true, "")
	}

	go d.Coord.CleanupWorker(d.ctx, msg.WorkerID)
	d.scheduleMore()
	d.checkCompletion()
}

// handleFusionComplete processes a fusion comparison result.
func (d *Daemon) handleFusionComplete(msg coordinator.FusionCompareDoneMsg, mergeCh chan coordinator.MergeCompleteMsg) {

	if msg.Err != nil || !msg.Passed {
		reason := "no passing implementations"
		if msg.Err != nil {
			reason = msg.Err.Error()
		}
		debuglog.Log("daemon: fusion %s failed: %s", msg.StoryID, reason)
		d.broadcastLogLine(fmt.Sprintf("Fusion %s failed: %s", msg.StoryID, reason))

		for _, cid := range msg.LoserChangeIDs {
			d.Coord.AbandonChange(d.ctx, cid)
		}
		if msg.WinnerChangeID != "" {
			d.Coord.AbandonChange(d.ctx, msg.WinnerChangeID)
		}
		for _, wid := range msg.LoserWorkerIDs {
			go d.Coord.CleanupWorker(d.ctx, wid)
		}
		d.Coord.CompleteFusion(msg.StoryID, false)
		d.scheduleMore()
	} else {
		debuglog.Log("daemon: fusion %s: winner selected (worker %d) — %s", msg.StoryID, msg.WinnerWorkerID, msg.Reason)
		d.broadcastLogLine(fmt.Sprintf("Fusion %s: winner worker %d — %s", msg.StoryID, msg.WinnerWorkerID, msg.Reason))

		for _, cid := range msg.LoserChangeIDs {
			d.Coord.AbandonChange(d.ctx, cid)
		}
		for _, wid := range msg.LoserWorkerIDs {
			go d.Coord.CleanupWorker(d.ctx, wid)
		}
		d.Coord.RecordFusionOutcome(msg.MultiplePassed, !msg.WasFirstPasser)
		d.Coord.CompleteFusion(msg.StoryID, true)
		d.notifyStoryComplete(msg.StoryID)
		winnerUpdate := worker.WorkerUpdate{
			WorkerID: msg.WinnerWorkerID,
			StoryID:  msg.StoryID,
			ChangeID: msg.WinnerChangeID,
			Passed:   true,
		}
		go d.mergeBack(winnerUpdate, mergeCh)
	}

	d.checkCompletion()
}

// scheduleMore triggers scheduling of ready stories after a state change.
func (d *Daemon) scheduleMore() {
	d.Coord.ScheduleReady(d.ctx)
}

// checkCompletion checks whether all stories are done and broadcasts
// notifications, but does NOT exit — the daemon stays alive for
// interactive follow-up tasks and exits only on explicit quit.
func (d *Daemon) checkCompletion() {
	if d.Coord.ActiveCount() > 0 {
		return
	}
	if !d.Coord.AllDone() {
		return
	}

	d.completedStories = d.Coord.CompletedCount()
	debuglog.Log("daemon: all stories complete (%d/%d)", d.completedStories, d.totalStories)
	d.broadcastLogLine(fmt.Sprintf("All stories complete (%d/%d)", d.completedStories, d.totalStories))

	if d.Notifier != nil {
		cost := 0.0
		if d.RunCosting != nil {
			cost = d.RunCosting.TotalCost
		}
		d.Notifier.RunComplete(d.ctx, d.completedStories, d.totalStories, cost)
	}

	if d.completedStories > 0 && d.postCompletionRan.CompareAndSwap(false, true) {
		go d.runPostCompletion()
	}
}

// runPostCompletion executes the full post-run pipeline in a
// background goroutine: quality review, memory synthesis, dream
// consolidation, checkpoint cleanup, SUMMARY.md, retrospective,
// run-history persist. It is gated by d.postCompletionRan so it fires
// at most once per daemon lifetime; the on-disk sentinel written at
// the end short-circuits re-runs across daemon restarts.
func (d *Daemon) runPostCompletion() {
	defer func() {
		if r := recover(); r != nil {
			debuglog.Log("daemon: runPostCompletion panic: %v", r)
			d.broadcastLogLine(fmt.Sprintf("Post-run pipeline panic: %v", r))
		}
	}()

	inputs := d.buildPostCompletionInputs()
	if err := postcompletion.Run(d.ctx, d.Cfg, inputs); err != nil {
		debuglog.Log("daemon: post-run pipeline error: %v", err)
		d.broadcastLogLine(fmt.Sprintf("Post-run pipeline error: %v", err))
	}
}

// buildPostCompletionInputs gathers the state postcompletion.Run needs
// from the daemon's live coordinator, cost tracker, and events log.
func (d *Daemon) buildPostCompletionInputs() postcompletion.Inputs {
	p, _ := prd.Load(d.Cfg.PRDFile)

	pq := d.Coord.GetPlanQuality()
	var firstPassRate float64
	if pq.TotalStories > 0 {
		firstPassRate = float64(pq.FirstPassCount) / float64(pq.TotalStories)
	}

	evts, _ := events.Load(d.Cfg.ProjectDir)

	fm := d.Coord.GetFusionMetrics()
	var fmPtr *costs.FusionMetrics
	if fm.GroupsCreated > 0 {
		fmPtr = &fm
	}

	return postcompletion.Inputs{
		BuildInputs: costs.BuildInputs{
			PRD:             p,
			TotalIterations: d.Coord.IterationCount(),
			FailedCount:     d.Coord.FailedCount(),
			FirstPassRate:   firstPassRate,
			RunCosting:      d.RunCosting,
			Events:          evts,
			FusionMetrics:   fmPtr,
			StartTime:       d.startTime,
			Workers:         d.Cfg.Workers,
			NoArchitect:     d.Cfg.NoArchitect,
			NoFusion:        d.Cfg.NoFusion,
			NoSimplify:      d.Cfg.NoSimplify,
			QualityReview:   d.Cfg.QualityReview,
			FusionWorkers:   d.Cfg.FusionWorkers,
			Kind:            history.KindDaemon,
		},
		UtilityRunClaude: runner.UtilityClaude(d.Cfg),
		Reporter:         daemonReporter{d: d},
	}
}

// daemonReporter bridges postcompletion.Reporter onto the daemon's SSE
// broadcast primitives.
type daemonReporter struct{ d *Daemon }

func (r daemonReporter) Log(s string) { r.d.broadcastLogLine(s) }

func (r daemonReporter) Phase(name, status, message string, iter int) {
	r.d.broadcastPostRunPhase(PostRunPhaseEvent{
		Phase:     name,
		Status:    status,
		Message:   message,
		Iteration: iter,
		Timestamp: time.Now(),
	})
	// Also emit a log line so viewers not yet rendering the typed
	// phase event still see the transition in their feed.
	line := fmt.Sprintf("[post-run] %s: %s", name, status)
	if message != "" {
		line += " — " + message
	}
	r.d.broadcastLogLine(line)
}

// mergeBack runs MergeAndSync and sends the result to mergeCh.
func (d *Daemon) mergeBack(u worker.WorkerUpdate, mergeCh chan coordinator.MergeCompleteMsg) {
	conflictsResolved, err := d.Coord.MergeAndSync(d.ctx, u)
	mergeCh <- coordinator.MergeCompleteMsg{
		StoryID:           u.StoryID,
		WorkerID:          u.WorkerID,
		ChangeID:          u.ChangeID,
		Err:               err,
		ConflictsResolved: conflictsResolved,
	}
}

// fusionCompare runs the fusion comparison and sends the result to fusionCh.
func (d *Daemon) fusionCompare(storyID string, fg *fusion.FusionGroup, fusionCh chan coordinator.FusionCompareDoneMsg) {
	story := d.Coord.GetStory(storyID)
	workers := d.Coord.Workers()

	getDiff := func(wID worker.WorkerID) (string, error) {
		w := workers[wID]
		if w == nil || w.Workspace == "" {
			return "", fmt.Errorf("worker %d has no workspace", wID)
		}
		// Find the change ID from the fusion result
		for _, r := range fg.Results {
			if r.WorkerID == wID {
				return rexec.JJDiff(d.ctx, w.Workspace, w.BaseChangeID, r.ChangeID)
			}
		}
		return "", fmt.Errorf("worker %d not in fusion group", wID)
	}

	cr := fusion.RunCompare(d.ctx, story, fg, getDiff)
	fusionCh <- coordinator.FusionCompareDoneMsg{
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
}

// notifyStoryComplete sends a notification for a completed story.
func (d *Daemon) notifyStoryComplete(storyID string) {
	if d.Notifier == nil {
		return
	}
	title := d.Coord.StoryTitle(storyID)
	if d.RunCosting != nil {
		if storyCost := d.RunCosting.StoryCost(storyID); storyCost > 0 {
			d.Notifier.StoryComplete(d.ctx, storyID, fmt.Sprintf("%s ($%.2f)", title, storyCost))
			return
		}
	}
	d.Notifier.StoryComplete(d.ctx, storyID, title)
}

// broadcastLogLine sends a log line event to all SSE subscribers.
func (d *Daemon) broadcastLogLine(line string) {
	data, err := json.Marshal(LogLineEvent{
		Line:      line,
		Timestamp: time.Now(),
	})
	if err != nil {
		debuglog.Log("daemon: broadcastLogLine marshal error: %v", err)
		return
	}
	d.broadcast(DaemonEvent{
		Type: EventLogLine,
		Data: data,
	})
}

// broadcastPostRunPhase sends a post-run phase event to all SSE
// subscribers. Used by daemonReporter from the post-completion goroutine.
func (d *Daemon) broadcastPostRunPhase(evt PostRunPhaseEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		debuglog.Log("daemon: broadcastPostRunPhase marshal error: %v", err)
		return
	}
	d.broadcast(DaemonEvent{
		Type: EventPostRunPhase,
		Data: data,
	})
}

// broadcastMergeResult sends a merge result event to all SSE subscribers.
func (d *Daemon) broadcastMergeResult(storyID string, success bool, errMsg string) {
	data, err := json.Marshal(MergeResultEvent{
		StoryID: storyID,
		Success: success,
		Error:   errMsg,
	})
	if err != nil {
		debuglog.Log("daemon: broadcastMergeResult marshal error: %v", err)
		return
	}
	d.broadcast(DaemonEvent{
		Type: EventMergeResult,
		Data: data,
	})
}

// --- Lifecycle management ---

// writePIDFile writes the daemon's PID to .ralph/daemon.pid.
func (d *Daemon) writePIDFile() error {
	dir := filepath.Dir(d.pidFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(d.pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// installSignalHandler installs SIGTERM/SIGINT handlers for graceful shutdown.
func (d *Daemon) installSignalHandler() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		debuglog.Log("daemon: received signal %v, shutting down", sig)
		d.cancel()
	}()
}

// cleanup cancels all workers, cleans up workspaces, and removes
// the PID file and Unix socket.
func (d *Daemon) cleanup() {
	debuglog.Log("daemon: cleaning up")

	// Cancel all workers
	if d.Coord != nil {
		d.Coord.CancelAll()
		d.Coord.CleanupAll(context.Background())
	}

	// Stop API server
	if d.apiServer != nil {
		_ = d.apiServer.Stop(context.Background())
	}

	// Remove PID file
	_ = os.Remove(d.pidFile)

	// Remove Unix socket
	_ = os.Remove(d.socketPath)

	// Release singleton lock and drop the lockfile so the next daemon
	// starts against a clean slate.
	if d.singleton != nil {
		d.singleton.Release()
		d.singleton = nil
		_ = os.Remove(d.lockPath)
	}

	debuglog.Log("daemon: cleanup complete")
}

