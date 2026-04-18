package viewer_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// seedIteration writes a manifest with a single story/iteration plus the
// prompt and jsonl files the iteration points at. Returns the file paths so
// tests can inspect them.
func seedIteration(t *testing.T, fp, runID, storyID string, iter int, role string) (promptPath, jsonlPath string) {
	t.Helper()

	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	runDir := filepath.Join(reposDir, fp, "runs", runID)
	turnDir := filepath.Join(runDir, "turns", storyID)
	if err := os.MkdirAll(turnDir, 0o755); err != nil {
		t.Fatalf("mkdir turn dir: %v", err)
	}
	stem := role + "-iter-" + itoa(iter)
	promptPath = filepath.Join(turnDir, stem+".prompt")
	jsonlPath = filepath.Join(turnDir, stem+".jsonl")

	// A minimal prompt + two-line stream: message_start → content_block_start
	// (text) → content_block_delta → content_block_stop → message_stop gives
	// us exactly one assistant turn (turn 1) plus the synthesised user turn
	// (turn 0).
	if err := os.WriteFile(promptPath, []byte("hello prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	lines := []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi back"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
	}
	if err := os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	m := history.Manifest{
		SchemaVersion: history.ManifestSchemaVersion,
		RunID:         runID,
		Kind:          history.KindDaemon,
		RepoFP:        fp,
		Status:        history.StatusComplete,
		StartTime:     now,
		Stories: []history.StoryRecord{{
			StoryID: storyID,
			Iterations: []history.IterationRecord{{
				Index:          iter,
				Role:           role,
				StartTime:      now,
				PromptFile:     promptPath,
				TranscriptFile: jsonlPath,
			}},
		}},
	}
	d, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), d, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return promptPath, jsonlPath
}

func itoa(i int) string {
	// avoid strconv import churn in tests — small positive ints only
	if i == 0 {
		return "0"
	}
	out := ""
	n := i
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

func TestHandleTranscript_StreamsNDJSONWithImmutableCache(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"
	seedIteration(t, fp, runID, "S1", 0, "implementer")

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID+"/transcript/S1/0")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("content-type=%q want application/x-ndjson", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=3600, immutable" {
		t.Errorf("cache-control=%q want public, max-age=3600, immutable", cc)
	}

	// One JSON object per line; each line parses; at least turn 0 (user) +
	// turn 1 (assistant). We additionally require a trailing newline so curl
	// terminates cleanly on EOF.
	body := rr.Body.Bytes()
	if len(body) == 0 || body[len(body)-1] != '\n' {
		t.Errorf("body does not end with newline: %q", body)
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	count := 0
	for sc.Scan() {
		line := sc.Bytes()
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %d not JSON: %v — %q", count, err, line)
		}
		count++
	}
	if sc.Err() != nil {
		t.Fatalf("scan: %v", sc.Err())
	}
	if count < 2 {
		t.Errorf("emitted %d turns, want >=2", count)
	}
}

func TestHandleTranscript_FollowSkipsCacheHeader(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"
	seedIteration(t, fp, runID, "S1", 0, "implementer")

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID+"/transcript/S1/0?follow=true")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("cache-control=%q want empty for follow=true", cc)
	}
}

func TestHandlePrompt_ReturnsTextPlain(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"
	seedIteration(t, fp, runID, "S1", 0, "implementer")

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID+"/prompt/S1/0")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type=%q want text/plain; charset=utf-8", ct)
	}
	if got := rr.Body.String(); got != "hello prompt" {
		t.Errorf("body=%q want %q", got, "hello prompt")
	}
}

func TestHandleTranscript_RejectsTraversal(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	_, h := newTestServer(t)

	for _, path := range []string{
		"/api/repos/fp/runs/rid/transcript/..%2Fescape/0",
		"/api/repos/fp/runs/rid/transcript/..%2F..%2Fetc/0",
	} {
		rr := doGet(t, h, path)
		if rr.Code == http.StatusOK {
			t.Errorf("path=%q returned 200; traversal not blocked", path)
		}
	}
}

func TestHandleTranscript_RejectsManifestWithEscapingPath(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"

	reposDir, _ := userdata.ReposDir()
	runDir := filepath.Join(reposDir, fp, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	escape := filepath.Join(reposDir, fp, "runs", runID, "..", "secret.prompt")
	if err := os.WriteFile(filepath.Join(reposDir, fp, "runs", "secret.prompt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	m := history.Manifest{
		RunID: runID,
		Stories: []history.StoryRecord{{
			StoryID: "S1",
			Iterations: []history.IterationRecord{{
				Index:          0,
				Role:           "implementer",
				PromptFile:     escape,
				TranscriptFile: escape,
			}},
		}},
	}
	d, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), d, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID+"/prompt/S1/0")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400 for escaping path; body=%q", rr.Code, rr.Body.String())
	}
}

func TestHandleTranscript_404WhenIterMissing(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"
	seedIteration(t, fp, runID, "S1", 0, "implementer")

	_, h := newTestServer(t)
	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID+"/transcript/S1/99")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", rr.Code)
	}
}

func TestHandleTranscript_StreamsIncrementally(t *testing.T) {
	// Use the handler against a real listener so we can observe the flusher
	// emitting each Turn as it is produced. The bufio-backed parser yields
	// synchronously so all turns land before the body is closed; the test
	// verifies the complete response is well-formed NDJSON.
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1-aaaaaa"
	seedIteration(t, fp, runID, "S1", 0, "implementer")

	srv, h := newTestServer(t)
	_ = srv
	ts := httptest.NewServer(h)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/repos/"+fp+"/runs/"+runID+"/transcript/S1/0", nil)
	req.Header.Set("X-Ralph-Token", "tok-abc")
	req.Host = "127.0.0.1"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Errorf("body does not terminate with newline: %q", data)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("got %d lines, want >=2: %q", len(lines), data)
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d not JSON: %v — %q", i, err, line)
		}
	}
}
