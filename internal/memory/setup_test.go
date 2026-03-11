package memory

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// saveFuncs saves the current function variables and returns a restore function.
func saveSetupFuncs() func() {
	origLookPath := lookPathFunc
	origEnvReady := envReadyFunc
	return func() {
		lookPathFunc = origLookPath
		envReadyFunc = origEnvReady
	}
}

func TestEnsureChromaDB_ExistingCondaEnv(t *testing.T) {
	defer saveSetupFuncs()()

	var logMessages []string
	logf := func(msg string) { logMessages = append(logMessages, msg) }

	envReadyFunc = func(envDir string) (string, bool) {
		if strings.HasSuffix(envDir, "conda-env") {
			return filepath.Join(envDir, "bin", "python"), true
		}
		return "", false
	}

	result, err := EnsureChromaDB("/tmp/test-data", logf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join("/tmp/test-data", "conda-env", "bin", "python")
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}

	if len(logMessages) != 1 || !strings.Contains(logMessages[0], "conda env") {
		t.Errorf("expected log about conda env skip, got %v", logMessages)
	}
}

func TestEnsureChromaDB_ExistingVenv(t *testing.T) {
	defer saveSetupFuncs()()

	var logMessages []string
	logf := func(msg string) { logMessages = append(logMessages, msg) }

	envReadyFunc = func(envDir string) (string, bool) {
		if strings.HasSuffix(envDir, "venv") {
			pyName := "python"
			dir := "bin"
			if runtime.GOOS == "windows" {
				pyName = "python.exe"
				dir = "Scripts"
			}
			return filepath.Join(envDir, dir, pyName), true
		}
		return "", false
	}

	result, err := EnsureChromaDB("/tmp/test-data", logf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "venv") {
		t.Errorf("expected venv path, got %q", result)
	}

	if len(logMessages) != 1 || !strings.Contains(logMessages[0], "venv") {
		t.Errorf("expected log about venv skip, got %v", logMessages)
	}
}

func TestEnsureChromaDB_NoPythonAvailable(t *testing.T) {
	defer saveSetupFuncs()()

	// No environments exist.
	envReadyFunc = func(envDir string) (string, bool) {
		return "", false
	}

	// Neither conda nor python3 found.
	lookPathFunc = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	_, err := EnsureChromaDB("/tmp/test-data", nil)
	if err == nil {
		t.Fatal("expected error when no python/conda available")
	}

	if !strings.Contains(err.Error(), "neither conda nor python3") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestEnsureChromaDB_CondaEnvCheckedBeforeVenv(t *testing.T) {
	defer saveSetupFuncs()()

	var checks []string
	envReadyFunc = func(envDir string) (string, bool) {
		if strings.HasSuffix(envDir, "conda-env") {
			checks = append(checks, "conda-env")
		} else if strings.HasSuffix(envDir, "venv") {
			checks = append(checks, "venv")
		}
		return "", false
	}

	lookPathFunc = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	// Will fail because no python available, but we can verify check order.
	_, _ = EnsureChromaDB("/tmp/test-data", nil)

	if len(checks) != 2 || checks[0] != "conda-env" || checks[1] != "venv" {
		t.Errorf("expected conda-env checked before venv, got %v", checks)
	}
}
