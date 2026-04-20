package viewer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/history"
)

// handleSettings serves GET /api/live/:fp/settings. When the repo's daemon
// socket is reachable, it forwards GET /api/state and returns the body under
// {source:"daemon", state:<raw>}. When the socket is missing or refuses, it
// falls back to <RepoMeta.Path>/.ralph/config.toml and returns {source:"file",
// config:{...}}. The "source" field is the SPA's signal for showing a
// "Daemon offline" banner. Cache-Control is always no-store.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	w.Header().Set("Cache-Control", "no-store")

	sock, err := s.resolveSock(r.Context(), fp)
	if err == nil {
		// Daemon reachable — proxy /api/state, capture the body, wrap.
		req, reqErr := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://daemon/api/state", nil)
		if reqErr == nil {
			resp, dialErr := unixRoundTrip(sock, req)
			if dialErr == nil {
				defer resp.Body.Close()
				body, readErr := io.ReadAll(resp.Body)
				if readErr == nil && resp.StatusCode == http.StatusOK {
					writeJSON(w, http.StatusOK, SettingsResponse{
						Source: "daemon",
						State:  json.RawMessage(body),
					})
					return
				}
			}
		}
		// Fall through to file source if the daemon round-trip failed for any
		// reason (transient socket issue, daemon mid-restart, etc.) — better
		// to show stale config than a blank page.
	}

	// File fallback: read .ralph/config.toml from the repo path.
	cfg, readErr := s.readRepoConfigToml(r.Context(), fp)
	if readErr != nil {
		if errors.Is(readErr, errRepoNotFound) {
			http.NotFound(w, r)
			return
		}
		// File missing or unparseable → return source:file with empty config,
		// not a 500. The SPA can still render the offline banner.
		cfg = map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, SettingsResponse{
		Source: "file",
		Config: cfg,
	})
}

var errRepoNotFound = errors.New("repo_not_found")

// readRepoConfigToml resolves :fp to RepoMeta.Path and parses
// <Path>/.ralph/config.toml into a generic map. Returns errRepoNotFound when
// the fp does not match a known repo. A missing config.toml is reported by
// returning (nil, os.ErrNotExist) so the caller can decide how to render.
func (s *Server) readRepoConfigToml(ctx context.Context, fp string) (map[string]interface{}, error) {
	repos, err := s.Index.Get(ctx)
	if err != nil {
		return nil, err
	}
	var meta *history.RepoMeta
	for i := range repos {
		if repos[i].FP == fp {
			meta = &repos[i].Meta
			break
		}
	}
	if meta == nil {
		return nil, errRepoNotFound
	}
	path := filepath.Join(meta.Path, ".ralph", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := toml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// handleRepoMeta serves GET /api/repos/:fp/meta. Returns the on-disk RepoMeta
// joined with aggregate cost stats from costs.LoadHistory and a per-Kind run
// count from history.LoadManifestsForRepo. Distinct from /api/repos/:fp,
// which returns RepoDetail without the per-Kind breakdown.
func (s *Server) handleRepoMeta(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	repos, err := s.Index.Get(r.Context())
	if err != nil {
		http.Error(w, "load repos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var meta *history.RepoMeta
	for i := range repos {
		if repos[i].FP == fp {
			meta = &repos[i].Meta
			break
		}
	}
	if meta == nil {
		http.NotFound(w, r)
		return
	}

	h, err := costs.LoadHistory(fp)
	if err != nil {
		http.Error(w, "load history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	manifests, err := history.LoadManifestsForRepo(fp)
	if err != nil {
		// Don't fail the whole response just because manifests can't be read —
		// counts default to empty.
		manifests = nil
	}
	counts := make(map[string]int)
	for _, m := range manifests {
		k := m.Kind
		if k == "" {
			k = "unknown"
		}
		counts[k]++
	}

	writeJSON(w, http.StatusOK, RepoMetaResponse{
		Meta:            *meta,
		AggCosts:        aggregate(h.Runs),
		RunCountsByKind: counts,
	})
}
