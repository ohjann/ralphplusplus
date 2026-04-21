package viewer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/userdata"
	"github.com/ohjann/ralphplusplus/internal/viewer"
)

// seedPRDRepo creates the reposDir meta.json for fp pointing at repoPath,
// and optionally writes a starting prd.json inside repoPath. Returns the
// current sha256-hex hash of that prd.json (or "" when content is nil).
func seedPRDRepo(t *testing.T, fp, repoPath string, content []byte) string {
	t.Helper()
	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(reposDir, fp), 0o755); err != nil {
		t.Fatalf("mkdir reposDir/fp: %v", err)
	}
	now := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)
	meta := history.RepoMeta{Path: repoPath, Name: "r", FirstSeen: now, LastSeen: now}
	md, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(reposDir, fp, "meta.json"), md, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if content != nil {
		if err := os.WriteFile(filepath.Join(repoPath, "prd.json"), content, 0o644); err != nil {
			t.Fatalf("write prd: %v", err)
		}
		sum := sha256.Sum256(content)
		return hex.EncodeToString(sum[:])
	}
	return ""
}

func doPostPRD(t *testing.T, h http.Handler, path string, body []byte, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, bytes.NewReader(body))
	req.Header.Set("X-Ralph-Token", "tok-abc")
	req.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func newServerForPRD(t *testing.T) http.Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := viewer.NewServer(ctx, "tok-abc", "v-test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s.Handler()
}

func TestHandlePRDPost_WritesAndReturnsHash(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "cafebabefeed"
	repoPath := t.TempDir()
	initialPRD := &prd.PRD{
		Project: "demo",
		UserStories: []prd.UserStory{
			{ID: "A-1", Title: "t", Description: "d", Priority: 1},
		},
	}
	initialBytes, _ := json.MarshalIndent(initialPRD, "", "  ")
	initialBytes = append(initialBytes, '\n')
	initialHash := seedPRDRepo(t, fp, repoPath, initialBytes)

	h := newServerForPRD(t)

	next := &prd.PRD{
		Project: "demo",
		UserStories: []prd.UserStory{
			{ID: "A-1", Title: "t", Description: "d", Priority: 1},
			{ID: "A-2", Title: "new", Description: "added", Priority: 2},
		},
	}
	body, _ := json.Marshal(next)

	rr := doPostPRD(t, h, "/api/repos/"+fp+"/prd", body, initialHash)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp viewer.PRDResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Hash == "" || resp.Hash == initialHash {
		t.Errorf("new hash should differ from initial: resp=%q initial=%q", resp.Hash, initialHash)
	}

	// Confirm on-disk is a valid, pretty-printed JSON file.
	onDisk, err := os.ReadFile(filepath.Join(repoPath, "prd.json"))
	if err != nil {
		t.Fatalf("read prd: %v", err)
	}
	if !bytes.Contains(onDisk, []byte("A-2")) {
		t.Errorf("prd.json missing new story: %s", onDisk)
	}
	if !bytes.HasSuffix(onDisk, []byte("\n")) {
		t.Errorf("prd.json missing trailing newline")
	}
	// sanity: the returned hash matches what's on disk.
	sum := sha256.Sum256(onDisk)
	if resp.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("response hash does not match on-disk hash")
	}
	// re-parse — must be valid JSON
	var reparsed prd.PRD
	if err := json.Unmarshal(onDisk, &reparsed); err != nil {
		t.Fatalf("on-disk prd.json is not valid: %v", err)
	}
	if len(reparsed.UserStories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(reparsed.UserStories))
	}
}

func TestHandlePRDPost_ConflictOnStaleHash(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "cafebabefeed"
	repoPath := t.TempDir()
	initial, _ := json.Marshal(&prd.PRD{Project: "x", UserStories: []prd.UserStory{{ID: "A-1", Title: "t", Description: "d"}}})
	seedPRDRepo(t, fp, repoPath, initial)

	h := newServerForPRD(t)

	next := &prd.PRD{Project: "x", UserStories: []prd.UserStory{{ID: "A-1", Title: "t", Description: "d"}}}
	body, _ := json.Marshal(next)

	rr := doPostPRD(t, h, "/api/repos/"+fp+"/prd", body, "deadbeef"+string(bytes.Repeat([]byte("0"), 56)))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%q", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] != "hash_mismatch" {
		t.Errorf("error=%q want hash_mismatch", resp["error"])
	}
	if resp["currentHash"] == "" {
		t.Errorf("currentHash is empty")
	}
}

func TestHandlePRDPost_ValidationFailed(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "cafebabefeed"
	repoPath := t.TempDir()
	initial, _ := json.Marshal(&prd.PRD{Project: "x", UserStories: []prd.UserStory{{ID: "A-1", Title: "t", Description: "d"}}})
	initialHash := seedPRDRepo(t, fp, repoPath, initial)

	h := newServerForPRD(t)

	// Story missing id + title + description should fail validation.
	bad := &prd.PRD{Project: "x", UserStories: []prd.UserStory{{Priority: 1}}}
	body, _ := json.Marshal(bad)

	rr := doPostPRD(t, h, "/api/repos/"+fp+"/prd", body, initialHash)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rr.Body.String())
	}
	if resp.Error != "validation_failed" {
		t.Errorf("error=%q want validation_failed", resp.Error)
	}
	if _, ok := resp.Fields["userStories[0].id"]; !ok {
		t.Errorf("expected fields[userStories[0].id]; got %+v", resp.Fields)
	}
}

func TestHandlePRDPost_InvalidJSON(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "cafebabefeed"
	repoPath := t.TempDir()
	initial, _ := json.Marshal(&prd.PRD{Project: "x", UserStories: []prd.UserStory{{ID: "A-1", Title: "t", Description: "d"}}})
	initialHash := seedPRDRepo(t, fp, repoPath, initial)

	h := newServerForPRD(t)

	rr := doPostPRD(t, h, "/api/repos/"+fp+"/prd", []byte("{not-json"), initialHash)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%q", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["error"] != "invalid_json" {
		t.Errorf("error=%q want invalid_json", resp["error"])
	}
}

func TestHandlePRDPost_CreatesWhenMissing(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "cafebabefeed"
	repoPath := t.TempDir() // no prd.json seeded
	seedPRDRepo(t, fp, repoPath, nil)

	h := newServerForPRD(t)

	next := &prd.PRD{Project: "demo", UserStories: []prd.UserStory{{ID: "A-1", Title: "t", Description: "d"}}}
	body, _ := json.Marshal(next)

	// empty If-Match header — allowed when no file exists yet.
	rr := doPostPRD(t, h, "/api/repos/"+fp+"/prd", body, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(repoPath, "prd.json")); err != nil {
		t.Fatalf("prd.json was not created: %v", err)
	}
}

func TestHandlePRDPost_404WhenRepoUnknown(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())

	h := newServerForPRD(t)

	body, _ := json.Marshal(&prd.PRD{Project: "x"})
	rr := doPostPRD(t, h, "/api/repos/unknownfp/prd", body, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
}
