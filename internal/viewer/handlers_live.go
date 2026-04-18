package viewer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"

	"github.com/ohjann/ralphplusplus/internal/history"
)

// errDaemonOffline marks the three failure modes the SPA renders identically:
// no such repo, no socket file, or dial refused.
var errDaemonOffline = errors.New("daemon_offline")

// resolveSock maps :fp to the repo's daemon socket. It returns
// errDaemonOffline when the repo has no meta, no .ralph/daemon.sock, or the
// socket path is not stat-able; os.Stat failures (ENOENT, permission, etc.)
// all collapse into the same 503 for the SPA.
func (s *Server) resolveSock(ctx context.Context, fp string) (string, error) {
	repos, err := s.Index.Get(ctx)
	if err != nil {
		return "", err
	}
	var meta *history.RepoMeta
	for i := range repos {
		if repos[i].FP == fp {
			meta = &repos[i].Meta
			break
		}
	}
	if meta == nil {
		return "", errDaemonOffline
	}
	sock := filepath.Join(meta.Path, ".ralph", "daemon.sock")
	if _, err := os.Stat(sock); err != nil {
		return "", errDaemonOffline
	}
	return sock, nil
}

// unixTransport is a net/http RoundTripper bound to a specific unix socket.
// It delegates to unixRoundTrip so ReverseProxy and ad-hoc callers share one
// dial code path.
type unixTransport struct{ sock string }

func (t *unixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return unixRoundTrip(t.sock, req)
}

// writeDaemonOffline emits the canonical 503 body the SPA matches on.
func writeDaemonOffline(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "daemon_offline"})
}

// proxyToDaemon wires :fp → unix socket → upstreamPath through a
// ReverseProxy. The browser's context propagates to the dialer, so the
// upstream SSE stream aborts when the SPA closes its EventSource.
func (s *Server) proxyToDaemon(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	fp := r.PathValue("fp")
	sock, err := s.resolveSock(r.Context(), fp)
	if err != nil {
		writeDaemonOffline(w)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "daemon"
			req.URL.Path = upstreamPath
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = "daemon"
		},
		Transport: &unixTransport{sock: sock},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeDaemonOffline(w)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		FlushInterval: -1,
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) handleLiveEvents(w http.ResponseWriter, r *http.Request) {
	s.proxyToDaemon(w, r, "/events")
}

func (s *Server) handleLiveState(w http.ResponseWriter, r *http.Request) {
	s.proxyToDaemon(w, r, "/api/state")
}

func (s *Server) handleLiveWorkerActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.proxyToDaemon(w, r, "/api/worker/"+id+"/activity")
}

// handleLiveCommand returns a handler that POSTs to the matching daemon
// /api/<cmd> endpoint. The reverse proxy preserves method, headers, and body
// verbatim; the daemon's response status and body are returned unchanged so
// validation errors (400 with {error:...}) surface to the SPA.
//
// The handler itself enforces POST-only (rather than relying on a
// method-scoped mux pattern) because the viewer mux has a "/" catchall that
// would otherwise swallow mismatched methods as 404s instead of 405s.
func (s *Server) handleLiveCommand(cmd string) http.HandlerFunc {
	upstream := "/api/" + cmd
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.proxyToDaemon(w, r, upstream)
	}
}
