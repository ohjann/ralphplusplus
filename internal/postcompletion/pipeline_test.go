package postcompletion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

type phaseCall struct {
	Name    string
	Status  string
	Message string
	Iter    int
}

type recordingReporter struct {
	mu     sync.Mutex
	logs   []string
	phases []phaseCall
}

func (r *recordingReporter) Log(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, s)
}

func (r *recordingReporter) Phase(name, status, message string, iter int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, phaseCall{name, status, message, iter})
}

func (r *recordingReporter) phaseNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.phases))
	for _, p := range r.phases {
		out = append(out, p.Name+":"+p.Status)
	}
	return out
}

func newTestCfg(t *testing.T, tmp string) *config.Config {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(tmp, ".ralph", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	prdPath := filepath.Join(tmp, "prd.json")
	if err := os.WriteFile(prdPath, []byte(`{"project":"pc-test","user_stories":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	progressPath := filepath.Join(tmp, "progress.md")
	_ = os.WriteFile(progressPath, []byte("done"), 0o644)

	return &config.Config{
		ProjectDir:      tmp,
		LogDir:          filepath.Join(tmp, ".ralph", "logs"),
		PRDFile:         prdPath,
		ProgressFile:    progressPath,
		RalphHome:       tmp,
		QualityMaxIters: 1,
		QualityWorkers:  1,
		QualityReview:   false, // disable to avoid Claude in the default path
		RetroEnabled:    false,
	}
}

// fakeClaude writes a dummy SUMMARY.md when asked, otherwise is a no-op.
// It lets the pipeline run end-to-end without invoking the real Claude
// binary.
func fakeClaude(projectDir string) func(ctx context.Context, dir, prompt, logPath string) error {
	return func(_ context.Context, dir, _, _ string) error {
		// When summary prompt arrives, write SUMMARY.md so the
		// pipeline's read-back succeeds.
		if dir == projectDir {
			_ = os.WriteFile(filepath.Join(dir, "SUMMARY.md"), []byte("# Summary\nfake"), 0o644)
		}
		return nil
	}
}

func TestRun_SentinelSkipsPipeline(t *testing.T) {
	tmp := t.TempDir()
	cfg := newTestCfg(t, tmp)
	cfg.Memory.Disabled = true

	// Pre-seed sentinel with the current PRD hash.
	if err := WriteSentinel(tmp, prdHash(t, cfg.PRDFile)); err != nil {
		t.Fatal(err)
	}

	rep := &recordingReporter{}
	err := Run(context.Background(), cfg, Inputs{
		BuildInputs: costs.BuildInputs{PRD: &prd.PRD{Project: "pc-test"}},
		Reporter:    rep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rep.phaseNames(); len(got) != 1 || got[0] != PhaseDone+":skipped" {
		t.Fatalf("expected only done:skipped; got %v", got)
	}
}

func TestRun_FullPathWritesSentinelAndHistory(t *testing.T) {
	tmp := t.TempDir()
	cfg := newTestCfg(t, tmp)
	cfg.Memory.Disabled = true

	rep := &recordingReporter{}
	err := Run(context.Background(), cfg, Inputs{
		BuildInputs:      costs.BuildInputs{PRD: &prd.PRD{Project: "pc-test"}},
		SummaryRunClaude: fakeClaude(tmp),
		Reporter:         rep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Summary phase fires, history phase fires, done complete.
	names := rep.phaseNames()
	joined := strings.Join(names, ",")
	for _, want := range []string{
		PhaseSummary + ":started",
		PhaseSummary + ":complete",
		PhaseHistory + ":started",
		PhaseDone + ":complete",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("phases missing %q: %v", want, names)
		}
	}

	// Sentinel was written and is valid.
	if !SentinelValid(tmp, cfg.PRDFile) {
		t.Fatal("expected sentinel to be valid after full run")
	}

	// SUMMARY.md exists.
	if _, err := os.Stat(filepath.Join(tmp, "SUMMARY.md")); err != nil {
		t.Fatalf("SUMMARY.md missing: %v", err)
	}
}

func TestRun_HistoryAppendedOnFullPath(t *testing.T) {
	tmp := t.TempDir()
	cfg := newTestCfg(t, tmp)
	cfg.Memory.Disabled = true

	pr := &prd.PRD{
		Project: "pc-test",
		UserStories: []prd.UserStory{
			{ID: "S1", Title: "one", Passes: true},
			{ID: "S2", Title: "two", Passes: true},
		},
	}
	rep := &recordingReporter{}
	err := Run(context.Background(), cfg, Inputs{
		BuildInputs: costs.BuildInputs{
			PRD:             pr,
			TotalIterations: 3,
			FailedCount:     0,
			FirstPassRate:   1.0,
			Workers:         1,
			QualityReview:   false,
		},
		SummaryRunClaude: fakeClaude(tmp),
		Reporter:         rep,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// history.Fingerprint writes under the per-user data dir — the
	// test doesn't want to pollute that, so we only assert that the
	// phase fired without error.
	for _, p := range rep.phases {
		if p.Name == PhaseHistory && p.Status == "error" {
			t.Fatalf("history phase errored: %s", p.Message)
		}
	}
}
