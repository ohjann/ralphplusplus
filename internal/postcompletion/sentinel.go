// Package postcompletion orchestrates the work the daemon runs once
// all stories in a PRD have completed: quality review, memory
// synthesis, dream consolidation, checkpoint cleanup, SUMMARY.md
// generation, retrospective, and run-history persistence.
//
// It exposes Run, which is invoked by the daemon's checkCompletion in
// a goroutine, and a sentinel file (.ralph/post-run-complete.json)
// that survives restarts and short-circuits re-execution for the same
// PRD hash.
package postcompletion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/ohjann/ralphplusplus/internal/checkpoint"
)

// sentinelFileName is the post-run-complete marker written under
// <projectDir>/.ralph/ after the orchestrator finishes all steps.
const sentinelFileName = "post-run-complete.json"

// Sentinel is the on-disk post-run-complete marker.
type Sentinel struct {
	PRDHash    string    `json:"prd_hash"`
	FinishedAt time.Time `json:"finished_at"`
}

func sentinelPath(projectDir string) string {
	return filepath.Join(projectDir, ".ralph", sentinelFileName)
}

// ReadSentinel returns the current sentinel, or (zero, false, nil) if
// it is absent. A read or parse error is returned so callers can
// distinguish missing from corrupted.
func ReadSentinel(projectDir string) (Sentinel, bool, error) {
	data, err := os.ReadFile(sentinelPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Sentinel{}, false, nil
		}
		return Sentinel{}, false, err
	}
	var s Sentinel
	if err := json.Unmarshal(data, &s); err != nil {
		return Sentinel{}, false, err
	}
	return s, true, nil
}

// WriteSentinel records post-run completion for the current PRD. The
// parent directory is assumed to exist (it is created on daemon
// startup alongside other .ralph/ files).
func WriteSentinel(projectDir, prdHash string) error {
	data, err := json.MarshalIndent(Sentinel{
		PRDHash:    prdHash,
		FinishedAt: time.Now(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sentinelPath(projectDir), data, 0o644)
}

// SentinelValid reports whether a sentinel exists for projectDir AND
// its recorded PRDHash matches the current hash of prdFile. A missing
// sentinel, an unreadable PRD, or a mismatched hash all return false,
// so the caller should run the post-run pipeline.
func SentinelValid(projectDir, prdFile string) bool {
	s, ok, err := ReadSentinel(projectDir)
	if err != nil || !ok {
		return false
	}
	current, err := checkpoint.ComputePRDHash(prdFile)
	if err != nil {
		return false
	}
	return s.PRDHash == current
}
