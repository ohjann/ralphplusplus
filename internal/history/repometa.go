// Package history owns per-repo metadata stored under the user data dir.
//
// A repo is keyed by a path-fingerprint (sha256 of the canonical absolute
// path, first 12 hex chars). When a repo is moved on disk its fingerprint
// changes; TouchRepo auto-reconciles by matching GitFirstSHA against old
// entries whose Path no longer exists.
package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohjann/ralphplusplus/internal/userdata"
)

// RepoMeta captures stable identity and activity timestamps for a repo.
type RepoMeta struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	GitFirstSHA string    `json:"git_first_sha,omitempty"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	LastRunID   string    `json:"last_run_id,omitempty"`
	RunCount    int       `json:"run_count"`
}

// Fingerprint computes sha256(EvalSymlinks(absPath))[:12].
// Symlink evaluation errors fall back to the absolute path so non-existent
// targets (e.g. a path that was just removed) still produce a stable key.
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

// gitFirstSHA returns the first-parent commit SHA of HEAD, or "" for a
// non-git / empty repo. Errors are deliberately swallowed.
func gitFirstSHA(path string) string {
	cmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// A repo with multiple root commits returns one per line; take the first.
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line)
}

func metaPath(fp string) (string, error) {
	d, err := userdata.RepoDir(fp)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "meta.json"), nil
}

func lockPath(fp string) (string, error) {
	// Sidecar lives next to the repo dir (not inside) so that
	// reconcilePathMove can os.Rename the repo dir atomically without the
	// lock holding it open.
	rd, err := userdata.ReposDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rd, fp+".lock"), nil
}

// acquireLock creates an O_EXCL sidecar lock with bounded retry. It returns a
// release func. Existing stale locks older than 30s are forcibly removed so a
// crashed daemon cannot wedge future runs.
func acquireLock(fp string) (func(), error) {
	lp, err := lockPath(fp)
	if err != nil {
		return nil, err
	}
	if err := userdata.EnsureDirs(filepath.Dir(lp)); err != nil {
		return nil, fmt.Errorf("ensure lock dir: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.OpenFile(lp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lp) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("open lock: %w", err)
		}
		if fi, statErr := os.Stat(lp); statErr == nil && time.Since(fi.ModTime()) > 30*time.Second {
			_ = os.Remove(lp)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock contention on %s", lp)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func readMeta(fp string) (*RepoMeta, error) {
	mp, err := metaPath(fp)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(mp)
	if err != nil {
		return nil, err
	}
	var m RepoMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", mp, err)
	}
	return &m, nil
}

// writeAtomicFn is swapped in tests to inject mid-write faults.
var writeAtomicFn = writeAtomic

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".meta-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

func writeMeta(fp string, m *RepoMeta) error {
	mp, err := metaPath(fp)
	if err != nil {
		return err
	}
	if err := userdata.EnsureDirs(filepath.Dir(mp)); err != nil {
		return fmt.Errorf("ensure repo dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return writeAtomicFn(mp, data)
}

// reconcilePathMove walks every other repo dir and, if exactly one entry
// matches by GitFirstSHA but whose Path no longer exists, moves the old
// fingerprint dir to the new fingerprint and rewrites Path in meta.json.
// Returns the reconciled meta (with updated Path) or nil if no match.
func reconcilePathMove(newFP, newPath, firstSHA string) (*RepoMeta, error) {
	if firstSHA == "" {
		return nil, nil
	}
	reposDir, err := userdata.ReposDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read repos dir: %w", err)
	}
	var match *RepoMeta
	var matchFP string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == newFP {
			continue
		}
		m, err := readMeta(e.Name())
		if err != nil {
			continue
		}
		if m.GitFirstSHA == "" || m.GitFirstSHA != firstSHA {
			continue
		}
		if _, err := os.Stat(m.Path); err == nil {
			continue // old path still exists, not a move
		}
		if match != nil {
			// ambiguous — refuse to reconcile
			return nil, nil
		}
		match = m
		matchFP = e.Name()
	}
	if match == nil {
		return nil, nil
	}
	oldDir := filepath.Join(reposDir, matchFP)
	newDir := filepath.Join(reposDir, newFP)
	// TouchRepo created an empty newDir before reaching reconcile; drop it
	// so os.Rename can land on a clear destination.
	_ = os.Remove(newDir)
	if err := os.Rename(oldDir, newDir); err != nil {
		return nil, fmt.Errorf("rename repo dir: %w", err)
	}
	match.Path = newPath
	if err := writeMeta(newFP, match); err != nil {
		return nil, err
	}
	return match, nil
}

// TouchRepo upserts <Dir>/repos/<fp>/meta.json for path. On first call for a
// moved repo it reconciles against the old fingerprint dir when GitFirstSHA
// matches.
func TouchRepo(path string) (string, *RepoMeta, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("abs: %w", err)
	}
	fp, err := Fingerprint(abs)
	if err != nil {
		return "", nil, err
	}
	repoDir, err := userdata.RepoDir(fp)
	if err != nil {
		return "", nil, err
	}
	if err := userdata.EnsureDirs(repoDir); err != nil {
		return "", nil, fmt.Errorf("ensure repo dir: %w", err)
	}
	release, err := acquireLock(fp)
	if err != nil {
		return "", nil, err
	}
	defer release()

	firstSHA := gitFirstSHA(abs)
	now := time.Now().UTC()

	meta, err := readMeta(fp)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if meta == nil {
		// Try reconciling from a stale fingerprint before creating fresh.
		reconciled, rerr := reconcilePathMove(fp, abs, firstSHA)
		if rerr != nil {
			return "", nil, rerr
		}
		meta = reconciled
	}
	if meta == nil {
		meta = &RepoMeta{
			Path:        abs,
			Name:        filepath.Base(abs),
			GitFirstSHA: firstSHA,
			FirstSeen:   now,
		}
	} else {
		meta.Path = abs
		if meta.Name == "" {
			meta.Name = filepath.Base(abs)
		}
		if meta.GitFirstSHA == "" && firstSHA != "" {
			meta.GitFirstSHA = firstSHA
		}
		if meta.FirstSeen.IsZero() {
			meta.FirstSeen = now
		}
	}
	meta.LastSeen = now
	meta.RunCount++
	if err := writeMeta(fp, meta); err != nil {
		return "", nil, err
	}
	return fp, meta, nil
}

// UpdateLastRunID writes LastRunID onto the meta for fp without bumping
// RunCount. Call this on shutdown with the final run id.
func UpdateLastRunID(fp, runID string) error {
	release, err := acquireLock(fp)
	if err != nil {
		return err
	}
	defer release()
	meta, err := readMeta(fp)
	if err != nil {
		return err
	}
	meta.LastRunID = runID
	meta.LastSeen = time.Now().UTC()
	return writeMeta(fp, meta)
}

// LoadAllRepos walks <Dir>/repos and returns every parseable meta.json.
// Unparseable or missing entries are silently skipped.
func LoadAllRepos() ([]RepoMeta, error) {
	reposDir, err := userdata.ReposDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []RepoMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readMeta(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *m)
	}
	return out, nil
}
