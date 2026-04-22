package quality

import (
	"context"
	"fmt"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
)

// PipelineOpts configures RunPipeline.
type PipelineOpts struct {
	// Log is an optional per-step progress callback. Receives short
	// status strings ("review iteration 1", "3 critical, 5 warning",
	// "all clean", "max iterations reached"). Nil disables logging.
	Log func(string)
}

// RunPipeline runs the review → fix → re-review loop up to
// cfg.QualityMaxIters times, returning the final assessment. It is the
// non-interactive counterpart to the TUI's phaseQualityReview/phaseQualityFix
// message loop: at max iterations it returns the last assessment
// instead of prompting the user, and errors from individual steps are
// logged + wrapped rather than surfaced as a phase.
//
// Returns a zero Assessment (and nil error) when GetDiffManifest finds
// no changes — this matches the TUI's "no changes to review" path.
func RunPipeline(ctx context.Context, cfg *config.Config, opts PipelineOpts) (Assessment, error) {
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}

	manifest, err := GetDiffManifest(ctx, cfg.ProjectDir, cfg.PRDFile)
	if err != nil || manifest == "" {
		debuglog.Log("quality pipeline: no changes to review: %v", err)
		log("no changes to review")
		return Assessment{}, nil
	}

	lenses := DefaultLenses()
	var last Assessment

	for iter := 1; iter <= cfg.QualityMaxIters; iter++ {
		log(fmt.Sprintf("review iteration %d", iter))

		results := RunReviewsParallel(ctx, cfg, cfg.ProjectDir, cfg.LogDir, lenses, manifest, iter, cfg.QualityWorkers)
		last = MergeAssessment(results, iter)
		_ = WriteAssessment(cfg.ProjectDir, last)

		if last.TotalFindings() == 0 {
			if last.HasParseFailures() {
				log("no findings parsed (some lenses failed)")
			} else {
				log("all clean")
			}
			return last, nil
		}

		critical, warning, info := last.CountBySeverity()
		log(fmt.Sprintf("iteration %d: %d critical, %d warning, %d info", iter, critical, warning, info))

		if dropped := FilterStaleFindings(cfg.ProjectDir, &last); dropped > 0 {
			debuglog.Log("quality pipeline: dropped %d stale findings", dropped)
		}

		if err := RunFix(ctx, cfg, cfg.ProjectDir, cfg.LogDir, last, iter); err != nil {
			debuglog.Log("quality pipeline: fix iteration %d failed: %v", iter, err)
			log(fmt.Sprintf("fix iteration %d error: %v", iter, err))
			return last, fmt.Errorf("fix iteration %d: %w", iter, err)
		}
	}

	log(fmt.Sprintf("max iterations (%d) reached; findings remain", cfg.QualityMaxIters))
	return last, nil
}
