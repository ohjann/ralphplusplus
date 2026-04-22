package daemon

import (
	"encoding/json"
	"time"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/roles"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// --- Event types sent from daemon to client ---

// DaemonStateEvent is a full snapshot of coordinator state, sufficient to render the TUI.
type DaemonStateEvent struct {
	Workers        map[worker.WorkerID]WorkerStatus `json:"workers"`
	Stories        map[string]StoryStatus            `json:"stories"`
	ActiveStoryIDs []string                          `json:"active_story_ids"`
	Phase          string                            `json:"phase"`
	Paused         bool                              `json:"paused"`
	TotalStories   int                               `json:"total_stories"`
	CompletedCount int                               `json:"completed_count"`
	FailedCount    int                               `json:"failed_count"`
	IterationCount int                               `json:"iteration_count"`
	AllDone        bool                              `json:"all_done"`
	CostTotals     CostTotals                        `json:"cost_totals"`
	PlanQuality    PlanQualityInfo                   `json:"plan_quality"`
	FusionMetrics  costs.FusionMetrics               `json:"fusion_metrics"`
	Uptime         string                            `json:"uptime"`
	ClientCount    int                               `json:"client_count"`
	Settings       SettingsSnapshot                  `json:"settings"`
	Timestamp      time.Time                         `json:"timestamp"`
}

// SettingsSnapshot mirrors the tunable subset of config.Config on the wire,
// so SPA clients can render the live values that ApplySettings updates.
// Field set is the same 19 tunables the SettingsRoute editor exposes.
type SettingsSnapshot struct {
	JudgeEnabled       bool   `json:"judge_enabled"`
	JudgeMaxRejections int    `json:"judge_max_rejections"`
	Workers            int    `json:"workers"`
	WorkersAuto        bool   `json:"workers_auto"`
	AutoMaxWorkers     int    `json:"auto_max_workers"`
	QualityReview      bool   `json:"quality_review"`
	QualityWorkers     int    `json:"quality_workers"`
	QualityMaxIters    int    `json:"quality_max_iterations"`
	MemoryDisable      bool   `json:"memory_disable"`
	NoArchitect        bool   `json:"no_architect"`
	NoSimplify         bool   `json:"no_simplify"`
	NoFusion           bool   `json:"no_fusion"`
	FusionWorkers      int    `json:"fusion_workers"`
	SpriteEnabled      bool   `json:"sprite_enabled"`
	WorkspaceBase      string `json:"workspace_base"`
	ModelOverride      string `json:"model_override"`
	ArchitectModel     string `json:"architect_model"`
	ImplementerModel   string `json:"implementer_model"`
	UtilityModel       string `json:"utility_model"`
}

// WorkerStatus mirrors the fields the TUI reads from worker.Worker.
type WorkerStatus struct {
	ID            worker.WorkerID `json:"id"`
	StoryID       string          `json:"story_id"`
	StoryTitle    string          `json:"story_title"`
	State         string          `json:"state"`
	Role          roles.Role      `json:"role"`
	Iteration     int             `json:"iteration"`
	ActivityPath  string          `json:"activity_path"`
	FusionSuffix  string          `json:"fusion_suffix,omitempty"`
}

// StoryStatus represents the current state of a story.
type StoryStatus struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	InProgress   bool   `json:"in_progress"`
	Completed    bool   `json:"completed"`
	Failed       bool   `json:"failed"`
	FailedError  string `json:"failed_error,omitempty"`
	BlockedByDep string `json:"blocked_by_dep,omitempty"`
}

// CostTotals summarises run-level cost data.
type CostTotals struct {
	TotalCost         float64 `json:"total_cost"`
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
}

// PlanQualityInfo mirrors coordinator.PlanQuality for the wire.
type PlanQualityInfo struct {
	FirstPassCount int     `json:"first_pass_count"`
	RetryCount     int     `json:"retry_count"`
	FailedCount    int     `json:"failed_count"`
	TotalStories   int     `json:"total_stories"`
	Score          float64 `json:"score"`
}

// WorkerLogEvent carries a log line from a specific worker.
type WorkerLogEvent struct {
	WorkerID  worker.WorkerID `json:"worker_id"`
	Line      string          `json:"line"`
	Timestamp time.Time       `json:"timestamp"`
}

// LogLineEvent is a general log line for the daemon event stream.
type LogLineEvent struct {
	Line      string    `json:"line"`
	Timestamp time.Time `json:"timestamp"`
}

// MergeResultEvent reports the outcome of a story merge.
type MergeResultEvent struct {
	StoryID string `json:"story_id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// StuckAlertEvent reports that a worker appears stuck.
type StuckAlertEvent struct {
	WorkerID    worker.WorkerID `json:"worker_id"`
	StoryID     string          `json:"story_id"`
	StuckReason string          `json:"stuck_reason"`
}

// PostRunPhaseEvent reports the start/end of a post-completion phase
// (quality review, memory synthesis, SUMMARY.md, retro, history,
// done). Viewers use this for first-class phase chips; a log_line
// with human detail is usually emitted alongside.
type PostRunPhaseEvent struct {
	Phase     string    `json:"phase"`             // see postcompletion.Phase* constants
	Status    string    `json:"status"`            // started, complete, error, skipped
	Message   string    `json:"message,omitempty"` // free-form detail (error text, iteration count)
	Iteration int       `json:"iteration,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// --- Envelope for polymorphic event deserialization ---

// Event type constants.
const (
	EventDaemonState   = "daemon_state"
	EventWorkerLog     = "worker_log"
	EventLogLine       = "log_line"
	EventMergeResult   = "merge_result"
	EventStuckAlert    = "stuck_alert"
	EventPostRunPhase  = "post_run_phase"
)

// DaemonEvent is the envelope sent over the wire. Type identifies the payload
// kind and Data holds the JSON-encoded event struct.
type DaemonEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// --- Command request types sent from client to daemon ---

// Command type constants.
const (
	CmdQuit     = "quit"
	CmdPause    = "pause"
	CmdResume   = "resume"
	CmdHint     = "hint"
	CmdTask     = "task"
	CmdSettings = "settings"
	CmdClarify  = "clarify"
)

// QuitRequest asks the daemon to shut down.
type QuitRequest struct{}

// PauseRequest asks the daemon to pause scheduling.
type PauseRequest struct{}

// ResumeRequest asks the daemon to resume scheduling.
type ResumeRequest struct{}

// HintRequest sends a hint to a specific worker.
type HintRequest struct {
	WorkerID worker.WorkerID `json:"worker_id"`
	Text     string          `json:"text"`
}

// TaskRequest adds an ad-hoc task.
type TaskRequest struct {
	Description string `json:"description"`
}

// SettingsRequest updates daemon settings.
type SettingsRequest struct{}

// ClarifyRequest sends a clarification response.
type ClarifyRequest struct {
	Text string `json:"text"`
}

// DaemonCommand is the envelope for commands sent from client to daemon.
type DaemonCommand struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
