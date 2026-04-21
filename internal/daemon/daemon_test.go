package daemon_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/coordinator"
	"github.com/ohjann/ralphplusplus/internal/daemon"
	"github.com/ohjann/ralphplusplus/internal/dag"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// unixHTTPClient returns an http.Client that dials the given Unix socket path.
func unixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
}

// postSettings POSTs the given JSON body to /api/settings over the daemon's
// unix socket and returns the status code plus the decoded JSON body.
func postSettings(t *testing.T, socketPath string, body string) (int, map[string]any) {
	t.Helper()
	hc := unixHTTPClient(socketPath)
	resp, err := hc.Post("http://daemon/api/settings", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/settings: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode settings response %q: %v", string(raw), err)
		}
	}
	return resp.StatusCode, out
}

// testDaemonShort creates a daemon with a short socket path suitable for Unix socket limits.
func testDaemonShort(t *testing.T) (*daemon.Daemon, *coordinator.Coordinator, string) {
	t.Helper()

	// Use /tmp directly for short socket paths
	tmpDir, err := os.MkdirTemp("/tmp", "rd")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	ralphDir := filepath.Join(tmpDir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatalf("mkdir .ralph: %v", err)
	}

	cfg := &config.Config{
		ProjectDir: tmpDir,
		PRDFile:    "/dev/null",
		NoFusion:   true,
		Workers:    1,
	}

	d := &dag.DAG{Nodes: map[string]*dag.StoryNode{
		"T-001": {StoryID: "T-001"},
	}}
	stories := []prd.UserStory{
		{ID: "T-001", Title: "Test Story", Priority: 1},
	}

	coord := coordinator.New(cfg, d, 1, stories)
	dmn := daemon.New(cfg, coord, daemon.DaemonOpts{
		TotalStories: 1,
	})

	return dmn, coord, tmpDir
}

// startDaemon starts a daemon in a goroutine and waits for the socket to appear.
func startDaemon(t *testing.T, dmn *daemon.Daemon) {
	t.Helper()
	go dmn.Run() //nolint:errcheck

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dmn.SocketPath()); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon socket did not appear within 2s")
}

// newTestWorker builds a minimal running worker for coordinator injection.
func newTestWorker(ctx context.Context, cancel context.CancelFunc) *worker.Worker {
	return &worker.Worker{
		ID:         1,
		StoryID:    "T-001",
		StoryTitle: "Test Story",
		State:      worker.WorkerRunning,
		Iteration:  1,
		JJMu:       &sync.Mutex{},
		Ctx:        ctx,
		Cancel:     cancel,
	}
}

func TestDaemonStartClientConnectsReceivesState(t *testing.T) {
	dmn, _, _ := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	state, err := client.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	if state.Phase != "parallel" {
		t.Errorf("expected phase 'parallel', got %q", state.Phase)
	}
	if state.TotalStories != 1 {
		t.Errorf("expected 1 total story, got %d", state.TotalStories)
	}
}

func TestSSEStreamReceivesInitialState(t *testing.T) {
	dmn, _, _ := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	evtCh := client.StreamEvents(ctx)

	// First event should be the initial daemon_state
	select {
	case evt := <-evtCh:
		if evt.Type != daemon.EventDaemonState {
			t.Errorf("expected event type %q, got %q", daemon.EventDaemonState, evt.Type)
		}
		var state daemon.DaemonStateEvent
		if err := json.Unmarshal(evt.Data, &state); err != nil {
			t.Fatalf("unmarshal state: %v", err)
		}
		if state.TotalStories != 1 {
			t.Errorf("expected 1 total story, got %d", state.TotalStories)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial SSE event")
	}
}

func TestSSEStreamReceivesBroadcastedEvents(t *testing.T) {
	dmn, coord, _ := testDaemonShort(t)

	wCtx, wCancel := context.WithCancel(context.Background())
	defer wCancel()
	coord.RegisterTestWorker(newTestWorker(wCtx, wCancel))

	startDaemon(t, dmn)
	defer dmn.Shutdown()

	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	evtCh := client.StreamEvents(ctx)

	// Drain initial state event
	select {
	case <-evtCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial event")
	}

	state, err := client.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.Workers) == 0 {
		t.Error("expected at least one worker in state snapshot")
	}
	ws, ok := state.Workers[1]
	if !ok {
		t.Error("expected worker 1 in state snapshot")
	} else if ws.StoryID != "T-001" {
		t.Errorf("expected worker story T-001, got %q", ws.StoryID)
	}
}

