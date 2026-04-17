package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/costs"
)

var runIDRe = regexp.MustCompile(`^run-\d{10,}-[a-z2-7]{6}$`)

// newRunEnv sets up a RALPH_DATA_DIR, picks a project dir, and returns
// (projectDir, prdFile). The returned project dir is *not* a git repo so the
// manifest's GitBranch/GitHeadSHA are empty — which tests touch explicitly.
func newRunEnv(t *testing.T) (projectDir, prdFile string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	proj := t.TempDir()
	prdPath := filepath.Join(proj, "prd.json")
	if err := os.WriteFile(prdPath, []byte(`{"project":"x"}`), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	return proj, prdPath
}

func readManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

func TestOpenRun_WritesRunningManifestWithKind(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "test-ver", RunOpts{Kind: KindRetro})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if !runIDRe.MatchString(r.ID()) {
		t.Errorf("run id %q doesn't match expected format", r.ID())
	}
	if fi, err := os.Stat(r.Dir()); err != nil || !fi.IsDir() {
		t.Fatalf("run dir missing: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(r.Dir(), "turns")); err != nil || !fi.IsDir() {
		t.Fatalf("turns dir missing: %v", err)
	}

	m := readManifest(t, r.Dir())
	if m.Status != StatusRunning {
		t.Errorf("status = %q, want running", m.Status)
	}
	if m.Kind != KindRetro {
		t.Errorf("kind = %q, want retro", m.Kind)
	}
	if m.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", m.PID, os.Getpid())
	}
	if m.Hostname == "" {
		t.Error("hostname is empty")
	}
	if m.ProcessStart.IsZero() {
		t.Error("process_start is zero")
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("schema_version = %d", m.SchemaVersion)
	}
	if m.PRDSnapshot == "" {
		t.Error("prd_snapshot should be populated when prd file exists")
	}
	if m.RalphVersion != "test-ver" {
		t.Errorf("ralph_version = %q", m.RalphVersion)
	}
}

func TestOpenRun_EmptyKindDefaultsToDaemon(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "v", RunOpts{})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	m := readManifest(t, r.Dir())
	if m.Kind != KindDaemon {
		t.Errorf("kind = %q, want daemon", m.Kind)
	}
}

func TestStartIteration_CreatesCorrectPaths(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "v", RunOpts{})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	iw, err := r.StartIteration("S-1", "implementer", 3, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("StartIteration: %v", err)
	}
	wantBase := filepath.Join(r.Dir(), "turns", "S-1", "implementer-iter-3")
	if iw.rec.PromptFile != wantBase+".prompt" {
		t.Errorf("prompt file = %q, want %q", iw.rec.PromptFile, wantBase+".prompt")
	}
	if iw.rec.TranscriptFile != wantBase+".jsonl" {
		t.Errorf("transcript file = %q, want %q", iw.rec.TranscriptFile, wantBase+".jsonl")
	}
	if iw.rec.MetaFile != wantBase+".meta.json" {
		t.Errorf("meta file = %q, want %q", iw.rec.MetaFile, wantBase+".meta.json")
	}
	if fi, err := os.Stat(filepath.Join(r.Dir(), "turns", "S-1")); err != nil || !fi.IsDir() {
		t.Fatalf("story turn dir missing: %v", err)
	}

	// Meta file is seeded at StartIteration so a crash mid-stream still
	// leaves a reconstruction breadcrumb on disk.
	if _, err := os.Stat(iw.rec.MetaFile); err != nil {
		t.Errorf("meta.json not written at StartIteration: %v", err)
	}

	// Manifest now references the iteration.
	m := readManifest(t, r.Dir())
	if len(m.Stories) != 1 || m.Stories[0].StoryID != "S-1" {
		t.Fatalf("stories = %#v", m.Stories)
	}
	if len(m.Stories[0].Iterations) != 1 || m.Stories[0].Iterations[0].Index != 3 {
		t.Fatalf("iterations = %#v", m.Stories[0].Iterations)
	}

	// WritePrompt lands on disk, and RawStreamWriter is usable.
	if err := iw.WritePrompt("hello prompt"); err != nil {
		t.Fatalf("WritePrompt: %v", err)
	}
	data, err := os.ReadFile(iw.rec.PromptFile)
	if err != nil || string(data) != "hello prompt" {
		t.Errorf("prompt = %q, err=%v", string(data), err)
	}
	if _, err := iw.RawStreamWriter().Write([]byte("{\"type\":\"x\"}\n")); err != nil {
		t.Fatalf("RawStreamWriter: %v", err)
	}

	if err := iw.Finish("sess-1", &costs.TokenUsage{InputTokens: 10, OutputTokens: 2}, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	m = readManifest(t, r.Dir())
	it := m.Stories[0].Iterations[0]
	if it.EndTime == nil || it.SessionID != "sess-1" {
		t.Errorf("iteration not finalized: %#v", it)
	}
	if it.TokenUsage == nil || it.TokenUsage.InputTokens != 10 {
		t.Errorf("token usage not recorded: %#v", it.TokenUsage)
	}

	// The .meta.json sidecar mirrors the iteration record.
	metaData, err := os.ReadFile(iw.rec.MetaFile)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var metaRec IterationRecord
	if err := json.Unmarshal(metaData, &metaRec); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if metaRec.SessionID != "sess-1" || metaRec.EndTime == nil {
		t.Errorf("meta sidecar not updated: %#v", metaRec)
	}
}

