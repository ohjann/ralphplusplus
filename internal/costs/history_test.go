package costs

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// testFP mints a synthetic fingerprint so tests can exercise the user-level
// path without touching internal/history.
func testFP(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:12]
}

// setupUserData points the costs package at an isolated user data dir and
// returns a stable fingerprint for tests to use.
func setupUserData(t *testing.T) string {
	t.Helper()
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	return testFP(t.Name())
}

func TestLoadHistory_NoFile(t *testing.T) {
	fp := setupUserData(t)
	h, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Runs) != 0 {
		t.Fatalf("expected empty runs, got %d", len(h.Runs))
	}
}

func TestSaveAndLoadHistory(t *testing.T) {
	fp := setupUserData(t)
	h := RunHistory{
		Runs: []RunSummary{
			{
				PRD:                   "test-project",
				Date:                  "2026-03-13T10:00:00Z",
				RunID:                 "run-123",
				Kind:                  "daemon",
				StoriesTotal:          5,
				StoriesCompleted:      4,
				StoriesFailed:         1,
				TotalCost:             1.23,
				DurationMinutes:       45.5,
				TotalIterations:       12,
				AvgIterationsPerStory: 3.0,
				StuckCount:            1,
				JudgeRejectionRate:    0.25,
			},
		},
	}

	if err := SaveHistory(fp, h); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(loaded.Runs))
	}
	r := loaded.Runs[0]
	if r.PRD != "test-project" {
		t.Errorf("PRD = %q, want %q", r.PRD, "test-project")
	}
	if r.RunID != "run-123" {
		t.Errorf("RunID = %q, want %q", r.RunID, "run-123")
	}
	if r.Kind != "daemon" {
		t.Errorf("Kind = %q, want %q", r.Kind, "daemon")
	}

	// Verify the file lives under <userdata>/repos/<fp>/.
	userDir := os.Getenv("RALPH_DATA_DIR")
	data, err := os.ReadFile(filepath.Join(userDir, "repos", fp, "run-history.json"))
	if err != nil {
		t.Fatalf("expected file under user data dir: %v", err)
	}
	if !containsByte(data, '\n') {
		t.Error("expected indented JSON with newlines")
	}
}

func TestAppendRun(t *testing.T) {
	fp := setupUserData(t)

	s1 := RunSummary{PRD: "proj1", Date: "2026-03-13T10:00:00Z", StoriesTotal: 3, StoriesCompleted: 3, Kind: "daemon"}
	s2 := RunSummary{PRD: "proj1", Date: "2026-03-13T11:00:00Z", StoriesTotal: 5, StoriesCompleted: 4, StoriesFailed: 1, Kind: "daemon"}

	if err := AppendRun(fp, s1); err != nil {
		t.Fatalf("first append error: %v", err)
	}
	if err := AppendRun(fp, s2); err != nil {
		t.Fatalf("second append error: %v", err)
	}

	h, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(h.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(h.Runs))
	}
	if h.Runs[0].StoriesTotal != 3 {
		t.Errorf("first run StoriesTotal = %d, want 3", h.Runs[0].StoriesTotal)
	}
	if h.Runs[1].StoriesFailed != 1 {
		t.Errorf("second run StoriesFailed = %d, want 1", h.Runs[1].StoriesFailed)
	}
}

