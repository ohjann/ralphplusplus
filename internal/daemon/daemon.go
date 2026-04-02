package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/coordinator"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	rexec "github.com/ohjann/ralphplusplus/internal/exec"
	"github.com/ohjann/ralphplusplus/internal/fusion"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/notify"
	"github.com/ohjann/ralphplusplus/internal/statuspage"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// Daemon owns the coordinator and runs the coordination event loop,
// replacing the TUI's Update() handler for worker/merge messages.
type Daemon struct {
	Coord      *coordinator.Coordinator
	Cfg        *config.Config
	Notifier   *notify.Notifier
	RunCosting *costs.RunCosting
	Version    string

	// Status page
	statusServer *statuspage.StatusServer
	statusMu     sync.Mutex

	// SSE broadcaster for daemon events
	sseMu      sync.Mutex
	sseClients []chan DaemonEvent

	// HTTP API server over Unix socket
	apiServer *APIServer

	// Lifecycle
	ctx        context.Context
	cancel     context.CancelFunc
	pidFile    string
	socketPath string
	startTime  time.Time

	// Coordination state
	totalStories     int
	completedStories int
}

// DaemonOpts holds optional configuration for the daemon.
type DaemonOpts struct {
	Notifier     *notify.Notifier
	RunCosting   *costs.RunCosting
	Version      string
	TotalStories int
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
		ctx:          ctx,
		cancel:       cancel,
		pidFile:      filepath.Join(cfg.ProjectDir, ".ralph", "daemon.pid"),
		socketPath:   filepath.Join(cfg.ProjectDir, ".ralph", "daemon.sock"),
		startTime:    time.Now(),
		totalStories: opts.TotalStories,
		sseClients:   make([]chan DaemonEvent, 0),
	}
}

// Run starts the daemon: writes PID file, installs signal handlers,
// starts the status page, and enters the coordination event loop.
// It blocks until shutdown is triggered.
func (d *Daemon) Run() error {
	if err := d.writePIDFile(); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	defer d.cleanup()

	d.installSignalHandler()
	d.startStatusPage()

	// Start the HTTP API over Unix socket
	apiServer, err := d.StartAPI()
	if err != nil {
		debuglog.Log("daemon: failed to start API server: %v", err)
		// Non-fatal — daemon can still operate without the API
	} else {
		d.apiServer = apiServer
	}

	// Initial scheduling
	d.Coord.ScheduleReady(d.ctx)
	d.updateStatusPage()

	// Enter the coordination event loop
	d.eventLoop()
	return nil
}

// Shutdown triggers graceful shutdown of the daemon.
func (d *Daemon) Shutdown() {
	d.cancel()
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
	return ch
}

// Unsubscribe removes a previously subscribed SSE channel.
func (d *Daemon) Unsubscribe(ch chan DaemonEvent) {
	d.sseMu.Lock()
	defer d.sseMu.Unlock()
	for i, c := range d.sseClients {
		if c == ch {
			d.sseClients = append(d.sseClients[:i], d.sseClients[i+1:]...)
			close(ch)
			return
		}
	}
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

	for {
		select {
		case <-d.ctx.Done():
			debuglog.Log("daemon: context cancelled, exiting event loop")
			return

		case u := <-d.Coord.UpdateCh():
			d.handleWorkerUpdate(u, mergeCh, fusionCh)

		case msg := <-mergeCh:
			d.handleMergeComplete(msg)

		case msg := <-fusionCh:
			d.handleFusionComplete(msg, mergeCh)
		}
	}
}

