package viewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/viewer/projects"
)

// Server owns the viewer's long-lived dependencies (auth token, version
// string, cached project index). It is built once per process and not
// intended to outlive its context.
type Server struct {
	Token   string
	Version string
	Index   *projects.Index
	// TailscaleURL is set by the viewer command when a tailnet listener is
	// up (http://<hostname>[:port]/). Empty when --tailscale was not used
	// or tsnet bring-up failed. Surfaced via /api/integrations.
	TailscaleURL string
}

// NewServer builds a Server and starts its fsnotify-backed project index.
// The watcher runs until ctx is cancelled.
func NewServer(ctx context.Context, token, version string) (*Server, error) {
	idx, err := projects.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("projects index: %w", err)
	}
	return &Server{Token: token, Version: version, Index: idx}, nil
}

// Handler returns the composed http.Handler with AuthMiddleware applied to
// every route. The SPA first loads via ?token=... in the URL; XHRs then
// send X-Ralph-Token. /api/bootstrap lets the SPA retrieve the token once
// it is parsed from the URL so subsequent calls have a header to send.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("GET /api/repos", s.handleRepos)
	mux.HandleFunc("GET /api/repos/{fp}", s.handleRepoDetail)
	mux.HandleFunc("GET /api/repos/{fp}/runs", s.handleRunsList)
	mux.HandleFunc("GET /api/repos/{fp}/runs/{runID}", s.handleRunDetail)
	mux.HandleFunc("GET /api/repos/{fp}/prd", s.handlePRDGet)
	mux.HandleFunc("POST /api/repos/{fp}/prd", s.handlePRDPost)
	mux.HandleFunc("GET /api/repos/{fp}/meta", s.handleRepoMeta)
	mux.HandleFunc("GET /api/repos/{fp}/runs/{runID}/transcript/{story}/{iter}", s.handleTranscript)
	mux.HandleFunc("GET /api/repos/{fp}/runs/{runID}/prompt/{story}/{iter}", s.handlePrompt)
	mux.HandleFunc("GET /api/live/{fp}/events", s.handleLiveEvents)
	mux.HandleFunc("GET /api/live/{fp}/state", s.handleLiveState)
	mux.HandleFunc("GET /api/live/{fp}/worker/{id}/activity", s.handleLiveWorkerActivity)
	mux.HandleFunc("GET /api/live/{fp}/settings", s.handleSettings)
	mux.HandleFunc("POST /api/live/{fp}/settings", s.handleSettingsPost)
	mux.HandleFunc("/api/live/{fp}/pause", s.handleLiveCommand("pause"))
	mux.HandleFunc("/api/live/{fp}/resume", s.handleLiveCommand("resume"))
	mux.HandleFunc("/api/live/{fp}/hint", s.handleLiveCommand("hint"))
	mux.HandleFunc("/api/live/{fp}/clarify", s.handleLiveCommand("clarify"))
	mux.HandleFunc("/api/live/{fp}/quit", s.handleLiveCommand("quit"))
	mux.HandleFunc("/api/spawn-daemon", s.handleSpawnDaemon)
	mux.HandleFunc("POST /api/spawn/retro/{fp}", s.handleSpawnRetro)
	mux.HandleFunc("GET /api/integrations", s.handleIntegrations)
	mux.HandleFunc("GET /api/repos/{fp}/docs", s.handleDocsList)
	mux.HandleFunc("GET /api/repos/{fp}/docs/raw", s.handleDocsRaw)
	mux.HandleFunc("/", s.handleRoot)
	return AuthMiddleware(s.Token, mux)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ralph viewer\n")
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Bootstrap{
		Version:      s.Version,
		FeatureFlags: []string{},
		Token:        s.Token,
	})
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.Index.Get(r.Context())
	if err != nil {
		http.Error(w, "load repos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]RepoSummary, 0, len(repos))
	for _, rp := range repos {
		out = append(out, RepoSummary{
			FP:       rp.FP,
			Path:     rp.Meta.Path,
			Name:     rp.Meta.Name,
			LastSeen: rp.Meta.LastSeen,
			RunCount: rp.Meta.RunCount,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRepoDetail(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, RepoDetail{
		Meta:     *meta,
		AggCosts: aggregate(h.Runs),
	})
}

func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	kindFilter := r.URL.Query().Get("kind")
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}

	manifests, err := history.LoadManifestsForRepo(fp)
	if err != nil {
		http.Error(w, "load manifests: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h, err := costs.LoadHistory(fp)
	if err != nil {
		http.Error(w, "load history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	byRunID := make(map[string]*costs.RunSummary, len(h.Runs))
	for i := range h.Runs {
		if id := h.Runs[i].RunID; id != "" {
			byRunID[id] = &h.Runs[i]
		}
	}

	items := make([]RunListItem, 0, len(manifests))
	for _, m := range manifests {
		if kindFilter != "" && m.Kind != kindFilter {
			continue
		}
		item := RunListItem{
			RunID:        m.RunID,
			DisplayName:  displayName(m),
			Kind:         m.Kind,
			Status:       m.Status,
			StartTime:    m.StartTime,
			EndTime:      m.EndTime,
			GitBranch:    m.GitBranch,
			GitHeadSHA:   m.GitHeadSHA,
			Iterations:   m.Totals.Iterations,
			InputTokens:  m.Totals.InputTokens,
			OutputTokens: m.Totals.OutputTokens,
		}
		if sum, ok := byRunID[m.RunID]; ok {
			cost := sum.TotalCost
			dur := sum.DurationMinutes
			fpr := sum.FirstPassRate
			item.TotalCost = &cost
			item.DurationMinutes = &dur
			item.FirstPassRate = &fpr
			item.ModelsUsed = sum.ModelsUsed
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].StartTime.After(items[j].StartTime)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	runID := r.PathValue("runID")
	m, err := history.ReadManifest(fp, runID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "read manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h, err := costs.LoadHistory(fp)
	if err != nil {
		http.Error(w, "load history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var summary *costs.RunSummary
	for i := range h.Runs {
		if h.Runs[i].RunID == runID {
			s := h.Runs[i]
			summary = &s
			break
		}
	}
	if m.DisplayName == "" {
		m.DisplayName = history.DisplayNameFor(m.RunID)
	}
	writeJSON(w, http.StatusOK, RunDetail{Manifest: *m, Summary: summary})
}

// displayName returns the manifest's stored DisplayName, backfilling from the
// run id for manifests written before the field existed. Ensures every run
// row in the sidebar has a name without a data migration.
func displayName(m history.Manifest) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return history.DisplayNameFor(m.RunID)
}

func aggregate(runs []costs.RunSummary) AggCosts {
	var a AggCosts
	for _, r := range runs {
		a.Runs++
		a.TotalCost += r.TotalCost
		a.DurationMinutes += r.DurationMinutes
		a.TotalIterations += r.TotalIterations
		a.StoriesTotal += r.StoriesTotal
		a.StoriesCompleted += r.StoriesCompleted
		a.StoriesFailed += r.StoriesFailed
	}
	return a
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
