package statuspage

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// StoryStatus represents the status of a single story.
type StoryStatus struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Status        string   `json:"status"` // queued, running, done, failed, stuck
	Cost          float64  `json:"cost"`
	Iteration     int      `json:"iteration,omitempty"`
	Role          string   `json:"role,omitempty"`   // e.g. "implementer", "architect"
	Detail        string   `json:"detail,omitempty"` // custom detail text
	WorkerID      int      `json:"worker_id,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`     // DAG dependency list
	IsInteractive bool     `json:"is_interactive,omitempty"` // T- prefix interactive task
	TaskStatus    string   `json:"task_status,omitempty"`    // interactive task status
}

// PlanQualityStatus holds plan quality metrics for the status page.
type PlanQualityStatus struct {
	Score          float64 `json:"score"`
	FirstPassCount int     `json:"first_pass_count"`
	RetryCount     int     `json:"retry_count"`
	FailedCount    int     `json:"failed_count"`
	TotalStories   int     `json:"total_stories"`
}

// SettingStatus holds a single setting's label and display value.
type SettingStatus struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// WorkerTab represents a single worker tab entry for the status page.
type WorkerTab struct {
	WorkerID int    `json:"worker_id"`
	StoryID  string `json:"story_id"`
	Role     string `json:"role,omitempty"`
	State    string `json:"state"`
	Active   bool   `json:"active"`
}

// Badge represents an enabled feature badge.
type Badge struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
}

// RateLimitStatus holds rate limit info for the status page.
type RateLimitStatus struct {
	Window    string `json:"window,omitempty"`
	Status    string `json:"status,omitempty"`
	ResetsIn  string `json:"resets_in,omitempty"`
	HasLimit  bool   `json:"has_limit"`
}

