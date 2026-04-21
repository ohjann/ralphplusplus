package viewer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// writeManifest seeds <RALPH_DATA_DIR>/repos/<fp>/runs/<runID>/manifest.json
// with the given Kind + Status so the retro helpers have something to
// observe. Kept in the internal test package so the unexported helpers
// can be exercised directly.
func writeManifest(t *testing.T, fp, runID, kind, status string) {
	t.Helper()
	reposDir, err := userdata.ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	runDir := filepath.Join(reposDir, fp, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	m := history.Manifest{
		SchemaVersion: history.ManifestSchemaVersion,
		RunID:         runID,
		Kind:          kind,
		RepoFP:        fp,
		Status:        status,
		StartTime:     time.Now().UTC(),
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestIsRetroRunning_NoManifests(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	id, ok, err := isRetroRunning("missing-fp")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if ok || id != "" {
		t.Fatalf("want (\"\", false); got (%q, %v)", id, ok)
	}
}

func TestIsRetroRunning_OnlyDaemon(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	writeManifest(t, fp, "run-1", history.KindDaemon, history.StatusRunning)
	id, ok, err := isRetroRunning(fp)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if ok {
		t.Fatalf("a daemon run must not satisfy the retro guard; got id=%q", id)
	}
}

func TestIsRetroRunning_RetroComplete(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	writeManifest(t, fp, "run-1", history.KindRetro, history.StatusComplete)
	_, ok, err := isRetroRunning(fp)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if ok {
		t.Fatal("completed retro must not satisfy the running guard")
	}
}

func TestIsRetroRunning_RetroRunning(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	const runID = "run-2"
	writeManifest(t, fp, runID, history.KindRetro, history.StatusRunning)
	id, ok, err := isRetroRunning(fp)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !ok || id != runID {
		t.Fatalf("want (%q, true); got (%q, %v)", runID, id, ok)
	}
}

func TestWaitForNewRetroRun_Timeout(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	id := waitForNewRetroRun(context.Background(), fp, map[string]struct{}{}, 150*time.Millisecond)
	if id != "" {
		t.Fatalf("want empty id on timeout, got %q", id)
	}
}

func TestWaitForNewRetroRun_SkipsKnown(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	writeManifest(t, fp, "run-old", history.KindRetro, history.StatusComplete)
	known := map[string]struct{}{"run-old": {}}

	done := make(chan string, 1)
	go func() {
		done <- waitForNewRetroRun(context.Background(), fp, known, 2*time.Second)
	}()
	time.Sleep(120 * time.Millisecond)
	writeManifest(t, fp, "run-new", history.KindRetro, history.StatusRunning)

	select {
	case got := <-done:
		if got != "run-new" {
			t.Fatalf("want run-new, got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForNewRetroRun did not return in time")
	}
}

func TestWaitForNewRetroRun_IgnoresNonRetro(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	writeManifest(t, fp, "run-daemon", history.KindDaemon, history.StatusRunning)
	id := waitForNewRetroRun(context.Background(), fp, map[string]struct{}{}, 150*time.Millisecond)
	if id != "" {
		t.Fatalf("daemon manifest must not satisfy retro poll; got %q", id)
	}
}

func TestRetroRunIDSet(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	const fp = "feedfacecafe"
	writeManifest(t, fp, "run-a", history.KindRetro, history.StatusComplete)
	writeManifest(t, fp, "run-b", history.KindDaemon, history.StatusRunning)
	writeManifest(t, fp, "run-c", history.KindRetro, history.StatusRunning)

	got := retroRunIDSet(fp)
	if _, ok := got["run-a"]; !ok {
		t.Error("retro run-a missing from set")
	}
	if _, ok := got["run-c"]; !ok {
		t.Error("retro run-c missing from set")
	}
	if _, ok := got["run-b"]; ok {
		t.Error("daemon run-b should not appear in retro set")
	}
}