// handleWorkerUpdate processes a worker update from the coordinator's channel.
func (d *Daemon) handleWorkerUpdate(u worker.WorkerUpdate, mergeCh chan coordinator.MergeCompleteMsg, fusionCh chan coordinator.FusionCompareDoneMsg) {
	willRetry := d.Coord.HandleUpdate(u)
	d.updateStatusPage()

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
		judge.AppendJudgeResult(d.Cfg.ProgressFile, u.StoryID, *u.JudgeResult)
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
	d.updateStatusPage()

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
	d.updateStatusPage()

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

		for i, cid := range msg.LoserChangeIDs {
			d.Coord.AbandonChange(d.ctx, cid)
			go d.Coord.CleanupWorker(d.ctx, msg.LoserWorkerIDs[i])
		}
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
	d.updateStatusPage()
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
	d.updateStatusPage()
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
	if story == nil {
		fusionCh <- coordinator.FusionCompareDoneMsg{
			StoryID: storyID,
			Err:     fmt.Errorf("story %s not found", storyID),
		}
		return
	}

	passing := fg.PassingResults()
	if len(passing) == 0 {
		fusionCh <- coordinator.FusionCompareDoneMsg{
			StoryID: storyID,
			Passed:  false,
		}
		return
	}

	// Single winner — no comparison needed
	if len(passing) == 1 {
		var loserIDs []worker.WorkerID
		var loserChangeIDs []string
		for _, r := range fg.Results {
			if r.WorkerID != passing[0].WorkerID {
				loserIDs = append(loserIDs, r.WorkerID)
				if r.ChangeID != "" {
					loserChangeIDs = append(loserChangeIDs, r.ChangeID)
				}
			}
		}
		fusionCh <- coordinator.FusionCompareDoneMsg{
			StoryID:        storyID,
			WinnerWorkerID: passing[0].WorkerID,
			WinnerChangeID: passing[0].ChangeID,
			LoserWorkerIDs: loserIDs,
			LoserChangeIDs: loserChangeIDs,
			Reason:         "only passing implementation",
			Passed:         true,
		}
		return
	}

	// Multiple passed — get diffs and run comparison judge
	var candidates []judge.CompareCandidate
	workers := d.Coord.Workers()
	for i, r := range passing {
		w := workers[r.WorkerID]
		if w == nil || w.Workspace == "" {
			continue
		}
		diff, err := rexec.JJDiff(d.ctx, w.Workspace, w.BaseChangeID, r.ChangeID)
		if err != nil {
			debuglog.Log("daemon: fusion diff failed for worker %d: %v", r.WorkerID, err)
			continue
		}
		candidates = append(candidates, judge.CompareCandidate{
			Index:    i,
			ChangeID: r.ChangeID,
			Diff:     diff,
		})
	}

	if len(candidates) == 0 {
		fusionCh <- coordinator.FusionCompareDoneMsg{
			StoryID: storyID,
			Err:     fmt.Errorf("could not extract diffs from any candidate"),
		}
		return
	}

	result, err := judge.RunComparison(d.ctx, story, candidates)
	if err != nil {
		fusionCh <- coordinator.FusionCompareDoneMsg{
			StoryID: storyID,
			Err:     err,
		}
		return
	}

	winnerPassing := passing[result.WinnerIndex]
	var loserIDs []worker.WorkerID
	var loserChangeIDs []string
	for _, r := range fg.Results {
		if r.WorkerID != winnerPassing.WorkerID {
			loserIDs = append(loserIDs, r.WorkerID)
			if r.ChangeID != "" {
				loserChangeIDs = append(loserChangeIDs, r.ChangeID)
			}
		}
	}
	fusionCh <- coordinator.FusionCompareDoneMsg{
		StoryID:        storyID,
		WinnerWorkerID: winnerPassing.WorkerID,
		WinnerChangeID: winnerPassing.ChangeID,
		LoserWorkerIDs: loserIDs,
		LoserChangeIDs: loserChangeIDs,
		Reason:         result.Reason,
		Passed:         true,
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
	data, _ := json.Marshal(LogLineEvent{
		Line:      line,
		Timestamp: time.Now(),
	})
	d.broadcast(DaemonEvent{
		Type: EventLogLine,
		Data: data,
	})
}

// broadcastMergeResult sends a merge result event to all SSE subscribers.
func (d *Daemon) broadcastMergeResult(storyID string, success bool, errMsg string) {
	data, _ := json.Marshal(MergeResultEvent{
		StoryID: storyID,
		Success: success,
		Error:   errMsg,
	})
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
	return os.WriteFile(d.pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
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

	// Stop status page
	d.stopStatusPage()

	// Remove PID file
	_ = os.Remove(d.pidFile)

	// Remove Unix socket
	_ = os.Remove(d.socketPath)

	debuglog.Log("daemon: cleanup complete")
}

// --- Status page ---

// startStatusPage starts the status page server if configured.
func (d *Daemon) startStatusPage() {
	port := d.Cfg.StatusPort
	if port == 0 {
		return
	}
	ss := statuspage.New()
	actualPort, err := ss.Start(port)
	if err != nil {
		debuglog.Log("daemon: status page failed to start on port %d: %v", port, err)
		return
	}
	d.statusMu.Lock()
	d.statusServer = ss
	d.Cfg.StatusPort = actualPort
	d.statusMu.Unlock()
	debuglog.Log("daemon: status page started on port %d", actualPort)
}

// stopStatusPage stops the status page server.
func (d *Daemon) stopStatusPage() {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if d.statusServer != nil {
		_ = d.statusServer.Stop(context.Background())
		d.statusServer = nil
	}
}

// updateStatusPage updates the status page with current state.
func (d *Daemon) updateStatusPage() {
	d.statusMu.Lock()
	ss := d.statusServer
	d.statusMu.Unlock()
	if ss == nil {
		return
	}
	ss.UpdateState(d.buildStatusState())
}

// buildStatusState constructs a StatusState from current daemon state.
func (d *Daemon) buildStatusState() statuspage.StatusState {
	elapsed := time.Since(d.startTime).Truncate(time.Second)

	state := statuspage.StatusState{
		Phase:       "parallel",
		PhaseIcon:   "⫘",
		RunDuration: formatDuration(elapsed),
		Running:     d.Coord.ActiveCount() > 0,
		Version:     d.Version,
		Completed:   d.Coord.CompletedCount(),
		Total:       d.totalStories,
		AllComplete: d.Coord.AllDone(),
	}

	if d.RunCosting != nil {
		state.TotalCost = d.RunCosting.TotalCost
		if d.RunCosting.TotalInputTokens > 0 || d.RunCosting.TotalOutputTokens > 0 {
			state.CostDisplay = fmt.Sprintf("$%.2f", d.RunCosting.TotalCost)
			state.HasTokenData = true
		}
	}

	if d.Cfg.JudgeEnabled {
		state.Badges = append(state.Badges, statuspage.Badge{Label: "Judge", Icon: "⚖"})
	}
	if d.Cfg.QualityReview {
		state.Badges = append(state.Badges, statuspage.Badge{Label: "Quality", Icon: "◇"})
	}

	return state
}

// formatDuration formats a duration as "Xh Ym Zs" or shorter forms.
func formatDuration(dur time.Duration) string {
	h := int(dur.Hours())
	m := int(dur.Minutes()) % 60
	s := int(dur.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
