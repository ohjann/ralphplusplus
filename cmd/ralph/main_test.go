package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/history"
)

// newTUIEnv sets up a fake userdata dir and a project dir with a prd.json so
// openAdHocRun can open a real ad-hoc history run.
func newTUIEnv(t *testing.T) (*config.Config, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dataDir)
	proj := t.TempDir()
	prdPath := filepath.Join(proj, "prd.json")
	if err := os.WriteFile(prdPath, []byte(`{"project":"x"}`), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	cfg := &config.Config{ProjectDir: proj, PRDFile: prdPath}
	return cfg, dataDir
}

// TestOpenAdHocRun_CapturesSyntheticClarifyIteration drives the TUI history
// entry path: opens an ad-hoc run the way runTUI does, feeds it a synthetic
// clarify iteration (as RunClaudeForIteration would when cfg.HistoryRun is
// set), finalizes on simulated normal exit, and verifies the manifest lands
// under <userdata>/repos/<fp>/runs/ with Kind="ad-hoc" and a non-empty
// Stories slice.
func TestOpenAdHocRun_CapturesSyntheticClarifyIteration(t *testing.T) {
	cfg, dataDir := newTUIEnv(t)

	hr := openAdHocRun(cfg, "test-ver")
	if hr == nil {
		t.Fatal("openAdHocRun returned nil on a writable userdata dir")
	}
	if cfg.HistoryRun == nil {
		t.Fatal("cfg.HistoryRun was not assigned")
	}
	if cfg.HistoryRun != hr {
		t.Fatal("cfg.HistoryRun does not match returned run")
	}

	// Mirror what runner.RunClaudeForIteration does for a clarify call when
	// cfg.HistoryRun is non-nil: open an iteration writer, write a prompt,
	// finish. This is the "fake RunClaude" — we bypass the real claude
	// binary but exercise the same history plumbing.
	iw, err := cfg.HistoryRun.StartIteration("STORY-XYZ", "clarify", 1, "")
	if err != nil {
		t.Fatalf("StartIteration: %v", err)
	}
	if err := iw.WritePrompt("what HTTP method?"); err != nil {
		t.Fatalf("WritePrompt: %v", err)
	}
	if err := iw.Finish("sess-1", nil, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Simulate normal TUI exit.
	if err := hr.Finalize(history.StatusComplete, hr.ComputeTotals(), nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Verify the manifest landed at <userdata>/repos/<fp>/runs/<runID>/manifest.json.
	fp := hr.RepoFP()
	runsDir := filepath.Join(dataDir, "repos", fp, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 run dir under %s, got %d", runsDir, len(entries))
	}

	manifestPath := filepath.Join(runsDir, entries[0].Name(), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m history.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if m.Kind != history.KindAdHoc {
		t.Errorf("manifest Kind = %q, want %q", m.Kind, history.KindAdHoc)
	}
	if m.Status != history.StatusComplete {
		t.Errorf("manifest Status = %q, want %q", m.Status, history.StatusComplete)
	}
	if len(m.Stories) == 0 {
		t.Fatal("manifest Stories is empty; want clarify iteration captured")
	}
	var found bool
	for _, s := range m.Stories {
		if s.StoryID == "STORY-XYZ" && len(s.Iterations) == 1 {
			found = true
			if s.Iterations[0].Role != "clarify" {
				t.Errorf("iteration role = %q, want clarify", s.Iterations[0].Role)
			}
		}
	}
	if !found {
		t.Errorf("STORY-XYZ clarify iteration not recorded: stories=%#v", m.Stories)
	}

	// The turn files exist under turns/STORY-XYZ.
	turnDir := filepath.Join(hr.Dir(), "turns", "STORY-XYZ")
	if _, err := os.Stat(filepath.Join(turnDir, "clarify-iter-1.prompt")); err != nil {
		t.Errorf("prompt file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(turnDir, "clarify-iter-1.meta.json")); err != nil {
		t.Errorf("iter meta file missing: %v", err)
	}
}

// TestOpenAdHocRun_UnwritableUserdataIsNonFatal verifies that when OpenRun
// fails (because the userdata dir cannot be created), openAdHocRun returns
// nil and leaves cfg.HistoryRun untouched — preserving the today's
// transparent-no-op behaviour in RunClaudeForIteration.
func TestOpenAdHocRun_UnwritableUserdataIsNonFatal(t *testing.T) {
	// Stage an unwritable "userdata dir" by pointing RALPH_DATA_DIR at a
	// subpath of a regular file. os.MkdirAll cannot create a directory
	// under a non-directory, so OpenRun's TouchRepo/EnsureDirs will fail.
	parent := t.TempDir()
	blocker := filepath.Join(parent, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	t.Setenv("RALPH_DATA_DIR", filepath.Join(blocker, "ralph"))

	proj := t.TempDir()
	prdPath := filepath.Join(proj, "prd.json")
	if err := os.WriteFile(prdPath, []byte(`{"project":"x"}`), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	cfg := &config.Config{ProjectDir: proj, PRDFile: prdPath}

	hr := openAdHocRun(cfg, "test-ver")
	if hr != nil {
		t.Fatal("openAdHocRun should return nil when OpenRun fails")
	}
	if cfg.HistoryRun != nil {
		t.Errorf("cfg.HistoryRun should remain nil after OpenRun failure, got %#v", cfg.HistoryRun)
	}
}
