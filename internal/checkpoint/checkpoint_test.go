package checkpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveCreatesValidJSON(t *testing.T) {
	dir := t.TempDir()
	cp := Checkpoint{
		PRDHash:          "abc123",
		Phase:            "serial",
		CompletedStories: []string{"S-001"},
		FailedStories:    map[string]FailedStory{"S-002": {Retries: 1, LastError: "fail"}},
		InProgress:       []string{"S-003"},
		DAG:              map[string][]string{"S-003": {"S-001"}},
		IterationCount:   5,
		Timestamp:        time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC),
	}

	if err := Save(dir, cp); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".ralph", "checkpoint.json"))
	if err != nil {
		t.Fatalf("checkpoint.json not created: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"prd_hash", "phase", "completed_stories", "failed_stories", "in_progress", "dag", "iteration_count", "timestamp"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected JSON key: %s", key)
		}
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 11, 14, 30, 0, 0, time.UTC)
	original := Checkpoint{
		PRDHash:          "deadbeef1234",
		Phase:            "parallel",
		CompletedStories: []string{"S-001", "S-002", "S-003"},
		FailedStories: map[string]FailedStory{
			"S-004": {Retries: 2, LastError: "compile error"},
			"S-005": {Retries: 1, LastError: "test failure"},
		},
		InProgress:     []string{"S-006", "S-007"},
		DAG:            map[string][]string{"S-006": {"S-001", "S-002"}, "S-007": {"S-003"}},
		IterationCount: 12,
		Timestamp:      now,
	}

	if err := Save(dir, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !exists {
		t.Fatal("Load() reported file does not exist after Save()")
	}

	if loaded.PRDHash != original.PRDHash {
		t.Errorf("PRDHash: got %q, want %q", loaded.PRDHash, original.PRDHash)
	}
	if loaded.Phase != original.Phase {
		t.Errorf("Phase: got %q, want %q", loaded.Phase, original.Phase)
	}
	if loaded.IterationCount != original.IterationCount {
		t.Errorf("IterationCount: got %d, want %d", loaded.IterationCount, original.IterationCount)
	}
	if !loaded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", loaded.Timestamp, original.Timestamp)
	}

	if len(loaded.CompletedStories) != len(original.CompletedStories) {
		t.Fatalf("CompletedStories length: got %d, want %d", len(loaded.CompletedStories), len(original.CompletedStories))
	}
	for i, s := range loaded.CompletedStories {
		if s != original.CompletedStories[i] {
			t.Errorf("CompletedStories[%d]: got %q, want %q", i, s, original.CompletedStories[i])
		}
	}

	if len(loaded.InProgress) != len(original.InProgress) {
		t.Fatalf("InProgress length: got %d, want %d", len(loaded.InProgress), len(original.InProgress))
	}
	for i, s := range loaded.InProgress {
		if s != original.InProgress[i] {
			t.Errorf("InProgress[%d]: got %q, want %q", i, s, original.InProgress[i])
		}
	}

	if len(loaded.FailedStories) != len(original.FailedStories) {
		t.Fatalf("FailedStories length: got %d, want %d", len(loaded.FailedStories), len(original.FailedStories))
	}
	for id, fs := range original.FailedStories {
		lfs, ok := loaded.FailedStories[id]
		if !ok {
			t.Errorf("FailedStories missing key %q", id)
			continue
		}
		if lfs.Retries != fs.Retries || lfs.LastError != fs.LastError {
			t.Errorf("FailedStories[%q]: got %+v, want %+v", id, lfs, fs)
		}
	}

	if len(loaded.DAG) != len(original.DAG) {
		t.Fatalf("DAG length: got %d, want %d", len(loaded.DAG), len(original.DAG))
	}
	for id, deps := range original.DAG {
		ldeps, ok := loaded.DAG[id]
		if !ok {
			t.Errorf("DAG missing key %q", id)
			continue
		}
		if len(ldeps) != len(deps) {
			t.Errorf("DAG[%q] length: got %d, want %d", id, len(ldeps), len(deps))
			continue
		}
		for i, d := range deps {
			if ldeps[i] != d {
				t.Errorf("DAG[%q][%d]: got %q, want %q", id, i, ldeps[i], d)
			}
		}
	}
}

