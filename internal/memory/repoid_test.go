package memory

import "testing"

func TestNormalizeRepoURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"git@github.com:eoghanhynes/ralph.git", "github.com/eoghanhynes/ralph"},
		{"https://github.com/eoghanhynes/ralph.git", "github.com/eoghanhynes/ralph"},
		{"https://github.com/eoghanhynes/ralph", "github.com/eoghanhynes/ralph"},
		{"ssh://git@github.com:eoghanhynes/ralph.git", "github.com/eoghanhynes/ralph"},
		{"http://github.com/foo/bar.git", "github.com/foo/bar"},
		{"git@gitlab.com:org/project.git", "gitlab.com/org/project"},
	}

	for _, tt := range tests {
		got := normalizeRepoURL(tt.input)
		if got != tt.want {
			t.Errorf("normalizeRepoURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRepoID_Fallback(t *testing.T) {
	// A non-existent directory should produce a stable local-xxx fallback.
	id := RepoID("/tmp/not-a-git-repo-1234567890")
	if id == "" {
		t.Fatal("RepoID returned empty string for non-git dir")
	}
	if len(id) < 10 {
		t.Fatalf("RepoID fallback too short: %q", id)
	}
	// Should be deterministic.
	if id2 := RepoID("/tmp/not-a-git-repo-1234567890"); id2 != id {
		t.Fatalf("RepoID not deterministic: %q vs %q", id, id2)
	}
}
