package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// APIServer serves the daemon HTTP API over a Unix domain socket.
type APIServer struct {
	daemon   *Daemon
	listener net.Listener
	server   *http.Server
}

// StartAPI creates and starts the Unix socket HTTP server.
// The socket file is created at the daemon's socketPath.
func (d *Daemon) StartAPI() (*APIServer, error) {
	// Clean up stale socket file
	_ = os.Remove(d.socketPath)

	// Ensure socket directory permissions are user-only
	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", d.socketPath, err)
	}

	// Set socket permissions to user-only
	if err := os.Chmod(d.socketPath, 0600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	mux := http.NewServeMux()
	api := &APIServer{
		daemon:   d,
		listener: listener,
		server: &http.Server{
			Handler: mux,
		},
	}

	// SSE endpoint
	mux.HandleFunc("/events", api.handleSSE)

	// Query endpoints
	mux.HandleFunc("/api/state", api.handleState)
	mux.HandleFunc("/api/worker/", api.handleWorkerActivity)

	// Command endpoints
	mux.HandleFunc("/api/quit", api.handleQuit)
	mux.HandleFunc("/api/pause", api.handlePause)
	mux.HandleFunc("/api/resume", api.handleResume)
	mux.HandleFunc("/api/hint", api.handleHint)
	mux.HandleFunc("/api/task", api.handleTask)
	mux.HandleFunc("/api/settings", api.handleSettings)
	mux.HandleFunc("/api/clarify", api.handleClarify)

	go func() {
		if err := api.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			debuglog.Log("daemon: API server error: %v", err)
		}
	}()

	debuglog.Log("daemon: API server started on %s", d.socketPath)
	return api, nil
}

// Stop gracefully shuts down the API server.
func (a *APIServer) Stop(ctx context.Context) error {
	return a.server.Shutdown(ctx)
}

// --- SSE endpoint ---

func (a *APIServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe to daemon events
	ch := a.daemon.Subscribe()
	defer a.daemon.Unsubscribe(ch)

	// Send initial state snapshot
	a.writeSSEEvent(w, flusher, a.daemon.buildStateEvent())

	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.daemon.ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			a.writeSSEEvent(w, flusher, evt)
		}
	}
}

func (a *APIServer) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, evt DaemonEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// --- Query endpoints ---

func (a *APIServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	state := a.daemon.BuildStateSnapshot()
	writeJSON(w, http.StatusOK, state)
}

func (a *APIServer) handleWorkerActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse worker ID from /api/worker/{id}/activity
	path := strings.TrimPrefix(r.URL.Path, "/api/worker/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "activity" {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	idNum, err := strconv.Atoi(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid worker ID"})
		return
	}

	wID := worker.WorkerID(idNum)
	activityPath := a.daemon.Coord.GetWorkerActivityPath(wID)
	if activityPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "worker not found or no activity log"})
		return
	}

	// Read recent lines from the activity log
	lines := runner.ReadLogTail(activityPath, 200)
	writeJSON(w, http.StatusOK, map[string]string{
		"worker_id": parts[0],
		"activity":  lines,
	})
}

// --- Command endpoints ---

func (a *APIServer) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	a.daemon.Shutdown()
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
}

func (a *APIServer) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	// Coordinator uses paused flag checked by ScheduleReady; cancel active workers
	a.daemon.Coord.CancelAll()
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (a *APIServer) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	a.daemon.Coord.Resume()
	a.daemon.Coord.ScheduleReady(a.daemon.ctx)
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (a *APIServer) handleHint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req HintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.WorkerID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker_id is required"})
		return
	}
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	if !a.daemon.Coord.IsWorkerActive(req.WorkerID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "worker not found or not active"})
		return
	}

	a.daemon.Coord.ResumeWorkerWithHint(req.WorkerID, req.Text)
	writeJSON(w, http.StatusOK, map[string]string{"status": "hint sent"})
}