func TestFinalize_WritesEndTimeAndTotals(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "v", RunOpts{})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	totals := Totals{InputTokens: 100, OutputTokens: 40, Iterations: 3}
	if err := r.Finalize(StatusComplete, totals, nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	m := readManifest(t, r.Dir())
	if m.Status != StatusComplete {
		t.Errorf("status = %q, want complete", m.Status)
	}
	if m.EndTime == nil {
		t.Fatal("end_time is nil")
	}
	if m.Totals.InputTokens != 100 || m.Totals.Iterations != 3 {
		t.Errorf("totals = %#v", m.Totals)
	}

	// Second Finalize is a no-op — don't overwrite a terminal status.
	if err := r.Finalize(StatusFailed, Totals{}, nil); err != nil {
		t.Fatalf("second Finalize: %v", err)
	}
	m = readManifest(t, r.Dir())
	if m.Status != StatusComplete {
		t.Errorf("status after re-finalize = %q, want complete", m.Status)
	}
}

// Finalize with a non-nil summary must append a run-history.json entry whose
// RunID and Kind match the manifest. This enforces the "two records cannot
// diverge" contract from IH-005.
func TestFinalize_AppendsCostSummaryWithManifestRunIDAndKind(t *testing.T) {
	cfg := newRunCfg(t)
	r, err := OpenRun(cfg, "v", RunOpts{Kind: KindRetro})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}

	summary := costs.RunSummary{PRD: "test", Date: "2026-04-17T12:00:00Z", StoriesTotal: 1, StoriesCompleted: 1}
	totals := Totals{InputTokens: 10, OutputTokens: 2}
	if err := r.Finalize(StatusComplete, totals, &summary); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// summary was mutated in place.
	if summary.RunID != r.ID() {
		t.Errorf("summary RunID = %q, want %q", summary.RunID, r.ID())
	}
	if summary.Kind != KindRetro {
		t.Errorf("summary Kind = %q, want %q", summary.Kind, KindRetro)
	}

	// And the run-history.json on disk reflects that.
	h, err := costs.LoadHistory(r.manifest.RepoFP)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(h.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(h.Runs))
	}
	if h.Runs[0].RunID != r.ID() || h.Runs[0].Kind != KindRetro {
		t.Errorf("persisted summary = %#v", h.Runs[0])
	}
}