func TestClientDisconnectDaemonContinuesNewClientReconnects(t *testing.T) {
	dmn, _, _ := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	// First client connects and reads state
	client1, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect client1: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	evtCh1 := client1.StreamEvents(ctx1)

	// Drain initial state event
	select {
	case <-evtCh1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client1 initial event")
	}

	// Simulate TUI crash: cancel the SSE stream and drop the client
	cancel1()

	// Wait for disconnect to propagate
	time.Sleep(100 * time.Millisecond)

	// Daemon should still be alive — verify with a new client
	client2, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect client2 after disconnect: %v", err)
	}

	state, err := client2.GetState()
	if err != nil {
		t.Fatalf("GetState from client2: %v", err)
	}

	if state.Phase != "parallel" {
		t.Errorf("expected phase 'parallel' from reconnected client, got %q", state.Phase)
	}
}

func TestSSEDisconnectAndReconnectPreservesState(t *testing.T) {
	dmn, coord, _ := testDaemonShort(t)

	wCtx, wCancel := context.WithCancel(context.Background())
	defer wCancel()
	coord.RegisterTestWorker(newTestWorker(wCtx, wCancel))

	startDaemon(t, dmn)
	defer dmn.Shutdown()

	// Connect first client and start SSE
	client1, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	evtCh1 := client1.StreamEvents(ctx1)

	// Drain initial state
	select {
	case <-evtCh1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	// Disconnect first client
	cancel1()
	time.Sleep(100 * time.Millisecond)

	// Reconnect — new client should still see the worker in state
	client2, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	state, err := client2.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.Workers) == 0 {
		t.Error("expected worker state to be preserved after reconnect")
	}
	if _, ok := state.Workers[1]; !ok {
		t.Error("expected worker 1 in state after reconnect")
	}
}

func TestPostQuitTriggersGracefulShutdown(t *testing.T) {
	dmn, _, tmpDir := testDaemonShort(t)
	startDaemon(t, dmn)

	sockPath := dmn.SocketPath()
	pidPath := filepath.Join(tmpDir, ".ralph", "daemon.pid")

	// Verify PID file and socket exist
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("PID file should exist: %v", err)
	}
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket should exist: %v", err)
	}

	// Send quit command
	client, err := daemon.Connect(sockPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := client.Quit(); err != nil {
		t.Fatalf("Quit: %v", err)
	}

	// Wait for cleanup (PID file and socket removed)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, pidErr := os.Stat(pidPath)
		_, sockErr := os.Stat(sockPath)
		if os.IsNotExist(pidErr) && os.IsNotExist(sockErr) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("PID file and/or socket were not cleaned up within 2s")
}

func TestPostHintDeliveredToWorker(t *testing.T) {
	dmn, coord, _ := testDaemonShort(t)

	wCtx, wCancel := context.WithCancel(context.Background())
	defer wCancel()
	fakeWorker := newTestWorker(wCtx, wCancel)
	coord.RegisterTestWorker(fakeWorker)

	startDaemon(t, dmn)
	defer dmn.Shutdown()

	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send hint to the active worker
	err = client.SendHint(1, "try a different approach")
	if err != nil {
		t.Fatalf("SendHint: %v", err)
	}

	// Verify the hint was delivered to the worker
	if fakeWorker.ResumeHint != "try a different approach" {
		t.Errorf("expected ResumeHint %q, got %q", "try a different approach", fakeWorker.ResumeHint)
	}
}

