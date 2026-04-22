package postcompletion

import (
	"context"
	"fmt"

	"github.com/ohjann/ralphplusplus/internal/checkpoint"
	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/quality"
	"github.com/ohjann/ralphplusplus/internal/retro"
	"github.com/ohjann/ralphplusplus/internal/summary"
)

// Reporter receives progress notifications as the pipeline advances.
// Implementations on the daemon side bridge to SSE broadcasts; tests
// use a recording stub.
type Reporter interface {
	// Log emits a free-form line (appears in daemon logs and viewer).
	Log(string)
	// Phase emits a typed phase transition. name is one of the phase
	// constants (PhaseQualityReview, PhaseSummary, ...). status is one
	// of "started", "complete", "error", "skipped". iter is the
	// quality-loop iteration number or 0.
	Phase(name, status, message string, iter int)
}

// Phase names emitted via Reporter.Phase.
const (
	PhaseQualityReview = "quality_review"
	PhaseSynthesis     = "synthesis"
	PhaseDream         = "dream"
	PhaseSummary       = "summary"
	PhaseRetro         = "retro"
	PhaseHistory       = "history"
	PhaseDone          = "done"
)

// Inputs carries the per-run data the orchestrator needs but cannot
// derive from cfg alone.
type Inputs struct {
	// BuildInputs populates the RunSummary written to run-history.json.
	// Must be populated by the caller (daemon or TUI-serial) from its
	// own source of truth. The PRD inside is also used for memory
	// synthesis.
	BuildInputs costs.BuildInputs

	// UtilityRunClaude drives memory synthesis and dream
	// consolidation. In production, pass runner.UtilityClaude(cfg).
	// Tests can swap in a recording stub.
	UtilityRunClaude memory.RunClaudeFunc

	// SummaryRunClaude, if non-nil, overrides summary.Generate's
	// default Claude invocation. Leave nil in production to keep the
	// "_summary/summary" history attribution; tests set a stub.
	SummaryRunClaude memory.RunClaudeFunc

	// RetroRunClaude, if non-nil, overrides retro.RunRetrospective's
	// default Claude invocation. Leave nil in production.
	RetroRunClaude memory.RunClaudeFunc

	// Reporter receives phase/log events. Nil is accepted; events are
	// dropped silently.
	Reporter Reporter
}

// Run executes the post-completion pipeline exactly once for the
// current PRD. It short-circuits via the on-disk sentinel when the
// pipeline has already completed for this PRD hash.
//
// All steps are best-effort: step errors are reported via Reporter and
// logged, but they do not abort subsequent steps — this matches the
// TUI's behaviour where synthesis/dream errors are non-fatal. The
// run-history append and sentinel write are performed last, so a
// partial-completion restart will retry from step 1.
func Run(ctx context.Context, cfg *config.Config, in Inputs) error {
	rep := in.Reporter
	if rep == nil {
		rep = noopReporter{}
	}

	if SentinelValid(cfg.ProjectDir, cfg.PRDFile) {
		rep.Phase(PhaseDone, "skipped", "post-run already complete for this PRD", 0)
		return nil
	}

	// 1. Quality review + fix loop.
	if cfg.QualityReview {
		rep.Phase(PhaseQualityReview, "started", "", 0)
		_, err := quality.RunPipeline(ctx, cfg, quality.PipelineOpts{
			Log: func(s string) { rep.Log("quality: " + s) },
		})
		rep.Phase(PhaseQualityReview, statusOf(err), errMsg(err), 0)
	}

	// 2. Memory synthesis.
	if !cfg.Memory.Disabled && in.UtilityRunClaude != nil {
		rep.Phase(PhaseSynthesis, "started", "", 0)
		err := memory.SynthesizeRun(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, in.BuildInputs.PRD, in.UtilityRunClaude)
		rep.Phase(PhaseSynthesis, statusOf(err), errMsg(err), 0)
	}

	// 3. Increment run counter and (maybe) run dream consolidation.
	if !cfg.Memory.Disabled && in.UtilityRunClaude != nil {
		if _, err := memory.IncrementRunCount(cfg.ProjectDir); err != nil {
			debuglog.Log("postcompletion: increment run count: %v", err)
		}
		if memory.ShouldDream(cfg.ProjectDir, cfg.Memory.DreamEveryNRuns) {
			rep.Phase(PhaseDream, "started", "", 0)
			err := memory.RunDream(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, cfg.Memory.MaxEntries, cfg.Memory.DreamEveryNRuns, in.UtilityRunClaude)
			rep.Phase(PhaseDream, statusOf(err), errMsg(err), 0)
		}
	}

	// 4. Checkpoint cleanup on clean completion.
	_ = checkpoint.Delete(cfg.ProjectDir)

	// 5. SUMMARY.md.
	rep.Phase(PhaseSummary, "started", "", 0)
	var sumErr error
	if in.SummaryRunClaude != nil {
		_, sumErr = summary.GenerateWith(ctx, cfg, in.SummaryRunClaude)
	} else {
		_, sumErr = summary.Generate(ctx, cfg)
	}
	rep.Phase(PhaseSummary, statusOf(sumErr), errMsg(sumErr), 0)

	// 6. Retrospective.
	if cfg.RetroEnabled {
		rep.Phase(PhaseRetro, "started", "", 0)
		var err error
		if in.RetroRunClaude != nil {
			_, err = retro.RunRetrospectiveWith(ctx, cfg, cfg.ProjectDir, cfg.LogDir, cfg.PRDFile, in.RetroRunClaude)
		} else {
			_, err = retro.RunRetrospective(ctx, cfg, cfg.ProjectDir, cfg.LogDir, cfg.PRDFile, cfg.UtilityModel)
		}
		rep.Phase(PhaseRetro, statusOf(err), errMsg(err), 0)
	}

	// 7. Run-history persist (always last so partial failures above do
	//    not leave a half-baked summary in run-history.json).
	rep.Phase(PhaseHistory, "started", "", 0)
	if err := persistHistory(cfg, in.BuildInputs); err != nil {
		rep.Phase(PhaseHistory, "error", err.Error(), 0)
	} else {
		rep.Phase(PhaseHistory, "complete", "", 0)
	}

	// 8. Sentinel — records "this PRD's post-run is done" so daemon
	//    restarts with the same PRD do not re-fire.
	if hash, err := checkpoint.ComputePRDHash(cfg.PRDFile); err == nil {
		if err := WriteSentinel(cfg.ProjectDir, hash); err != nil {
			debuglog.Log("postcompletion: write sentinel: %v", err)
		}
	}

	rep.Phase(PhaseDone, "complete", "", 0)
	return nil
}

// persistHistory builds a RunSummary from the supplied inputs and
// appends it to the per-repo run-history.json. The Kind defaults to
// KindDaemon if the caller did not set one.
func persistHistory(cfg *config.Config, in costs.BuildInputs) error {
	if in.Kind == "" {
		in.Kind = history.KindDaemon
	}
	summary := costs.BuildRunSummary(in)
	fp, err := history.Fingerprint(cfg.ProjectDir)
	if err != nil {
		return fmt.Errorf("fingerprint: %w", err)
	}
	return costs.AppendRun(fp, summary)
}

func statusOf(err error) string {
	if err != nil {
		return "error"
	}
	return "complete"
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type noopReporter struct{}

func (noopReporter) Log(string)                         {}
func (noopReporter) Phase(string, string, string, int) {}
