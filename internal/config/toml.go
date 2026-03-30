package config

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/eoghanhynes/ralph/internal/debuglog"
)

// tomlConfig mirrors the tunable subset of Config.
// Pointer fields distinguish "not set in file" (nil) from "set to zero value".
type tomlConfig struct {
	JudgeEnabled       *bool    `toml:"judge_enabled"`
	JudgeMaxRejections *int    `toml:"judge_max_rejections"`
	Workers            *int    `toml:"workers"`
	QualityReview      *bool   `toml:"quality_review"`
	QualityWorkers     *int    `toml:"quality_workers"`
	QualityMaxIters    *int    `toml:"quality_max_iterations"`
	MemoryDisable      *bool   `toml:"memory_disable"`
	NoArchitect        *bool   `toml:"no_architect"`
	SpriteEnabled      *bool   `toml:"sprite_enabled"`
	WorkspaceBase      *string `toml:"workspace_base"`
	ModelOverride      *string `toml:"model_override"`
	ArchitectModel     *string `toml:"architect_model"`
	ImplementerModel   *string `toml:"implementer_model"`
	NoSimplify         *bool   `toml:"no_simplify"`
	NoFusion           *bool   `toml:"no_fusion"`
	FusionWorkers      *int    `toml:"fusion_workers"`
}

// loadTomlConfig reads .ralph/config.toml from the given project directory.
// Returns nil, nil if the file does not exist.
func loadTomlConfig(projectDir string) (*tomlConfig, error) {
	path := filepath.Join(projectDir, ".ralph", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tc tomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return nil, err
	}

	debuglog.Log("config.toml: loaded from %s", path)
	return &tc, nil
}

// applyTo overwrites non-nil fields onto the given Config.
func (tc *tomlConfig) applyTo(cfg *Config) {
	if tc == nil {
		return
	}
	if tc.JudgeEnabled != nil {
		cfg.JudgeEnabled = *tc.JudgeEnabled
	}
	if tc.JudgeMaxRejections != nil {
		cfg.JudgeMaxRejections = *tc.JudgeMaxRejections
	}
	if tc.Workers != nil {
		cfg.Workers = *tc.Workers
	}
	if tc.QualityReview != nil {
		cfg.QualityReview = *tc.QualityReview
	}
	if tc.QualityWorkers != nil {
		cfg.QualityWorkers = *tc.QualityWorkers
	}
	if tc.QualityMaxIters != nil {
		cfg.QualityMaxIters = *tc.QualityMaxIters
	}
	if tc.MemoryDisable != nil {
		cfg.Memory.Disabled = *tc.MemoryDisable
	}
	if tc.NoArchitect != nil {
		cfg.NoArchitect = *tc.NoArchitect
	}
	if tc.SpriteEnabled != nil {
		cfg.SpriteEnabled = *tc.SpriteEnabled
	}
	if tc.WorkspaceBase != nil {
		cfg.WorkspaceBase = *tc.WorkspaceBase
	}
	if tc.ModelOverride != nil {
		cfg.ModelOverride = *tc.ModelOverride
	}
	if tc.ArchitectModel != nil {
		cfg.ArchitectModel = *tc.ArchitectModel
	}
	if tc.ImplementerModel != nil {
		cfg.ImplementerModel = *tc.ImplementerModel
	}
	if tc.NoSimplify != nil {
		cfg.NoSimplify = *tc.NoSimplify
	}
	if tc.NoFusion != nil {
		cfg.NoFusion = *tc.NoFusion
	}
	if tc.FusionWorkers != nil {
		cfg.FusionWorkers = *tc.FusionWorkers
	}
}

// SaveConfig writes the current tunable settings to .ralph/config.toml.
func (cfg *Config) SaveConfig() error {
	tc := tomlConfig{
		JudgeEnabled:       boolPtr(cfg.JudgeEnabled),
		JudgeMaxRejections: intPtr(cfg.JudgeMaxRejections),
		Workers:            intPtr(cfg.Workers),
		QualityReview:      boolPtr(cfg.QualityReview),
		QualityWorkers:     intPtr(cfg.QualityWorkers),
		QualityMaxIters:    intPtr(cfg.QualityMaxIters),
		MemoryDisable:      boolPtr(cfg.Memory.Disabled),
		NoArchitect:        boolPtr(cfg.NoArchitect),
		SpriteEnabled:      boolPtr(cfg.SpriteEnabled),
		WorkspaceBase:      stringPtr(cfg.WorkspaceBase),
		ModelOverride:      stringPtr(cfg.ModelOverride),
		ArchitectModel:     stringPtr(cfg.ArchitectModel),
		ImplementerModel:   stringPtr(cfg.ImplementerModel),
		NoSimplify:         boolPtr(cfg.NoSimplify),
		NoFusion:           boolPtr(cfg.NoFusion),
		FusionWorkers:      intPtr(cfg.FusionWorkers),
	}

	dir := filepath.Join(cfg.ProjectDir, ".ralph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("# Ralph configuration — saved settings across runs.\n")
	buf.WriteString("# CLI flags override these values.\n\n")
	if err := toml.NewEncoder(&buf).Encode(tc); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "config.toml"), buf.Bytes(), 0o644)
}

func boolPtr(v bool) *bool       { return &v }
func intPtr(v int) *int           { return &v }
func stringPtr(v string) *string  { return &v }