// stageRunningManifest writes a manifest.json under a synthetic run dir for
// repo fingerprint fp and returns the manifest path.
func stageRunningManifest(t *testing.T, fp, hostname string, pid int) string {
	t.Helper()
	runID, err := newRunID()
	if err != nil {
		t.Fatalf("newRunID: %v", err)
	}
	dir, err := runDirFor(fp, runID)
	if err != nil {
		t.Fatalf("runDirFor: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	m := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		RunID:         runID,
		Kind:          KindDaemon,
		RepoFP:        fp,
		PID:           pid,
		Hostname:      hostname,
		Status:        StatusRunning,
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestSweepInterrupted_FlipsStaleDeadPIDRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	host, _ := os.Hostname()
	path := stageRunningManifest(t, "fp1", host, 99999)

	origLive := pidLiveFn
	defer func() { pidLiveFn = origLive }()
	pidLiveFn = func(int) bool { return false }

	if err := SweepInterrupted(); err != nil {
		t.Fatalf("SweepInterrupted: %v", err)
	}
	data, _ := os.ReadFile(path)
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Status != StatusInterrupted {
		t.Errorf("status = %q, want interrupted", m.Status)
	}
	if m.EndTime == nil {
		t.Error("end_time should be stamped when sweeping")
	}
}

func TestSweepInterrupted_LeavesLivePIDRunAlone(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	host, _ := os.Hostname()
	path := stageRunningManifest(t, "fp2", host, os.Getpid())

	origLive := pidLiveFn
	defer func() { pidLiveFn = origLive }()
	pidLiveFn = func(int) bool { return true }

	if err := SweepInterrupted(); err != nil {
		t.Fatalf("SweepInterrupted: %v", err)
	}
	data, _ := os.ReadFile(path)
	var m Manifest
	_ = json.Unmarshal(data, &m)
	if m.Status != StatusRunning {
		t.Errorf("status = %q, want running (live PID must not be flipped)", m.Status)
	}
}

func TestSweepInterrupted_LeavesOtherHostnameRunAlone(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	host, _ := os.Hostname()
	// Use a distinct hostname — even if PID is dead on this box, the
	// manifest may belong to another machine whose process is still alive.
	otherHost := host + "-somewhere-else"
	path := stageRunningManifest(t, "fp3", otherHost, 99999)

	origLive := pidLiveFn
	defer func() { pidLiveFn = origLive }()
	pidLiveFn = func(int) bool { return false }

	if err := SweepInterrupted(); err != nil {
		t.Fatalf("SweepInterrupted: %v", err)
	}
	data, _ := os.ReadFile(path)
	var m Manifest
	_ = json.Unmarshal(data, &m)
	if m.Status != StatusRunning {
		t.Errorf("status = %q, want running (other-host must not be flipped)", m.Status)
	}
}

func TestSweepInterrupted_NoReposDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	// No repos dir exists — sweep is a no-op.
	if err := SweepInterrupted(); err != nil {
		t.Fatalf("SweepInterrupted: %v", err)
	}
}

func TestRunIDFormat_SortableAndDistinct(t *testing.T) {
	a, err := newRunID()
	if err != nil {
		t.Fatalf("newRunID: %v", err)
	}
	b, err := newRunID()
	if err != nil {
		t.Fatalf("newRunID: %v", err)
	}
	if !runIDRe.MatchString(a) || !runIDRe.MatchString(b) {
		t.Errorf("ids don't match format: %s %s", a, b)
	}
	if a == b {
		t.Errorf("duplicate run ids: %s", a)
	}
	// Lexicographic sort matches chronological order via the ms prefix.
	if !strings.HasPrefix(a, "run-") {
		t.Errorf("missing prefix: %s", a)
	}
}

func TestUpdateStoryList_PreservesIterations(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "v", RunOpts{})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	iw, err := r.StartIteration("S-1", "implementer", 1, "")
	if err != nil {
		t.Fatalf("StartIteration: %v", err)
	}
	_ = iw.Finish("s", nil, nil)

	// Re-seed the list (as the DAG might evolve) — the prior iteration
	// must survive.
	if err := r.UpdateStoryList([]StoryRecord{{StoryID: "S-1", Title: "First"}, {StoryID: "S-2", Title: "Second"}}); err != nil {
		t.Fatalf("UpdateStoryList: %v", err)
	}
	m := readManifest(t, r.Dir())
	if len(m.Stories) != 2 {
		t.Fatalf("stories = %d", len(m.Stories))
	}
	if m.Stories[0].Title != "First" {
		t.Errorf("title not applied: %#v", m.Stories[0])
	}
	if len(m.Stories[0].Iterations) != 1 {
		t.Errorf("iteration history lost: %#v", m.Stories[0])
	}
}

func TestSetStoryFinal(t *testing.T) {
	proj, prd := newRunEnv(t)
	r, err := OpenRun(proj, prd, "v", RunOpts{})
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if err := r.SetStoryFinal("S-9", "complete"); err != nil {
		t.Fatalf("SetStoryFinal: %v", err)
	}
	m := readManifest(t, r.Dir())
	if len(m.Stories) != 1 || m.Stories[0].FinalStatus != "complete" {
		t.Errorf("final status not recorded: %#v", m.Stories)
	}
}
