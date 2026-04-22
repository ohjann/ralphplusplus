package quality

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/config"
)

// TestRunPipeline_NoChanges verifies the early-return path: when
// GetDiffManifest cannot produce a diff (tmpdir is not a git/jj repo),
// RunPipeline returns a zero Assessment and nil error without calling
// Claude or looping.
func TestRunPipeline_NoChanges(t *testing.T) {
	tmp := t.TempDir()
	prdPath := filepath.Join(tmp, "prd.json")
	if err := os.WriteFile(prdPath, []byte(`{"project":"t","user_stories":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		ProjectDir:      tmp,
		LogDir:          filepath.Join(tmp, ".ralph", "logs"),
		PRDFile:         prdPath,
		QualityMaxIters: 3,
		QualityWorkers:  2,
	}

	var logs []string
	ass, err := RunPipeline(context.Background(), cfg, PipelineOpts{
		Log: func(s string) { logs = append(logs, s) },
	})
	if err != nil {
		t.Fatalf("RunPipeline err: %v", err)
	}
	if ass.TotalFindings() != 0 {
		t.Fatalf("expected zero findings, got %d", ass.TotalFindings())
	}
	if len(logs) == 0 || logs[0] != "no changes to review" {
		t.Fatalf("expected 'no changes to review' log; got %v", logs)
	}
}