// StatusState mirrors what the TUI shows: phase, story statuses, costs, iteration info.
type StatusState struct {
	PRDName         string          `json:"prd_name"`
	Phase           string          `json:"phase"`
	PhaseIcon       string          `json:"phase_icon"`
	RunDuration     string          `json:"run_duration"`
	Stories         []StoryStatus   `json:"stories"`
	TotalCost       float64         `json:"total_cost"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Completed       int             `json:"completed"`
	Total           int             `json:"total"`
	AllComplete     bool            `json:"all_complete"`
	Running         bool            `json:"running"`
	Badges          []Badge         `json:"badges,omitempty"`
	ProgressContent string          `json:"progress_content,omitempty"`
	WorktreeContent string          `json:"worktree_content,omitempty"`
	JudgeContent    string          `json:"judge_content,omitempty"`
	QualityContent  string          `json:"quality_content,omitempty"`
	MemoryContent   string          `json:"memory_content,omitempty"`
	CostsContent    string          `json:"costs_content,omitempty"`
	ClaudeActivity   string              `json:"claude_activity,omitempty"`
	WorkerLogs       map[int]string      `json:"worker_logs,omitempty"`
	WorkerTabs       []WorkerTab         `json:"worker_tabs,omitempty"`
	StuckAlert       string              `json:"stuck_alert,omitempty"`
	RateLimit        RateLimitStatus     `json:"rate_limit"`
	Version          string              `json:"version,omitempty"`
	HasTokenData     bool                `json:"has_token_data"`
	CostDisplay      string              `json:"cost_display,omitempty"`
	CompletionReason string              `json:"completion_reason,omitempty"`
	PlanQuality      *PlanQualityStatus  `json:"plan_quality,omitempty"`
	Settings         []SettingStatus     `json:"settings,omitempty"`
	CurrentTask      string              `json:"current_task,omitempty"`
}

type sseClient struct {
	ch chan []byte
}

// StatusServer serves a mobile-friendly status page with live SSE updates.
type StatusServer struct {
	mu      sync.RWMutex
	state   StatusState
	clients map[*sseClient]struct{}
	server  *http.Server
}

// New creates a new StatusServer.
func New() *StatusServer {
	return &StatusServer{
		clients: make(map[*sseClient]struct{}),
	}
}

// Start starts the HTTP server on the given port.
func (s *StatusServer) Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/api/status", s.handleAPIStatus)

	addr := fmt.Sprintf(":%d", port)

	// Try to bind the port early so we can return an error if it's in use.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("status page server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the server.
func (s *StatusServer) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}

	// Close all SSE clients.
	s.mu.Lock()
	for c := range s.clients {
		close(c.ch)
		delete(s.clients, c)
	}
	s.mu.Unlock()

	return s.server.Shutdown(ctx)
}

// UpdateState pushes a new state to all connected SSE clients.
func (s *StatusServer) UpdateState(state StatusState) {
	state.UpdatedAt = time.Now()

	s.mu.Lock()
	s.state = state

	data, err := json.Marshal(state)
	if err != nil {
		s.mu.Unlock()
		return
	}

	msg := fmt.Appendf(nil, "data: %s\n\n", data)
	for c := range s.clients {
		select {
		case c.ch <- msg:
		default:
			// Client too slow, skip this update.
		}
	}
	s.mu.Unlock()
}

func (s *StatusServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(state)
}

func (s *StatusServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := &sseClient{ch: make(chan []byte, 16)}

	s.mu.Lock()
	s.clients[client] = struct{}{}

	// Send current state immediately.
	data, err := json.Marshal(s.state)
	if err == nil {
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
	}()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			w.Write(msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *StatusServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderHTML(state))
}

func renderHTML(state StatusState) string {
	// Server-render initial story rows
	storiesHTML := ""
	for _, st := range state.Stories {
		storiesHTML += renderStoryRow(st)
	}

	initialCtx := renderInitialCtxContent(state.ProgressContent)

	claudeContent := ""
	if state.ClaudeActivity != "" {
		claudeContent = escapeHTML(state.ClaudeActivity)
	} else {
		claudeContent = `<span class="claude-empty">Monitoring...</span>`
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#1e1e2e">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<title>ralph %s %s</title>
<style>
:root {
  --base: #1e1e2e;
  --mantle: #181825;
  --crust: #11111b;
  --surface0: #313244;
  --surface1: #45475a;
  --surface2: #585b70;
  --overlay0: #6c7086;
  --subtext0: #a6adc8;
  --subtext1: #bac2de;
  --text: #cdd6f4;
  --lavender: #b4befe;
  --blue: #89b4fa;
  --sky: #89dceb;
  --teal: #94e2d5;
  --green: #a6e3a1;
  --yellow: #f9e2af;
  --peach: #fab387;
  --red: #f38ba8;
  --flamingo: #f2cdcd;
  --claude: #f9845c;
  --mauve: #cba6f7;
  --border-ch: "┊";
  --corner-tl: "╭";
  --corner-tr: "╮";
  --corner-bl: "╰";
  --corner-br: "╯";
}

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

html {
  font-size: clamp(13px, 3.2vw, 16px);
  -webkit-text-size-adjust: none;
  text-size-adjust: none;
}

body {
  font-family: "SF Mono", "Cascadia Code", "JetBrains Mono", "Fira Code", Menlo, Monaco, "Courier New", monospace;
  background: var(--base);
  color: var(--text);
  line-height: 1.45;
  min-height: 100dvh;
  padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
  overflow-x: hidden;
}

.shell {
  max-width: min(96ch, 100vw - 2rem);
  margin: 0 auto;
  padding: 0.5rem;
  overflow: hidden;
}

/* ── Header ────────────────────────────────── */

.header {
  margin-bottom: 0.25rem;
}

.header-line1 {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 0.75ch;
  padding: 0.25rem 0;
}

.diamond {
  color: var(--claude);
  font-weight: 700;
}

.diamond.running {
  animation: diamond-pulse 1.5s ease-in-out infinite;
}

@keyframes diamond-pulse {
  0%%, 70%%, 100%% { color: var(--claude); }
  10%% { color: #fff; }
  25%% { color: var(--peach); }
}

.title {
  color: var(--yellow);
  font-weight: 700;
  letter-spacing: 0.05em;
}

.version {
  color: var(--overlay0);
  font-size: 0.85em;
}

.phase-badge {
  color: var(--yellow);
  font-weight: 700;
}

.phase-badge.done { color: var(--green); }
.phase-badge.idle { color: var(--green); }
.phase-badge.paused { color: var(--red); font-weight: 700; }

.header-line2 {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.15rem 1ch;
  padding: 0.15rem 0;
  font-size: 0.9em;
}

.progress-bar {
  display: inline-flex;
  align-items: center;
  gap: 0.5ch;
}

.bar-track {
  display: inline-block;
  white-space: nowrap;
  letter-spacing: -0.05em;
}

.bar-filled { color: var(--green); }
.bar-empty { color: var(--overlay0); }

.bar-label {
  color: var(--overlay0);
}

.elapsed {
  color: var(--subtext1);
}

.cost-display {
  color: var(--yellow);
}

.separator {
  margin: 0.15rem 0 0.25rem;
  overflow: hidden;
}

.sep-line {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  font-size: 0.9em;
  line-height: 1.4;
  text-align: center;
}

.sep-accent { color: var(--claude); font-weight: 700; }
.sep-heavy { color: var(--claude); }
.sep-medium { color: var(--surface2); }
.sep-light { color: var(--surface1); }

/* Pulse sweep: a bright arc radiates outward from the center ✦ */
.separator .sweep-overlay {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  pointer-events: none;
  opacity: 0;
  background: radial-gradient(ellipse at 50%% 50%%, rgba(249,132,92,0.6) 0%%, transparent 40%%);
  mix-blend-mode: screen;
}

.separator.running {
  position: relative;
}

.separator.running .sweep-overlay {
  animation: arc-sweep 1.5s cubic-bezier(0.16,1,0.3,1) infinite;
}

@keyframes arc-sweep {
  0%% {
    opacity: 0.9;
    transform: scaleX(0.05);
  }
  15%% {
    opacity: 0.7;
  }
  100%% {
    opacity: 0;
    transform: scaleX(3);
  }
}

/* ── Badges ────────────────────────────────── */

.badges {
  display: flex;
  flex-wrap: wrap;
  gap: 0 1.5ch;
  font-size: 0.85em;
}

.badge {
  white-space: nowrap;
  font-weight: 700;
}

.badge-judge { color: var(--green); }
.badge-quality { color: var(--teal); }
.badge-workers { color: var(--sky); }
.badge-ntfy { color: var(--green); }

/* ── Stuck Alert ───────────────────────────── */

.stuck-bar {
  background: var(--red);
  color: var(--crust);
  font-weight: 700;
  padding: 0.3rem 1ch;
  margin: 0.35rem 0;
  display: none;
}

.stuck-bar.visible { display: block; }

/* ── Panel frame ───────────────────────────── */

.panel {
  margin: 0.5rem 0;
  overflow: hidden;
  min-width: 0;
}

.panel-top {
  color: var(--surface2);
  font-size: 0.9em;
  line-height: 1;
  white-space: nowrap;
  overflow: hidden;
}

.panel-top .corner { color: var(--surface2); }
.panel-top .dash { color: var(--surface2); }
.panel-top .ptitle { color: var(--blue); font-weight: 700; }
.panel-top .picon { color: var(--claude); font-weight: 700; }

.panel-body {
  padding: 0.2rem 0;
  position: relative;
  margin: 0 0.25ch;
  border-left: 1px solid var(--surface2);
  border-right: 1px solid var(--surface2);
  border-image: repeating-linear-gradient(
    to bottom,
    var(--surface2) 0, var(--surface2) 4px,
    transparent 4px, transparent 8px
  ) 1;
}

.panel-content {
  padding: 0 1.5ch;
  min-height: 2rem;
  overflow: hidden;
}

.panel-bottom {
  color: var(--surface2);
  font-size: 0.9em;
  line-height: 1;
  white-space: nowrap;
  overflow: hidden;
}

/* ── Ornate Claude panel ───────────────────── */

.panel-ornate .panel-top .dash { color: var(--surface2); }
.panel-ornate .panel-top .star { color: var(--claude); font-weight: 700; }

/* ── Stories ────────────────────────────────── */

.story-row {
  display: grid;
  grid-template-columns: auto 2ch minmax(6ch, auto) 1fr;
  gap: 0 0.5ch;
  padding: 0.15rem 0;
  align-items: baseline;
  min-height: 1.45em;
}

.story-tree {
  color: var(--overlay0);
  white-space: pre;
  font-size: 0.9em;
}

.story-icon {
  text-align: center;
  flex-shrink: 0;
}

.story-icon.done { color: var(--green); }
.story-icon.running { color: var(--claude); font-weight: 700; }
.story-icon.failed { color: var(--red); }
.story-icon.queued { color: var(--overlay0); }
.story-icon.stuck { color: var(--red); }

.story-id {
  font-weight: 700;
  white-space: nowrap;
}

.story-id.done { color: var(--green); }
.story-id.running { color: var(--claude); }
.story-id.failed { color: var(--red); }
.story-id.queued { color: var(--overlay0); }
.story-id.stuck { color: var(--red); }

.story-title {
  color: var(--subtext1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.story-title.done {
  color: var(--overlay0);
  text-decoration: line-through;
}

.story-title.failed, .story-title.stuck {
  color: var(--red);
}

.story-meta {
  grid-column: 3 / -1;
  color: var(--overlay0);
  font-size: 0.85em;
  padding-left: 0;
}

.story-meta .role-tag {
  color: var(--teal);
}

.story-meta .worker-tag {
  color: var(--sky);
  font-weight: 700;
}

.story-meta .iter-tag {
  color: var(--overlay0);
}

.story-meta .task-status-tag {
  color: var(--overlay0);
  font-size: 0.9em;
}

.story-icon.interactive { color: var(--yellow); font-weight: 700; }
.story-id.interactive { color: var(--yellow); }

/* ── Current task & plan quality ─────────── */

.current-task {
  color: var(--subtext1);
  font-size: 0.9em;
}

.plan-quality {
  font-weight: 700;
  font-size: 0.9em;
}

.plan-quality.good { color: var(--green); }
.plan-quality.ok { color: var(--peach); }
.plan-quality.poor { color: var(--red); }

.plan-quality-detail {
  color: var(--overlay0);
  font-size: 0.85em;
}

.completion-reason {
  color: var(--red);
  font-size: 0.9em;
}

.completion-reason.success {
  color: var(--green);
}

/* ── Settings tab ────────────────────────── */

.settings-list {
  padding: 0.25rem 0;
}

.settings-row {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 0 2ch;
  padding: 0.1rem 0;
  font-size: 0.9em;
}

.settings-label {
  color: var(--subtext1);
}

.settings-value {
  color: var(--text);
  font-weight: 700;
  text-align: right;
}

.settings-value.val-true { color: var(--green); }
.settings-value.val-false { color: var(--red); }

/* Spinner for running stories */
.spinner {
  display: inline-block;
}

.spinner::after {
  content: "⠋";
  animation: braille-spin 0.8s steps(10) infinite;
}

@keyframes braille-spin {
  0%% { content: "⠋"; }
  10%% { content: "⠙"; }
  20%% { content: "⠹"; }
  30%% { content: "⠸"; }
  40%% { content: "⠼"; }
  50%% { content: "⠴"; }
  60%% { content: "⠦"; }
  70%% { content: "⠧"; }
  80%% { content: "⠇"; }
  90%% { content: "⠏"; }
}

/* ── Mini progress bar ─────────────────────── */

.mini-progress {
  display: flex;
  align-items: center;
  gap: 0.5ch;
  padding: 0.15rem 0 0.35rem;
  font-size: 0.9em;
}

.mini-bar {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  letter-spacing: -0.05em;
}

.mini-filled { color: var(--green); }
.mini-empty { color: var(--overlay0); }
.mini-label { color: var(--overlay0); white-space: nowrap; }

/* ── Context tabs ──────────────────────────── */

.ctx-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0.15rem 0;
  padding: 0.15rem 0 0.35rem;
  overflow: hidden;
}

.ctx-tab {
  padding: 0.25rem 0.75ch;
  min-height: 2rem;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  color: var(--overlay0);
  font-size: 0.9em;
  border: none;
  background: none;
  font-family: inherit;
  white-space: nowrap;
  transition: color 0.15s ease-out, background 0.15s ease-out;
  display: inline-flex;
  align-items: center;
}

.ctx-tab.active {
  background: var(--claude);
  color: var(--crust);
  font-weight: 700;
}

.ctx-tab:not(.active):hover {
  color: var(--subtext1);
}

.ctx-tab:not(.active):active {
  color: var(--text);
}

.ctx-content {
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--subtext0);
  font-size: 0.9em;
  min-height: 3rem;
  max-height: 40dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: thin;
  scrollbar-color: var(--surface1) transparent;
}

.ctx-content::-webkit-scrollbar { width: 4px; }
.ctx-content::-webkit-scrollbar-track { background: transparent; }
.ctx-content::-webkit-scrollbar-thumb { background: var(--surface1); border-radius: 2px; }

.ctx-placeholder {
  color: var(--overlay0);
  font-style: italic;
}

/* ── Claude Activity panel ─────────────────── */

.claude-title {
  display: flex;
  align-items: center;
  gap: 0.5ch;
  padding: 0.15rem 0 0.25rem;
}

.claude-sparkle {
  color: var(--claude);
  font-weight: 700;
}

.claude-sparkle.running {
  animation: sparkle-pulse 1.2s ease-in-out infinite;
}

@keyframes sparkle-pulse {
  0%%, 100%% { opacity: 1; }
  50%% { opacity: 0.4; }
}

.claude-label {
  color: var(--blue);
  font-weight: 700;
}

.claude-scroll {
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--subtext0);
  font-size: 0.9em;
  max-height: 35dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: thin;
  scrollbar-color: var(--surface1) transparent;
}

.claude-scroll::-webkit-scrollbar { width: 4px; }
.claude-scroll::-webkit-scrollbar-track { background: transparent; }
.claude-scroll::-webkit-scrollbar-thumb { background: var(--surface1); border-radius: 2px; }

.claude-empty {
  color: var(--overlay0);
  padding: 0.5rem 0;
}

/* ── Worker tabs inside Claude panel ──── */

.worker-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0.15rem 0;
  padding: 0.15rem 0 0.25rem;
  overflow: hidden;
}

.worker-tab {
  padding: 0.2rem 0.6ch;
  min-height: 1.8rem;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  color: var(--overlay0);
  font-size: 0.8em;
  border: none;
  background: none;
  font-family: inherit;
  white-space: nowrap;
  transition: color 0.15s ease-out, background 0.15s ease-out;
  display: inline-flex;
  align-items: center;
  gap: 0.3ch;
}

.worker-tab.active {
  background: var(--claude);
  color: var(--crust);
  font-weight: 700;
}

.worker-tab:not(.active):hover {
  color: var(--subtext1);
}

.worker-tab .wt-id { font-weight: 700; }
.worker-tab .wt-story { }
.worker-tab .wt-state { opacity: 0.7; }
.worker-tab .wt-sep { color: var(--surface2); }

/* ── Footer ────────────────────────────────── */

.footer {
  color: var(--overlay0);
  font-size: 0.8em;
  padding: 0.35rem 0;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.footer .live-indicator {
  display: inline-flex;
  align-items: center;
  gap: 0.5ch;
}

.live-dot {
  display: inline-block;
  width: 0.5em;
  height: 0.5em;
  background: var(--green);
  border-radius: 50%%;
  animation: dot-pulse 2s ease-in-out infinite;
}

.live-dot.disconnected {
  background: var(--red);
  animation: none;
}

@keyframes dot-pulse {
  0%%, 100%% { opacity: 1; }
  50%% { opacity: 0.3; }
}

.updated-at {
  color: var(--surface2);
}

/* ── Rate limit ────────────────────────────── */

.rate-limit-bar {
  background: var(--surface0);
  padding: 0.25rem 1ch;
  margin: 0.25rem 0;
  font-size: 0.85em;
  display: none;
}

.rate-limit-bar.visible {
  display: block;
}

.rate-limit-bar .rl-label { color: var(--yellow); font-weight: 700; }
.rate-limit-bar .rl-value { color: var(--subtext0); }

/* ── Responsive: collapse to single column on narrow ── */

@media (max-width: 480px) {
  html { font-size: 13px; }
  .shell { padding: 0.35rem; }
  .story-row {
    grid-template-columns: auto 2ch auto 1fr;
  }
  .ctx-tab { padding: 0.1rem 0.5ch; font-size: 0.85em; }
}

@media (min-width: 768px) {
  .two-col {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0 0.5rem;
    align-items: start;
    overflow: hidden;
  }
  .two-col > .panel {
    min-width: 0;
  }
}

/* Touch devices: larger tap targets */
@media (pointer: coarse) {
  .shell { padding-top: 0.75rem; }
  .ctx-tab { min-height: 2.75rem; padding: 0.4rem 1ch; }
}

/* Reduced motion */
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>
</head>
<body>
<div class="shell">

  <!-- Header -->
  <div class="header">
    <div class="header-line1">
      <span id="diamond" class="diamond%s">❖</span>
      <span class="title">RALPH</span>
      <span class="version" id="version">%s</span>
      <span style="color:var(--surface2)">┃</span>
      <span id="phase" class="phase-badge%s">%s %s</span>
      <span style="color:var(--surface2)">│</span>
      <span id="current-task" class="current-task">%s</span>
      <span id="plan-quality"></span>
      <span id="completion-reason"></span>
    </div>
    <div class="header-line2">
      <span class="progress-bar">
        <span class="bar-track" id="bar-track">%s</span>
        <span class="bar-label" id="bar-label">%d/%d</span>
      </span>
      <span style="color:var(--surface2)">│</span>
      <span class="elapsed">⏱ <span id="elapsed">%s</span></span>
      <span style="color:var(--surface2)">│</span>
      <span class="cost-display" id="cost-display">%s</span>
    </div>
    <div class="badges" id="badges">%s</div>
  </div>

  <div class="separator%s">
    <span class="sep-line">%s</span>
    <div class="sweep-overlay"></div>
  </div>

  <!-- Stuck alert -->
  <div id="stuck-bar" class="stuck-bar%s">%s</div>

  <!-- Rate limit -->
  <div id="rate-limit" class="rate-limit-bar%s">%s</div>

  <!-- Two column on desktop, stacked on mobile -->
  <div class="two-col">

    <!-- Stories Panel -->
    <div class="panel">
      <div class="panel-top"><span class="corner">╭</span><span class="dash">─</span> <span class="picon">◆</span> <span class="ptitle">Stories</span> <span class="dash" id="stories-top-fill">%s</span><span class="corner">╮</span></div>
      <div class="panel-body">
        <div class="panel-content">
          <div class="mini-progress" id="mini-progress">%s</div>
          <div id="stories">%s</div>
        </div>
      </div>
      <div class="panel-bottom"><span class="corner">╰</span><span class="dash" id="stories-bot-fill">%s</span><span class="corner">╯</span></div>
    </div>

    <!-- Context Panel -->
    <div class="panel">
      <div class="panel-top"><span class="corner">╭</span><span class="dash">─</span> <span class="picon">◈</span> <span class="ptitle">Context</span> <span class="dash" id="ctx-top-fill">%s</span><span class="corner">╮</span></div>
      <div class="panel-body">
        <div class="panel-content">
          <div class="ctx-tabs" id="ctx-tabs">
            <button class="ctx-tab active" data-tab="progress">◈ Progress</button>
            <button class="ctx-tab" data-tab="worktree">⌥ Tree</button>
            <button class="ctx-tab" data-tab="judge">⚖ Judge</button>
            <button class="ctx-tab" data-tab="quality">◇ Quality</button>
            <button class="ctx-tab" data-tab="memory">⧫ Memory</button>
            <button class="ctx-tab" data-tab="usage">◎ Usage</button>
            <button class="ctx-tab" data-tab="settings">⚙ Settings</button>
          </div>
          <div id="ctx-content" class="ctx-content">%s</div>
        </div>
      </div>
      <div class="panel-bottom"><span class="corner">╰</span><span class="dash" id="ctx-bot-fill">%s</span><span class="corner">╯</span></div>
    </div>

  </div>

  <!-- Claude Activity Panel (ornate) -->
  <div class="panel panel-ornate">
    <div class="panel-top"><span class="corner">╭</span><span class="dash">─</span><span class="star">✦</span><span class="dash" id="claude-top-fill">%s</span><span class="star">✦</span><span class="dash">─</span><span class="corner">╮</span></div>
    <div class="panel-body">
      <div class="panel-content">
        <div class="claude-title">
          <span id="claude-sparkle" class="claude-sparkle%s">✻</span>
          <span class="claude-label">Claude</span>
        </div>
        <div id="worker-tabs" class="worker-tabs" style="display:none"></div>
        <div id="claude-activity" class="claude-scroll">%s</div>
      </div>
    </div>
    <div class="panel-bottom"><span class="corner">╰</span><span class="dash">─</span><span class="star">✦</span><span class="dash" id="claude-bot-fill">%s</span><span class="star">✦</span><span class="dash">─</span><span class="corner">╯</span></div>
  </div>

  <!-- Footer -->
  <div class="footer">
    <span class="live-indicator"><span id="live-dot" class="live-dot"></span> <span id="conn-status">connected</span></span>
    <span class="updated-at" id="updated-at"></span>
  </div>

</div>

<script>
(function(){
  "use strict";

  // State
  var currentTab = "progress";
  var ctxData = { progress: "", worktree: "", judge: "", quality: "", memory: "", usage: "", settings: "" };
  var activeWorker = null; // null = show main claude_activity, or worker ID int
  var workerLogs = {};     // worker_id -> log content

  // Elements
  var $ = function(id){ return document.getElementById(id); };

  // ── Helpers ──
  function esc(s) {
    if (!s) return "";
    var d = document.createElement("div");
    d.textContent = s;
    return d.innerHTML;
  }

  function buildBar(done, total, width) {
    if (total === 0) return { track: "", label: "0/0" };
    var filled = Math.round((done / total) * width);
    if (filled > width) filled = width;
    var empty = width - filled;
    var track = '<span class="bar-filled">' + "━".repeat(filled) + '</span>' +
                '<span class="bar-empty">' + "─".repeat(empty) + '</span>';
    return { track: track, label: done + "/" + total };
  }

  function buildMiniBar(done, total) {
    if (total === 0) return "";
    var w = Math.min(24, Math.max(8, Math.floor(window.innerWidth / 20)));
    var filled = Math.round((done / total) * w);
    var empty = w - filled;
    return '<span class="mini-filled">' + "━".repeat(filled) + '</span>' +
           '<span class="mini-empty">' + "─".repeat(empty) + '</span>' +
           ' <span class="mini-label">' + done + '/' + total + '</span>';
  }

  function storyIcon(status, isInteractive) {
    if (isInteractive) return "⚡";
    switch(status) {
      case "done": return "✓";
      case "running": return '<span class="spinner"></span>';
      case "failed": return "✗";
      case "stuck": return "✗";
      default: return "○";
    }
  }

  // treeLayout computes tree positions from story dependency data (mirrors TUI treeLayout)
  function treeLayout(stories) {
    if (!stories || stories.length === 0) return [];

    var hasDeps = false;
    for (var i = 0; i < stories.length; i++) {
      if (stories[i].depends_on && stories[i].depends_on.length > 0) { hasDeps = true; break; }
    }
    if (!hasDeps) {
      // Flat list — no tree
      var flat = [];
      for (var i = 0; i < stories.length; i++) flat.push({ idx: i, depth: 0, connector: "" });
      return flat;
    }

    // Build index and children map
    var idToIdx = {};
    for (var i = 0; i < stories.length; i++) idToIdx[stories[i].id] = i;

    var children = {};
    var roots = [];
    for (var i = 0; i < stories.length; i++) {
      var s = stories[i];
      var deps = s.depends_on;
      if (!deps || deps.length === 0) {
        roots.push(s.id);
        continue;
      }
      var parent = deps[deps.length - 1];
      if (!children[parent]) children[parent] = [];
      children[parent].push(s.id);
    }

    // Sort children by original index
    for (var k in children) {
      children[k].sort(function(a, b) { return (idToIdx[a] || 0) - (idToIdx[b] || 0); });
    }

    var entries = [];
    function walk(id, depth, isLast, parentPrefixes) {
      var idx = idToIdx[id];
      if (idx === undefined) return;

      var connector = "";
      if (depth > 0) {
        connector = parentPrefixes + (isLast ? "└─" : "├─");
      }

      entries.push({ idx: idx, depth: depth, connector: connector });

      var ch = children[id] || [];
      var childPrefix = parentPrefixes;
      if (depth > 0) {
        childPrefix += isLast ? "  " : "│ ";
      }
      for (var i = 0; i < ch.length; i++) {
        walk(ch[i], depth + 1, i === ch.length - 1, childPrefix);
      }
    }

    for (var i = 0; i < roots.length; i++) {
      walk(roots[i], 0, i === roots.length - 1, "");
    }

    return entries;
  }

  function renderStoryRow(s, treeConnector) {
    var isInteractive = s.is_interactive || false;
    var iconClass = isInteractive ? "interactive" : esc(s.status);
    var idClass = isInteractive ? "interactive" : esc(s.status);

    var meta = "";
    var metaParts = [];
    if (s.role) metaParts.push('<span class="role-tag">' + esc(s.role) + '</span>');
    if (s.iteration > 0) metaParts.push('<span class="iter-tag">iter ' + s.iteration + '</span>');
    if (s.worker_id > 0) metaParts.push('<span class="worker-tag">W' + s.worker_id + '</span>');
    if (isInteractive && s.task_status && s.status !== "running") {
      metaParts.push('<span class="task-status-tag">[' + esc(s.task_status) + ']</span>');
    }
    if (metaParts.length > 0) {
      meta = '<div class="story-meta">' + metaParts.join(" · ") + '</div>';
    }

    var treeHTML = '<span class="story-tree">' + esc(treeConnector || "") + '</span>';

    return '<div class="story-row">' +
      treeHTML +
      '<span class="story-icon ' + iconClass + '">' + storyIcon(s.status, isInteractive) + '</span>' +
      '<span class="story-id ' + idClass + '">' + esc(s.id) + '</span>' +
      '<span class="story-title ' + esc(s.status) + '">' + esc(s.title) + '</span>' +
      meta +
    '</div>';
  }

  function renderSettingsContent(settings) {
    if (!settings || settings.length === 0) return '<span class="ctx-placeholder">No settings data</span>';
    var html = '<div class="settings-list">';
    for (var i = 0; i < settings.length; i++) {
      var s = settings[i];
      var valClass = "settings-value";
      if (s.value === "true") valClass += " val-true";
      else if (s.value === "false") valClass += " val-false";
      html += '<div class="settings-row"><span class="settings-label">' + esc(s.label) +
        '</span><span class="' + valClass + '">' + esc(s.value) + '</span></div>';
    }
    html += '</div>';
    return html;
  }

  function phaseClass(phase) {
    var p = (phase || "").toLowerCase();
    if (p === "complete") return " done";
    if (p === "idle") return " idle";
    if (p === "paused") return " paused";
    return "";
  }

  function fillDash(n) { return "─".repeat(Math.max(0, n)); }

  // ── Tab switching ──
  var tabButtons = document.querySelectorAll(".ctx-tab");
  for (var i = 0; i < tabButtons.length; i++) {
    tabButtons[i].addEventListener("click", function() {
      currentTab = this.getAttribute("data-tab");
      for (var j = 0; j < tabButtons.length; j++) {
        tabButtons[j].classList.toggle("active", tabButtons[j] === this);
      }
      showTab();
    });
  }

  function showTab() {
    var el = $("ctx-content");
    if (currentTab === "settings") {
      el.innerHTML = ctxData.settings || '<span class="ctx-placeholder">Waiting for data...</span>';
      return;
    }
    var content = ctxData[currentTab] || "";
    if (content) {
      el.textContent = content;
    } else {
      el.innerHTML = '<span class="ctx-placeholder">Waiting for data...</span>';
    }
  }

  // Fill panel dashes based on actual panel pixel widths
  function charWidth() {
    // Measure one monospace character
    var m = document.createElement("span");
    m.style.cssText = "position:absolute;visibility:hidden;font:inherit;white-space:pre";
    m.textContent = "─";
    document.body.appendChild(m);
    var cw = m.getBoundingClientRect().width || 8;
    document.body.removeChild(m);
    return cw;
  }

  function fillPanels() {
    var ch = charWidth();
    // Measure each panel's actual width
    var panels = document.querySelectorAll(".panel");
    panels.forEach(function(panel) {
      var pw = Math.floor(panel.getBoundingClientRect().width / ch);
      var topFill = panel.querySelector("[id$='-top-fill']");
      var botFill = panel.querySelector("[id$='-bot-fill']");
      if (topFill) topFill.textContent = fillDash(Math.max(0, pw - 16));
      if (botFill) botFill.textContent = fillDash(Math.max(0, pw - 4));
    });
    // Claude ornate panel uses ━
    var claudePanel = document.querySelector(".panel-ornate");
    if (claudePanel) {
      var cw = Math.floor(claudePanel.getBoundingClientRect().width / ch);
      var fill = Math.max(0, cw - 10);
      $("claude-top-fill").textContent = "━".repeat(fill);
      $("claude-bot-fill").textContent = "━".repeat(fill);
    }
  }
  fillPanels();
  window.addEventListener("resize", fillPanels);

  // ── SSE Connection ──
  // Browsers keep the entire SSE response body in memory. Reconnecting
  // periodically releases that buffer and prevents unbounded growth.
  var es;
  var retryDelay = 1000;
  var msgCount = 0;
  var MAX_MSGS = 200; // reconnect every ~200 messages to flush browser buffer

  function connect() {
    msgCount = 0;
    es = new EventSource("/events");

    es.onopen = function() {
      retryDelay = 1000;
      $("live-dot").classList.remove("disconnected");
      $("conn-status").textContent = "connected";
    };

    es.onmessage = function(e) {
      try {
        var d = JSON.parse(e.data);
        applyState(d);
      } catch(err) {}
      msgCount++;
      if (msgCount >= MAX_MSGS) {
        es.close();
        setTimeout(connect, 50);
      }
    };

    es.onerror = function() {
      $("live-dot").classList.add("disconnected");
      $("conn-status").textContent = "reconnecting...";
      es.close();
      setTimeout(connect, retryDelay);
      retryDelay = Math.min(retryDelay * 2, 30000);
    };
  }

  function applyState(d) {
    // Title
    document.title = "ralph " + (d.phase || "") + " — " + (d.prd_name || "");

    // Header line 1
    var diamondEl = $("diamond");
    if (d.running) {
      diamondEl.classList.add("running");
    } else {
      diamondEl.classList.remove("running");
    }
    $("version").textContent = d.version || "";
    $("phase").textContent = (d.phase_icon || "") + " " + (d.phase || "");
    $("phase").className = "phase-badge" + phaseClass(d.phase);

    // Current task
    $("current-task").textContent = d.current_task || "";

    // Plan quality (shown when data exists)
    var pqEl = $("plan-quality");
    if (d.plan_quality && d.plan_quality.total_stories > 0) {
      var score = d.plan_quality.score;
      var pqClass = "plan-quality";
      if (score >= 0.8) pqClass += " good";
      else if (score >= 0.5) pqClass += " ok";
      else pqClass += " poor";
      pqEl.className = pqClass;
      pqEl.innerHTML = "Plan: " + Math.round(score * 100) + "%%" +
        ' <span class="plan-quality-detail">(' +
        d.plan_quality.first_pass_count + " first-pass, " +
        d.plan_quality.retry_count + " retried, " +
        d.plan_quality.failed_count + " failed)</span>";
    } else {
      pqEl.innerHTML = "";
    }

    // Completion reason
    var crEl = $("completion-reason");
    if (d.completion_reason && d.phase === "Complete") {
      crEl.textContent = d.completion_reason;
      crEl.className = d.all_complete ? "completion-reason success" : "completion-reason";
    } else {
      crEl.textContent = "";
    }

    // Header line 2
    var bar = buildBar(d.completed || 0, d.total || 0, 16);
    $("bar-track").innerHTML = bar.track;
    $("bar-label").textContent = bar.label;
    $("elapsed").textContent = d.run_duration || "0s";
    $("cost-display").textContent = d.cost_display || "—";

    // Badges
    var badgesHTML = "";
    if (d.badges) {
      for (var i = 0; i < d.badges.length; i++) {
        var b = d.badges[i];
        var cls = "badge";
        var label = b.label.toLowerCase();
        if (label.indexOf("judge") >= 0) cls += " badge-judge";
        else if (label.indexOf("quality") >= 0) cls += " badge-quality";
        else if (label.indexOf("worker") >= 0) cls += " badge-workers";
        else if (label.indexOf("ntfy") >= 0) cls += " badge-ntfy";
        badgesHTML += '<span class="' + cls + '">' + esc(b.icon) + ' ' + esc(b.label) + '</span>';
      }
    }
    $("badges").innerHTML = badgesHTML;

    // Separator
    var sep = document.querySelector(".separator");
    if (d.running) {
      sep.classList.add("running");
    } else {
      sep.classList.remove("running");
    }

    // Stuck alert
    var stuckEl = $("stuck-bar");
    if (d.stuck_alert) {
      stuckEl.textContent = d.stuck_alert;
      stuckEl.classList.add("visible");
    } else {
      stuckEl.classList.remove("visible");
    }

    // Rate limit
    var rlEl = $("rate-limit");
    if (d.rate_limit && d.rate_limit.has_limit) {
      rlEl.innerHTML = '<span class="rl-label">Plan Usage</span> ' +
        '<span class="rl-value">' + esc(d.rate_limit.window) + ' · ' + esc(d.rate_limit.status) +
        (d.rate_limit.resets_in ? ' · resets in ' + esc(d.rate_limit.resets_in) : '') + '</span>';
      rlEl.classList.add("visible");
    } else {
      rlEl.classList.remove("visible");
    }

    // Stories with tree layout
    var storiesHTML = "";
    var done = 0;
    if (d.stories) {
      var layout = treeLayout(d.stories);
      for (var i = 0; i < layout.length; i++) {
        var entry = layout[i];
        var s = d.stories[entry.idx];
        storiesHTML += renderStoryRow(s, entry.connector);
        if (s.status === "done") done++;
      }
    }
    $("stories").innerHTML = storiesHTML;

    // Mini progress
    $("mini-progress").innerHTML = buildMiniBar(d.completed || 0, d.total || 0);

    // Context tab data — all 7 tabs mirroring the TUI
    if (d.progress_content !== undefined) ctxData.progress = d.progress_content;
    if (d.worktree_content !== undefined) ctxData.worktree = d.worktree_content;
    if (d.judge_content !== undefined) ctxData.judge = d.judge_content;
    if (d.quality_content !== undefined) ctxData.quality = d.quality_content;
    if (d.memory_content !== undefined) ctxData.memory = d.memory_content;
    if (d.costs_content !== undefined) ctxData.usage = d.costs_content;
    if (d.settings !== undefined) ctxData.settings = renderSettingsContent(d.settings);
    showTab();

    // Worker tabs and logs
    var wtEl = $("worker-tabs");
    if (d.worker_tabs && d.worker_tabs.length > 0) {
      // Update stored worker logs
      if (d.worker_logs) {
        for (var wid in d.worker_logs) {
          workerLogs[wid] = d.worker_logs[wid];
        }
      }
      // Render worker tab bar
      var wtHTML = "";
      for (var i = 0; i < d.worker_tabs.length; i++) {
        var wt = d.worker_tabs[i];
        var isActive = (activeWorker === wt.worker_id) || (activeWorker === null && wt.active);
        wtHTML += '<button class="worker-tab' + (isActive ? ' active' : '') + '" data-wid="' + wt.worker_id + '">' +
          '<span class="wt-id">W' + wt.worker_id + '</span>' +
          '<span class="wt-story">' + esc(wt.story_id) + '</span>' +
          (wt.role ? ' <span class="wt-state">' + esc(wt.role) + '</span>' : '') +
          '<span class="wt-state">[' + esc(wt.state) + ']</span>' +
          '</button>';
        if (i < d.worker_tabs.length - 1) wtHTML += '<span class="wt-sep">│</span>';
      }
      wtEl.innerHTML = wtHTML;
      wtEl.style.display = "flex";

      // Bind click handlers
      var wtBtns = wtEl.querySelectorAll(".worker-tab");
      for (var i = 0; i < wtBtns.length; i++) {
        wtBtns[i].addEventListener("click", function() {
          activeWorker = parseInt(this.getAttribute("data-wid"), 10);
          // Update active state visually
          var all = wtEl.querySelectorAll(".worker-tab");
          for (var j = 0; j < all.length; j++) all[j].classList.remove("active");
          this.classList.add("active");
          showWorkerLog();
        });
      }

      // Auto-select first active worker if none selected
      if (activeWorker === null) {
        for (var i = 0; i < d.worker_tabs.length; i++) {
          if (d.worker_tabs[i].active) {
            activeWorker = d.worker_tabs[i].worker_id;
            break;
          }
        }
      }
    } else {
      wtEl.style.display = "none";
      activeWorker = null;
      workerLogs = {};
    }

    // Claude activity — show per-worker log if tabs are active
    var claudeEl = $("claude-activity");
    var sparkleEl = $("claude-sparkle");
    if (d.worker_tabs && d.worker_tabs.length > 0) {
      showWorkerLog();
    } else if (d.claude_activity) {
      claudeEl.textContent = d.claude_activity;
      claudeEl.scrollTop = claudeEl.scrollHeight;
    } else {
      claudeEl.innerHTML = '<span class="claude-empty">Monitoring...</span>';
    }
    if (d.running) {
      sparkleEl.classList.add("running");
    } else {
      sparkleEl.classList.remove("running");
    }

    // Updated at
    if (d.updated_at) {
      var t = new Date(d.updated_at);
      $("updated-at").textContent = t.toLocaleTimeString();
    }
  }

  function showWorkerLog() {
    var claudeEl = $("claude-activity");
    if (activeWorker !== null && workerLogs[activeWorker]) {
      claudeEl.textContent = workerLogs[activeWorker];
      claudeEl.scrollTop = claudeEl.scrollHeight;
    } else {
      claudeEl.innerHTML = '<span class="claude-empty">Waiting for worker output...</span>';
    }
  }

  connect();
})();
</script>
</body>
</html>`,
		esc(state.PRDName), esc(state.Phase),
		runningClass(state.Running), esc(state.Version),
		phaseClass(state.Phase), esc(state.PhaseIcon), esc(state.Phase),
		esc(state.CurrentTask),
		renderBarTrack(state.Completed, state.Total, 16),
		state.Completed, state.Total,
		esc(state.RunDuration),
		esc(state.CostDisplay),
		renderBadgesHTML(state.Badges),
		runningClass(state.Running),
		renderSepLine(40),
		stuckVisibleClass(state.StuckAlert), esc(state.StuckAlert),
		rateLimitVisibleClass(state.RateLimit), renderRateLimitHTML(state.RateLimit),
		repeatDash(20), renderMiniBarHTML(state.Completed, state.Total),
		storiesHTML, repeatDash(30),
		repeatDash(20),
		initialCtx,
		repeatDash(30),
		repeatHeavy(40),
		runningClass(state.Running),
		claudeContent,
		repeatHeavy(40),
	)
}

