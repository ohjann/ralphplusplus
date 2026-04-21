package viewer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// handleSpawnDaemon serves POST /api/spawn-daemon. Body: {repoPath, flags,
// confirm?}. On success returns 200 {fp, pid}; on path-outside-$HOME with
// confirm unset returns 409 {warn:"path outside $HOME", resolved}; on a
// daemon already running for that repo returns 409 {error:"...", fp};
// anything else (invalid flag, missing directory, spawn failure) returns
// 400.
func (s *Server) handleSpawnDaemon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_body: " + err.Error()})
		return
	}
	var req SpawnRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json: " + err.Error()})
			return
		}
	}

	resolved, insideHome, rerr := ResolvePath(req.RepoPath)
	if rerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_path",
			"details": "path must be an existing directory that resolves via Abs+EvalSymlinks",
		})
		return
	}
	if !insideHome && !req.Confirm {
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"warn":     "path outside $HOME",
			"resolved": resolved,
		})
		return
	}

	result, serr := SpawnDaemon(r.Context(), resolved, req.Flags)
	if serr != nil {
		if errors.Is(serr, ErrDaemonAlreadyRunning) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "daemon_already_running",
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": serr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
