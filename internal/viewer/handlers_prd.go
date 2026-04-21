package viewer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/prd"
)

// handlePRDGet serves GET /api/repos/:fp/prd — returns the parsed on-disk
// PRD plus its sha256 hex hash. When a run_id query param is supplied and
// the manifest carries a PRDSnapshot, the response also reports whether
// the current file hash matches that snapshot.
func (s *Server) handlePRDGet(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	meta, ok := s.lookupRepo(w, r, fp)
	if !ok {
		return
	}

	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	data, err := os.ReadFile(filepath.Join(meta.Path, "prd.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "prd_missing"})
			return
		}
		http.Error(w, "read prd: "+err.Error(), http.StatusInternalServerError)
		return
	}

	hash := hashBytes(data)
	resp := PRDResponse{Hash: hash, Content: json.RawMessage(data)}
	if runID := r.URL.Query().Get("run_id"); runID != "" {
		m, err := history.ReadManifest(fp, runID)
		if err == nil && m.PRDSnapshot != "" {
			match := m.PRDSnapshot == hash
			resp.MatchesRunSnapshot = &match
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePRDPost serves POST /api/repos/:fp/prd — validates the submitted
// PRD, writes it to disk, and returns the new hash. Optimistic concurrency
// is enforced with an optional If-Match header carrying the sha256 hash
// the client last saw: if present and stale, 409 is returned with the
// current on-disk hash so the SPA can prompt the user to reload. The SPA
// does its own pre-flight hash check; If-Match is a defence-in-depth step
// that catches a tiny race between the pre-flight GET and this POST.
func (s *Server) handlePRDPost(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	meta, ok := s.lookupRepo(w, r, fp)
	if !ok {
		return
	}

	w.Header().Set("Cache-Control", "no-store")

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "read_body",
			"detail": err.Error(),
		})
		return
	}

	var p prd.PRD
	if err := json.Unmarshal(body, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "invalid_json",
			"detail": err.Error(),
		})
		return
	}

	if errs := prd.Validate(&p); len(errs) > 0 {
		fields := make(map[string]string, len(errs))
		for _, fe := range errs {
			if _, dup := fields[fe.Path]; !dup {
				fields[fe.Path] = fe.Message
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation_failed",
			"fields": fields,
		})
		return
	}

	prdPath := filepath.Join(meta.Path, "prd.json")

	// Optimistic concurrency: if the client sent If-Match, compare it to the
	// current on-disk hash. If the file is missing, the client must send an
	// empty If-Match (or omit the header) — any other value is stale.
	ifMatch := r.Header.Get("If-Match")
	current, currentErr := currentPRDHash(prdPath)
	if currentErr != nil {
		http.Error(w, "hash on-disk prd: "+currentErr.Error(), http.StatusInternalServerError)
		return
	}
	if ifMatch != "" && ifMatch != current {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":       "hash_mismatch",
			"currentHash": current,
		})
		return
	}

	if err := prd.Save(prdPath, &p); err != nil {
		http.Error(w, "save prd: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := os.ReadFile(prdPath)
	if err != nil {
		http.Error(w, "re-read prd: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, PRDResponse{
		Hash:    hashBytes(data),
		Content: json.RawMessage(data),
	})
}

// lookupRepo resolves a :fp path parameter to a RepoMeta. It writes a 404
// (or 500 on index errors) to w and returns (nil, false) when the fp is
// unknown or unresolvable, so callers can early-return without further
// response writes.
func (s *Server) lookupRepo(w http.ResponseWriter, r *http.Request, fp string) (*history.RepoMeta, bool) {
	repos, err := s.Index.Get(r.Context())
	if err != nil {
		http.Error(w, "load repos: "+err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	for i := range repos {
		if repos[i].FP == fp {
			return &repos[i].Meta, true
		}
	}
	http.NotFound(w, r)
	return nil, false
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// currentPRDHash returns the sha256 hex of the file at path. A missing
// file hashes to the empty string so callers can treat "no file" as a
// legitimate pre-condition for create-from-scratch writes.
func currentPRDHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return hashBytes(data), nil
}