// ── HTML helpers ──

func escapeHTML(s string) string {
	// Minimal HTML escaping for content inserted into pre-escaped contexts.
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b = append(b, []byte("&amp;")...)
		case '<':
			b = append(b, []byte("&lt;")...)
		case '>':
			b = append(b, []byte("&gt;")...)
		case '"':
			b = append(b, []byte("&quot;")...)
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}

func esc(s string) string {
	return escapeHTML(s)
}

func runningClass(running bool) string {
	if running {
		return " running"
	}
	return ""
}

func phaseClass(phase string) string {
	switch phase {
	case "Complete":
		return " done"
	case "Idle":
		return " idle"
	case "Paused":
		return " paused"
	default:
		return ""
	}
}

func stuckVisibleClass(alert string) string {
	if alert != "" {
		return " visible"
	}
	return ""
}

func rateLimitVisibleClass(rl RateLimitStatus) string {
	if rl.HasLimit {
		return " visible"
	}
	return ""
}

func renderRateLimitHTML(rl RateLimitStatus) string {
	if !rl.HasLimit {
		return ""
	}
	s := fmt.Sprintf(`<span class="rl-label">Plan Usage</span> <span class="rl-value">%s`, esc(rl.Window))
	if rl.Status != "" {
		s += " · " + esc(rl.Status)
	}
	if rl.ResetsIn != "" {
		s += " · resets in " + esc(rl.ResetsIn)
	}
	s += "</span>"
	return s
}

