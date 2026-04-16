// Package assets bundles Ralph's prompt templates and the ralph skill
// directory into the binary via go:embed. Callers read prompts through
// ReadPrompt, which first consults $RALPH_HOME for an on-disk override and
// then falls back to the embedded copy. This lets operators tweak prompts in
// place while the default install remains a single self-contained binary.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:files
var embedded embed.FS

const embedRoot = "files"

// ReadPrompt returns the bytes for the named prompt file. Names use forward
// slashes relative to the embed root (e.g. "ralph-prompt.md",
// "prompts/architect.md"). If $RALPH_HOME is set and contains the named file,
// that override is returned. Otherwise the embedded copy is used.
func ReadPrompt(name string) ([]byte, error) {
	if home := os.Getenv("RALPH_HOME"); home != "" {
		override := filepath.Join(home, filepath.FromSlash(name))
		if data, err := os.ReadFile(override); err == nil {
			return data, nil
		}
	}
	data, err := embedded.ReadFile(path.Join(embedRoot, name))
	if err != nil {
		return nil, fmt.Errorf("reading embedded asset %q: %w", name, err)
	}
	return data, nil
}

// SkillFS returns the embedded skills/ralph/ subtree rooted so callers can
// walk it with fs.WalkDir to mirror the tree to disk.
func SkillFS() fs.FS {
	sub, err := fs.Sub(embedded, path.Join(embedRoot, "skills", "ralph"))
	if err != nil {
		panic(fmt.Errorf("assets: skills/ralph subtree missing: %w", err))
	}
	return sub
}

// Available lists bundled prompt file names (relative to the embed root),
// excluding the skills subtree. Useful for diagnostics and tests.
func Available() []string {
	skillsDir := path.Join(embedRoot, "skills")
	prefix := embedRoot + "/"
	var names []string
	_ = fs.WalkDir(embedded, embedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p == skillsDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		names = append(names, strings.TrimPrefix(p, prefix))
		return nil
	})
	sort.Strings(names)
	return names
}
