package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// LogFunc is a callback for logging setup progress messages.
type LogFunc func(msg string)

// Function variables for testability.
var lookPathFunc = exec.LookPath
var envReadyFunc = envReady

// EnsureChromaDB ensures that a Python environment with chromadb installed
// exists under dataDir. It uses conda if available, falling back to python3 venv.
// Returns the path to the python executable in the environment.
func EnsureChromaDB(dataDir string, logf LogFunc) (string, error) {
	if logf == nil {
		logf = func(string) {}
	}

	condaEnvDir := filepath.Join(dataDir, "conda-env")
	venvDir := filepath.Join(dataDir, "venv")

	// Check if conda env already exists with chromadb installed.
	if pythonPath, ok := envReadyFunc(condaEnvDir); ok {
		logf("chromadb already installed in conda env, skipping setup")
		return pythonPath, nil
	}

	// Check if venv already exists with chromadb installed.
	if pythonPath, ok := envReadyFunc(venvDir); ok {
		logf("chromadb already installed in venv, skipping setup")
		return pythonPath, nil
	}

	// Try conda first.
	condaPath, err := lookPathFunc("conda")
	if err == nil {
		return setupConda(condaPath, condaEnvDir, logf)
	}

	// Fall back to python3 venv.
	python3Path, err := lookPathFunc("python3")
	if err != nil {
		return "", fmt.Errorf("neither conda nor python3 found: cannot set up chromadb environment")
	}

	return setupVenv(python3Path, venvDir, logf)
}

// pythonInEnv returns the path to the python executable inside an environment directory.
func pythonInEnv(envDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(envDir, "Scripts", "python.exe")
	}
	return filepath.Join(envDir, "bin", "python")
}

// envReady checks if the environment directory exists and has chromadb installed.
func envReady(envDir string) (string, bool) {
	pythonPath := pythonInEnv(envDir)
	if _, err := os.Stat(pythonPath); err != nil {
		return "", false
	}
	cmd := exec.Command(pythonPath, "-c", "import chromadb")
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return pythonPath, true
}

// setupConda creates a conda environment and installs chromadb.
func setupConda(condaPath, envDir string, logf LogFunc) (string, error) {
	if _, err := os.Stat(envDir); os.IsNotExist(err) {
		logf("creating conda environment at " + envDir)
		cmd := exec.Command(condaPath, "create", "-p", envDir, "python=3.11", "-y")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to create conda environment: %w\n%s", err, out)
		}
	}

	logf("installing chromadb in conda environment")
	cmd := exec.Command(condaPath, "run", "-p", envDir, "pip", "install", "chromadb")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to install chromadb in conda environment: %w\n%s", err, out)
	}

	pythonPath := pythonInEnv(envDir)
	if _, err := os.Stat(pythonPath); err != nil {
		return "", fmt.Errorf("python not found in conda environment at %s: %w", pythonPath, err)
	}

	return pythonPath, nil
}

// setupVenv creates a python3 venv and installs chromadb.
func setupVenv(python3Path, venvDir string, logf LogFunc) (string, error) {
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		logf("creating python venv at " + venvDir)
		cmd := exec.Command(python3Path, "-m", "venv", venvDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to create venv: %w\n%s", err, out)
		}
	}

	pythonPath := pythonInEnv(venvDir)
	logf("installing chromadb in venv")
	pipPath := filepath.Join(filepath.Dir(pythonPath), "pip")
	cmd := exec.Command(pipPath, "install", "chromadb")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to install chromadb in venv: %w\n%s", err, out)
	}

	return pythonPath, nil
}
