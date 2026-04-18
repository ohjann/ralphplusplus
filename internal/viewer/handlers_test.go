package viewer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
	"github.com/ohjann/ralphplusplus/internal/viewer"
)

// seedRepo writes one repo meta, one manifest, and one matching RunSummary
// so the handler join has something to resolve. The only cross-reference is
// the RunID shared between the manifest and the costs.RunSummary entry.
func seedRepo(t *testing.T, fp, runID string) {
	t.Helper()

	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	runDir := filepath.Join(reposDir, fp, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	meta := history.RepoMeta{
		Path:      "/tmp/fake-repo",
		Name:      "fake-repo",
		FirstSeen: now,
		LastSeen:  now,
		RunCount:  1,
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, fp, "meta.json"), metaData, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	end := now.Add(5 * time.Minute)
	manifest := history.Manifest{
		SchemaVersion: history.ManifestSchemaVersion,
		RunID:         runID,
		Kind:          history.KindDaemon,
		RepoFP:        fp,
		RepoPath:      meta.Path,
		RepoName:      meta.Name,
		StartTime:     now,
		EndTime:       &end,
		Status:        history.StatusComplete,
		Totals: history.Totals{
			InputTokens:  1000,
			OutputTokens: 500,
			Iterations:   3,
		},
	}
	manData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manData, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	summary := costs.RunSummary{
		RunID:           runID,
		Kind:            history.KindDaemon,
		TotalCost:       1.23,
		DurationMinutes: 5.0,
		TotalIterations: 3,
		StoriesTotal:    4,
		FirstPassRate:   0.75,
		ModelsUsed:      []string{"claude-opus-4-7"},
	}
	if err := costs.AppendRun(fp, summary); err != nil {
		t.Fatalf("AppendRun: %v", err)
	}
}

func newTestServer(t *testing.T) (*viewer.Server, http.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := viewer.NewServer(ctx, "tok-abc", "v-test")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, s.Handler()
}

func doGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
	req.Header.Set("X-Ralph-Token", "tok-abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleRepos_SortsByLastSeenDesc(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())

	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	older := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	writeRepo := func(fp, name string, ls time.Time, runs int) {
		dir := filepath.Join(reposDir, fp)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		m := history.RepoMeta{Path: "/tmp/" + name, Name: name, FirstSeen: ls, LastSeen: ls, RunCount: runs}
		d, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), d, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	writeRepo("aaaaaaaaaaaa", "old", older, 1)
	writeRepo("bbbbbbbbbbbb", "new", newer, 2)

	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got []viewer.RepoSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Name != "new" || got[1].Name != "old" {
		t.Fatalf("sort order wrong: got %q then %q", got[0].Name, got[1].Name)
	}
	if got[0].RunCount != 2 {
		t.Fatalf("runCount=%d want 2", got[0].RunCount)
	}
}

func TestHandleRunsList_JoinsSummaryByRunID(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-1700000000000-abc123"
	seedRepo(t, fp, runID)

	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos/"+fp+"/runs")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var items []viewer.RunListItem
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rr.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("len=%d want 1", len(items))
	}
	it := items[0]
	if it.RunID != runID {
		t.Errorf("runId=%q want %q", it.RunID, runID)
	}
	if it.Kind != history.KindDaemon {
		t.Errorf("kind=%q want %q", it.Kind, history.KindDaemon)
	}
	if it.Iterations != 3 {
		t.Errorf("iterations=%d want 3 (from manifest Totals)", it.Iterations)
	}
	if it.TotalCost == nil || *it.TotalCost != 1.23 {
		t.Errorf("totalCost=%v want 1.23 (joined from RunSummary)", it.TotalCost)
	}
	if it.DurationMinutes == nil || *it.DurationMinutes != 5.0 {
		t.Errorf("durationMinutes=%v want 5.0", it.DurationMinutes)
	}
	if it.FirstPassRate == nil || *it.FirstPassRate != 0.75 {
		t.Errorf("firstPassRate=%v want 0.75", it.FirstPassRate)
	}
	if len(it.ModelsUsed) != 1 || it.ModelsUsed[0] != "claude-opus-4-7" {
		t.Errorf("modelsUsed=%v want [claude-opus-4-7]", it.ModelsUsed)
	}
}

func TestHandleRunsList_KindFilterAndLimit(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	// seed a daemon run
	seedRepo(t, fp, "run-1-aaaaaa")

	// seed a retro manifest without a matching RunSummary
	reposDir, _ := userdata.ReposDir()
	retroDir := filepath.Join(reposDir, fp, "runs", "run-2-bbbbbb")
	if err := os.MkdirAll(retroDir, 0o755); err != nil {
		t.Fatalf("mkdir retro: %v", err)
	}
	retro := history.Manifest{
		RunID:     "run-2-bbbbbb",
		Kind:      history.KindRetro,
		StartTime: time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
		Status:    history.StatusComplete,
	}
	d, _ := json.MarshalIndent(retro, "", "  ")
	if err := os.WriteFile(filepath.Join(retroDir, "manifest.json"), d, 0o644); err != nil {
		t.Fatalf("write retro manifest: %v", err)
	}

	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos/"+fp+"/runs?kind=retro")
	var items []viewer.RunListItem
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 || items[0].Kind != history.KindRetro {
		t.Fatalf("kind filter failed: %+v", items)
	}
	if items[0].TotalCost != nil {
		t.Errorf("retro has no RunSummary — totalCost should be nil, got %v", items[0].TotalCost)
	}

	rr = doGet(t, h, "/api/repos/"+fp+"/runs?limit=1")
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("limit=1 returned %d entries", len(items))
	}
	// Newest first — retro is 2026-04-02, daemon is 2026-04-01.
	if items[0].Kind != history.KindRetro {
		t.Errorf("sort desc failed: first kind=%q", items[0].Kind)
	}
}

func TestHandleRunDetail_SummaryNilWhenMissing(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-only-manifest"

	reposDir, _ := userdata.ReposDir()
	runDir := filepath.Join(reposDir, fp, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	m := history.Manifest{RunID: runID, Kind: history.KindAdHoc, Status: history.StatusRunning, StartTime: time.Now().UTC()}
	d, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), d, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos/"+fp+"/runs/"+runID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var body viewer.RunDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Manifest.RunID != runID {
		t.Errorf("manifest.runId=%q want %q", body.Manifest.RunID, runID)
	}
	if body.Summary != nil {
		t.Errorf("summary should be nil when no RunSummary exists; got %+v", body.Summary)
	}
}

func TestHandleRunDetail_404WhenMissing(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos/deadbeef/runs/does-not-exist")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
}

func TestHandleRepoDetail_AggregatesCosts(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	seedRepo(t, fp, "run-1-aaaaaa")
	if err := costs.AppendRun(fp, costs.RunSummary{
		RunID:           "run-2-bbbbbb",
		TotalCost:       2.0,
		DurationMinutes: 10.0,
		TotalIterations: 4,
		StoriesTotal:    2,
	}); err != nil {
		t.Fatalf("AppendRun: %v", err)
	}

	_, h := newTestServer(t)

	rr := doGet(t, h, "/api/repos/"+fp)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var body viewer.RepoDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.AggCosts.Runs != 2 {
		t.Errorf("runs=%d want 2", body.AggCosts.Runs)
	}
	if body.AggCosts.TotalCost != 3.23 {
		t.Errorf("totalCost=%v want 3.23", body.AggCosts.TotalCost)
	}
	if body.AggCosts.DurationMinutes != 15.0 {
		t.Errorf("durationMinutes=%v want 15.0", body.AggCosts.DurationMinutes)
	}
}