func TestBackwardCompatibility_OldHistoryFormat(t *testing.T) {
	fp := setupUserData(t)
	userDir := os.Getenv("RALPH_DATA_DIR")
	repoDir := filepath.Join(userDir, "repos", fp)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a history file with only the original fields (no new fields like
	// run_id or kind) — simulates a run-history produced before IH-005.
	oldJSON := `{
  "runs": [
    {
      "prd": "old-project",
      "date": "2026-01-01T00:00:00Z",
      "stories_total": 3,
      "stories_completed": 2,
      "stories_failed": 1,
      "total_cost": 0,
      "duration_minutes": 10.5,
      "total_iterations": 4,
      "avg_iterations_per_story": 2.0,
      "stuck_count": 0,
      "judge_rejection_rate": 0.1
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repoDir, "run-history.json"), []byte(oldJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("failed to load old history format: %v", err)
	}
	if len(h.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(h.Runs))
	}
	r := h.Runs[0]
	if r.PRD != "old-project" {
		t.Errorf("PRD = %q, want %q", r.PRD, "old-project")
	}
	if r.RunID != "" {
		t.Errorf("RunID = %q, want empty for pre-IH-005 entry", r.RunID)
	}
	if r.Kind != "" {
		t.Errorf("Kind = %q, want empty for pre-IH-005 entry", r.Kind)
	}
	if !r.IsDaemon() {
		t.Error("pre-IH-005 entry with empty Kind must be grandfathered as daemon")
	}
}

func TestIsDaemon(t *testing.T) {
	cases := []struct {
		kind string
		want bool
	}{
		{"", true},
		{"daemon", true},
		{"retro", false},
		{"memory-consolidate", false},
		{"ad-hoc", false},
	}
	for _, c := range cases {
		got := RunSummary{Kind: c.kind}.IsDaemon()
		if got != c.want {
			t.Errorf("Kind=%q IsDaemon=%v, want %v", c.kind, got, c.want)
		}
	}
}

func TestNewFieldsRoundTrip(t *testing.T) {
	fp := setupUserData(t)
	h := RunHistory{
		Runs: []RunSummary{
			{
				PRD:               "test",
				Date:              "2026-03-25T10:00:00Z",
				RunID:             "run-abc",
				Kind:              "daemon",
				StoriesTotal:      5,
				StoriesCompleted:  4,
				StoriesFailed:     1,
				FirstPassRate:     0.75,
				ModelsUsed:        []string{"claude-opus-4-6", "claude-sonnet-4-6"},
				TotalInputTokens:  100000,
				TotalOutputTokens: 50000,
				CacheHitRate:      0.42,
				StoryDetails: []StorySummary{
					{StoryID: "S-001", Title: "Auth", Iterations: 2, Passed: true, JudgeRejects: 1, Model: "claude-opus-4-6"},
					{StoryID: "S-002", Title: "API", Iterations: 1, Passed: true, JudgeRejects: 0, Model: "claude-sonnet-4-6"},
				},
			},
		},
	}

	if err := SaveHistory(fp, h); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	r := loaded.Runs[0]
	if r.FirstPassRate != 0.75 {
		t.Errorf("FirstPassRate = %f, want 0.75", r.FirstPassRate)
	}
	if len(r.ModelsUsed) != 2 {
		t.Errorf("ModelsUsed length = %d, want 2", len(r.ModelsUsed))
	}
	if r.TotalInputTokens != 100000 {
		t.Errorf("TotalInputTokens = %d, want 100000", r.TotalInputTokens)
	}
	if r.CacheHitRate != 0.42 {
		t.Errorf("CacheHitRate = %f, want 0.42", r.CacheHitRate)
	}
	if len(r.StoryDetails) != 2 {
		t.Fatalf("StoryDetails length = %d, want 2", len(r.StoryDetails))
	}
	if r.StoryDetails[0].JudgeRejects != 1 {
		t.Errorf("StoryDetails[0].JudgeRejects = %d, want 1", r.StoryDetails[0].JudgeRejects)
	}
	if r.StoryDetails[1].Model != "claude-sonnet-4-6" {
		t.Errorf("StoryDetails[1].Model = %q, want %q", r.StoryDetails[1].Model, "claude-sonnet-4-6")
	}
}

func TestMigrateLegacyHistory_MovesFileAndStampsMarker(t *testing.T) {
	fp := setupUserData(t)
	projectDir := t.TempDir()
	legacyDir := filepath.Join(projectDir, ".ralph")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "run-history.json")
	legacyBody := `{"runs":[{"prd":"legacy","date":"2026-01-01T00:00:00Z","stories_total":1,"stories_completed":1,"stories_failed":0,"total_cost":0,"duration_minutes":1,"total_iterations":1,"avg_iterations_per_story":1,"stuck_count":0,"judge_rejection_rate":0}]}`
	if err := os.WriteFile(legacy, []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyHistory(projectDir, fp); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Legacy file must still be in place (rollback affordance).
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("legacy file should remain in place: %v", err)
	}
	// Marker must be stamped.
	marker := legacy + ".migrated"
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker not written: %v", err)
	}
	// User-level file must now exist and contain the legacy body.
	loaded, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(loaded.Runs) != 1 || loaded.Runs[0].PRD != "legacy" {
		t.Errorf("user-level content = %#v", loaded.Runs)
	}
}

func TestMigrateLegacyHistory_BothExistPrefersUserLevel(t *testing.T) {
	fp := setupUserData(t)
	projectDir := t.TempDir()
	legacyDir := filepath.Join(projectDir, ".ralph")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "run-history.json")
	if err := os.WriteFile(legacy, []byte(`{"runs":[{"prd":"legacy","date":""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-populate user-level with different content.
	if err := SaveHistory(fp, RunHistory{Runs: []RunSummary{{PRD: "user-level"}}}); err != nil {
		t.Fatalf("SaveHistory: %v", err)
	}

	if err := MigrateLegacyHistory(projectDir, fp); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Marker MUST NOT be stamped — we didn't migrate.
	if _, err := os.Stat(legacy + ".migrated"); err == nil {
		t.Error("marker must not be stamped when user-level already exists")
	}
	h, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(h.Runs) != 1 || h.Runs[0].PRD != "user-level" {
		t.Errorf("user-level content must win: %#v", h.Runs)
	}
}

func TestMigrateLegacyHistory_NoLegacyIsNoOp(t *testing.T) {
	fp := setupUserData(t)
	projectDir := t.TempDir()

	if err := MigrateLegacyHistory(projectDir, fp); err != nil {
		t.Fatalf("migrate with no legacy: %v", err)
	}
	// User-level should still be absent (empty history).
	h, err := LoadHistory(fp)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(h.Runs) != 0 {
		t.Errorf("expected no runs, got %d", len(h.Runs))
	}
}

func TestLoadAllHistory_AggregatesAcrossRepos(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	fpA := testFP("repo-a")
	fpB := testFP("repo-b")

	if err := SaveHistory(fpA, RunHistory{Runs: []RunSummary{{PRD: "a1", Kind: "daemon"}, {PRD: "a2", Kind: "retro"}}}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := SaveHistory(fpB, RunHistory{Runs: []RunSummary{{PRD: "b1", Kind: "daemon"}}}); err != nil {
		t.Fatalf("save b: %v", err)
	}

	all, err := LoadAllHistory()
	if err != nil {
		t.Fatalf("LoadAllHistory: %v", err)
	}
	if len(all.Runs) != 3 {
		t.Fatalf("expected 3 runs, got %d: %#v", len(all.Runs), all.Runs)
	}
}

func TestLoadAllHistory_NoReposDir(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	all, err := LoadAllHistory()
	if err != nil {
		t.Fatalf("LoadAllHistory with no repos dir: %v", err)
	}
	if len(all.Runs) != 0 {
		t.Errorf("expected empty, got %#v", all.Runs)
	}
}

func containsByte(data []byte, b byte) bool {
	for _, c := range data {
		if c == b {
			return true
		}
	}
	return false
}
