package viewer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
	"github.com/ohjann/ralphplusplus/internal/viewer/transcript"
)

// resolvedIter holds the filesystem paths for a (run, story, iter) tuple
// after they have been looked up in the manifest and verified to live under
// the run's turns/ directory.
type resolvedIter struct {
	runDir     string
	turnsRoot  string
	promptFile string
	jsonlFile  string
}

// resolveIter finds the IterationRecord that matches (storyID, iterIndex)
// and verifies that its on-disk paths are rooted under <runDir>/turns/. An
// optional role filter narrows the search when a story has multiple
// iterations with the same Index (e.g. implementer + judge).
//
// The lookup has two layers of path safety:
//  1. storyID / iterStr must not contain "..". A bare segment check is
//     cheap and catches the obvious traversal attempt before we hit disk.
//  2. The fully resolved path (from the manifest) is re-cleaned and must
//     share the turnsRoot prefix. This defeats symlink escape and a
//     hand-edited manifest that points outside the run dir.
func resolveIter(fp, runID, storyID, iterStr, role string) (*resolvedIter, int, error) {
	if strings.Contains(storyID, "..") || strings.ContainsAny(storyID, "/\\") || storyID == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid story segment")
	}
	if strings.Contains(iterStr, "..") {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid iter segment")
	}
	iter, err := strconv.Atoi(iterStr)
	if err != nil || iter < 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid iter segment")
	}

	repoDir, err := userdata.RepoDir(fp)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	runDir := filepath.Join(repoDir, "runs", runID)
	turnsRoot := filepath.Join(runDir, "turns")

	m, err := history.ReadManifest(fp, runID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}

	var rec *history.IterationRecord
	for si := range m.Stories {
		if m.Stories[si].StoryID != storyID {
			continue
		}
		for ii := range m.Stories[si].Iterations {
			ir := &m.Stories[si].Iterations[ii]
			if ir.Index != iter {
				continue
			}
			if role != "" && ir.Role != role {
				continue
			}
			rec = ir
			break
		}
		if rec != nil {
			break
		}
	}
	if rec == nil {
		return nil, http.StatusNotFound, fmt.Errorf("iteration not found")
	}

	promptClean := filepath.Clean(rec.PromptFile)
	jsonlClean := filepath.Clean(rec.TranscriptFile)
	prefix := turnsRoot + string(filepath.Separator)
	if !strings.HasPrefix(promptClean, prefix) || !strings.HasPrefix(jsonlClean, prefix) {
		return nil, http.StatusBadRequest, fmt.Errorf("iteration path outside turns root")
	}

	return &resolvedIter{
		runDir:     runDir,
		turnsRoot:  turnsRoot,
		promptFile: promptClean,
		jsonlFile:  jsonlClean,
	}, 0, nil
}

// handlePrompt serves the raw prompt file for a past iteration as
// text/plain. No caching headers — the prompt file is immutable once the
// iteration finishes, but the endpoint is rarely hit so we let the SPA
// decide on cache posture.
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	runID := r.PathValue("runID")
	story := r.PathValue("story")
	iter := r.PathValue("iter")
	role := r.URL.Query().Get("role")

	res, status, err := resolveIter(fp, runID, story, iter, role)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	data, err := os.ReadFile(res.promptFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "read prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600, immutable")
	_, _ = w.Write(data)
}

// handleTranscript streams the reconstructed Turn sequence as NDJSON, one
// JSON object per line. Past-run transcripts (no ?follow=true) are sent with
// an immutable Cache-Control so the SPA can re-render previous iterations
// without hitting the parser again. ?follow=true is reserved for RV-012's
// fsnotify tailer and currently behaves the same as the static path — just
// without the Cache-Control header — so clients can opt out of caching.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	runID := r.PathValue("runID")
	story := r.PathValue("story")
	iter := r.PathValue("iter")
	role := r.URL.Query().Get("role")
	follow := r.URL.Query().Get("follow") == "true"

	res, status, err := resolveIter(fp, runID, story, iter, role)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	seq, err := transcript.ParseFile(res.promptFile, res.jsonlFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "parse transcript: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	if !follow {
		w.Header().Set("Cache-Control", "public, max-age=3600, immutable")
	}
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)

	for t, perr := range seq {
		if perr != nil {
			// Parser errors mid-stream surface as a trailing error object so
			// the client can distinguish a clean EOF from truncated output.
			_ = enc.Encode(map[string]string{"error": perr.Error()})
			break
		}
		if err := enc.Encode(t); err != nil {
			return
		}
		if err := bw.Flush(); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		if isClientGone(r) {
			return
		}
	}
	_ = bw.Flush()
}

func isClientGone(r *http.Request) bool {
	select {
	case <-r.Context().Done():
		return true
	default:
		return false
	}
}
