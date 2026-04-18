package viewer_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// seedRepoWithPath writes a meta.json pointing at repoPath so resolveSock can
// find .ralph/daemon.sock inside it. Parallel story fixtures set up different
// combinations; this helper keeps the live tests independent of the richer
// seedRepo used by the REST handler tests.
func seedRepoWithPath(t *testing.T, fp, repoPath string) {
	t.Helper()
	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	dir := filepath.Join(reposDir, fp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	m := history.RepoMeta{
		Path:      repoPath,
		Name:      filepath.Base(repoPath),
		FirstSeen: now,
		LastSeen:  now,
		RunCount:  1,
	}
	d, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), d, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

// startFakeDaemon listens on a short unix socket path under t.TempDir() and
// returns the socket path plus the repo-root dir that contains
// .ralph/daemon.sock. macOS caps sun_path at 104 bytes, and t.TempDir()
// itself can exceed that, so callers pass a pre-shortened root.
func startFakeDaemon(t *testing.T, repoRoot string, handler http.Handler) string {
	t.Helper()
	ralphDir := filepath.Join(repoRoot, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatalf("mkdir .ralph: %v", err)
	}
	sock := filepath.Join(ralphDir, "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = ln.Close()
	})
	return sock
}

// shortTempRoot creates a short-enough directory for a unix socket to live
// under. macOS enforces a 104-byte limit on sun_path; bare t.TempDir() paths
// like /var/folders/... + viewer subpaths push right up to that boundary, so
// we root the repo under /tmp instead.
func shortTempRoot(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "rv011-")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestHandleLiveEvents_ForwardsSSEAndTerminatesOnEOF(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)

	const payload = "data: {\"type\":\"hello\"}\n\n" +
		"data: {\"type\":\"tick\",\"n\":1}\n\n"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream-Marker", "1")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, payload)
		// returning from the handler closes the body, which the proxy
		// sees as upstream EOF and forwards to the client.
	})
	_ = startFakeDaemon(t, repoRoot, handler)

	_, h := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/live/"+fp+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Ralph-Token", "tok-abc")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(b))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
	if m := resp.Header.Get("X-Upstream-Marker"); m != "1" {
		t.Errorf("upstream marker not forwarded: %q", m)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != payload {
		t.Errorf("body=%q\nwant %q", string(body), payload)
	}
}

func TestHandleLiveEvents_BrowserDisconnectCancelsUpstream(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)

	upstreamDone := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"type\":\"hello\"}\n\n")
		flusher.Flush()
		<-r.Context().Done()
		upstreamDone <- struct{}{}
	})
	_ = startFakeDaemon(t, repoRoot, handler)

	_, h := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/live/"+fp+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Ralph-Token", "tok-abc")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	// Drain the first event so we know the connection is established.
	br := bufio.NewReader(resp.Body)
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatalf("ReadString: %v", err)
	}

	// Client disconnects; upstream handler's r.Context() must fire.
	cancel()
	resp.Body.Close()

	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream handler was not cancelled on client disconnect")
	}
}

func TestHandleLiveState_ProxiesBody(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/state" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"phase":"parallel","paused":false}`)
	})
	_ = startFakeDaemon(t, repoRoot, handler)

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/live/"+fp+"/state")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
	if got := rr.Body.String(); got != `{"phase":"parallel","paused":false}` {
		t.Errorf("body=%q", got)
	}
}

func TestHandleLiveWorkerActivity_ProxiesPath(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)

	var seenPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"worker_id":"7","activity":"x"}`)
	})
	_ = startFakeDaemon(t, repoRoot, handler)

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/live/"+fp+"/worker/7/activity")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if seenPath != "/api/worker/7/activity" {
		t.Errorf("upstream path=%q want /api/worker/7/activity", seenPath)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
}

func TestHandleLive_DaemonOfflineWhenSocketMissing(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)
	// Deliberately no .ralph/daemon.sock.

	_, h := newTestServer(t)

	for _, p := range []string{
		"/api/live/" + fp + "/events",
		"/api/live/" + fp + "/state",
		"/api/live/" + fp + "/worker/1/activity",
	} {
		rr := doGet(t, h, p)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status=%d want 503", p, rr.Code)
		}
		if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control=%q want no-store", p, cc)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: unmarshal: %v body=%q", p, err, rr.Body.String())
		}
		if body["error"] != "daemon_offline" {
			t.Errorf("%s: body=%+v want {error:daemon_offline}", p, body)
		}
	}
}

func TestHandleLive_DaemonOfflineWhenDialRefused(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	repoRoot := shortTempRoot(t)
	seedRepoWithPath(t, fp, repoRoot)

	// Create the socket file but nothing is listening: dial should fail.
	ralphDir := filepath.Join(repoRoot, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sock := filepath.Join(ralphDir, "daemon.sock")
	f, err := os.Create(sock)
	if err != nil {
		t.Fatalf("create socket file: %v", err)
	}
	_ = f.Close()

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/live/"+fp+"/state")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["error"] != "daemon_offline" {
		t.Errorf("body=%+v want daemon_offline", body)
	}
}
