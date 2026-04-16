package userdata

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDir_RalphDataDirOverrideWins(t *testing.T) {
	want := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", want)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg")) // must be ignored
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != want {
		t.Fatalf("Dir=%q want %q", got, want)
	}
}

func TestDir_DarwinDefault(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only path")
	}
	t.Setenv("RALPH_DATA_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, "Library", "Application Support", "ralph")
	if got != want {
		t.Fatalf("Dir=%q want %q", got, want)
	}
}

func TestDir_LinuxXDGOverride(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux-style path")
	}
	t.Setenv("RALPH_DATA_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(xdg, "ralph")
	if got != want {
		t.Fatalf("Dir=%q want %q", got, want)
	}
}

func TestDir_LinuxDefault(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux-style path")
	}
	t.Setenv("RALPH_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "ralph")
	if got != want {
		t.Fatalf("Dir=%q want %q", got, want)
	}
}

func TestReposDir_AndRepoDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RALPH_DATA_DIR", root)
	rd, err := ReposDir()
	if err != nil {
		t.Fatalf("ReposDir: %v", err)
	}
	if want := filepath.Join(root, "repos"); rd != want {
		t.Fatalf("ReposDir=%q want %q", rd, want)
	}
	r, err := RepoDir("abc123")
	if err != nil {
		t.Fatalf("RepoDir: %v", err)
	}
	if want := filepath.Join(root, "repos", "abc123"); r != want {
		t.Fatalf("RepoDir=%q want %q", r, want)
	}
}

func TestEnsureDirs(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c")
	if err := EnsureDirs(target); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("not a dir")
	}
}
