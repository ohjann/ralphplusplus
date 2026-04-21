package viewer_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/userdata"
)

func TestHandleSpawnRetro_NotFound(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())
	_, h := newTestServer(t)

	rr := doPost(t, h, "/api/spawn/retro/deadbeefdead", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%q", rr.Code, rr.Body.String())
	}
}

// TestHandleSpawnRetro_Conflict seeds a retro manifest with Status=running
// under the real repo fingerprint so SpawnRetro's guard fires before the
// exec path. Using a real temp dir + userdata.Fingerprint matches what the
// handler computes internally.
func TestHandleSpawnRetro_Conflict(t *testing.T) {
	t.Setenv("RALPH_DATA_DIR", t.TempDir())

	repoPath := t.TempDir()
	fp, err := userdata.Fingerprint(repoPath)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	reposDir, _ := userdata.ReposDir()
	repoDir := filepath.Join(reposDir, fp)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	meta := history.RepoMeta{
		Path:      repoPath,
		Name:      filepath.Base(repoPath),
		FirstSeen: time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
		RunCount:  0,
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(repoDir, "meta.json"), metaData, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	// Seed a retro manifest that's already running for this repo fp.
	const existingRunID = "run-existing-retro"
	runDir := filepath.Join(repoDir, "runs", existingRunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	manifest := history.Manifest{
		SchemaVersion: history.ManifestSchemaVersion,
		RunID:         existingRunID,
		Kind:          history.KindRetro,
		RepoFP:        fp,
		RepoPath:      repoPath,
		Status:        history.StatusRunning,
		StartTime:     time.Now().UTC(),
	}
	manData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manData, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, h := newTestServer(t)

	rr := doPost(t, h, "/api/spawn/retro/"+fp, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409; body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, rr.Body.String())
	}
	if body["error"] != "retro_already_running" {
		t.Errorf("error=%q want retro_already_running", body["error"])
	}
	if body["runId"] != existingRunID {
		t.Errorf("runId=%q want %q", body["runId"], existingRunID)
	}
}
