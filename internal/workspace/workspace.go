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

// CreateResult holds the output of workspace creation.
type CreateResult struct {
	Dir          string // workspace directory path
	BaseChangeID string // change ID of the commit the workspace branched from
}

// Create creates a jj workspace for the given story, branched from current @-.
// If a workspace with the same name already exists (e.g. from a previous failed
// attempt), it is forgotten and cleaned up before creating a fresh one.
func Create(ctx context.Context, projectDir, storyID, baseDir string) (CreateResult, error) {
	wsDir := filepath.Join(baseDir, storyID)
	wsName := WorkspaceName(storyID)

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return CreateResult{}, fmt.Errorf("creating workspace base dir: %w", err)
	}

	// Capture the base change ID (@- is the parent of the working copy,
	// i.e. the last real commit) before creating the workspace.
	baseCmd := exec.CommandContext(ctx, "jj", "log", "-r", "@-", "--no-graph", "-T", "change_id")
	baseCmd.Dir = projectDir
	baseOut, err := baseCmd.CombinedOutput()
	if err != nil {
		return CreateResult{}, fmt.Errorf("jj log base: %s: %w", strings.TrimSpace(string(baseOut)), err)
	}
	baseChangeID := strings.TrimSpace(string(baseOut))

	// Use @- so the workspace branches from the last real commit,
	// not the empty working-copy change.
	cmd := exec.CommandContext(ctx, "jj", "workspace", "add", wsDir, "-r", "@-")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		// If the workspace already exists (stale from a previous failed attempt),
		// clean it up and retry.
		if strings.Contains(outStr, "already exists") || strings.Contains(outStr, "is not an empty directory") {
			// Forget the jj workspace (best-effort — may already be partially cleaned up)
			forgetCmd := exec.CommandContext(ctx, "jj", "workspace", "forget", wsName)
			forgetCmd.Dir = projectDir
			_ = forgetCmd.Run()

			// Remove the leftover directory
			_ = os.RemoveAll(wsDir)

			// Retry workspace creation
			retryCmd := exec.CommandContext(ctx, "jj", "workspace", "add", wsDir, "-r", "@-")
			retryCmd.Dir = projectDir
			retryOut, retryErr := retryCmd.CombinedOutput()
			if retryErr != nil {
				return CreateResult{}, fmt.Errorf("jj workspace add (retry after cleanup): %s: %w", strings.TrimSpace(string(retryOut)), retryErr)
			}
		} else {
			return CreateResult{}, fmt.Errorf("jj workspace add: %s: %w", outStr, err)
		}
	}

	return CreateResult{Dir: wsDir, BaseChangeID: baseChangeID}, nil
}

// Destroy runs the teardown hook (if any), forgets the workspace, and removes its directory.
func Destroy(ctx context.Context, projectDir, wsName, wsDir string) error {
	// Run project-specific teardown (.ralph/workspace-teardown.sh) before cleanup.
	RunTeardown(ctx, wsDir)

	// Forget the workspace — this also cleans up the workspace's working-copy commit.
	cmd := exec.CommandContext(ctx, "jj", "workspace", "forget", wsName)
	cmd.Dir = projectDir
	_ = cmd.Run() // best-effort

	// Remove the directory
	return os.RemoveAll(wsDir)
}

// RunTeardown runs .ralph/workspace-teardown.sh if it exists. Best-effort — errors are ignored.
func RunTeardown(ctx context.Context, wsDir string) {
	teardownScript := filepath.Join(wsDir, ".ralph", "workspace-teardown.sh")
	if _, err := os.Stat(teardownScript); os.IsNotExist(err) {
		return
	}
	cmd := exec.CommandContext(ctx, "bash", teardownScript)
	cmd.Dir = wsDir
	cmd.Env = append(os.Environ(), "WORKSPACE_DIR="+wsDir)
	_ = cmd.Run() // best-effort
}