func TestPostHintFailsForNonExistentWorker(t *testing.T) {
	dmn, _, _ := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Worker 99 doesn't exist — should fail
	err = client.SendHint(99, "this should fail")
	if err == nil {
		t.Fatal("expected error sending hint to non-existent worker")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestStaleDaemonDetectionNewDaemonStartsCleanly(t *testing.T) {
	// Use /tmp for short socket paths
	tmpDir, err := os.MkdirTemp("/tmp", "rd")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ralphDir := filepath.Join(tmpDir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatalf("mkdir .ralph: %v", err)
	}

	// Write a stale PID file (PID of a dead process)
	stalePID := 99999
	// Make sure this PID is actually dead
	proc, err := os.FindProcess(stalePID)
	if err == nil {
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			stalePID = 99998
		}
	}
	pidPath := filepath.Join(ralphDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(stalePID)), 0o644); err != nil {
		t.Fatalf("write stale PID: %v", err)
	}

	// Create a stale socket file (not a real socket)
	sockPath := filepath.Join(ralphDir, "daemon.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale socket: %v", err)
	}

	// Now start a new daemon — it should start cleanly despite stale files
	// StartAPI does os.Remove before net.Listen, so the stale file is cleaned up
	cfg := &config.Config{
		ProjectDir: tmpDir,
		PRDFile:    "/dev/null",
		NoFusion:   true,
		Workers:    1,
	}
	d := &dag.DAG{Nodes: map[string]*dag.StoryNode{
		"T-001": {StoryID: "T-001"},
	}}
	stories := []prd.UserStory{
		{ID: "T-001", Title: "Test Story", Priority: 1},
	}
	coord := coordinator.New(cfg, d, 1, stories)
	dmn := daemon.New(cfg, coord, daemon.DaemonOpts{TotalStories: 1})

	// Start daemon and wait for a successful connection (not just file existence,
	// since the stale socket file already exists before the daemon starts).
	go dmn.Run() //nolint:errcheck

	// Wait for the daemon to be connectable
	deadline := time.Now().Add(2 * time.Second)
	var client *daemon.DaemonClient
	for time.Now().Before(deadline) {
		client, err = daemon.Connect(dmn.SocketPath())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("could not connect to daemon after stale cleanup: %v", err)
	}
	defer dmn.Shutdown()

	state, err := client.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Phase != "parallel" {
		t.Errorf("expected phase 'parallel', got %q", state.Phase)
	}

	// PID file should have the current process's PID (not the stale one)
	newPIDData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read new PID file: %v", err)
	}
	newPID, err := strconv.Atoi(strings.TrimSpace(string(newPIDData)))
	if err != nil {
		t.Fatalf("parse new PID: %v", err)
	}
	if newPID == stalePID {
		t.Error("PID file still contains stale PID")
	}
}

func TestIsolatedTempDirectories(t *testing.T) {
	// Start two daemons concurrently and verify no socket path conflicts
	dmn1, _, tmpDir1 := testDaemonShort(t)
	dmn2, _, tmpDir2 := testDaemonShort(t)

	if dmn1.SocketPath() == dmn2.SocketPath() {
		t.Fatal("two daemons should have different socket paths")
	}

	startDaemon(t, dmn1)
	defer dmn1.Shutdown()

	startDaemon(t, dmn2)
	defer dmn2.Shutdown()

	// Both should be independently reachable
	client1, err := daemon.Connect(dmn1.SocketPath())
	if err != nil {
		t.Fatalf("Connect daemon1: %v", err)
	}

	client2, err := daemon.Connect(dmn2.SocketPath())
	if err != nil {
		t.Fatalf("Connect daemon2: %v", err)
	}

	state1, err := client1.GetState()
	if err != nil {
		t.Fatalf("GetState daemon1: %v", err)
	}
	state2, err := client2.GetState()
	if err != nil {
		t.Fatalf("GetState daemon2: %v", err)
	}

	if state1.Phase != "parallel" || state2.Phase != "parallel" {
		t.Error("both daemons should be in 'parallel' phase")
	}

	if tmpDir1 == tmpDir2 {
		t.Error("temp dirs should be different")
	}
}

// TestKillDaemonFunction tests the killDaemon flow end-to-end by starting a
// daemon in a goroutine and sending SIGTERM via os.FindProcess, matching what
// the --kill flag does in main.go. Note: this sends SIGTERM to the test
// process itself, which works because the daemon's signal handler runs
// in-process and calls cancel() on the daemon's context.
func TestKillDaemonFunction(t *testing.T) {
	// Start a daemon, send SIGTERM via the kill path, verify cleanup
	dmn, _, tmpDir := testDaemonShort(t)
	startDaemon(t, dmn)

	sockPath := dmn.SocketPath()
	pidPath := filepath.Join(tmpDir, ".ralph", "daemon.pid")

	// Read and verify the PID
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read PID: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("parse PID: %v", err)
	}

	// Exercise the same code path as killDaemon() in main.go:
	// 1. Find process by PID
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}

	// 2. Check process is alive (signal 0)
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process %d not alive: %v", pid, err)
	}

	// 3. Send SIGTERM
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM to %d: %v", pid, err)
	}

	// 4. Wait for socket removal (same as killDaemon's wait loop)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			// Socket removed — verify PID file also gone
			if _, err := os.Stat(pidPath); os.IsNotExist(err) {
				return // success
			}
			// PID file may still be cleaned up
			continue
		}
	}
	t.Error("killDaemon flow: socket was not removed within 10s after SIGTERM")
}

func TestIdleTimeoutShutdown(t *testing.T) {
	dmn, coord, _ := testDaemonShort(t)
	// Set a very short idle timeout
	dmn.Cfg.IdleTimeout = 500 * time.Millisecond

	// Mark the only story as complete before starting
	coord.MarkCompleteForTest("T-001")

	startDaemon(t, dmn)
	// Do NOT defer Shutdown — the daemon should shut itself down

	// Verify daemon is alive initially
	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = client // GetState was called in Connect, no persistent SSE subscription

	// Wait for idle timeout to fire (500ms + buffer)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dmn.SocketPath()); os.IsNotExist(err) {
			return // success — daemon shut itself down
		}
		time.Sleep(50 * time.Millisecond)
	}
	dmn.Shutdown() // cleanup if test fails
	t.Error("daemon did not auto-shutdown after idle timeout")
}

