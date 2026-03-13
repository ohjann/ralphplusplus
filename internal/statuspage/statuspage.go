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
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"` // queued, running, done, failed, stuck
	Cost      float64 `json:"cost"`
	Iteration int     `json:"iteration,omitempty"`
	Role      string  `json:"role,omitempty"` // e.g. "implementer", "architect"
	Detail    string  `json:"detail,omitempty"`
}

// StatusState mirrors what the TUI shows: phase, story statuses, costs, iteration info.
type StatusState struct {
	PRDName     string        `json:"prd_name"`
	Phase       string        `json:"phase"`
	RunDuration string        `json:"run_duration"`
	Stories     []StoryStatus `json:"stories"`
	TotalCost   float64       `json:"total_cost"`
	UpdatedAt   time.Time     `json:"updated_at"`
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

func statusEmoji(status string) string {
	switch status {
	case "done":
		return "&#x2705;"
	case "failed":
		return "&#x274C;"
	case "running":
		return "&#x2699;&#xFE0F;"
	case "stuck":
		return "&#x274C;"
	case "queued":
		return "&#x23F3;"
	default:
		return "&#x2B55;"
	}
}

func renderHTML(state StatusState) string {
	storiesHTML := ""
	doneCount := 0
	failedCount := 0
	runningCount := 0
	for _, st := range state.Stories {
		switch st.Status {
		case "done":
			doneCount++
		case "failed", "stuck":
			failedCount++
		case "running":
			runningCount++
		}
		costStr := "&mdash;"
		if st.Cost > 0 {
			costStr = fmt.Sprintf("$%.2f", st.Cost)
		}
		detail := ""
		if st.Detail != "" {
			detail = fmt.Sprintf(`<span class="detail">(%s)</span>`, st.Detail)
		} else if st.Iteration > 0 && st.Role != "" {
			detail = fmt.Sprintf(`<span class="detail">(iter %d, %s)</span>`, st.Iteration, st.Role)
		}
		storiesHTML += fmt.Sprintf(
			`<div class="story %s"><span class="emoji">%s</span><span class="id">%s</span><span class="status">%s</span><span class="cost">%s</span>%s</div>`,
			st.Status, statusEmoji(st.Status), st.ID, st.Status, costStr, detail,
		)
	}

	summary := fmt.Sprintf("%d/%d complete", doneCount, len(state.Stories))
	if failedCount > 0 {
		summary += fmt.Sprintf(" | %d failed", failedCount)
	}
	if runningCount > 0 {
		summary += fmt.Sprintf(" | %d in progress", runningCount)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Ralph &mdash; %s</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,sans-serif;
background:#0d1117;color:#c9d1d9;padding:16px;max-width:600px;margin:0 auto;
font-size:16px;line-height:1.5}
h1{font-size:1.3em;color:#58a6ff;margin-bottom:4px}
.meta{color:#8b949e;font-size:0.9em;margin-bottom:16px;border-bottom:1px solid #21262d;padding-bottom:12px}
.meta span{display:inline-block;margin-right:16px}
.summary{font-size:1em;margin-bottom:12px;color:#c9d1d9}
.cost-total{font-size:1.1em;color:#3fb950;font-weight:600;margin-bottom:16px}
.story{display:grid;grid-template-columns:28px 90px 70px 60px 1fr;align-items:center;
padding:8px 4px;border-bottom:1px solid #21262d;font-size:0.88em;gap:4px}
.story .emoji{text-align:center}
.story .id{font-weight:600;color:#58a6ff;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.story.done .status{color:#3fb950}
.story.failed .status,.story.stuck .status{color:#f85149}
.story.running .status{color:#d29922}
.story.queued .status{color:#8b949e}
.story .cost{text-align:right;color:#8b949e}
.story .detail{color:#8b949e;font-size:0.85em}
.live-dot{display:inline-block;width:8px;height:8px;background:#3fb950;border-radius:50%%;
margin-right:6px;animation:pulse 2s infinite}
@keyframes pulse{0%%,100%%{opacity:1}50%%{opacity:0.4}}
@media(max-width:480px){
  .story{grid-template-columns:24px 70px 60px 50px 1fr;font-size:0.82em}
  body{padding:12px}
}
</style>
</head>
<body>
<h1>Ralph &mdash; %s</h1>
<div class="meta">
  <span>Phase: %s</span>
  <span>Running: %s</span>
</div>
<div class="summary"><span class="live-dot"></span>Stories: %s</div>
<div class="cost-total">Cost: $%.2f</div>
<div class="stories">%s</div>
<script>
(function(){
  var es = new EventSource("/events");
  es.onmessage = function(e){
    try{
      var d = JSON.parse(e.data);
      document.location.reload();
    }catch(err){}
  };
  es.onerror = function(){
    setTimeout(function(){es=new EventSource("/events")},3000);
  };
})();
</script>
</body>
</html>`,
		state.PRDName,
		state.PRDName,
		state.Phase,
		state.RunDuration,
		summary,
		state.TotalCost,
		storiesHTML,
	)
}
