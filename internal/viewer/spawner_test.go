package viewer_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ohjann/ralphplusplus/internal/viewer"
)

func TestResolvePath_Rejects(t *testing.T) {
	// Empty path and a non-existent path both collapse to ErrInvalidPath so
	// callers don't have to enumerate filesystem error cases.
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"missing", filepath.Join(t.TempDir(), "not-there")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := viewer.ResolvePath(tc.in); !errors.Is(err, viewer.ErrInvalidPath) {
				t.Fatalf("err=%v want ErrInvalidPath", err)
			}
		})
	}
}

func TestResolvePath_RejectsFileNotDir(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := viewer.ResolvePath(f.Name()); !errors.Is(err, viewer.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for a file, got %v", err)
	}
}

func TestResolvePath_InsideHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no $HOME")
	}
	resolved, inside, err := viewer.ResolvePath(home)
	if err != nil {
		t.Fatalf("ResolvePath(HOME): %v", err)
	}
	if !inside {
		t.Fatalf("HOME should be insideHome=true, got resolved=%q", resolved)
	}
}

func TestResolvePath_OutsideHome(t *testing.T) {
	// t.TempDir() lives under $TMPDIR on macOS / /tmp on Linux — outside $HOME.
	dir := t.TempDir()
	_, inside, err := viewer.ResolvePath(dir)
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if inside {
		t.Fatalf("TempDir %q unexpectedly inside $HOME", dir)
	}
}

func TestBuildDaemonArgs_Whitelist(t *testing.T) {
	// Every whitelisted key should produce the expected CLI flag; an unknown
	// key is a hard 400 — this is the fat-finger guard the AC mandates.
	cases := []struct {
		name  string
		flags map[string]interface{}
		want  []string
		err   bool
	}{
		{"empty", nil, []string{"--daemon", "--dir", "/repo"}, false},
		{"workers-int", map[string]interface{}{"Workers": float64(3)}, []string{"--daemon", "--dir", "/repo", "--workers", "3"}, false},
		{"workers-auto-string", map[string]interface{}{"Workers": "auto"}, []string{"--daemon", "--dir", "/repo", "--workers", "auto"}, false},
		{"workers-auto-flag", map[string]interface{}{"WorkersAuto": true}, []string{"--daemon", "--dir", "/repo", "--workers", "auto"}, false},
		{"no-judge", map[string]interface{}{"JudgeEnabled": false}, []string{"--daemon", "--dir", "/repo", "--no-judge"}, false},
		{"judge-enabled", map[string]interface{}{"JudgeEnabled": true}, []string{"--daemon", "--dir", "/repo"}, false},
		{"no-quality", map[string]interface{}{"QualityReview": false}, []string{"--daemon", "--dir", "/repo", "--no-quality-review"}, false},
		{"model", map[string]interface{}{"ModelOverride": "opus"}, []string{"--daemon", "--dir", "/repo", "--model", "opus"}, false},
		{"unknown", map[string]interface{}{"Bogus": 1}, nil, true},
		{"wrong-type", map[string]interface{}{"JudgeEnabled": "yes"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := viewer.BuildDaemonArgs("/repo", tc.flags)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got args=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestBuildDaemonArgs_UnknownFlag_NamesIt(t *testing.T) {
	_, err := viewer.BuildDaemonArgs("/repo", map[string]interface{}{"NotAThing": 1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "NotAThing") {
		t.Fatalf("error should name the offending flag, got %q", err)
	}
}