// AbandonChange removes a change from the jj graph. Use this to clean up
// commits from failed or non-passing workers that will never be merged back,
// preventing orphaned side branches in the history.
func AbandonChange(ctx context.Context, projectDir, changeID string) error {
	if changeID == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "jj", "abandon", changeID)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("jj abandon %s: %s: %w", changeID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// MergeResult describes the outcome of a MergeBack operation.
type MergeResult struct {
	HasConflict     bool
	ConflictedFiles string // newline-separated list of conflicted files
}

// MergeBack rebases the workspace's committed change onto main's current @-
// (the parent of the working copy), then advances @ on top so subsequent
// merges form a linear chain without empty intermediate commits.
//
// Uses -r (not -s) so only the single squashed commit is moved — the
// workspace's empty working-copy child is left behind and cleaned up by
// workspace forget.
//
// If the rebase produces conflicts, it switches to editing the conflicted
// commit (jj edit) and returns HasConflict=true so the caller can resolve
// before advancing @.
func MergeBack(ctx context.Context, mainDir, changeID string) (MergeResult, error) {
	// Sync main workspace's operation log — workspace commits create sibling
	// operations that must be integrated before we can rebase here.
	syncCmd := exec.CommandContext(ctx, "jj", "workspace", "update-stale")
	syncCmd.Dir = mainDir
	_ = syncCmd.Run() // best-effort; no-op if already up to date

	cmd := exec.CommandContext(ctx, "jj", "rebase", "-r", changeID, "-d", "@-")
	cmd.Dir = mainDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return MergeResult{}, fmt.Errorf("jj rebase: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Check if the rebased commit has conflicts.
	conflictCmd := exec.CommandContext(ctx, "jj", "log", "-r", changeID, "--no-graph", "-T", "conflict")
	conflictCmd.Dir = mainDir
	conflictOut, _ := conflictCmd.CombinedOutput()
	if strings.TrimSpace(string(conflictOut)) == "true" {
		// Switch to editing the conflicted commit directly so Claude
		// can resolve the conflict markers in the working copy.
		editCmd := exec.CommandContext(ctx, "jj", "edit", changeID)
		editCmd.Dir = mainDir
		if eOut, eErr := editCmd.CombinedOutput(); eErr != nil {
			return MergeResult{}, fmt.Errorf("jj edit conflicted: %s: %w", strings.TrimSpace(string(eOut)), eErr)
		}

		// Get the list of conflicted files for the resolution prompt.
		listCmd := exec.CommandContext(ctx, "jj", "resolve", "--list")
		listCmd.Dir = mainDir
		listOut, _ := listCmd.CombinedOutput()

		return MergeResult{
			HasConflict:     true,
			ConflictedFiles: strings.TrimSpace(string(listOut)),
		}, nil
	}

	// No conflicts — advance @ to sit on top of the rebased change.
	cmd = exec.CommandContext(ctx, "jj", "new", changeID)
	cmd.Dir = mainDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		return MergeResult{}, fmt.Errorf("jj new after rebase: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return MergeResult{}, nil
}

// AdvanceAfterResolve creates a new @ on top of the current commit after
// conflict resolution. Call this after resolving conflicts in a jj edit session.
func AdvanceAfterResolve(ctx context.Context, mainDir string) error {
	cmd := exec.CommandContext(ctx, "jj", "new")
	cmd.Dir = mainDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("jj new after resolve: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CommitWorkspace commits the working copy in the workspace, squashes all
// intermediate commits into a single commit, and returns the change ID.
func CommitWorkspace(ctx context.Context, wsDir, storyID, title, baseChangeID string) (string, error) {
	msg := fmt.Sprintf("story %s: %s", storyID, title)

	// Commit any remaining working-copy changes.
	commitCmd := exec.CommandContext(ctx, "jj", "commit", "-m", msg)
	commitCmd.Dir = wsDir
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("jj commit: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Squash all intermediate commits (between base and @-) into a single commit.
	// After `jj commit`, the history is: base → C1 → C2 → ... → final(@-) → empty(@)
	// This moves changes from C1..C(n-1) into final, and abandoned empty sources.
	squashRevset := fmt.Sprintf("%s::@- ~ %s ~ @-", baseChangeID, baseChangeID)
	squashCmd := exec.CommandContext(ctx, "jj", "squash", "--from", squashRevset, "--into", "@-", "-m", msg)
	squashCmd.Dir = wsDir
	squashOut, err := squashCmd.CombinedOutput()
	if err != nil {
		// Squash can fail if there's only one commit (nothing to squash) — that's fine.
		outStr := strings.TrimSpace(string(squashOut))
		if !strings.Contains(outStr, "nothing to do") && !strings.Contains(outStr, "No matching revisions") {
			return "", fmt.Errorf("jj squash: %s: %w", outStr, err)
		}
	}

	// Get the change ID of the squashed commit (now @-)
	logCmd := exec.CommandContext(ctx, "jj", "log", "-r", "@-", "--no-graph", "-T", "change_id")
	logCmd.Dir = wsDir
	idOut, err := logCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("jj log: %s: %w", strings.TrimSpace(string(idOut)), err)
	}

	return strings.TrimSpace(string(idOut)), nil
}

// SetupResult holds the outcome of RunSetup.
type SetupResult struct {
	Warning string // non-empty if setup was skipped (e.g. no script found)
	Ran     bool   // true if the setup script was executed
}

// RunSetup runs .ralph/workspace-setup.sh in the workspace directory if it exists.
// This allows projects to define setup steps (e.g. copying node_modules, .env files,
// generating clients) that run after workspace creation. If the script does not exist,
// a warning is returned and execution continues normally.
func RunSetup(ctx context.Context, wsDir string) (SetupResult, error) {
	setupScript := filepath.Join(wsDir, ".ralph", "workspace-setup.sh")
	if _, err := os.Stat(setupScript); os.IsNotExist(err) {
		return SetupResult{Warning: "no .ralph/workspace-setup.sh found, skipping project setup"}, nil
	}

	cmd := exec.CommandContext(ctx, "bash", setupScript)
	cmd.Dir = wsDir
	cmd.Env = append(os.Environ(),
		"WORKSPACE_DIR="+wsDir,
		"RALPH_WORKSPACE=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return SetupResult{}, fmt.Errorf("workspace-setup.sh failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return SetupResult{Ran: true}, nil
}

// CopyState copies prd.json, progress.md, and .ralph/ state into the workspace.
// When storyID is non-empty, only .ralph/stories/{storyID}/ is copied (not other
// stories' state) to avoid leaking unrelated state between parallel workers.
func CopyState(mainDir, wsDir, storyID string) error {
	// Copy prd.json
	if err := copyFile(
		filepath.Join(mainDir, "prd.json"),
		filepath.Join(wsDir, "prd.json"),
	); err != nil {
		return err
	}

	// Copy progress.md
	_ = copyFile(
		filepath.Join(mainDir, "progress.md"),
		filepath.Join(wsDir, "progress.md"),
	)

	// Copy .ralph/ directory, but only the relevant story's state
	srcRalph := filepath.Join(mainDir, ".ralph")
	dstRalph := filepath.Join(wsDir, ".ralph")
	if _, err := os.Stat(srcRalph); err == nil {
		if err := copyDirSelective(srcRalph, dstRalph, storyID); err != nil {
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

// copyDirSelective copies src to dst but only includes .ralph/stories/{storyID}/
// when storyID is non-empty. Other stories' state directories are skipped.
func copyDirSelective(src, dst, storyID string) error {
	storiesDir := filepath.Join(src, "stories")
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip other stories' directories when storyID is specified
		if storyID != "" && strings.HasPrefix(path, storiesDir+string(filepath.Separator)) {
			rel, _ := filepath.Rel(storiesDir, path)
			// rel is like "P1-003/state.json" or "P1-003"
			topDir := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if topDir != storyID {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
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
