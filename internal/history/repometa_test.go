package history

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setDataDir isolates every test to its own RALPH_DATA_DIR.
func setDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", dir)
	return dir
}

func TestFingerprint_Stable(t *testing.T) {
	setDataDir(t)
	repo := t.TempDir()
	a, err := Fingerprint(repo)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	b, err := Fingerprint(repo)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if a != b {
		t.Fatalf("fingerprint not stable: %q vs %q", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("fingerprint len=%d want 12", len(a))
	}
}

func TestTouchRepo_FreshUpsert(t *testing.T) {
	setDataDir(t)
	repo := t.TempDir()
	fp, meta, err := TouchRepo(repo)
	if err != nil {
		t.Fatalf("TouchRepo: %v", err)
	}
	if meta.RunCount != 1 {
		t.Fatalf("RunCount=%d want 1", meta.RunCount)
	}
	if meta.FirstSeen.IsZero() || meta.LastSeen.IsZero() {
		t.Fatalf("timestamps unset")
	}
	absRepo, _ := filepath.Abs(repo)
	if meta.Path != absRepo {
		t.Fatalf("Path=%q want %q", meta.Path, absRepo)
	}
	// meta.json exists
	reposDir, _ := os.ReadFile(filepathMetaJSON(t, fp))
	if len(reposDir) == 0 {
		t.Fatalf("meta.json empty")
	}
}

func filepathMetaJSON(t *testing.T, fp string) string {
	t.Helper()
	dir := os.Getenv("RALPH_DATA_DIR")
	return filepath.Join(dir, "repos", fp, "meta.json")
}

func TestTouchRepo_Idempotent(t *testing.T) {
	setDataDir(t)
	repo := t.TempDir()
	fp1, m1, err := TouchRepo(repo)
	if err != nil {
		t.Fatalf("TouchRepo#1: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	fp2, m2, err := TouchRepo(repo)
	if err != nil {
		t.Fatalf("TouchRepo#2: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint changed: %q -> %q", fp1, fp2)
	}
	if m2.RunCount != 2 {
		t.Fatalf("RunCount=%d want 2", m2.RunCount)
	}
	if !m2.LastSeen.After(m1.LastSeen) && !m2.LastSeen.Equal(m1.LastSeen) {
		t.Fatalf("LastSeen not advanced")
	}
	if m2.FirstSeen != m1.FirstSeen {
		t.Fatalf("FirstSeen changed on re-touch: %v -> %v", m1.FirstSeen, m2.FirstSeen)
	}
}

// initGitRepo sets up a git repo with a single commit so gitFirstSHA returns
// a stable SHA. Skips the test if git is unavailable.
func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return string(out[:len(out)-1])
}

func TestTouchRepo_ReconcilePathMove(t *testing.T) {
	setDataDir(t)
	base := t.TempDir()
	oldPath := filepath.Join(base, "old")
	newPath := filepath.Join(base, "new")
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sha := initGitRepo(t, oldPath)

	fpOld, m1, err := TouchRepo(oldPath)
	if err != nil {
		t.Fatalf("TouchRepo old: %v", err)
	}
	if m1.GitFirstSHA != sha {
		t.Fatalf("GitFirstSHA=%q want %q", m1.GitFirstSHA, sha)
	}

	// Move the repo on disk.
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	fpNew, m2, err := TouchRepo(newPath)
	if err != nil {
		t.Fatalf("TouchRepo new: %v", err)
	}
	if fpOld == fpNew {
		t.Fatalf("expected fingerprint change")
	}
	if m2.GitFirstSHA != sha {
		t.Fatalf("post-move GitFirstSHA=%q want %q", m2.GitFirstSHA, sha)
	}
	if m2.RunCount != 2 {
		t.Fatalf("RunCount=%d want 2 (continued from old meta)", m2.RunCount)
	}
	if !pathEquals(t, m2.Path, newPath) {
		t.Fatalf("Path=%q want %q", m2.Path, newPath)
	}
	// Old fingerprint dir must be gone.
	dataDir := os.Getenv("RALPH_DATA_DIR")
	if _, err := os.Stat(filepath.Join(dataDir, "repos", fpOld)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old fingerprint dir still exists: err=%v", err)
	}
}

func pathEquals(t *testing.T, got, want string) bool {
	t.Helper()
	gAbs, _ := filepath.Abs(got)
	wAbs, _ := filepath.Abs(want)
	if gR, err := filepath.EvalSymlinks(gAbs); err == nil {
		gAbs = gR
	}
	if wR, err := filepath.EvalSymlinks(wAbs); err == nil {
		wAbs = wR
	}
	return gAbs == wAbs
}

func TestTouchRepo_NoReconcileWhenGitFirstSHAEmpty(t *testing.T) {
	setDataDir(t)
	base := t.TempDir()
	oldPath := filepath.Join(base, "old")
	newPath := filepath.Join(base, "new")
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No git init — GitFirstSHA stays empty.
	fpOld, m1, err := TouchRepo(oldPath)
	if err != nil {
		t.Fatalf("TouchRepo old: %v", err)
	}
	if m1.GitFirstSHA != "" {
		t.Fatalf("expected empty GitFirstSHA, got %q", m1.GitFirstSHA)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	fpNew, m2, err := TouchRepo(newPath)
	if err != nil {
		t.Fatalf("TouchRepo new: %v", err)
	}
	if fpOld == fpNew {
		t.Fatalf("expected fingerprint change")
	}
	if m2.RunCount != 1 {
		t.Fatalf("RunCount=%d want 1 (no reconciliation possible)", m2.RunCount)
	}
	// Both fingerprint dirs exist — old one is orphaned.
	dataDir := os.Getenv("RALPH_DATA_DIR")
	if _, err := os.Stat(filepath.Join(dataDir, "repos", fpOld)); err != nil {
		t.Fatalf("old fingerprint dir gone: %v", err)
	}
}

func TestTouchRepo_ConcurrentWithFaultInjection(t *testing.T) {
	setDataDir(t)
	repo := t.TempDir()

	// Inject a one-shot mid-write fault for the first writer.
	var failed atomic.Int32
	prev := writeAtomicFn
	t.Cleanup(func() { writeAtomicFn = prev })
	writeAtomicFn = func(path string, data []byte) error {
		if failed.CompareAndSwap(0, 1) {
			return errors.New("injected write fault")
		}
		return writeAtomic(path, data)
	}

	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := TouchRepo(repo)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	failures := 0
	successes := 0
	for _, e := range errs {
		if e != nil {
			failures++
		} else {
			successes++
		}
	}
	if failures != 1 {
		t.Fatalf("expected exactly 1 injected failure, got %d (successes=%d)", failures, successes)
	}
	if successes != 9 {
		t.Fatalf("expected 9 successes, got %d", successes)
	}

	// Meta.json must be valid JSON and reflect the completed writes.
	fp, err := Fingerprint(repo)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	data, err := os.ReadFile(filepathMetaJSON(t, fp))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var m RepoMeta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}
	if m.RunCount != successes {
		t.Fatalf("RunCount=%d want %d", m.RunCount, successes)
	}

	// Lock sidecar must be released.
	dataDir := os.Getenv("RALPH_DATA_DIR")
	if _, err := os.Stat(filepath.Join(dataDir, "repos", fp+".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock sidecar not released: err=%v", err)
	}
}

func TestLoadAllRepos(t *testing.T) {
	setDataDir(t)
	r1 := t.TempDir()
	r2 := t.TempDir()
	if _, _, err := TouchRepo(r1); err != nil {
		t.Fatalf("touch r1: %v", err)
	}
	if _, _, err := TouchRepo(r2); err != nil {
		t.Fatalf("touch r2: %v", err)
	}
	all, err := LoadAllRepos()
	if err != nil {
		t.Fatalf("LoadAllRepos: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d want 2", len(all))
	}
}

func TestLoadAllRepos_MissingReposDir(t *testing.T) {
	setDataDir(t)
	all, err := LoadAllRepos()
	if err != nil {
		t.Fatalf("LoadAllRepos: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty, got %d", len(all))
	}
}

func TestUpdateLastRunID(t *testing.T) {
	setDataDir(t)
	repo := t.TempDir()
	fp, _, err := TouchRepo(repo)
	if err != nil {
		t.Fatalf("TouchRepo: %v", err)
	}
	if err := UpdateLastRunID(fp, "run-42"); err != nil {
		t.Fatalf("UpdateLastRunID: %v", err)
	}
	m, err := readMeta(fp)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if m.LastRunID != "run-42" {
		t.Fatalf("LastRunID=%q want run-42", m.LastRunID)
	}
	if m.RunCount != 1 {
		t.Fatalf("RunCount bumped by UpdateLastRunID: %d", m.RunCount)
	}
}
