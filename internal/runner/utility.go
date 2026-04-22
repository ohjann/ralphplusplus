package runner

import (
	"context"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/memory"
)

// UtilityClaude returns a RunClaudeFunc suitable for utility tasks
// (memory synthesis, dream consolidation, post-run summary, retro).
// It wraps RunClaudeForIteration with StoryID/Role "_utility"/"utility"
// and the cheap model from cfg.UtilityModel, drawing a fresh iteration
// number from cfg.UtilityIter each call.
func UtilityClaude(cfg *config.Config) memory.RunClaudeFunc {
	return func(ctx context.Context, projectDir, prompt, logFilePath string) error {
		iter := int(cfg.UtilityIter.Add(1))
		_, err := RunClaudeForIteration(ctx, cfg, projectDir, prompt, logFilePath, IterationOpts{
			StoryID: "_utility",
			Role:    "utility",
			Iter:    iter,
			Model:   cfg.UtilityModel,
		})
		return err
	}
}
