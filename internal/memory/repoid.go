package memory

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
)

// RepoID derives a stable, short identifier for a repository from its git
// remote origin URL. If no remote is configured (or git is unavailable), it
// falls back to a hash of the absolute directory path. The returned string is
// safe to use as a ChromaDB metadata value.
func RepoID(projectDir string) string {
	out, err := exec.Command("git", "-C", projectDir, "config", "--get", "remote.origin.url").Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		if url != "" {
			return normalizeRepoURL(url)
		}
	}

	// Fallback: hash the directory path so it's stable but not overly long.
	h := sha256.Sum256([]byte(projectDir))
	return fmt.Sprintf("local-%x", h[:8])
}

// normalizeRepoURL strips protocol, auth, and .git suffix to produce a
// canonical "host/owner/repo" identifier that is the same whether the repo
// was cloned via HTTPS or SSH.
func normalizeRepoURL(raw string) string {
	u := raw

	// SSH format: git@github.com:owner/repo.git
	if strings.Contains(u, "@") && strings.Contains(u, ":") {
		// Strip everything before @, then replace : with /
		idx := strings.Index(u, "@")
		u = u[idx+1:]
		u = strings.Replace(u, ":", "/", 1)
	}

	// Strip common prefixes.
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "ssh://")

	// Strip .git suffix.
	u = strings.TrimSuffix(u, ".git")

	// Strip trailing slashes.
	u = strings.TrimRight(u, "/")

	return u
}
