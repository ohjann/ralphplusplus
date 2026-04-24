package viewer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
	"github.com/ohjann/ralphplusplus/internal/viewer/transcript"
)

// followIdleDeadline bounds how long the tailer will wait between Write
// events before re-checking manifest.Status and the daemon socket. Exposed
// as a package-level var so tests can drop it to a few hundred ms without
// sleeping 30 s of real time.
var followIdleDeadline = 30 * time.Second

// resolvedIter holds the filesystem paths for a (run, story, iter) tuple
// after they have been looked up in the manifest and verified to live under
// the run's turns/ directory.
type resolvedIter struct {
	runID      string
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
		runID:      runID,
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
// without hitting the parser again. ?follow=true switches to the fsnotify
// tailer (see streamTranscriptFollow), which streams existing content plus
// any subsequent appends and closes on run-terminal / daemon-dead.
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

	if follow {
		s.streamTranscriptFollow(w, r, fp, res)
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
	w.Header().Set("Cache-Control", "public, max-age=3600, immutable")
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

// streamTranscriptFollow serves ?follow=true: replay the synthesised prompt
// turn + everything currently in the jsonl, then fsnotify-watch the jsonl
// for Write events and stream new Turns as they parse. Replay is stateless —
// a fresh request always starts at Turn 0 — so clients de-dupe by
// Turn.Index across reconnects.
//
// Termination:
//   - Client disconnects (r.Context().Done())
//   - fsnotify reports Remove/Rename on the jsonl (the parent run was rolled
//     or the file was rotated out from under us)
//   - No Write event for followIdleDeadline AND the manifest has transitioned
//     to a terminal status (complete/failed/interrupted). During the idle
//     window we additionally treat a missing daemon.sock as a close signal
//     — if the daemon process died without flipping the manifest, the
//     socket disappearance is our second line of defence.
func (s *Server) streamTranscriptFollow(w http.ResponseWriter, r *http.Request, fp string, res *resolvedIter) {
	promptBytes, err := os.ReadFile(res.promptFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "read prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}

	f, err := os.Open(res.jsonlFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "open jsonl: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		http.Error(w, "fsnotify: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer watcher.Close()
	if err := watcher.Add(res.jsonlFile); err != nil {
		http.Error(w, "fsnotify add: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)

	flush := func() error {
		if err := bw.Flush(); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	emit := func(t transcript.Turn) error {
		if err := enc.Encode(t); err != nil {
			return err
		}
		return flush()
	}

	if err := emit(transcript.Turn{
		Index:  0,
		Role:   "user",
		Blocks: []transcript.Block{{Kind: "text", Text: string(promptBytes)}},
	}); err != nil {
		return
	}

	tailer := transcript.NewTailer(1)
	drain := func() error {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if err := tailer.Feed(buf[:n], emit); err != nil {
					return err
				}
			}
			if rerr == io.EOF {
				return nil
			}
			if rerr != nil {
				return rerr
			}
		}
	}
	if err := drain(); err != nil {
		return
	}

	// Fast-path: if the manifest is already terminal, the file won't grow
	// again — close immediately instead of making clients wait out the idle
	// deadline. Lets the frontend send ?follow=true unconditionally. Only
	// inspect the manifest here (not the sock): an absent sock is a valid
	// idle-tick heuristic but not a reason to close before a single tick.
	if manifestIsTerminal(fp, res.runID) {
		return
	}

	idle := time.NewTimer(followIdleDeadline)
	defer idle.Stop()
	resetIdle := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(followIdleDeadline)
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				return
			}
			if ev.Op&fsnotify.Write != 0 {
				if err := drain(); err != nil {
					return
				}
				resetIdle()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			return
		case <-idle.C:
			if s.followShouldClose(r.Context(), fp, res.runID) {
				return
			}
			idle.Reset(followIdleDeadline)
		}
	}
}

// followShouldClose returns true when the daemon has gone away or the
// manifest reports the run is terminal — either condition ends the tail.
// A terminal manifest is authoritative: even if the socket is still around,
// no more jsonl writes are coming.
func (s *Server) followShouldClose(ctx context.Context, fp, runID string) bool {
	if manifestIsTerminal(fp, runID) {
		return true
	}
	if _, err := s.resolveSock(ctx, fp); err != nil {
		return true
	}
	return false
}

// manifestIsTerminal checks only the on-disk manifest status. Used by the
// follow fast-path where an absent daemon sock shouldn't cause a premature
// close (a running daemon that hasn't yet bound its sock is indistinguishable
// from a dead one at that level).
func manifestIsTerminal(fp, runID string) bool {
	m, err := history.ReadManifest(fp, runID)
	if err != nil {
		return false
	}
	switch m.Status {
	case history.StatusComplete, history.StatusFailed, history.StatusInterrupted:
		return true
	}
	return false
}

func isClientGone(r *http.Request) bool {
	select {
	case <-r.Context().Done():
		return true
	default:
		return false
	}
}
