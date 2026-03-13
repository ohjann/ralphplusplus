package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eoghanhynes/ralph/internal/costs"
)

// Checkpoint represents the saved state of a ralph run for resume capability.
type Checkpoint struct {
	PRDHash          string                 `json:"prd_hash"`
	Phase            string                 `json:"phase"`
	CompletedStories []string               `json:"completed_stories"`
	FailedStories    map[string]FailedStory `json:"failed_stories"`
	InProgress       []string               `json:"in_progress"`
	DAG              map[string][]string    `json:"dag"`
	IterationCount   int                    `json:"iteration_count"`
	Timestamp        time.Time              `json:"timestamp"`
	CostData         *costs.CostSnapshot    `json:"cost_data,omitempty"`
}

// FailedStory tracks retry information for a failed story.
type FailedStory struct {
	Retries   int    `json:"retries"`
	LastError string `json:"last_error"`
}

func checkpointPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", "checkpoint.json")
}

// Save writes a checkpoint to .ralph/checkpoint.json.
func Save(projectDir string, cp Checkpoint) error {
	path := checkpointPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating checkpoint dir: %w", err)
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling checkpoint: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Load reads a checkpoint from .ralph/checkpoint.json.
// Returns the checkpoint, whether the file exists, and any error.
func Load(projectDir string) (Checkpoint, bool, error) {
	path := checkpointPath(projectDir)
	data, err := os.ReadFile(path)
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

// Delete removes .ralph/checkpoint.json. No error if file doesn't exist.
func Delete(projectDir string) error {
	path := checkpointPath(projectDir)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting checkpoint: %w", err)
	}
	return nil
}

// ComputePRDHash returns the SHA-256 hex digest of a file's contents.
func ComputePRDHash(prdFilePath string) (string, error) {
	data, err := os.ReadFile(prdFilePath)
	if err != nil {
		return "", fmt.Errorf("reading prd for hash: %w", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
