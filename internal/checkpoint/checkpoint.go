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

// FailedStory records retry count and last error for a failed story.
type FailedStory struct {
	Retries   int    `json:"retries"`
	LastError string `json:"last_error"`
}

// Checkpoint represents the persisted state of a ralph run for resume support.
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

// Save writes the checkpoint to .ralph/checkpoint.json with JSON indentation.
func Save(projectDir string, cp Checkpoint) error {
	dir := filepath.Join(projectDir, ".ralph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating .ralph directory: %w", err)
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}
	data = append(data, '\n')

	return os.WriteFile(checkpointPath(projectDir), data, 0o644)
}

// Load reads and parses .ralph/checkpoint.json. The bool indicates if the file
// exists. Returns (zero, false, nil) when the file doesn't exist.
func Load(projectDir string) (Checkpoint, bool, error) {
	data, err := os.ReadFile(checkpointPath(projectDir))
	if os.IsNotExist(err) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("reading checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, false, fmt.Errorf("parsing checkpoint: %w", err)
	}
	return cp, true, nil
}

// Delete removes .ralph/checkpoint.json. Returns no error if the file doesn't exist.
func Delete(projectDir string) error {
	err := os.Remove(checkpointPath(projectDir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ComputePRDHash returns the SHA-256 hex string of the file contents at prdFilePath.
func ComputePRDHash(prdFilePath string) (string, error) {
	data, err := os.ReadFile(prdFilePath)
	if err != nil {
		return "", fmt.Errorf("reading prd file: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