func TestIdleTimeoutResetOnClientConnect(t *testing.T) {
	dmn, coord, _ := testDaemonShort(t)
	dmn.Cfg.IdleTimeout = 500 * time.Millisecond

	coord.MarkCompleteForTest("T-001")

	startDaemon(t, dmn)
	defer dmn.Shutdown()

	// Connect a client with SSE subscription to prevent idle shutdown
	client, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evtCh := client.StreamEvents(ctx)

	// Drain initial state
	select {
	case <-evtCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial event")
	}

	// Wait longer than the idle timeout — daemon should still be alive
	// because we have a connected SSE client
	time.Sleep(800 * time.Millisecond)

	// Verify daemon is still alive
	client2, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("daemon should still be alive with connected client: %v", err)
	}
	state, err := client2.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !state.AllDone {
		t.Error("expected AllDone to be true")
	}
}

func TestApplySettings_UpdatesConfigPersistsAndBroadcasts(t *testing.T) {
	dmn, _, tmpDir := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	// Subscribe to SSE stream so we can observe the broadcast that follows
	// ApplySettings. Drain the initial state event first.
	sseClient, err := daemon.Connect(dmn.SocketPath())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer sseCancel()
	evtCh := sseClient.StreamEvents(sseCtx)
	select {
	case <-evtCh:
	case <-sseCtx.Done():
		t.Fatal("timed out waiting for initial SSE event")
	}

	code, body := postSettings(t, dmn.SocketPath(), `{"workers":4,"no_architect":true}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%v)", code, body)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	appliedAny, ok := body["applied"].([]any)
	if !ok {
		t.Fatalf("expected applied to be []any, got %T (%v)", body["applied"], body["applied"])
	}
	applied := map[string]bool{}
	for _, f := range appliedAny {
		if s, ok := f.(string); ok {
			applied[s] = true
		}
	}
	if !applied["workers"] || !applied["no_architect"] {
		t.Errorf("expected applied to include workers and no_architect, got %v", appliedAny)
	}

	// Verify the in-memory Config mutated via Snapshot().
	snap := dmn.Cfg.Snapshot()
	if snap.Workers != 4 {
		t.Errorf("expected cfg.Workers=4, got %d", snap.Workers)
	}
	if !snap.NoArchitect {
		t.Errorf("expected cfg.NoArchitect=true")
	}

	// Verify persistence to .ralph/config.toml.
	cfgPath := filepath.Join(tmpDir, ".ralph", "config.toml")
	tomlBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	tomlStr := string(tomlBytes)
	if !strings.Contains(tomlStr, "workers = 4") {
		t.Errorf("expected config.toml to contain 'workers = 4', got:\n%s", tomlStr)
	}
	if !strings.Contains(tomlStr, "no_architect = true") {
		t.Errorf("expected config.toml to contain 'no_architect = true', got:\n%s", tomlStr)
	}

	// Verify a daemon_state SSE broadcast reflects the new settings.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-evtCh:
			if !ok {
				t.Fatal("SSE channel closed before receiving updated state")
			}
			if evt.Type != daemon.EventDaemonState {
				continue
			}
			var state daemon.DaemonStateEvent
			if err := json.Unmarshal(evt.Data, &state); err != nil {
				t.Fatalf("unmarshal state: %v", err)
			}
			if state.Settings.Workers == 4 && state.Settings.NoArchitect {
				return // success
			}
		case <-deadline:
			t.Fatal("timed out waiting for daemon_state broadcast reflecting new settings")
		}
	}
}

func TestApplySettings_InvalidWorkers(t *testing.T) {
	dmn, _, _ := testDaemonShort(t)
	startDaemon(t, dmn)
	defer dmn.Shutdown()

	code, body := postSettings(t, dmn.SocketPath(), `{"workers":0}`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%v)", code, body)
	}
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("expected errors map, got %T (%v)", body["errors"], body["errors"])
	}
	if errs["workers"] != "must be >= 1" {
		t.Errorf("expected workers error 'must be >= 1', got %v", errs["workers"])
	}

	// Config must not have mutated on invalid input.
	if snap := dmn.Cfg.Snapshot(); snap.Workers != 1 {
		t.Errorf("expected cfg.Workers unchanged (=1), got %d", snap.Workers)
	}
}

