package viewer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
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

// handleSpawnRetro serves POST /api/spawn/retro/:fp. Resolves :fp against
// the project index, refuses with 409 if a retro is already running for
// that repo, otherwise detaches `ralph retro --dir <repoPath>` via the
// shared spawner and polls briefly for the new manifest so the response
// can include runId.
func (s *Server) handleSpawnRetro(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	fp := r.PathValue("fp")
	if fp == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_fp"})
		return
	}

	repos, err := s.Index.Get(r.Context())
	if err != nil {
		http.Error(w, "load repos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var repoPath string
	for i := range repos {
		if repos[i].FP == fp {
			repoPath = repos[i].Meta.Path
			break
		}
	}
	if repoPath == "" {
		http.NotFound(w, r)
		return
	}

	// Snapshot existing retro run ids so the post-spawn poll can ignore them.
	known := retroRunIDSet(fp)

	result, serr := SpawnRetro(r.Context(), repoPath)
	if serr != nil {
		if errors.Is(serr, ErrRetroAlreadyRunning) {
			// Re-query so the 409 body can carry the existing runId.
			existing, _, _ := isRetroRunning(fp)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "retro_already_running",
				"runId": existing,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": serr.Error(),
		})
		return
	}

	if id := waitForNewRetroRun(r.Context(), fp, known, 2*time.Second); id != "" {
		result.RunID = id
	}
	writeJSON(w, http.StatusOK, result)
}
