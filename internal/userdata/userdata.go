// Package userdata resolves the per-user data directory for Ralph.
//
// Layout under Dir():
//
//	repos/<fp>/meta.json   per-repo metadata (see internal/history)
//
// Resolution order: $RALPH_DATA_DIR wins. Otherwise platform defaults apply:
// macOS uses ~/Library/Application Support/ralph, Linux uses
// $XDG_DATA_HOME/ralph (default ~/.local/share/ralph).
package userdata

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Fingerprint computes sha256(EvalSymlinks(absPath))[:12] — a stable 12-char
// hex key for a repo path. Lives here (not internal/history) so that lower
// packages like costs can key their files without importing history.
// Symlink evaluation errors fall back to the absolute path so a path that
// was just removed still produces a stable key.
func Fingerprint(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("abs path: %w", err)
	}
	canonical := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		canonical = resolved
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:12], nil
}

// Dir returns the root user data directory for Ralph.
func Dir() (string, error) {
	if v := os.Getenv("RALPH_DATA_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ralph"), nil
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return filepath.Join(v, "ralph"), nil
		}
		return filepath.Join(home, ".local", "share", "ralph"), nil
	}
}

// ReposDir returns <Dir>/repos.
func ReposDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "repos"), nil
}

// RepoDir returns <Dir>/repos/<fp>.
func RepoDir(fp string) (string, error) {
	d, err := ReposDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, fp), nil
}

// EnsureDirs is an os.MkdirAll helper that creates path with 0o755 perms.
func EnsureDirs(path string) error {
	return os.MkdirAll(path, 0o755)
}
