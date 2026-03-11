package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FailedStory records retry information for a failed story.
type FailedStory struct {
	Retries   int    `json:"retries"`
	LastError string `json:"last_error"`
}

// Checkpoint represents the persisted state of a run for resume capability.
type Checkpoint struct {
	PRDHash          string                 `json:"prd_hash"`
	Phase            string                 `json:"phase"`
	CompletedStories []string               `json:"completed_stories"`
	FailedStories    map[string]FailedStory `json:"failed_stories"`
	InProgress       []string               `json:"in_progress"`
	DAG              map[string][]string    `json:"dag"`
	IterationCount   int                    `json:"iteration_count"`
	Timestamp        time.Time              `json:"timestamp"`
}

func checkpointPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", "checkpoint.json")
}

// Save writes the checkpoint to .ralph/checkpoint.json.
func Save(projectDir string, cp Checkpoint) error {
	cp.Timestamp = time.Now()
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(checkpointPath(projectDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating checkpoint dir: %w", err)
	}

	return os.WriteFile(checkpointPath(projectDir), data, 0o644)
}

// Load reads the checkpoint from .ralph/checkpoint.json.
// Returns the checkpoint, whether the file exists, and any error.
func Load(projectDir string) (Checkpoint, bool, error) {
	data, err := os.ReadFile(checkpointPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Checkpoint{}, false, nil
		}
		return Checkpoint{}, false, fmt.Errorf("reading checkpoint: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, false, fmt.Errorf("parsing checkpoint: %w", err)
	}
	return cp, true, nil
}

// Delete removes the checkpoint file. No error if the file doesn't exist.
func Delete(projectDir string) error {
	err := os.Remove(checkpointPath(projectDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting checkpoint: %w", err)
	}
	return nil
}

// ComputePRDHash returns the SHA-256 hex string of the file contents.
func ComputePRDHash(prdFilePath string) (string, error) {
	data, err := os.ReadFile(prdFilePath)
	if err != nil {
		return "", fmt.Errorf("reading prd for hash: %w", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