func renderBadgesHTML(badges []Badge) string {
	if len(badges) == 0 {
		return ""
	}
	s := ""
	for _, b := range badges {
		cls := "badge"
		switch {
		case b.Label == "Judge":
			cls += " badge-judge"
		case b.Label == "Quality":
			cls += " badge-quality"
		case len(b.Label) > 0 && b.Label[len(b.Label)-1] == 's' && b.Label != "Workers":
			// fallthrough
		case contains(b.Label, "Worker"):
			cls += " badge-workers"
		case b.Label == "ntfy":
			cls += " badge-ntfy"
		}
		s += fmt.Sprintf(`<span class="%s">%s %s</span>`, cls, esc(b.Icon), esc(b.Label))
	}
	return s
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > len(sub) && (s[:len(sub)] == sub || s[len(s)-len(sub):] == sub || containsInner(s, sub)))
}

func containsInner(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func renderBarTrack(done, total, width int) string {
	if total == 0 {
		return repeatStr("─", width)
	}
	filled := width * done / total
	if filled > width {
		filled = width
	}
	empty := width - filled
	return fmt.Sprintf(`<span class="bar-filled">%s</span><span class="bar-empty">%s</span>`,
		repeatStr("━", filled), repeatStr("─", empty))
}

func renderMiniBarHTML(done, total int) string {
	if total == 0 {
		return ""
	}
	w := 20
	filled := w * done / total
	if filled > w {
		filled = w
	}
	empty := w - filled
	return fmt.Sprintf(`<span class="mini-filled">%s</span><span class="mini-empty">%s</span> <span class="mini-label">%d/%d</span>`,
		repeatStr("━", filled), repeatStr("─", empty), done, total)
}

func renderSepLine(width int) string {
	// Build: ━━✦━━━━...━━━━✦━━
	if width < 6 {
		return repeatStr("─", width)
	}
	half := (width - 3) / 2
	return fmt.Sprintf(
		`<span class="sep-heavy">%s</span> <span class="sep-accent">✦</span> <span class="sep-heavy">%s</span>`,
		repeatStr("━", half), repeatStr("━", width-3-half))
}

func renderInitialCtxContent(progress string) string {
	if progress != "" {
		return escapeHTML(progress)
	}
	return `<span class="ctx-placeholder">Waiting for progress updates...</span>`
}

func renderStoryRow(st StoryStatus) string {
	iconClass := esc(st.Status)
	idClass := esc(st.Status)
	if st.IsInteractive {
		iconClass = "interactive"
		idClass = "interactive"
	}
	icon := storyIconHTML(st.Status, st.IsInteractive)
	meta := ""
	var parts []string
	if st.Role != "" {
		parts = append(parts, fmt.Sprintf(`<span class="role-tag">%s</span>`, esc(st.Role)))
	}
	if st.Iteration > 0 {
		parts = append(parts, fmt.Sprintf(`<span class="iter-tag">iter %d</span>`, st.Iteration))
	}
	if st.WorkerID > 0 {
		parts = append(parts, fmt.Sprintf(`<span class="worker-tag">W%d</span>`, st.WorkerID))
	}
	if st.IsInteractive && st.TaskStatus != "" && st.Status != "running" {
		parts = append(parts, fmt.Sprintf(`<span class="task-status-tag">[%s]</span>`, esc(st.TaskStatus)))
	}
	if len(parts) > 0 {
		meta = `<div class="story-meta">` + joinStrings(parts, " · ") + `</div>`
	}

	return fmt.Sprintf(
		`<div class="story-row"><span class="story-tree"></span><span class="story-icon %s">%s</span><span class="story-id %s">%s</span><span class="story-title %s">%s</span>%s</div>`,
		iconClass, icon,
		idClass, esc(st.ID),
		esc(st.Status), esc(st.Title),
		meta,
	)
}

func storyIconHTML(status string, isInteractive bool) string {
	if isInteractive {
		return "⚡"
	}
	switch status {
	case "done":
		return "✓"
	case "running":
		return `<span class="spinner"></span>`
	case "failed", "stuck":
		return "✗"
	default:
		return "○"
	}
}

func repeatStr(ch string, n int) string {
	if n <= 0 {
		return ""
	}
	s := ""
	for i := 0; i < n; i++ {
		s += ch
	}
	return s
}

func repeatDash(n int) string  { return repeatStr("─", n) }
func repeatHeavy(n int) string { return repeatStr("━", n) }

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += sep + p
	}
	return s
}
