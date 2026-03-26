package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidate_NoPRDWhenFileAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		ProjectDir: dir,
		PRDFile:    filepath.Join(dir, "prd.json"),
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	if !cfg.NoPRD {
		t.Error("expected NoPRD=true when prd.json does not exist")
	}
}

func TestValidate_NoPRDFalseWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	prdFile := filepath.Join(dir, "prd.json")
	if err := os.WriteFile(prdFile, []byte(`{"project":"test","userStories":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		ProjectDir: dir,
		PRDFile:    prdFile,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	if cfg.NoPRD {
		t.Error("expected NoPRD=false when prd.json exists")
	}
}

func TestValidate_IdleModeSkipsValidation(t *testing.T) {
	cfg := Config{
		IdleMode: true,
		PRDFile:  "/nonexistent/prd.json",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error in idle mode: %v", err)
	}

	// NoPRD should remain false since validation was skipped
	if cfg.NoPRD {
		t.Error("expected NoPRD=false in idle mode (validation skipped)")
	}
}

func TestParse_ModelFlags(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantModel        string
		wantArchitect    string
		wantImplementer  string
	}{
		{
			name:      "no model flags",
			args:      []string{"--idle"},
			wantModel: "",
		},
		{
			name:      "--model sets ModelOverride",
			args:      []string{"--idle", "--model", "haiku"},
			wantModel: "haiku",
		},
		{
			name:          "--architect-model sets ArchitectModel",
			args:          []string{"--idle", "--architect-model", "opus"},
			wantArchitect: "opus",
		},
		{
			name:            "--implementer-model sets ImplementerModel",
			args:            []string{"--idle", "--implementer-model", "sonnet"},
			wantImplementer: "sonnet",
		},
		{
			name:            "all model flags together",
			args:            []string{"--idle", "--model", "haiku", "--architect-model", "opus", "--implementer-model", "sonnet"},
			wantModel:       "haiku",
			wantArchitect:   "opus",
			wantImplementer: "sonnet",
		},
		{
			name:      "--model=value form",
			args:      []string{"--idle", "--model=opus"},
			wantModel: "opus",
		},
		{
			name:          "--architect-model=value form",
			args:          []string{"--idle", "--architect-model=haiku"},
			wantArchitect: "haiku",
		},
		{
			name:            "--implementer-model=value form",
			args:            []string{"--idle", "--implementer-model=opus"},
			wantImplementer: "opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.args)
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			if cfg.ModelOverride != tt.wantModel {
				t.Errorf("ModelOverride = %q, want %q", cfg.ModelOverride, tt.wantModel)
			}
			if cfg.ArchitectModel != tt.wantArchitect {
				t.Errorf("ArchitectModel = %q, want %q", cfg.ArchitectModel, tt.wantArchitect)
			}
			if cfg.ImplementerModel != tt.wantImplementer {
				t.Errorf("ImplementerModel = %q, want %q", cfg.ImplementerModel, tt.wantImplementer)
			}
		})
	}
}

func TestParse_ModelFlagsMissingValue(t *testing.T) {
	flags := []string{"--model", "--architect-model", "--implementer-model"}
	for _, flag := range flags {
		t.Run(flag, func(t *testing.T) {
			_, err := Parse([]string{flag})
			if err == nil {
				t.Errorf("expected error for %s without value", flag)
			}
		})
	}
}

func TestParse_TomlModelOverride(t *testing.T) {
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlContent := `model_override = "haiku"
architect_model = "opus"
implementer_model = "sonnet"
`
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--dir", dir, "--idle"})
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if cfg.ModelOverride != "haiku" {
		t.Errorf("ModelOverride = %q, want %q", cfg.ModelOverride, "haiku")
	}
	if cfg.ArchitectModel != "opus" {
		t.Errorf("ArchitectModel = %q, want %q", cfg.ArchitectModel, "opus")
	}
	if cfg.ImplementerModel != "sonnet" {
		t.Errorf("ImplementerModel = %q, want %q", cfg.ImplementerModel, "sonnet")
	}
}

func TestParse_CLIOverridesToml(t *testing.T) {
	dir := t.TempDir()
	ralphDir := filepath.Join(dir, ".ralph")
	if err := os.MkdirAll(ralphDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlContent := `model_override = "haiku"
`
	if err := os.WriteFile(filepath.Join(ralphDir, "config.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--dir", dir, "--idle", "--model", "opus"})
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	// CLI flag should override TOML value
	if cfg.ModelOverride != "opus" {
		t.Errorf("ModelOverride = %q, want %q (CLI should override TOML)", cfg.ModelOverride, "opus")
	}
}
