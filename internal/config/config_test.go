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