func (a *APIServer) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Description == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description is required"})
		return
	}

	// Task handling is a placeholder — the full implementation depends on
	// how ad-hoc tasks integrate with the PRD/DAG (out of scope for this story).
	writeJSON(w, http.StatusOK, map[string]string{"status": "task received", "description": req.Description})
}

func (a *APIServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	// Settings update is a placeholder for future expansion.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *APIServer) handleClarify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req ClarifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Clarification handling is a placeholder for future expansion.
	writeJSON(w, http.StatusOK, map[string]string{"status": "clarification received"})
}

// --- State snapshot building ---

// BuildStateSnapshot constructs a full DaemonStateEvent from current coordinator state.
func (d *Daemon) BuildStateSnapshot() DaemonStateEvent {
	workers := d.Coord.Workers()
	workerStatuses := make(map[worker.WorkerID]WorkerStatus, len(workers))
	for id, w := range workers {
		workerStatuses[id] = WorkerStatus{
			ID:           w.ID,
			StoryID:      w.StoryID,
			StoryTitle:   w.StoryTitle,
			State:        w.State.String(),
			Role:         w.Role,
			Iteration:    w.Iteration,
			ActivityPath: d.Coord.GetWorkerActivityPath(w.ID),
			FusionSuffix: w.FusionSuffix,
		}
	}

	// Build story statuses from coordinator state
	storyStatuses := make(map[string]StoryStatus)
	for _, sid := range d.Coord.ActiveStoryIDs() {
		storyStatuses[sid] = StoryStatus{
			ID:         sid,
			Title:      d.Coord.StoryTitle(sid),
			InProgress: true,
		}
	}
	// Add completed and failed stories from coordinator queries
	// We iterate workers to collect known story IDs, plus check coordinator state
	knownStories := make(map[string]bool)
	for _, w := range workers {
		knownStories[w.StoryID] = true
	}
	for sid := range knownStories {
		if _, exists := storyStatuses[sid]; exists {
			continue
		}
		ss := StoryStatus{
			ID:    sid,
			Title: d.Coord.StoryTitle(sid),
		}
		if d.Coord.IsCompleted(sid) {
			ss.Completed = true
		} else if d.Coord.IsFailed(sid) {
			ss.Failed = true
			ss.FailedError = d.Coord.FailedError(sid)
		} else if blocked, dep := d.Coord.IsBlockedByFailure(sid); blocked {
			ss.BlockedByDep = dep
		}
		storyStatuses[sid] = ss
	}

	pq := d.Coord.GetPlanQuality()
	var costTotals CostTotals
	if d.RunCosting != nil {
		d.RunCosting.Lock()
		costTotals = CostTotals{
			TotalCost:         d.RunCosting.TotalCost,
			TotalInputTokens:  d.RunCosting.TotalInputTokens,
			TotalOutputTokens: d.RunCosting.TotalOutputTokens,
		}
		d.RunCosting.Unlock()
	}

	return DaemonStateEvent{
		Workers:        workerStatuses,
		Stories:        storyStatuses,
		ActiveStoryIDs: d.Coord.ActiveStoryIDs(),
		Phase:          "parallel",
		Paused:         d.Coord.IsPaused(),
		TotalStories:   d.totalStories,
		CompletedCount: d.Coord.CompletedCount(),
		FailedCount:    d.Coord.FailedCount(),
		IterationCount: d.Coord.IterationCount(),
		AllDone:        d.Coord.AllDone(),
		CostTotals:     costTotals,
		PlanQuality: PlanQualityInfo{
			FirstPassCount: pq.FirstPassCount,
			RetryCount:     pq.RetryCount,
			FailedCount:    pq.FailedCount,
			TotalStories:   pq.TotalStories,
			Score:          pq.Score(),
		},
		Timestamp: time.Now(),
	}
}

// buildStateEvent wraps BuildStateSnapshot in a DaemonEvent envelope.
func (d *Daemon) buildStateEvent() DaemonEvent {
	state := d.BuildStateSnapshot()
	data, _ := json.Marshal(state)
	return DaemonEvent{
		Type: EventDaemonState,
		Data: data,
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
