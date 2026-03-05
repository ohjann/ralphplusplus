package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Create creates a jj workspace for the given story, branched from current @.
// Returns the workspace directory path.
func Create(ctx context.Context, projectDir, storyID, baseDir string) (string, error) {
	wsDir := filepath.Join(baseDir, storyID)

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", fmt.Errorf("creating workspace base dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "jj", "workspace", "add", wsDir, "-r", "@")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("jj workspace add: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return wsDir, nil
}

// Destroy forgets a workspace and removes its directory.
func Destroy(ctx context.Context, projectDir, wsName, wsDir string) error {
	// Forget the workspace in the main repo
	cmd := exec.CommandContext(ctx, "jj", "workspace", "forget", wsName)
	cmd.Dir = projectDir
	_ = cmd.Run() // best-effort

	// Remove the directory
	return os.RemoveAll(wsDir)
}

// MergeBack rebases the workspace's committed change onto main's current @.
func MergeBack(ctx context.Context, mainDir, changeID string) error {
	cmd := exec.CommandContext(ctx, "jj", "rebase", "-s", changeID, "-d", "@")
	cmd.Dir = mainDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("jj rebase: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CommitWorkspace commits the working copy in the workspace and returns the change ID.
func CommitWorkspace(ctx context.Context, wsDir, storyID, title string) (string, error) {
	msg := fmt.Sprintf("story %s: %s", storyID, title)
	commitCmd := exec.CommandContext(ctx, "jj", "commit", "-m", msg)
	commitCmd.Dir = wsDir
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("jj commit: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Get the change ID of the parent (what we just committed)
	logCmd := exec.CommandContext(ctx, "jj", "log", "-r", "@-", "--no-graph", "-T", "change_id")
	logCmd.Dir = wsDir
	idOut, err := logCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("jj log: %s: %w", strings.TrimSpace(string(idOut)), err)
	}

	return strings.TrimSpace(string(idOut)), nil
}

// CopyState copies prd.json, progress.txt, and .ralph/ state into the workspace.
func CopyState(mainDir, wsDir string) error {
	// Copy prd.json
	if err := copyFile(
		filepath.Join(mainDir, "prd.json"),
		filepath.Join(wsDir, "prd.json"),
	); err != nil {
		return err
	}

	// Copy progress.txt
	_ = copyFile(
		filepath.Join(mainDir, "progress.txt"),
		filepath.Join(wsDir, "progress.txt"),
	)

	// Copy .ralph/ directory
	srcRalph := filepath.Join(mainDir, ".ralph")
	dstRalph := filepath.Join(wsDir, ".ralph")
	if _, err := os.Stat(srcRalph); err == nil {
		if err := copyDir(srcRalph, dstRalph); err != nil {
			return fmt.Errorf("copying .ralph: %w", err)
		}
	}

	return nil
}

// WorkspaceName returns the jj workspace name for a story ID.
// jj uses the last path component as the workspace name.
func WorkspaceName(storyID string) string {
	return storyID
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		// Skip large log files
		if info.Size() > 10*1024*1024 {
			return nil
		}

		return copyFile(path, target)
	})
}