func TestLoadNonExistent(t *testing.T) {
	dir := t.TempDir()
	cp, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() should not error for missing file: %v", err)
	}
	if exists {
		t.Error("Load() should return false for missing file")
	}
	if cp.PRDHash != "" || cp.Phase != "" || cp.IterationCount != 0 ||
		len(cp.CompletedStories) != 0 || len(cp.FailedStories) != 0 ||
		len(cp.InProgress) != 0 || len(cp.DAG) != 0 || !cp.Timestamp.IsZero() {
		t.Errorf("expected zero-value Checkpoint, got %+v", cp)
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	cp := Checkpoint{PRDHash: "test", Phase: "serial"}
	if err := Save(dir, cp); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if err := Delete(dir); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	_, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() after Delete() error: %v", err)
	}
	if exists {
		t.Error("file should not exist after Delete()")
	}
}

func TestDeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	if err := Delete(dir); err != nil {
		t.Fatalf("Delete() should not error for missing file: %v", err)
	}
}

func TestComputePRDHashConsistent(t *testing.T) {
	dir := t.TempDir()
	prdFile := filepath.Join(dir, "prd.json")
	if err := os.WriteFile(prdFile, []byte(`{"project":"test","stories":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	hash1, err := ComputePRDHash(prdFile)
	if err != nil {
		t.Fatalf("ComputePRDHash() error: %v", err)
	}
	hash2, err := ComputePRDHash(prdFile)
	if err != nil {
		t.Fatalf("ComputePRDHash() second call error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes should be consistent: got %q and %q", hash1, hash2)
	}
	if len(hash1) != 64 {
		t.Errorf("SHA-256 hex should be 64 chars, got %d", len(hash1))
	}
}

func TestComputePRDHashDifferentContents(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "prd1.json")
	file2 := filepath.Join(dir, "prd2.json")
	if err := os.WriteFile(file1, []byte(`{"project":"alpha"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(`{"project":"beta"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	hash1, err := ComputePRDHash(file1)
	if err != nil {
		t.Fatalf("ComputePRDHash(file1) error: %v", err)
	}
	hash2, err := ComputePRDHash(file2)
	if err != nil {
		t.Fatalf("ComputePRDHash(file2) error: %v", err)
	}

	if hash1 == hash2 {
		t.Error("different file contents should produce different hashes")
	}
}

func TestCheckpointWithPopulatedMaps(t *testing.T) {
	dir := t.TempDir()
	cp := Checkpoint{
		PRDHash:          "hash123",
		Phase:            "parallel",
		CompletedStories: []string{"P1-001", "P1-002"},
		FailedStories: map[string]FailedStory{
			"P1-003": {Retries: 3, LastError: "context exhausted"},
			"P1-004": {Retries: 1, LastError: "build failed"},
			"P1-005": {Retries: 2, LastError: "test timeout"},
		},
		InProgress: []string{"P1-006"},
		DAG: map[string][]string{
			"P1-001": {},
			"P1-002": {"P1-001"},
			"P1-003": {"P1-001", "P1-002"},
			"P1-004": {"P1-002"},
			"P1-005": {"P1-003"},
			"P1-006": {"P1-004", "P1-005"},
		},
		IterationCount: 25,
		Timestamp:      time.Date(2026, 3, 11, 18, 0, 0, 0, time.UTC),
	}

	if err := Save(dir, cp); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, exists, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !exists {
		t.Fatal("expected checkpoint to exist")
	}

	if len(loaded.FailedStories) != 3 {
		t.Errorf("FailedStories: got %d entries, want 3", len(loaded.FailedStories))
	}
	if loaded.FailedStories["P1-003"].Retries != 3 {
		t.Errorf("P1-003 retries: got %d, want 3", loaded.FailedStories["P1-003"].Retries)
	}
	if loaded.FailedStories["P1-005"].LastError != "test timeout" {
		t.Errorf("P1-005 last_error: got %q, want %q", loaded.FailedStories["P1-005"].LastError, "test timeout")
	}

	if len(loaded.DAG) != 6 {
		t.Errorf("DAG: got %d entries, want 6", len(loaded.DAG))
	}
	if len(loaded.DAG["P1-001"]) != 0 {
		t.Errorf("DAG[P1-001]: got %d deps, want 0", len(loaded.DAG["P1-001"]))
	}
	if len(loaded.DAG["P1-006"]) != 2 {
		t.Errorf("DAG[P1-006]: got %d deps, want 2", len(loaded.DAG["P1-006"]))
	}
}
