package viewer

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// docsMaxBytes caps per-file reads so a wayward binary.md cannot blow up the
// response. Markdown files in the wild are rarely over a few hundred KB;
// 1 MiB gives comfortable headroom.
const docsMaxBytes = 1 << 20 // 1 MiB

// docsAllowedRoots scopes the listing to well-known documentation folders
// plus the repo root. Keeps us from walking node_modules/**/*.md.
var docsAllowedRoots = []string{".", "docs"}

// DocFile is one entry in the /api/repos/:fp/docs listing.
type DocFile struct {
	// Path is relative to the repo root, always forward-slashed.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
}

// handleDocsList serves GET /api/repos/:fp/docs — returns every *.md file
// under the repo root (non-recursive) and under docs/** (recursive). The
// frontend renders them in a sidebar file tree.
func (s *Server) handleDocsList(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	meta, ok := s.lookupRepo(w, r, fp)
	if !ok {
		return
	}
	files := collectDocs(meta.Path)
	writeJSON(w, http.StatusOK, files)
}

// handleDocsRaw serves GET /api/repos/:fp/docs/raw?path=<rel> — returns the
// raw markdown file text. The path query is re-resolved against the repo
// root and rejected if it escapes the root or targets a non-.md file.
func (s *Server) handleDocsRaw(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	meta, ok := s.lookupRepo(w, r, fp)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := resolveDocPath(meta.Path, rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "stat: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir() || info.Size() > docsMaxBytes {
		http.Error(w, "not a readable markdown file", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// resolveDocPath joins rel against root and refuses paths that escape the
// root, hit hidden dirs, or target non-.md files. Returns the absolute,
// symlink-resolved path on success.
func resolveDocPath(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fs.ErrInvalid
	}
	// Normalise: the wire protocol uses forward slashes; we want OS-native.
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fs.ErrInvalid
	}
	if strings.ToLower(filepath.Ext(clean)) != ".md" {
		return "", fs.ErrInvalid
	}
	abs := filepath.Join(root, clean)
	// EvalSymlinks so a symlinked docs/README.md → outside still fails the
	// containment check below.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved+string(filepath.Separator), rootAbs+string(filepath.Separator)) &&
		resolved != rootAbs {
		return "", fs.ErrInvalid
	}
	return resolved, nil
}

// collectDocs returns every .md file under the repo's allowed roots. Root
// entries list non-recursively (README.md, CLAUDE.md); docs/** is recursive
// but skips hidden directories and typical vendor trees.
func collectDocs(root string) []DocFile {
	seen := make(map[string]struct{})
	var out []DocFile
	for _, sub := range docsAllowedRoots {
		base := filepath.Join(root, sub)
		info, err := os.Stat(base)
		if err != nil || !info.IsDir() {
			continue
		}
		if sub == "." {
			// Root: non-recursive .md files only — avoid walking node_modules.
			entries, err := os.ReadDir(base)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !isMarkdown(e.Name()) {
					continue
				}
				if _, dup := seen[e.Name()]; dup {
					continue
				}
				seen[e.Name()] = struct{}{}
				if df, ok := describeDoc(root, e.Name()); ok {
					out = append(out, df)
				}
			}
			continue
		}
		// Sub-directory: recursive, skipping hidden dirs and common vendor dirs.
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name != sub && (strings.HasPrefix(name, ".") || isVendorDir(name)) {
					return fs.SkipDir
				}
				return nil
			}
			if !isMarkdown(d.Name()) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if _, dup := seen[rel]; dup {
				return nil
			}
			seen[rel] = struct{}{}
			if df, ok := describeDoc(root, rel); ok {
				out = append(out, df)
			}
			return nil
		})
	}
	// Stable order: root files first (by name), then sub-paths (by path).
	sort.SliceStable(out, func(i, j int) bool {
		ai := strings.Contains(out[i].Path, "/")
		aj := strings.Contains(out[j].Path, "/")
		if ai != aj {
			return !ai
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func describeDoc(root, rel string) (DocFile, bool) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return DocFile{}, false
	}
	return DocFile{
		Path:  rel,
		Name:  filepath.Base(rel),
		Size:  info.Size(),
		MTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}, true
}

func isMarkdown(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".md"
}

func isVendorDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "__pycache__":
		return true
	}
	return false
}
