package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/eoghanhynes/ralph/internal/checkpoint"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/tui/sprite"
	"github.com/eoghanhynes/ralph/internal/costs"
	"github.com/eoghanhynes/ralph/internal/notify"
	"github.com/eoghanhynes/ralph/internal/statuspage"
	"github.com/eoghanhynes/ralph/internal/coordinator"
	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/events"
	"github.com/eoghanhynes/ralph/internal/interactive"
	"github.com/eoghanhynes/ralph/internal/judge"
	"github.com/eoghanhynes/ralph/internal/memory"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/quality"
	"github.com/eoghanhynes/ralph/internal/roles"
	"github.com/eoghanhynes/ralph/internal/runner"
	"github.com/eoghanhynes/ralph/internal/storystate"
	"github.com/eoghanhynes/ralph/internal/worker"
	"github.com/eoghanhynes/ralph/internal/workspace"
)

const (
	panelStories = iota
	panelContext
	panelClaude
	panelCount
)

type Model struct {
	cfg     *config.Config
	version string
	ctx     context.Context
	cancel  context.CancelFunc

	// State
	phase            phase
	iteration        int
	currentStoryID    string
	currentStoryTitle string
	currentRole       roles.Role
	preRevs          []judge.DirRev
	completedStories int
	totalStories     int
	allComplete      bool
	exitCode         int
	completionReason string // why the run finished
	startTime        time.Time
	confirmQuit      bool
	pausedDuring     phase // which phase we were in when paused (for resume)

	// Panel content
	progressContent string
	progressChanged bool
	worktreeContent string
	claudeContent   string
	judgeContent    string
	qualityContent  string

	costsContent        string
	antiPatternsContent string

	// Story data for the stories panel
	storyDisplayInfos  []StoryDisplayInfo
	cachedPRDStories   []prd.UserStory // cached from slow tick to avoid re-reading prd.json every 500ms
	animFrame          int    // animation frame for spinners
	storiesSelectedIdx int    // cursor position in stories list
	storiesExpandedID  string // ID of currently expanded story (empty = none)

	// Context panel mode
	ctxMode          contextMode
	ctxModeManual    bool  // true when user manually switched tabs; suppresses auto-select
	ctxManualAtPhase phase // phase when manual override was set; resets on phase change

	// Active panel for scrolling
	activePanel int

	// Components
	storiesVP  viewport.Model
	contextVP  viewport.Model
	claudeVP   viewport.Model
	spinner    spinner.Model

	// Terminal size
	width  int
	height int

	// Track if we should auto-scroll
	prevContextLen int
	prevClaudeLen  int

	// Spring-animated progress bar
	progressSpring harmonica.Spring
	animatedFill   float64 // current animated fill ratio (0.0–1.0)
	fillVelocity   float64 // spring velocity

	// Parallel execution
	coord            *coordinator.Coordinator
	storyDAG         *dag.DAG
	activeWorkerView worker.WorkerID // which worker's output to show in Claude panel

	// Checkpoint
	prdHash string

	// Quality review
	qualityIteration  int
	lastAssessment    *quality.Assessment

	// Worker log cache: persists logs after workspace cleanup
	workerLogCache map[worker.WorkerID]string
	// Ordered list of worker tab entries for stable 1-9 mapping
	workerTabOrder []worker.WorkerID

	// Resume checkpoint (loaded during phaseInit)
	loadedCheckpoint *checkpoint.Checkpoint

	// Cost tracking
	runCosting    *costs.RunCosting
	rateLimitInfo *costs.RateLimitInfo // latest rate limit info from Claude CLI

	memoryContent    string // rendered content for the memory context panel tab

	// Stuck alert (shown as status bar) + hint input
	stuckAlert    *runner.StuckInfo
	stuckAlertAt  time.Time
	hintInput     textarea.Model
	hintActive    bool // true when user is typing a hint

	// Task input bar (interactive mode)
	taskInput       textarea.Model
	taskInputActive bool // true when user is typing a task

	// Clarification Q&A state (P55-005)
	clarifyingTask  string   // original task text being clarified
	clarifyQuestions []string // questions from Claude
	clarifyAnswers  []string // answers collected so far
	clarifyIndex    int      // index of current question being answered

	// Interactive story creation
	storyCreator *interactive.StoryCreator
	livePRD      *prd.PRD // reference to the in-memory PRD for dynamic story injection

	// Status bar (vim-like, bottom of screen)
	statusText  string
	statusLevel statusLevel

	// Push notifications
	notifier *notify.Notifier

	// Remote status page
	statusServer *statuspage.StatusServer

	// Sprite mascot
	mascot *sprite.Mascot

	// Settings panel state
	settings settingsState
}

func NewModel(cfg *config.Config, version string) *Model {
	ctx, cancel := context.WithCancel(context.Background())

	// Always create notifier for terminal notifications; ntfy push only fires if topic is set
	n := notify.NewNotifier(cfg.NotifyTopic, cfg.NtfyServer)

	// Start status page server if --status-port is configured
	var ss *statuspage.StatusServer
	if cfg.StatusPort > 0 {
		ss = statuspage.New()
		actualPort, err := ss.Start(cfg.StatusPort)
		if err != nil {
			debuglog.Log("warning: status page failed to start on port %d: %v", cfg.StatusPort, err)
			ss = nil
		} else {
			cfg.StatusPort = actualPort
			debuglog.Log("Status page: http://localhost:%d", actualPort)
		}
	}

	initialContent := ""
	if summary := cfg.MonitoringSummary(); summary != "" {
		initialContent = summary + "\n\n"
	}

	hi := textarea.New()
	hi.Placeholder = "Type a hint for Claude..."
	hi.CharLimit = 500
	hi.SetHeight(1)
	hi.ShowLineNumbers = false

	ti := textarea.New()
	ti.Placeholder = "Type a task and press Enter..."
	ti.CharLimit = 500
	ti.SetHeight(1)
	ti.ShowLineNumbers = false

	var m *sprite.Mascot
	if cfg.SpriteEnabled {
		m = sprite.NewMascot(cfg.Workers)
	}

	return &Model{
		cfg:            cfg,
		version:        version,
		ctx:            ctx,
		cancel:         cancel,
		phase:          phaseInit,
		startTime:      time.Now(),
		spinner:        newSpinner(),
		storiesVP:      newStoriesViewport(35, 10),
		contextVP:      newContextViewport(60, 10),
		claudeVP:       newClaudeViewport(80, 20),
		claudeContent:  initialContent,
		progressSpring: harmonica.NewSpring(harmonica.FPS(30), 6.0, 0.5),
		runCosting:     costs.NewRunCosting(),
		workerLogCache: make(map[worker.WorkerID]string),
		notifier:       n,
		statusServer:   ss,
		hintInput:      hi,
		taskInput:      ti,
		mascot:         m,
		storyCreator:   interactive.NewStoryCreator(),
		settings:       newSettingsState(cfg),
	}
}

func (m *Model) ExitCode() int {
	return m.exitCode
}

// tsLog returns a timestamped log line for the claude panel.
func tsLog(format string, args ...interface{}) string {
	msg := fmt.Sprintf(format, args...)
	return fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
}

// stopStatusServer gracefully shuts down the status page server if running.
func (m *Model) stopStatusServer() {
	if m.statusServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.statusServer.Stop(ctx); err != nil {
			debuglog.Log("status server stop error: %v", err)
		}
	}
}

// updateStatusPage pushes the current model state to the status page server.
func (m *Model) updateStatusPage() {
	if m.statusServer == nil {
		return
	}
	m.statusServer.UpdateState(m.buildStatusState())
}

// buildStatusState constructs a StatusState from current Model fields.
func (m *Model) buildStatusState() statuspage.StatusState {
	state := statuspage.StatusState{
		Phase:     phaseToString(m.phase),
		PhaseIcon: phaseIcon(m.phase),
		TotalCost: m.runCosting.TotalCost,
		Running:   isLoopActive(m.phase),
		Version:   m.version,
	}

	elapsed := time.Since(m.startTime).Truncate(time.Second)
	state.RunDuration = formatDuration(elapsed)

	// Cost display (mirrors header logic)
	if m.rateLimitInfo != nil && !m.rateLimitInfo.ResetsAt.IsZero() {
		pct := rateLimitUsagePercent(m.rateLimitInfo)
		state.CostDisplay = fmt.Sprintf("Usage: %d%%", pct)
	} else if m.runCosting.TotalInputTokens > 0 || m.runCosting.TotalOutputTokens > 0 {
		state.CostDisplay = fmt.Sprintf("$%.2f", m.runCosting.TotalCost)
		state.HasTokenData = true
	} else {
		totalIters := len(m.runCosting.Stories)
		if totalIters > 0 {
			state.CostDisplay = fmt.Sprintf("%d stories tracked", totalIters)
		} else {
			state.CostDisplay = "—"
		}
	}

	// Badges
	if m.cfg.JudgeEnabled {
		state.Badges = append(state.Badges, statuspage.Badge{Label: "Judge", Icon: "⚖"})
	}
	if m.cfg.QualityReview {
		state.Badges = append(state.Badges, statuspage.Badge{Label: "Quality", Icon: "◇"})
	}
	if m.cfg.WorkersAuto {
		if m.cfg.Workers > 1 {
			state.Badges = append(state.Badges, statuspage.Badge{
				Label: fmt.Sprintf("Auto %d Workers", m.cfg.Workers), Icon: "⫘"})
		} else {
			state.Badges = append(state.Badges, statuspage.Badge{
				Label: "Auto Workers", Icon: "⫘"})
		}
	} else if m.cfg.Workers > 1 {
		state.Badges = append(state.Badges, statuspage.Badge{
			Label: fmt.Sprintf("%d Workers", m.cfg.Workers), Icon: "⫘"})
	}
	if m.cfg.NotifyTopic != "" {
		state.Badges = append(state.Badges, statuspage.Badge{Label: "ntfy", Icon: "🔔"})
	}

	// Completion reason and plan quality
	state.CompletionReason = m.completionReason
	if m.coord != nil {
		pq := m.coord.GetPlanQuality()
		if pq.TotalStories > 0 {
			state.PlanQuality = &statuspage.PlanQualityStatus{
				Score:          pq.Score(),
				FirstPassCount: pq.FirstPassCount,
				RetryCount:     pq.RetryCount,
				FailedCount:    pq.FailedCount,
				TotalStories:   pq.TotalStories,
			}
		}
	}

	// Settings
	for _, e := range m.settings.Entries {
		var val string
		switch e.Type {
		case settingBool:
			val = fmt.Sprintf("%t", e.BoolVal)
		case settingInt:
			val = fmt.Sprintf("%d", e.IntVal)
		case settingFloat:
			val = fmt.Sprintf("%.2f", e.FloatVal)
		}
		state.Settings = append(state.Settings, statuspage.SettingStatus{
			Label: e.Label,
			Value: val,
		})
	}

	// Current task description (plain text version of renderCurrentTask)
	state.CurrentTask = m.buildCurrentTaskText()

	// Context panel content (all tabs) — tail-truncate to keep SSE payloads bounded.
	const maxContentLen = 8000
	state.ProgressContent = tailTruncate(m.progressContent, maxContentLen)
	state.WorktreeContent = tailTruncate(m.worktreeContent, maxContentLen)
	state.JudgeContent = tailTruncate(m.judgeContent, maxContentLen)
	state.QualityContent = tailTruncate(m.qualityContent, maxContentLen)
	state.MemoryContent = tailTruncate(m.memoryContent, maxContentLen)
	state.CostsContent = tailTruncate(m.costsContent, maxContentLen)

	// Claude activity (last portion to keep payload reasonable)
	state.ClaudeActivity = tailTruncate(m.claudeContent, 4000)

	// Worker logs and tabs for parallel/interactive mode
	if (m.phase == phaseParallel || m.phase == phaseInteractive) && m.coord != nil && len(m.workerLogCache) > 0 {
		workerLogs := make(map[int]string, len(m.workerLogCache))
		for wID, content := range m.workerLogCache {
			workerLogs[int(wID)] = tailTruncate(content, 4000)
		}
		state.WorkerLogs = workerLogs

		// Build worker tabs matching the TUI tab order
		workers := m.coord.Workers()
		for _, wID := range m.workerTabOrder {
			w := workers[wID]
			if w == nil {
				continue
			}
			state.WorkerTabs = append(state.WorkerTabs, statuspage.WorkerTab{
				WorkerID: int(wID),
				StoryID:  w.StoryID,
				Role:     string(w.Role),
				State:    w.State.String(),
				Active:   wID == m.activeWorkerView,
			})
		}
	}

	// Stuck alert
	if m.stuckAlert != nil {
		state.StuckAlert = fmt.Sprintf("⚠ STUCK: %s — %s (%dx)",
			m.stuckAlert.StoryID, m.stuckAlert.Pattern, m.stuckAlert.Count)
	}

	// Rate limit
	if m.rateLimitInfo != nil && !m.rateLimitInfo.ResetsAt.IsZero() {
		remaining := time.Until(m.rateLimitInfo.ResetsAt)
		if remaining < 0 {
			remaining = 0
		}
		windowLabel := m.rateLimitInfo.RateLimitType
		switch windowLabel {
		case "five_hour":
			windowLabel = "5-hour window"
		case "daily":
			windowLabel = "daily window"
		}
		state.RateLimit = statuspage.RateLimitStatus{
			HasLimit: true,
			Window:   windowLabel,
			Status:   m.rateLimitInfo.Status,
			ResetsIn: formatDuration(remaining.Truncate(time.Second)),
		}
	}

	// Build worker assignments for parallel mode
	workerAssignments := make(map[string]int)
	workerIterations := make(map[string]int)
	workerRoles := make(map[string]string)
	if m.coord != nil {
		for wID, w := range m.coord.Workers() {
			if w.State == worker.WorkerRunning || w.State == worker.WorkerSetup || w.State == worker.WorkerJudging {
				workerAssignments[w.StoryID] = int(wID)
				workerIterations[w.StoryID] = w.Iteration
				workerRoles[w.StoryID] = string(w.Role)
			}
		}
	}

	// Load PRD for story data and project name
	if p, err := prd.Load(m.cfg.PRDFile); err == nil {
		state.PRDName = p.Project
		state.Total = len(p.UserStories)
		for _, s := range p.UserStories {
			ss := statuspage.StoryStatus{
				ID:            s.ID,
				Title:         s.Title,
				IsInteractive: strings.HasPrefix(s.ID, "T-"),
			}
			if s.Passes {
				ss.Status = "done"
				state.Completed++
				if ss.IsInteractive {
					ss.TaskStatus = "done"
				}
			} else if s.ID == m.currentStoryID && m.phase == phaseClaudeRun {
				ss.Status = "running"
				ss.Iteration = m.iteration
				ss.Role = string(m.currentRole)
				if ss.IsInteractive {
					ss.TaskStatus = "running"
				}
			} else {
				ss.Status = "queued"
				if ss.IsInteractive {
					ss.TaskStatus = "queued"
				}
			}

			// In parallel mode, check coordinator for running/failed status
			if m.coord != nil {
				if m.coord.IsInProgress(s.ID) {
					ss.Status = "running"
					ss.WorkerID = workerAssignments[s.ID]
					ss.Iteration = workerIterations[s.ID]
					ss.Role = workerRoles[s.ID]
					if ss.IsInteractive {
						ss.TaskStatus = "running"
					}
				} else if m.coord.IsFailed(s.ID) {
					ss.Status = "failed"
					if ss.IsInteractive {
						ss.TaskStatus = "failed"
					}
				}
			}

			// DAG dependencies — prefer the PRD's own DependsOn, fall back to runtime DAG
			if len(s.DependsOn) > 0 {
				ss.DependsOn = s.DependsOn
			} else if m.storyDAG != nil && len(m.storyDAG.Nodes) > 0 {
				if node, ok := m.storyDAG.Nodes[s.ID]; ok && len(node.DependsOn) > 0 {
					ss.DependsOn = node.DependsOn
				}
			}

			// Add per-story cost from RunCosting
			ss.Cost = m.runCosting.StoryCost(s.ID)

			state.Stories = append(state.Stories, ss)
		}
	}

	state.AllComplete = state.Completed == state.Total && state.Total > 0

	return state
}

// buildCurrentTaskText returns a plain-text version of renderCurrentTask for the status page.
func (m *Model) buildCurrentTaskText() string {
	switch m.phase {
	case phaseIdle:
		return "Idle mode"
	case phasePlanning:
		return "Generating prd.json from plan..."
	case phaseReview:
		return "Review prd.json — press Enter to execute"
	case phaseDagAnalysis:
		return "Analyzing story dependencies..."
	case phaseQualityReview:
		return fmt.Sprintf("Quality review (round %d)...", m.qualityIteration)
	case phaseQualityFix:
		return fmt.Sprintf("Fixing quality issues (round %d)...", m.qualityIteration)
	case phaseQualityPrompt:
		return "Issues remain — Enter to continue, q to finish"
	case phasePaused:
		return "Usage limit — press Enter to resume"
	case phaseDone:
		if m.allComplete {
			return "All stories complete!"
		} else if m.completionReason != "" {
			return m.completionReason
		}
		return "Some failed stories"
	case phaseParallel:
		if m.coord != nil {
			active := m.coord.ActiveStoryIDs()
			if len(active) > 0 {
				return strings.Join(active, ", ")
			}
			return "Scheduling..."
		}
		return "Starting workers..."
	case phaseInit:
		return "Initializing..."
	case phaseSummary:
		return "Generating summary..."
	case phaseResumePrompt:
		return "Resume from checkpoint? Press Enter to continue, q to restart"
	case phaseInteractive:
		return "Interactive — press t to add a task"
	default:
		if m.currentStoryID != "" {
			s := m.currentStoryID
			if m.currentRole != "" {
				s += " · " + string(m.currentRole)
			}
			if m.currentStoryTitle != "" {
				s += " " + m.currentStoryTitle
			}
			if strings.HasPrefix(m.currentStoryID, "FIX-") {
				s += " [AUTO-FIX]"
			}
			return s
		}
		return "Preparing next story..."
	}
}

// phaseIcon returns a unicode icon for the phase.
func phaseIcon(p phase) string {
	switch p {
	case phaseInit:
		return "◌"
	case phaseIterating:
		return "✦"
	case phaseClaudeRun:
		return "⚡"
	case phaseJudgeRun:
		return "⚖"
	case phasePlanning:
		return "✦"
	case phaseReview:
		return "◇"
	case phaseDone:
		return "✓"
	case phaseIdle:
		return "◇"
	case phaseDagAnalysis:
		return "◌"
	case phaseParallel:
		return "⚡"
	case phaseQualityReview:
		return "⚖"
	case phaseQualityFix:
		return "⚡"
	case phaseQualityPrompt:
		return "◇"
	case phasePaused:
		return "⏸"
	case phaseInteractive:
		return "⚡"
	default:
		return ""
	}
}

// phaseToString converts a phase to a human-readable string for the status page.
func phaseToString(p phase) string {
	switch p {
	case phaseInit:
		return "Initializing"
	case phaseIterating:
		return "Finding story"
	case phaseClaudeRun:
		return "Claude running"
	case phaseJudgeRun:
		return "Judge reviewing"
	case phasePlanning:
		return "Planning"
	case phaseReview:
		return "Review"
	case phaseDone:
		return "Complete"
	case phaseIdle:
		return "Idle"
	case phaseDagAnalysis:
		return "Analyzing DAG"
	case phaseParallel:
		return "Parallel"
	case phaseQualityReview:
		return "Quality Review"
	case phaseQualityFix:
		return "Quality Fix"
	case phaseQualityPrompt:
		return "Quality Prompt"
	case phaseSummary:
		return "Summary"
	case phaseResumePrompt:
		return "Resume Prompt"
	case phasePaused:
		return "Paused"
	case phaseInteractive:
		return "Interactive"
	default:
		return "Unknown"
	}
}

// contextContentWidth returns the usable content width for the context panel.
func (m *Model) contextContentWidth() int {
	storiesWidth := m.width * 35 / 100
	contextWidth := m.width - storiesWidth
	return max(contextWidth-4, 0)
}

func (m *Model) Init() tea.Cmd {
	setTitle := tea.SetWindowTitle("✦ ralph")

	// Start sprite tick loop if mascot is enabled.
	var spriteInit tea.Cmd
	if m.mascot != nil {
		spriteInit = spriteTickCmd()
	}

	// Check memory file sizes at startup (shown once via status bar).
	memCheck := checkMemorySizeCmd(m.cfg.ProjectDir)

	if m.cfg.IdleMode {
		m.phase = phaseIdle
		return tea.Batch(
			setTitle,
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
			spriteInit,
			memCheck,
		)
	}
	if m.cfg.PlanFile != "" {
		m.phase = phasePlanning
		return tea.Batch(
			setTitle,
			planCmd(m.ctx, m.cfg),
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
			spriteInit,
			memCheck,
		)
	}
	if m.cfg.NoPRD {
		// Skip archive and DAG analysis — go straight to interactive mode.
		// Load the PRD written by main.go so interactive tasks can be appended.
		if p, err := prd.Load(m.cfg.PRDFile); err == nil {
			m.livePRD = p
			m.storyDAG = &dag.DAG{Nodes: make(map[string]*dag.StoryNode)}
		}
		m.phase = phaseInteractive
		m.claudeContent += tsLog("── Interactive mode — add tasks with the input bar ──\n")
		m.claudeVP.SetContent(m.claudeContent)
		m.prevClaudeLen = len(m.claudeContent)
		initCmds := []tea.Cmd{
			setTitle,
			m.spinner.Tick,
			fastTickCmd(),
			tickCmd(),
			memCheck,
		}
		if spriteInit != nil {
			initCmds = append(initCmds, spriteInit)
		}
		return tea.Batch(initCmds...)
	}
	return tea.Batch(
		setTitle,
		archiveCmd(m.cfg),
		m.spinner.Tick,
		fastTickCmd(),
		tickCmd(),
		spriteInit,
		memCheck,
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Recompute viewport dimensions so SetContent wraps at the correct width
		chrome := 4 // header(3) + footer(1)
		if m.stuckAlert != nil {
			chrome++ // status bar
		}
		available := m.height - chrome
		if available < 10 {
			available = 10
		}
		topHeight := available * 35 / 100
		if topHeight < 5 {
			topHeight = 5
		}
		claudeHeight := available - topHeight

		storiesWidth := m.width * 35 / 100
		contextWidth := m.width - storiesWidth

		m.storiesVP.Width = storiesWidth - 4
		m.storiesVP.Height = topHeight - 3
		m.contextVP.Width = contextWidth - 4
		m.contextVP.Height = topHeight - 4 // extra line for tab bar
		m.claudeVP.Width = m.width - 4
		m.claudeVP.Height = claudeHeight - 3

		if m.mascot != nil {
			m.mascot.Resize(sprite.LayoutParams{
				Width:       m.width,
				Height:      m.height,
				HasStuckBar: m.stuckAlert != nil,
				HasHintInput: m.hintActive,
			})
		}

		// Re-render markdown at new width if we have content.
		if m.progressContent != "" {
			contentW := m.contextContentWidth()
			if cmd := maybeRenderMarkdown(m.progressContent, contentW); cmd != nil {
				return m, cmd
			}
		}
		return m, nil

	case tea.KeyMsg:
		// Hint input mode: capture all keys
		if m.hintActive {
			switch msg.Type {
			case tea.KeyEsc:
				m.hintActive = false
				m.hintInput.Blur()
				m.hintInput.Reset()
				return m, nil
			case tea.KeyEnter:
				hint := strings.TrimSpace(m.hintInput.Value())
				if hint != "" && m.stuckAlert != nil {
					storyID := m.stuckAlert.StoryID
					_ = storystate.SaveHint(m.cfg.ProjectDir, storyID, hint)
					m.claudeContent += "\n" + tsLog("── Hint injected: %s ──\n", hint)
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
				}
				m.hintActive = false
				m.hintInput.Blur()
				m.hintInput.Reset()
				m.stuckAlert = nil
				return m, nil
			case tea.KeyTab:
				if m.phase == phaseInteractive || m.phase == phaseParallel {
					m.hintActive = false
					m.hintInput.Blur()
					m.taskInputActive = true
					m.taskInput.SetWidth(m.width - 4)
					m.taskInput.Focus()
					return m, m.taskInput.Focus()
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.hintInput, cmd = m.hintInput.Update(msg)
				return m, cmd
			}
		}

		// Task input mode: capture all keys
		if m.taskInputActive {
			switch msg.Type {
			case tea.KeyEsc:
				if m.isClarifying() {
					// Cancel clarification entirely
					m.claudeContent += tsLog("── Clarification cancelled ──\n")
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
					m.clearClarifyState()
				}
				m.taskInputActive = false
				m.taskInput.Blur()
				m.taskInput.Reset()
				m.taskInput.Placeholder = "Type a task and press Enter..."
				return m, nil
			case tea.KeyEnter:
				if m.isClarifying() {
					// Submit answer to current question
					answer := strings.TrimSpace(m.taskInput.Value())
					m.taskInput.Reset()
					m.clarifyAnswers = append(m.clarifyAnswers, answer)
					m.claudeContent += fmt.Sprintf("  A%d: %s\n", m.clarifyIndex+1, answer)
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
					m.clarifyIndex++

					if m.clarifyIndex >= len(m.clarifyQuestions) {
						// All questions answered — bundle description and dispatch
						desc := m.buildClarifyDescription()
						m.claudeContent += tsLog("── Clarification complete, creating story ──\n")
						m.claudeVP.SetContent(m.claudeContent)
						m.claudeVP.GotoBottom()
						m.prevClaudeLen = len(m.claudeContent)

						m.clearClarifyState()
						m.taskInputActive = false
						m.taskInput.Blur()
						m.taskInput.Reset()
						m.taskInput.Placeholder = "Type a task and press Enter..."

						// Dispatch the story using the clarified description
						cmd := m.dispatchInteractiveTask(desc)
						return m, cmd
					} else {
						// Advance to next question
						m.taskInput.Placeholder = fmt.Sprintf("Answer question %d:", m.clarifyIndex+1)
					}
					return m, nil
				}
				task := strings.TrimSpace(m.taskInput.Value())
				m.taskInputActive = false
				m.taskInput.Blur()
				m.taskInput.Reset()
				if task != "" {
					m.claudeContent += "\n" + tsLog("── Task submitted: %s ──\n", task) + tsLog("── Clarifying... ──\n")
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
					projectName := filepath.Base(m.cfg.ProjectDir)
					return m, clarifyTaskCmd(m.ctx, m.cfg.ProjectDir, projectName, task, m.cachedPRDStories)
				}
				return m, nil
			case tea.KeyTab:
				if m.stuckAlert != nil {
					// Toggle to hint input
					m.taskInputActive = false
					m.taskInput.Blur()
					m.hintActive = true
					m.hintInput.SetWidth(m.width - 4)
					m.hintInput.Focus()
					return m, m.hintInput.Focus()
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.taskInput, cmd = m.taskInput.Update(msg)
				return m, cmd
			}
		}

		// 'i' to enter hint mode when stuck bar is showing
		if msg.String() == "i" && m.stuckAlert != nil {
			m.hintActive = true
			m.hintInput.SetWidth(m.width - 4)
			m.hintInput.Focus()
			return m, m.hintInput.Focus()
		}

		// 't' to enter task input mode when in interactive, parallel, or done phase
		if msg.String() == "t" && (m.phase == phaseInteractive || m.phase == phaseParallel || m.phase == phaseDone) {
			m.taskInputActive = true
			m.taskInput.SetWidth(m.width - 4)
			m.taskInput.Focus()
			return m, m.taskInput.Focus()
		}

		// Interactive sprite mode: capture ALL input except q/ctrl+c
		if m.mascot != nil && m.mascot.Interactive {
			switch msg.String() {
			case "ctrl+c":
				if m.coord != nil {
					m.coord.CancelAll()
					m.coord.CleanupAll(context.Background())
				}
				m.cleanupWorkerLogs()
				m.stopStatusServer()
					m.cancel()
				return m, tea.Quit
			case "q":
				if m.confirmQuit || m.phase == phaseDone || m.phase == phaseIdle {
					m.cleanupWorkerLogs()
					m.stopStatusServer()
							m.cancel()
					return m, tea.Quit
				}
				m.confirmQuit = true
				return m, nil
			case "p", "esc":
				m.mascot.Interactive = false
				return m, nil
			default:
				m.confirmQuit = false
				sprite.HandleKey(msg, m.mascot.Spr, &m.mascot.World)
				return m, nil
			}
		}

		// 'p' enters interactive sprite mode
		if msg.String() == "p" && m.mascot != nil {
			m.mascot.Interactive = true
			return m, nil
		}

		switch {
		case msg.String() == "ctrl+c":
			if m.coord != nil {
				m.coord.CancelAll()
				m.coord.CleanupAll(context.Background())
			}
			m.cleanupWorkerLogs()
			m.stopStatusServer()
			m.cancel()
			return m, tea.Quit
		case msg.String() == "y":
			if m.phase == phaseResumePrompt {
				cp := m.loadedCheckpoint
				m.iteration = cp.IterationCount
				m.completedStories = len(cp.CompletedStories)

				// Restore cost data from checkpoint (nil CostData = start fresh)
				if cp.CostData != nil {
					m.runCosting = costs.NewFromSnapshot(*cp.CostData)
					m.costsContent = renderCostsContent(m.runCosting, m.storyDisplayInfos)
				}

				if cp.Phase == "parallel" && (m.cfg.Workers > 1 || m.cfg.WorkersAuto) {
					p, err := prd.Load(m.cfg.PRDFile)
					if err != nil {
						debuglog.Log("Error loading PRD for resume: %v", err)
						m.phase = phaseIterating
						cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
						return m, tea.Batch(cmds...)
					}

					m.storyDAG = dag.FromCheckpoint(cp.DAG, p.UserStories)
					m.livePRD = p

					var incomplete []prd.UserStory
					for _, s := range p.UserStories {
						if !s.Passes {
							incomplete = append(incomplete, s)
						}
					}

					m.cfg.ResolveAutoWorkers(len(incomplete))
					m.coord = coordinator.NewFromCheckpoint(
						m.cfg, m.storyDAG, m.cfg.Workers, incomplete,
						cp.CompletedStories, cp.FailedStories, cp.IterationCount,
					)
						m.coord.SetRunCosting(m.runCosting)
					m.phase = phaseParallel
					m.coord.ScheduleReady(m.ctx)
					cmds = append(cmds, m.coord.ListenCmd())
				} else {
					m.phase = phaseIterating
					cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
				}
				return m, tea.Batch(cmds...)
			}
		case msg.String() == "n":
			if m.phase == phaseResumePrompt {
				// Start fresh — delete checkpoint and continue normal startup
				_ = checkpoint.Delete(m.cfg.ProjectDir)
				m.loadedCheckpoint = nil
				if m.cfg.Workers > 1 || m.cfg.WorkersAuto {
					m.phase = phaseDagAnalysis
					cmds = append(cmds, dagAnalyzeCmd(m.ctx, m.cfg))
				} else {
					m.phase = phaseIterating
					cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
				}
				return m, tea.Batch(cmds...)
			}
		case msg.String() == "q":
			if m.phase == phaseResumePrompt {
				m.cleanupWorkerLogs()
				m.stopStatusServer()
					m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseReview {
				m.cleanupWorkerLogs()
				m.stopStatusServer()
					m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseQualityPrompt {
				// User chose to skip remaining quality fixes
				return m.transitionToSummary()
			}
			if m.confirmQuit || m.phase == phaseDone || m.phase == phaseIdle {
				m.cleanupWorkerLogs()
				m.stopStatusServer()
					m.cancel()
				return m, tea.Quit
			}
			if m.phase == phaseInteractive {
				// In interactive mode, quit transitions to done
				m.phase = phaseDone
				m.allComplete = m.coord == nil || m.coord.AllDone()
				if !m.allComplete {
					m.exitCode = 1
					m.completionReason = "User quit interactive mode with active workers"
				} else {
					m.completionReason = "User quit interactive mode"
				}
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.cancel()
				m.showCompletionReport()
				return m, nil
			}
			m.confirmQuit = true
			return m, nil
		case msg.String() == "tab":
			m.activePanel = (m.activePanel + 1) % panelCount
			return m, nil
		case msg.String() == "s" && !m.hintActive && !m.taskInputActive:
			m.ctxMode = contextSettings
			m.ctxModeManual = true
			m.ctxManualAtPhase = m.phase
			m.activePanel = panelContext
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && (msg.String() == "j" || msg.String() == "down"):
			m.settings.moveDown()
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && (msg.String() == "k" || msg.String() == "up"):
			m.settings.moveUp()
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && msg.String() == "enter":
			m.settings.toggle()
			m.settings.applyTo(m.cfg)
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && (msg.String() == "+" || msg.String() == "="):
			m.settings.increment()
			m.settings.applyTo(m.cfg)
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && msg.String() == "-":
			m.settings.decrement()
			m.settings.applyTo(m.cfg)
			return m, nil
		case m.ctxMode == contextSettings && m.activePanel == panelContext && msg.String() == "ctrl+s":
			if err := m.cfg.SaveConfig(); err != nil {
				m.statusText = fmt.Sprintf("Save failed: %v", err)
				m.statusLevel = statusError
			} else {
				m.settings.Dirty = false
				m.statusText = "Settings saved to .ralph/config.toml"
				m.statusLevel = statusInfo
			}
			return m, nil
		case msg.String() == "j" || msg.String() == "down":
			switch m.activePanel {
			case panelStories:
				if len(m.storyDisplayInfos) > 0 && m.storiesSelectedIdx < len(m.storyDisplayInfos)-1 {
					m.storiesSelectedIdx++
				}
			case panelContext:
				m.contextVP.LineDown(1)
			case panelClaude:
				m.claudeVP.LineDown(1)
			}
			return m, nil
		case msg.String() == "k" || msg.String() == "up":
			switch m.activePanel {
			case panelStories:
				if m.storiesSelectedIdx > 0 {
					m.storiesSelectedIdx--
				}
			case panelContext:
				m.contextVP.LineUp(1)
			case panelClaude:
				m.claudeVP.LineUp(1)
			}
			return m, nil
		case (msg.String() == "enter" || msg.String() == "right" || msg.String() == "l") && m.activePanel == panelStories:
			if len(m.storyDisplayInfos) > 0 && m.storiesSelectedIdx < len(m.storyDisplayInfos) {
				storyID := m.storyDisplayInfos[m.storiesSelectedIdx].ID
				if m.storiesExpandedID == storyID {
					m.storiesExpandedID = ""
				} else {
					m.storiesExpandedID = storyID
				}
			}
			return m, nil
		case msg.String() == "left" || msg.String() == "h":
			if m.activePanel == panelStories && m.storiesExpandedID != "" {
				m.storiesExpandedID = ""
				return m, nil
			}
		case msg.String() == "pgdown":
			switch m.activePanel {
			case panelStories:
				m.storiesVP.ViewDown()
			case panelContext:
				m.contextVP.ViewDown()
			case panelClaude:
				m.claudeVP.ViewDown()
			}
			return m, nil
		case msg.String() == "pgup":
			switch m.activePanel {
			case panelStories:
				m.storiesVP.ViewUp()
			case panelContext:
				m.contextVP.ViewUp()
			case panelClaude:
				m.claudeVP.ViewUp()
			}
			return m, nil
		case msg.String() == "m":
			// Toggle all monitoring (status page + ntfy) on/off at runtime
			if m.statusServer != nil {
				// Turn everything off
				m.stopStatusServer()
				m.statusServer = nil
				m.cfg.StatusPort = 0
				m.notifier.SetDisabled(true)
				m.claudeContent += "\n" + tsLog("── Monitoring stopped (status page + notifications) ──\n")
			} else {
				// Turn everything on
				port := m.cfg.StatusPort
				if port == 0 {
					port = 8080
				}
				ss := statuspage.New()
				actualPort, err := ss.Start(port)
				if err != nil {
					m.claudeContent += "\n" + tsLog("── Status page failed to start on port %d: %v ──\n", port, err)
				} else {
					m.statusServer = ss
					m.cfg.StatusPort = actualPort
					m.updateStatusPage()
					m.claudeContent += "\n" + tsLog("── Status page started: http://localhost:%d ──\n", actualPort)
				}
				m.notifier.SetDisabled(false)
				m.claudeContent += tsLog("── Notifications enabled ──\n")
			}
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		case msg.String() == "[" || msg.String() == "]":
			// Cycle context panel tabs
			if msg.String() == "]" {
				m.ctxMode = (m.ctxMode + 1) % contextModeCount
			} else {
				m.ctxMode = (m.ctxMode + contextModeCount - 1) % contextModeCount // -1 with wrap
			}
			m.ctxModeManual = true
			m.ctxManualAtPhase = m.phase
			return m, nil
		case msg.String() == "enter":
			if m.phase == phasePaused {
				m.claudeContent += "\n" + tsLog("── Resuming... ──\n")
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				m.ctx, m.cancel = context.WithCancel(context.Background())

				switch m.pausedDuring {
				case phaseParallel:
					m.coord.Resume()
					m.phase = phaseParallel
					m.coord.ScheduleReady(m.ctx)
					return m, tea.Batch(m.coord.ListenCmd(), fastTickCmd(), tickCmd())
				case phaseInteractive:
					m.coord.Resume()
					m.phase = phaseInteractive
					m.coord.ScheduleReady(m.ctx)
					return m, tea.Batch(m.coord.ListenCmd(), fastTickCmd(), tickCmd())
				case phasePlanning:
					m.phase = phasePlanning
					return m, tea.Batch(planCmd(m.ctx, m.cfg), fastTickCmd())
				default:
					// Serial mode — retry current story
					m.phase = phaseIterating
					return m, findNextStoryCmd(m.cfg.PRDFile)
				}
			}
			if m.phase == phaseReview {
				m.phase = phaseInit
				m.claudeContent = ""
				m.prevClaudeLen = 0
				return m, archiveCmd(m.cfg)
			}
			if m.phase == phaseQualityPrompt {
				// User chose to continue fixing
				m.qualityIteration++
				m.phase = phaseQualityReview
				m.claudeContent += "\n" + tsLog("── Continuing quality review (iteration %d)... ──\n", m.qualityIteration)
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration)
			}
		default:
			m.confirmQuit = false
			if (m.phase == phaseParallel || m.phase == phaseInteractive) && len(m.workerTabOrder) > 0 {
				// Worker tab switching: 1-9 maps to tab position, not worker ID
				if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
					idx := int(msg.String()[0]-'0') - 1
					m.switchToWorkerTab(idx)
				}
				// </>: cycle prev/next worker (wraps around, works for any count)
				if msg.String() == "<" || msg.String() == "," {
					cur := m.workerTabIndex()
					next := cur - 1
					if next < 0 {
						next = len(m.workerTabOrder) - 1
					}
					m.switchToWorkerTab(next)
				}
				if msg.String() == ">" || msg.String() == "." {
					cur := m.workerTabIndex()
					next := (cur + 1) % len(m.workerTabOrder)
					m.switchToWorkerTab(next)
				}
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	// --- Sprite tick: advance mascot animation ---
	case spriteTickMsg:
		if m.mascot != nil {
			m.mascot.Tick()
			cmds = append(cmds, spriteTickCmd())
		}

	// --- Fast tick: poll activity + progress ---
	case fastTickMsg:
		cmds = append(cmds, fastTickCmd())
		cmds = append(cmds, pollProgressCmd(m.cfg.ProgressFile))
		m.updateStatusPage()

		// Advance animation frame
		m.animFrame++

		// Rebuild story display infos from cached PRD stories (updated on slow tick).
		// This avoids re-reading and parsing prd.json from disk every 500ms.
		m.rebuildStoryDisplayInfos()

		// Auto-select context mode based on phase (skip if user manually switched)
		// Reset manual override when the phase changes so new phases can auto-select.
		if m.ctxModeManual && m.phase != m.ctxManualAtPhase {
			m.ctxModeManual = false
		}
		if !m.ctxModeManual {
			autoMode := autoSelectContextMode(m.phase, m.judgeContent, m.qualityContent)
			if autoMode != m.ctxMode {
				switch m.phase {
				case phaseJudgeRun, phaseQualityReview, phaseQualityFix, phaseQualityPrompt:
					m.ctxMode = autoMode
				}
			}
		}

		// Update spring-animated progress bar
		target := 0.0
		if m.totalStories > 0 {
			target = float64(m.completedStories) / float64(m.totalStories)
		}
		m.animatedFill, m.fillVelocity = m.progressSpring.Update(
			m.animatedFill, m.fillVelocity, target,
		)
		if m.phase == phasePlanning {
			activityPath := filepath.Join(m.cfg.LogDir, "plan-activity.log")
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseClaudeRun {
			activityPath := runner.ActivityFilePath(m.cfg.LogDir, m.iteration)
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseClaudeRun {
			cmds = append(cmds, pollStuckCmd(m.cfg.ProjectDir, m.iteration))
		}
		if m.phase == phaseQualityFix {
			activityPath := filepath.Join(m.cfg.LogDir, fmt.Sprintf("quality-fix-%d-activity.log", m.qualityIteration))
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if m.phase == phaseSummary {
			activityPath := filepath.Join(m.cfg.LogDir, "summary-activity.log")
			cmds = append(cmds, pollActivityCmd(activityPath))
		}
		if (m.phase == phaseParallel || m.phase == phaseInteractive) && m.coord != nil && m.activeWorkerView > 0 && m.coord.IsWorkerActive(m.activeWorkerView) {
			activityPath := m.coord.GetWorkerActivityPath(m.activeWorkerView)
			if activityPath != "" {
				wID := m.activeWorkerView
				cmds = append(cmds, pollWorkerActivityCmd(wID, activityPath))
			}
		}

	// --- Slow tick: poll worktree + prd ---
	case tickMsg:
		cmds = append(cmds, tickCmd())
		cmds = append(cmds, pollWorktreeCmd(m.ctx, m.cfg.ProjectDir))
		cmds = append(cmds, reloadPRDCmd(m.cfg.PRDFile))
		cmds = append(cmds, pollMemoryStatsCmd(m.cfg.RalphHome))
		// Auto-dismiss stuck alert after 30s
		if m.stuckAlert != nil && time.Since(m.stuckAlertAt) > 30*time.Second {
			m.stuckAlert = nil
		}

	// --- Data updates ---
	case progressContentMsg:
		if msg.Content != m.progressContent {
			m.progressChanged = true
			// Kick off async markdown rendering for the new content.
			contentW := m.contextContentWidth()
			if cmd := maybeRenderMarkdown(msg.Content, contentW); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		m.progressContent = msg.Content

	case markdownRenderedMsg:
		applyMarkdownRendered(msg)

	case markdownDebounceMsg:
		if cmd := handleMarkdownDebounce(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case worktreeMsg:
		m.worktreeContent = msg.Content

	case claudeActivityMsg:
		m.claudeContent = msg.Content
		newLen := len(msg.Content)
		m.claudeVP.SetContent(msg.Content)
		if newLen > m.prevClaudeLen {
			m.claudeVP.GotoBottom()
		}
		m.prevClaudeLen = newLen

	case prdReloadedMsg:
		m.completedStories = msg.CompletedCount
		m.totalStories = msg.TotalCount
		// Cache PRD stories so fast tick can rebuild display infos without disk I/O
		m.cachedPRDStories = msg.Stories

	// --- Phase transitions ---
	case planDoneMsg:
		if msg.Err != nil {
			var usageErr *runner.UsageLimitError
			if errors.As(msg.Err, &usageErr) {
				m.pausedDuring = phasePlanning
				m.phase = phasePaused
				m.claudeContent += "\n" + tsLog("── Usage Limit Hit ──\n") + "Claude API usage limit reached during planning.\nPress Enter to resume when your limit resets.\n"
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, nil
			}
			m.claudeContent += "\n" + tsLog("── Plan Error ──\n") + fmt.Sprintf("%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.phase = phaseDone
			m.exitCode = 1
			m.completionReason = fmt.Sprintf("Planning failed: %v", msg.Err)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
			return m, nil
		}
		m.phase = phaseReview
		m.claudeContent += "\n" + tsLog("── prd.json generated. Review it, then press Enter to execute (q to quit) ──\n")
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		return m, nil

	case archiveDoneMsg:
		// Show last run summary if history exists
		if h, err := costs.LoadHistory(m.cfg.ProjectDir); err == nil && len(h.Runs) > 0 {
			last := h.Runs[len(h.Runs)-1]
			date := last.Date
			if len(date) > 10 {
				date = date[:10]
			}
			m.claudeContent += fmt.Sprintf("Last run: %s on %s, %d/%d stories, $%.2f, %.0f min\n",
				last.PRD, date, last.StoriesCompleted, last.StoriesTotal, last.TotalCost, last.DurationMinutes)
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
		}

		// Check for existing checkpoint to offer resume
		cp, exists, err := checkpoint.Load(m.cfg.ProjectDir)
		if err == nil && exists {
			currentHash, hashErr := checkpoint.ComputePRDHash(m.cfg.PRDFile)
			if hashErr == nil && currentHash == cp.PRDHash {
				// Valid checkpoint — offer resume
				m.loadedCheckpoint = &cp
				m.phase = phaseResumePrompt
				return m, tea.Batch(cmds...)
			}
			// Stale checkpoint — PRD changed, delete and continue
			m.claudeContent += tsLog("── Checkpoint found but PRD has changed — starting fresh ──\n")
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
			_ = checkpoint.Delete(m.cfg.ProjectDir)
		}
		// Compute PRD hash for checkpointing
		if hash, err := checkpoint.ComputePRDHash(m.cfg.PRDFile); err == nil {
			m.prdHash = hash
		}
		// If no PRD stories exist, enter interactive mode
		if p, err := prd.Load(m.cfg.PRDFile); err != nil || len(p.UserStories) == 0 {
			m.phase = phaseInteractive
			m.claudeContent += tsLog("── Interactive mode — no PRD stories found ──\n")
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
		} else if p.AllComplete() {
			// All stories already complete — skip straight to quality check / summary
			m.totalStories = p.TotalCount()
			m.completedStories = m.totalStories
			m.claudeContent += tsLog("── All %d stories already complete ──\n", m.totalStories)
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
			return m.transitionToComplete()
		} else if m.cfg.Workers > 1 || m.cfg.WorkersAuto {
			m.phase = phaseDagAnalysis
			cmds = append(cmds, dagAnalyzeCmd(m.ctx, m.cfg))
		} else {
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		}

	case memoryStatsMsg:
		m.memoryContent = renderMemoryContent(msg.Stats)

	case costUpdateMsg:
		if m.runCosting != nil {
			m.runCosting.AddIteration(msg.StoryID, msg.Usage, 0)
			m.costsContent = renderCostsContent(m.runCosting, m.storyDisplayInfos)
			m.updateStatusPage()
		}

	case statusMsg:
		m.statusText = msg.Text
		m.statusLevel = msg.Level
		cmds = append(cmds, tea.Tick(5*time.Second, func(time.Time) tea.Msg {
			return statusClearMsg{}
		}))

	case statusClearMsg:
		m.statusText = ""

	case clarifyResultMsg:
		dispatchReady := false
		if msg.Err != nil {
			// Clarification failed — fall back to dispatching task as-is with warning
			debuglog.Log("clarifyResultMsg: error — falling back to direct dispatch: %v", msg.Err)
			m.claudeContent += tsLog("── Clarification failed (%v), dispatching task as-is ──\n", msg.Err)
			m.statusText = "⚠ Clarification failed, task dispatched as-is"
			m.statusLevel = statusWarn
			cmds = append(cmds, tea.Tick(5*time.Second, func(time.Time) tea.Msg {
				return statusClearMsg{}
			}))
			dispatchReady = true
		} else if msg.Ready {
			// Task is clear — proceed to story creation
			m.claudeContent += tsLog("── Task is clear, creating story ──\n")
			dispatchReady = true
		} else {
			// Questions returned — enter clarification Q&A mode
			m.clarifyingTask = msg.TaskText
			m.clarifyQuestions = msg.Questions
			m.clarifyAnswers = make([]string, 0, len(msg.Questions))
			m.clarifyIndex = 0

			m.claudeContent += tsLog("── Clarifying questions: ──\n")
			for i, q := range msg.Questions {
				m.claudeContent += fmt.Sprintf("  %d. %s\n", i+1, q)
			}
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)

			// Activate task input for answering
			m.taskInputActive = true
			m.taskInput.SetWidth(m.width - 4)
			m.taskInput.Placeholder = fmt.Sprintf("Answer question %d:", 1)
			m.taskInput.Reset()
			cmds = append(cmds, m.taskInput.Focus())

			// Update stories panel to show clarifying status
			m.rebuildStoryDisplayInfos()
		}
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		// Create and dispatch the interactive story when task is ready
		if dispatchReady {
			if cmd := m.dispatchInteractiveTask(msg.TaskText); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case nextStoryMsg:
		if msg.AllDone {
			return m.transitionToComplete()
		}
		m.iteration++
		m.currentStoryID = msg.StoryID
		m.currentStoryTitle = msg.StoryTitle
		// Determine starting role for display
		if p, err := prd.Load(m.cfg.PRDFile); err == nil {
			story := p.FindStory(msg.StoryID)
			if needsArchitect(m.cfg.ProjectDir, msg.StoryID, story) {
				m.currentRole = roles.RoleArchitect
			} else {
				m.currentRole = roles.RoleImplementer
			}
		} else {
			m.currentRole = roles.RoleImplementer
		}
		m.phase = phaseClaudeRun
		m.updateStatusPage()
		m.claudeContent = ""
		m.prevClaudeLen = 0

		// Capture revision for judge diff baseline
		if m.cfg.JudgeEnabled {
			dirs := []string{m.cfg.ProjectDir}
			if p, err := prd.Load(m.cfg.PRDFile); err == nil {
				for _, r := range p.Repos {
					if filepath.IsAbs(r) {
						dirs = append(dirs, r)
					} else {
						dirs = append(dirs, filepath.Join(m.cfg.ProjectDir, r))
					}
				}
			}
			m.preRevs = captureRevsCmd(m.ctx, dirs)
		}

		cmds = append(cmds, runClaudeCmd(m.ctx, m.cfg, msg.StoryID, m.iteration))

	case claudeDoneMsg:
		m.currentRole = msg.Role
		debuglog.Log("claudeDone: story=%s err=%v completeSignal=%v", m.currentStoryID, msg.Err, msg.CompleteSignal)
		if msg.Err != nil {
			// Context cancelled = user quit
			if m.ctx.Err() != nil {
				debuglog.Log("claudeDone: context cancelled, quitting")
				m.cleanupWorkerLogs()
				m.stopStatusServer()
					return m, tea.Quit
			}
			// Usage limit — pause and wait for user
			var usageErr *runner.UsageLimitError
			if errors.As(msg.Err, &usageErr) {
				m.pausedDuring = phaseClaudeRun
				m.phase = phasePaused
				m.claudeContent += "\n" + tsLog("── Usage Limit Hit ──\n") + "Claude API usage limit reached.\nPress Enter to resume when your limit resets.\n"
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				return m, nil
			}

			// Show Claude error in activity panel
			m.notifier.Error(m.ctx, msg.Err.Error())
			m.claudeContent += "\n" + tsLog("── Claude Error ──\n") + fmt.Sprintf("%s\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		// Send cost update if token usage was collected
		if msg.TokenUsage != nil && m.currentStoryID != "" {
			cmds = append(cmds, func() tea.Msg {
				return costUpdateMsg{Usage: *msg.TokenUsage, StoryID: m.currentStoryID}
			})
		}
		// Update rate limit info if available
		if msg.RateLimitInfo != nil {
			m.rateLimitInfo = msg.RateLimitInfo
		}
		m.updateStatusPage()

		// Mark current story as passed in prd.json if agent reported it complete.
		// The system owns the passes field — the agent no longer modifies prd.json.
		if msg.Err == nil && m.currentStoryID != "" {
			ss, _ := storystate.Load(m.cfg.ProjectDir, m.currentStoryID)
			if ss.Status == storystate.StatusComplete {
				m.notifyStoryComplete(m.currentStoryID, m.currentStoryTitle)
				if p, err := prd.Load(m.cfg.PRDFile); err == nil {
					p.SetPasses(m.currentStoryID, true)
					_ = prd.Save(m.cfg.PRDFile, p)
				}
			}
		}

		// Write checkpoint after each serial iteration
		m.writeSerialCheckpoint(msg.Err)

		if msg.CompleteSignal {
			debuglog.Log("claudeDone: COMPLETE signal received during story=%s, transitioning to complete", m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventStoryComplete,
				StoryID: m.currentStoryID,
				Summary: "All stories complete (COMPLETE signal received)",
			})
			return m.transitionToComplete()
		}

		// Judge check
		if m.cfg.JudgeEnabled && m.currentStoryID != "" {
			if judgeCmd := m.handleJudgeCheck(); judgeCmd != nil {
				cmds = append(cmds, judgeCmd)
				return m, tea.Batch(cmds...)
			}
		}

		// No judge or story didn't pass yet — next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	case stuckDetectedMsg:
		debuglog.Log("stuck detected: story=%s pattern=%s count=%d", msg.Info.StoryID, msg.Info.Pattern, msg.Info.Count)
		info := msg.Info
		m.stuckAlert = &info
		m.stuckAlertAt = time.Now()
		m.notifier.StoryStuck(m.ctx, msg.Info.StoryID, fmt.Sprintf("%s (%dx)", msg.Info.Pattern, msg.Info.Count))
		m.updateStatusPage()
		// Cancel Claude — it's stuck
		m.cancel()
		// Recreate context for future operations
		m.ctx, m.cancel = context.WithCancel(context.Background())

		_ = events.Append(m.cfg.ProjectDir, events.Event{
			Type:    events.EventStuck,
			StoryID: msg.Info.StoryID,
			Summary: fmt.Sprintf("Stuck: %s (%dx)", msg.Info.Pattern, msg.Info.Count),
			Errors:  msg.Info.Commands,
			Meta:    map[string]string{"iteration": fmt.Sprintf("%d", msg.Info.Iteration)},
		})

		// Append [STUCK] to progress
		if f, err := os.OpenFile(m.cfg.ProgressFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fmt.Fprintf(f, "\n## [%s] %s [STUCK]\n- Pattern: %s (%dx)\n- Commands: %s\n---\n",
				time.Now().Format("2006-01-02 15:04"), msg.Info.StoryID, msg.Info.Pattern, msg.Info.Count,
				strings.Join(msg.Info.Commands, ", "))
			f.Close()
		}

		m.claudeContent += "\n" + tsLog("── STUCK DETECTED: %s (%dx) ──\n", msg.Info.Pattern, msg.Info.Count)
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		// If this is a FIX- story, mark as failed and move on
		if strings.HasPrefix(m.currentStoryID, "FIX-") {
			if p, err := prd.Load(m.cfg.PRDFile); err == nil {
				p.SetPasses(m.currentStoryID, false)
				_ = prd.Save(m.cfg.PRDFile, p)
			}
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		} else {
			// Generate fix story if one doesn't already exist
			fixID := "FIX-" + m.currentStoryID
			if p, err := prd.Load(m.cfg.PRDFile); err == nil && !p.HasStory(fixID) {
				cmds = append(cmds, generateFixStoryCmd(m.ctx, m.cfg, msg.Info))
			} else {
				m.phase = phaseIterating
				cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
			}
		}

	case fixStoryGeneratedMsg:
		if msg.Err != nil {
			m.claudeContent += "\n" + tsLog("── Fix story generation failed: %s ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		} else if msg.StoryID != "" {
			m.claudeContent += "\n" + tsLog("── Fix story generated: %s ──\n", msg.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	// --- Parallel execution messages ---
	case coordinator.DAGAnalyzedMsg:
		if msg.Err != nil || msg.DAG == nil {
			// Fallback to serial
			m.claudeContent += "\n" + tsLog("── DAG analysis failed: %v — falling back to serial ──\n", msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.cfg.Workers = 1
			m.phase = phaseIterating
			cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))
		} else {
			m.storyDAG = msg.DAG
			p, err := prd.Load(m.cfg.PRDFile)
			if err != nil {
				m.phase = phaseDone
				m.exitCode = 1
				m.completionReason = fmt.Sprintf("Failed to load prd.json for parallel execution: %v", err)
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.showCompletionReport()
				return m, nil
			}
			m.livePRD = p
			// Filter to incomplete stories only
			var incomplete []prd.UserStory
			for _, s := range p.UserStories {
				if !s.Passes {
					incomplete = append(incomplete, s)
				}
			}
			// Resolve --workers auto now that we know the story count
			m.cfg.ResolveAutoWorkers(len(incomplete))
			if m.cfg.WorkersAuto {
				m.claudeContent += "\n" + tsLog("── Auto workers: scaling to %d (DAG width cap) ──\n", m.cfg.Workers)
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
			}
			m.coord = coordinator.New(m.cfg, m.storyDAG, m.cfg.Workers, incomplete)
			m.coord.SetRunCosting(m.runCosting)
			m.coord.SetNotifier(m.notifier)
			m.phase = phaseParallel
			m.updateStatusPage()
			m.coord.ScheduleReady(m.ctx)
			cmds = append(cmds, m.coord.ListenCmd())
		}

	case coordinator.WorkerUpdateMsg:
		u := msg.Update
		willRetry := m.coord.HandleUpdate(u)
		m.updateStatusPage()

		// Track usage data from parallel workers
		if u.TokenUsage != nil && m.runCosting != nil {
			m.runCosting.AddIteration(u.StoryID, *u.TokenUsage, 0)
			m.costsContent = renderCostsContent(m.runCosting, m.storyDisplayInfos)
		}
		// Update rate limit info from parallel workers
		if u.RateLimitInfo != nil {
			m.rateLimitInfo = u.RateLimitInfo
		}

		// Forward worker status messages to the status bar
		if u.StatusText != "" {
			m.statusText = u.StatusText
			if u.StatusWarn {
				m.statusLevel = statusWarn
			} else {
				m.statusLevel = statusInfo
			}
			cmds = append(cmds, tea.Tick(5*time.Second, func(time.Time) tea.Msg {
				return statusClearMsg{}
			}))
		}

		// Usage limit — pause everything and wait for user
		if u.UsageLimit {
			m.pausedDuring = m.phase
			m.phase = phasePaused
			m.claudeContent += "\n" + tsLog("── Usage Limit Hit (%s) ──\n", u.StoryID) + "Claude API usage limit reached. All workers paused.\nPress Enter to resume when your limit resets.\n"
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		}

		// Auto-select first worker if none selected yet
		if m.activeWorkerView == 0 {
			m.activeWorkerView = u.WorkerID
		}

		// Display judge result if present
		if u.JudgeResult != nil {
			m.judgeContent += judge.FormatResult(u.StoryID, *u.JudgeResult)
			m.ctxMode = contextJudge
			judge.AppendJudgeResult(m.cfg.ProgressFile, u.StoryID, *u.JudgeResult)
		}

		switch u.State {
		case worker.WorkerDone:
			// Cache the activity log before workspace cleanup
			m.cacheWorkerLog(u.WorkerID)
			// Preserve logs to .ralph/logs/ so they survive workspace destruction
			m.coord.PreserveWorkerLogs(u.StoryID, u.WorkerID)

			// Check if this is a fusion worker — if so, collect result and dispatch comparison when ready
			if fg := m.coord.FusionGroupReady(u.StoryID); fg != nil {
				done, total := len(fg.Results), fg.Expected
				m.claudeContent += "\n" + tsLog("── Fusion %s: all %d/%d implementations complete — comparing ──\n", u.StoryID, done, total)
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				cmds = append(cmds, fusionCompareCmd(m.ctx, m.coord, u.StoryID, fg))
				break
			} else if m.coord.IsFusionStory(u.StoryID) {
				// Still waiting for other fusion workers
				done, total := m.coord.FusionProgress(u.StoryID)
				m.claudeContent += "\n" + tsLog("── Fusion %s: worker %d done (%d/%d) — waiting for others ──\n", u.StoryID, u.WorkerID, done, total)
				m.claudeVP.SetContent(m.claudeContent)
				m.claudeVP.GotoBottom()
				m.prevClaudeLen = len(m.claudeContent)
				break
			}

			if u.Passed && u.ChangeID != "" {
				m.notifyStoryComplete(u.StoryID, m.coord.StoryTitle(u.StoryID))
				cmds = append(cmds, mergeBackCmd(m.ctx, m.coord, u))
			} else {
				// Abandon the committed change so it doesn't leave an orphaned
				// side branch in the jj history.
				if u.ChangeID != "" {
					_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, u.ChangeID)
				}
				// Preserve activity log for debugging before workspace is destroyed
				m.coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
				go m.coord.CleanupWorker(m.ctx, u.WorkerID)
				if willRetry {
					m.claudeContent += "\n" + tsLog("── Worker %d (%s): story did not pass — retrying ──\n", u.WorkerID, u.StoryID)
					m.claudeVP.SetContent(m.claudeContent)
					m.claudeVP.GotoBottom()
					m.prevClaudeLen = len(m.claudeContent)
				} else {
					errMsg := "story did not pass"
					if u.Err != nil {
						errMsg = u.Err.Error()
					}
					m.notifier.StoryFailed(m.ctx, u.StoryID, errMsg)
				}
				// Try to schedule more
				m.coord.ScheduleReady(m.ctx)
				if m.coord.AllDone() && m.phase != phaseInteractive {
					if m.coord.CompletedCount() == m.totalStories || m.checkPRDAllComplete() {
						m.completedStories = m.totalStories
						return m.transitionToComplete()
					}
					m.phase = phaseDone
					m.allComplete = false
					m.exitCode = 1
					m.completionReason = fmt.Sprintf("Parallel workers done but only %d/%d stories completed (worker did not pass)", m.coord.CompletedCount(), m.totalStories)
					debuglog.Log("entering phaseDone: %s", m.completionReason)
					m.showCompletionReport()
					return m, nil
				}
			}
		case worker.WorkerFailed:
			m.cacheWorkerLog(u.WorkerID)
			// Preserve activity log for debugging before workspace is destroyed
			m.coord.PreserveFailedLogs(u.StoryID, u.WorkerID)
			go m.coord.CleanupWorker(m.ctx, u.WorkerID)
			if willRetry {
				m.claudeContent += "\n" + tsLog("── Worker %d failed (%s): %v — retrying ──\n", u.WorkerID, u.StoryID, u.Err)
			} else {
				errMsg := "unknown error"
				if u.Err != nil {
					errMsg = u.Err.Error()
				}
				m.notifier.StoryFailed(m.ctx, u.StoryID, errMsg)
				m.claudeContent += "\n" + tsLog("── Worker %d failed (%s): %v ──\n", u.WorkerID, u.StoryID, u.Err)
			}
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			m.coord.ScheduleReady(m.ctx)
			if m.coord.AllDone() && m.phase != phaseInteractive {
				if m.checkPRDAllComplete() {
					m.completedStories = m.totalStories
					return m.transitionToComplete()
				}
				m.phase = phaseDone
				m.exitCode = 1
				m.completionReason = fmt.Sprintf("Worker failed and all work done (%d/%d completed)", m.coord.CompletedCount(), m.totalStories)
				debuglog.Log("entering phaseDone: %s", m.completionReason)
				m.showCompletionReport()
				return m, nil
			}
		}
		// Only keep listening if there are active workers; otherwise we'd
		// block forever on the update channel with no one sending.
		if m.coord.ActiveCount() > 0 {
			cmds = append(cmds, m.coord.ListenCmd())
		} else if len(cmds) > 0 {
			// There are pending commands (e.g. mergeBackCmd) — don't enter
			// phaseDone yet; let them run and check completion afterwards.
		} else if m.coord.AllDone() && m.phase != phaseInteractive {
			// No active workers and nothing left to schedule — we're done
			m.allComplete = m.coord.CompletedCount() == m.totalStories || m.checkPRDAllComplete()
			if m.allComplete {
				m.completedStories = m.totalStories
				return m.transitionToComplete()
			}
			m.phase = phaseDone
			m.exitCode = 1
			m.completionReason = fmt.Sprintf("No active workers remaining (%d/%d completed)", m.coord.CompletedCount(), m.totalStories)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
			return m, nil
		}

	case coordinator.FusionCompareDoneMsg:
		m.updateStatusPage()
		if msg.Err != nil || !msg.Passed {
			reason := "no passing implementations"
			if msg.Err != nil {
				reason = msg.Err.Error()
			}
			m.claudeContent += "\n" + tsLog("── Fusion %s failed: %s ──\n", msg.StoryID, reason)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			// Abandon all change IDs
			for _, cid := range msg.LoserChangeIDs {
				_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, cid)
			}
			if msg.WinnerChangeID != "" {
				_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, msg.WinnerChangeID)
			}
			// Clean up all fusion workers
			for _, wid := range msg.LoserWorkerIDs {
				go m.coord.CleanupWorker(m.ctx, wid)
			}
			m.coord.CompleteFusion(msg.StoryID, false)
			m.coord.ScheduleReady(m.ctx)
		} else {
			m.claudeContent += "\n" + tsLog("── Fusion %s: winner selected (worker %d) — %s ──\n", msg.StoryID, msg.WinnerWorkerID, msg.Reason)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			// Abandon losers
			for i, cid := range msg.LoserChangeIDs {
				_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, cid)
				go m.coord.CleanupWorker(m.ctx, msg.LoserWorkerIDs[i])
			}
			// Merge winner
			m.coord.CompleteFusion(msg.StoryID, true)
			winnerUpdate := worker.WorkerUpdate{
				WorkerID: msg.WinnerWorkerID,
				StoryID:  msg.StoryID,
				ChangeID: msg.WinnerChangeID,
				Passed:   true,
			}
			cmds = append(cmds, mergeBackCmd(m.ctx, m.coord, winnerUpdate))
			m.notifyStoryComplete(msg.StoryID, m.coord.StoryTitle(msg.StoryID))
		}
		if m.coord.ActiveCount() > 0 {
			cmds = append(cmds, m.coord.ListenCmd())
		}

	case coordinator.MergeCompleteMsg:
		m.updateStatusPage()
		if msg.Err != nil {
			// Abandon the change so it doesn't leave an orphaned side branch.
			if msg.ChangeID != "" {
				_ = workspace.AbandonChange(m.ctx, m.cfg.ProjectDir, msg.ChangeID)
			}
			m.claudeContent += "\n" + tsLog("── Merge failed (%s): %v ──\n", msg.StoryID, msg.Err)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		} else if msg.ConflictsResolved {
			m.claudeContent += "\n" + tsLog("── Merged %s into main (conflicts resolved) ──\n", msg.StoryID)
		} else {
			m.claudeContent += "\n" + tsLog("── Merged %s into main ──\n", msg.StoryID)
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			// Update story counts immediately (don't wait for slow tick)
			m.completedStories = m.coord.CompletedCount()
		}
		m.cacheWorkerLog(msg.WorkerID)
		go m.coord.CleanupWorker(m.ctx, msg.WorkerID)
		// Schedule more work
		m.coord.ScheduleReady(m.ctx)
		// Re-register listener if new workers were launched — without this,
		// workers scheduled from a merge-complete (rather than a worker-update)
		// would send updates into the channel with nobody listening, stalling
		// the entire run.
		if m.coord.ActiveCount() > 0 {
			cmds = append(cmds, m.coord.ListenCmd())
		} else if m.coord.AllDone() {
			// Final sync of story counts
			m.completedStories = m.coord.CompletedCount()
			if m.completedStories == m.totalStories || m.checkPRDAllComplete() {
				m.completedStories = m.totalStories
				return m.transitionToComplete()
			}
			m.phase = phaseDone
			m.allComplete = false
			m.exitCode = 1
			m.completionReason = fmt.Sprintf("All parallel work done after merge but only %d/%d stories completed", m.completedStories, m.totalStories)
			debuglog.Log("entering phaseDone: %s", m.completionReason)
			m.showCompletionReport()
			return m, nil
		}

	case coordinator.WorkerActivityMsg:
		// Don't overwrite cached content with empty content — this happens
		// when the workspace is destroyed but the poll fires one more time.
		if msg.Content == "" {
			break
		}
		m.workerLogCache[msg.WorkerID] = msg.Content
		if msg.WorkerID == m.activeWorkerView {
			m.claudeContent = msg.Content
			newLen := len(msg.Content)
			m.claudeVP.SetContent(msg.Content)
			if newLen > m.prevClaudeLen {
				m.claudeVP.GotoBottom()
			}
			m.prevClaudeLen = newLen
		}

	case coordinator.WorkerStuckMsg:
		m.notifier.StoryStuck(m.ctx, msg.StoryID, fmt.Sprintf("worker %d stuck", msg.WorkerID))

	case judgeDoneMsg:
		debuglog.Log("judgeDone: story=%s passed=%v reason=%s", m.currentStoryID, msg.Result.Passed, msg.Result.Reason)
		// Show judge result in the context panel
		m.judgeContent += judge.FormatResult(m.currentStoryID, msg.Result)
		m.ctxMode = contextJudge

		// Persist judge result to progress.md
		judge.AppendJudgeResult(m.cfg.ProgressFile, m.currentStoryID, msg.Result)

		if msg.Result.Passed {
			judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventJudgeResult,
				StoryID: m.currentStoryID,
				Summary: "Judge passed: " + msg.Result.Reason,
				Meta:    map[string]string{"verdict": "pass"},
			})
		} else {
			m.notifier.StoryFailed(m.ctx, m.currentStoryID, "Judge rejected: "+msg.Result.Reason)
			judge.IncrementRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
			_ = events.Append(m.cfg.ProjectDir, events.Event{
				Type:    events.EventJudgeResult,
				StoryID: m.currentStoryID,
				Summary: "Judge failed: " + msg.Result.Reason,
				Errors:  msg.Result.CriteriaFailed,
				Meta:    map[string]string{"verdict": "fail"},
			})
		}
		// Either way, move to next iteration
		m.phase = phaseIterating
		cmds = append(cmds, findNextStoryCmd(m.cfg.PRDFile))

	// --- Quality review messages ---
	case qualityReviewDoneMsg:
		m.ctxMode = contextQuality
		if msg.Err != nil {
			errMsg := "\n" + tsLog("── Quality review error: %v ──\n", msg.Err)
			m.qualityContent += errMsg
			m.claudeContent += errMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m.transitionToSummary()
		}

		m.lastAssessment = &msg.Assessment
		summary := quality.FormatSummary(msg.Assessment)
		m.claudeContent += "\n" + summary
		m.qualityContent += "\n" + summary
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		if msg.Assessment.TotalFindings() == 0 {
			var statusMsg string
			if msg.Assessment.HasParseFailures() {
				statusMsg = "\n" + tsLog("── Quality review: no findings parsed (some lenses failed to parse) ──\n")
			} else {
				statusMsg = "\n" + tsLog("── Quality review: all clean! ──\n")
			}
			m.claudeContent += statusMsg
			m.qualityContent += statusMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m.transitionToSummary()
		}

		// Has findings — start fix phase
		m.phase = phaseQualityFix
		fixMsg := "\n" + tsLog("── Fixing quality issues... ──\n")
		m.claudeContent += fixMsg
		m.qualityContent += fixMsg
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityFixCmd(m.ctx, m.cfg, msg.Assessment, m.qualityIteration))

	case qualityFixDoneMsg:
		if msg.Err != nil {
			errMsg := "\n" + tsLog("── Quality fix error: %v ──\n", msg.Err)
			m.claudeContent += errMsg
			m.qualityContent += errMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
		}

		if m.qualityIteration >= m.cfg.QualityMaxIters {
			// Max iterations reached — prompt user
			m.phase = phaseQualityPrompt
			maxMsg := "\n" + tsLog("── Max quality iterations reached. Press Enter to continue fixing, q to finish ──\n")
			m.claudeContent += maxMsg
			m.qualityContent += maxMsg
			m.claudeVP.SetContent(m.claudeContent)
			m.claudeVP.GotoBottom()
			m.prevClaudeLen = len(m.claudeContent)
			return m, nil
		}

		// Re-review
		m.qualityIteration++
		m.phase = phaseQualityReview
		reReviewMsg := "\n" + tsLog("── Re-reviewing (iteration %d)... ──\n", m.qualityIteration)
		m.claudeContent += reReviewMsg
		m.qualityContent += reReviewMsg
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		cmds = append(cmds, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration))

	case synthesisDoneMsg:
		if msg.Err != nil {
			debuglog.Log("post-run synthesis error (non-fatal): %v", msg.Err)
			m.claudeContent += tsLog("── Synthesis error (non-fatal): %v ──\n", msg.Err)
		} else {
			m.claudeContent += tsLog("── Post-run synthesis complete ──\n")
		}
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)

		// Increment run counter and check if dream consolidation is needed
		runCount, err := memory.IncrementRunCount(m.cfg.ProjectDir)
		if err != nil {
			debuglog.Log("run counter increment error (non-fatal): %v", err)
		}
		if memory.ShouldDream(m.cfg.ProjectDir, m.cfg.Memory.DreamEveryNRuns) {
			debuglog.Log("dream consolidation triggered (run_count=%d, threshold=%d)", runCount, m.cfg.Memory.DreamEveryNRuns)
			m.claudeContent += tsLog("── Running dream consolidation... ──\n")
			m.claudeVP.SetContent(m.claudeContent)
			m.prevClaudeLen = len(m.claudeContent)
			return m, dreamCmd(m.ctx, m.cfg)
		}
		return m.finishSummary()

	case dreamDoneMsg:
		if msg.Err != nil {
			debuglog.Log("dream consolidation error (non-fatal): %v", msg.Err)
			m.claudeContent += tsLog("── Dream error (non-fatal): %v ──\n", msg.Err)
		} else {
			m.claudeContent += tsLog("── Dream consolidation complete ──\n")
		}
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		return m.finishSummary()

	case summaryDoneMsg:
		if msg.Err != nil {
			m.claudeContent += "\n" + tsLog("── Summary generation error: %v ──\n", msg.Err)
		}
		if msg.Content != "" {
			m.claudeContent = msg.Content
		} else {
			m.claudeContent += "\n" + tsLog("── Summary generation complete (no SUMMARY.md produced) ──\n")
		}
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		// Best-effort checkpoint cleanup on clean completion
		_ = checkpoint.Delete(m.cfg.ProjectDir)
		m.persistRunHistory()
		m.phase = phaseInteractive
		m.allComplete = true
		m.claudeContent += "\n" + tsLog("── Interactive mode — all PRD stories complete, accepting follow-up tasks ──\n")
		m.claudeVP.SetContent(m.claudeContent)
		m.claudeVP.GotoBottom()
		m.prevClaudeLen = len(m.claudeContent)
		debuglog.Log("entering phaseInteractive after PRD completion")
		m.updateStatusPage()

	}

	return m, tea.Batch(cmds...)
}

// checkPRDAllComplete checks the PRD file directly to see if all stories
// are marked as passing. This is a fallback for when the coordinator's
// completed count doesn't match totalStories (e.g. after a restart without
// a valid checkpoint, where only the remaining stories were dispatched).
func (m *Model) checkPRDAllComplete() bool {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		return false
	}
	return p.AllComplete()
}

// transitionToComplete handles the "all stories done" transition.
// If quality review is enabled and hasn't run yet, starts quality review.
// Otherwise, transitions to summary generation.
func (m *Model) transitionToComplete() (tea.Model, tea.Cmd) {
	debuglog.Log("transitionToComplete: iteration=%d, currentStory=%s", m.iteration, m.currentStoryID)
	m.updateStatusPage()
	if m.cfg.QualityReview && m.qualityIteration == 0 {
		m.qualityIteration = 1
		m.phase = phaseQualityReview
		m.claudeContent = tsLog("── Starting quality review ──\n")
		m.claudeVP.SetContent(m.claudeContent)
		m.prevClaudeLen = len(m.claudeContent)
		return m, qualityReviewCmd(m.ctx, m.cfg, m.qualityIteration)
	}
	return m.transitionToSummary()
}

// transitionToSummary starts post-run synthesis (if memory is enabled),
// then generates a final summary of all changes.
func (m *Model) transitionToSummary() (tea.Model, tea.Cmd) {
	m.notifier.RunComplete(m.ctx, m.completedStories, m.totalStories, m.totalCost())
	if !m.cfg.Memory.Disabled {
		m.claudeContent += tsLog("── Running post-run synthesis... ──\n")
		m.claudeVP.SetContent(m.claudeContent)
		m.prevClaudeLen = len(m.claudeContent)
		return m, synthesisCmd(m.ctx, m.cfg)
	}
	return m.finishSummary()
}

// finishSummary stops the sidecar and starts summary generation.
func (m *Model) finishSummary() (tea.Model, tea.Cmd) {
	// Stop status page — no more updates needed
	m.stopStatusServer()
	// Best-effort checkpoint cleanup on clean completion
	_ = checkpoint.Delete(m.cfg.ProjectDir)
	m.phase = phaseSummary
	m.claudeContent += tsLog("── Generating summary of all changes... ──\n")
	m.claudeVP.SetContent(m.claudeContent)
	m.prevClaudeLen = len(m.claudeContent)
	return m, generateSummaryCmd(m.ctx, m.cfg)
}

// cacheWorkerLog reads the activity log for a worker and stores it in memory.
func (m *Model) cacheWorkerLog(wID worker.WorkerID) {
	if m.coord == nil {
		return
	}
	// If we already have content from the activity poll, it's already cached
	if _, ok := m.workerLogCache[wID]; ok {
		return
	}
	// Try to read from disk as fallback
	actPath := m.coord.GetWorkerActivityPath(wID)
	if actPath == "" {
		return
	}
	if data, err := os.ReadFile(actPath); err == nil {
		m.workerLogCache[wID] = string(data)
	}
}

// cleanupWorkerLogs removes persisted worker log files from .ralph/logs/.
func (m *Model) cleanupWorkerLogs() {
	logsDir := filepath.Join(m.cfg.ProjectDir, ".ralph", "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "worker-") && strings.HasSuffix(e.Name(), ".log") {
			_ = os.Remove(filepath.Join(logsDir, e.Name()))
		}
	}
}

// switchToWorkerTab switches the active worker view to the worker at the given tab index.
func (m *Model) switchToWorkerTab(idx int) {
	if idx < 0 || idx >= len(m.workerTabOrder) {
		return
	}
	wID := m.workerTabOrder[idx]
	m.activeWorkerView = wID
	if cached, ok := m.workerLogCache[wID]; ok {
		m.claudeContent = cached
		m.prevClaudeLen = len(cached)
	} else {
		m.claudeContent = ""
		m.prevClaudeLen = 0
	}
}

// workerTabIndex returns the current tab index for the active worker view.
func (m *Model) workerTabIndex() int {
	for i, id := range m.workerTabOrder {
		if id == m.activeWorkerView {
			return i
		}
	}
	return 0
}

// totalCost returns the current run total cost, or 0 if cost tracking is not available.
func (m *Model) totalCost() float64 {
	if m.runCosting == nil {
		return 0
	}
	return m.runCosting.GetTotalCost()
}

// storyCost returns the cost for a specific story, or 0 if not tracked.
func (m *Model) storyCost(storyID string) float64 {
	if m.runCosting == nil {
		return 0
	}
	return m.runCosting.GetStoryCost(storyID)
}

// notifyStoryComplete sends a story-complete notification enriched with cost if available.
func (m *Model) notifyStoryComplete(storyID, title string) {
	if cost := m.storyCost(storyID); cost > 0 {
		m.notifier.StoryComplete(m.ctx, storyID, fmt.Sprintf("%s ($%.2f)", title, cost))
	} else {
		m.notifier.StoryComplete(m.ctx, storyID, title)
	}
}

// writeSerialCheckpoint writes a checkpoint after each serial iteration completes.
func (m *Model) writeSerialCheckpoint(iterErr error) {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		return
	}

	var completed []string
	failed := make(map[string]checkpoint.FailedStory)

	for _, s := range p.UserStories {
		if s.Passes {
			completed = append(completed, s.ID)
		}
	}

	// If the current iteration had an error, record the current story as failed
	if iterErr != nil && m.currentStoryID != "" {
		failed[m.currentStoryID] = checkpoint.FailedStory{
			Retries:   1,
			LastError: iterErr.Error(),
		}
	}

	// Recompute PRD hash from current file (it may have been modified during the run)
	if hash, err := checkpoint.ComputePRDHash(m.cfg.PRDFile); err == nil {
		m.prdHash = hash
	}

	cp := checkpoint.Checkpoint{
		PRDHash:          m.prdHash,
		Phase:            "serial",
		CompletedStories: completed,
		FailedStories:    failed,
		InProgress:       nil,
		DAG:              nil,
		IterationCount:   m.iteration,
		Timestamp:        time.Now(),
	}

	if m.runCosting != nil {
		snap := m.runCosting.Snapshot()
		cp.CostData = &snap
	}

	_ = checkpoint.Save(m.cfg.ProjectDir, cp)
}

func (m *Model) handleJudgeCheck() tea.Cmd {
	// Skip judge if no pre-revisions were captured
	if len(m.preRevs) == 0 {
		debuglog.Log("handleJudgeCheck: skipping judge for %s — no pre-revisions", m.currentStoryID)
		return nil
	}

	// Reload PRD to check if story passes
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		debuglog.Log("handleJudgeCheck: prd load error: %v", err)
		return nil
	}
	story := p.FindStory(m.currentStoryID)
	if story == nil || !story.Passes {
		debuglog.Log("handleJudgeCheck: story %s not passing (story=%v), skipping judge", m.currentStoryID, story != nil)
		return nil
	}

	// Story claims to pass — run judge
	rejections := judge.GetRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
	if rejections >= m.cfg.JudgeMaxRejections {
		debuglog.Log("handleJudgeCheck: auto-passing %s after %d rejections", m.currentStoryID, rejections)
		// Auto-pass
		judge.AppendAutoPass(m.cfg.ProgressFile, m.currentStoryID, rejections)
		judge.ClearRejectionCount(m.cfg.ProjectDir, m.currentStoryID)
		m.judgeContent += fmt.Sprintf("\n── Judge: %s ── AUTO-PASS after %d rejections [HUMAN REVIEW NEEDED] ──\n", m.currentStoryID, rejections)
		m.ctxMode = contextJudge
		return nil
	}

	m.phase = phaseJudgeRun
	m.judgeContent += "\n── Judge reviewing " + m.currentStoryID + "... ──\n"
	m.ctxMode = contextJudge
	return runJudgeCmd(m.ctx, m.cfg, m.currentStoryID, m.preRevs)
}

func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.phase == phaseResumePrompt && m.loadedCheckpoint != nil {
		return m.renderResumePrompt()
	}

	// Render header and footer first so we can measure their actual height
	header := renderHeader(m, m.width)
	footer := renderFooter(m.width, m.confirmQuit, m.phase == phaseDone, m.phase == phaseIdle, m.phase == phaseParallel || m.phase == phaseInteractive, m.phase == phaseReview, m.phase == phaseQualityPrompt, m.phase == phaseResumePrompt, m.phase == phasePaused, m.mascot != nil && m.mascot.Interactive)

	// Use lipgloss.Height() for dynamic layout instead of hardcoded values
	headerHeight := lipgloss.Height(header)
	footerHeight := lipgloss.Height(footer)
	statusBarHeight := 0
	if m.stuckAlert != nil {
		statusBarHeight = 1
	}
	statusLineHeight := 0
	if m.statusText != "" {
		statusLineHeight = 1
	}
	hintHeight := 0
	if m.hintActive {
		hintHeight = 3
	}
	taskInputHeight := 0
	if m.taskInputActive || m.phase == phaseInteractive || m.phase == phaseParallel {
		taskInputHeight = 3
		if m.isClarifying() && m.taskInputActive {
			taskInputHeight = 4 // extra line for question display
		}
	}
	available := m.height - headerHeight - footerHeight - statusBarHeight - statusLineHeight - hintHeight - taskInputHeight
	if available < 10 {
		available = 10
	}

	// Split: 35% top panels, 65% claude activity
	topHeight := available * 35 / 100
	if topHeight < 5 {
		topHeight = 5
	}
	claudeHeight := available - topHeight

	storiesWidth := m.width * 35 / 100
	contextWidth := m.width - storiesWidth

	// Stories panel
	storiesPanel := renderStoriesPanel(
		&m.storiesVP,
		m.storyDisplayInfos,
		m.activePanel == panelStories,
		storiesWidth,
		topHeight,
		m.animFrame,
		m.storiesSelectedIdx,
		m.storiesExpandedID,
		m.cfg.PRDFile,
		m.storyDAG,
	)

	// Context panel
	ctxData := contextPanelData{
		Mode:             m.ctxMode,
		ProgressContent:  m.progressContent,
		ProgressChanged:  m.progressChanged,
		WorktreeContent:  m.worktreeContent,
		JudgeContent:     m.judgeContent,
		QualityContent:   m.qualityContent,
		MemoryContent:    m.memoryContent,
		CostsContent:        m.costsContent,
		AntiPatternsContent: m.antiPatternsContent,
		RateLimitContent:    renderRateLimitContent(m.rateLimitInfo),
		Phase:               m.phase,
		Settings:            &m.settings,
	}
	ctxPanel := renderContextPanel(
		&m.contextVP,
		ctxData,
		m.activePanel == panelContext,
		contextWidth,
		topHeight,
	)
	m.progressChanged = false

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, storiesPanel, ctxPanel)

	// Claude panel
	claudeRunning := m.phase == phaseClaudeRun || m.phase == phaseJudgeRun || m.phase == phaseParallel || m.phase == phaseInteractive || m.phase == phaseDagAnalysis || m.phase == phasePlanning || m.phase == phaseQualityReview || m.phase == phaseQualityFix || m.phase == phaseSummary
	var workerTabStr string
	if (m.phase == phaseParallel || m.phase == phaseInteractive) && m.coord != nil {
		workers := m.coord.Workers()
		// Sort: active workers first (by ID), then completed/failed (by ID)
		var activeIDs, doneIDs []worker.WorkerID
		for id, w := range workers {
			if w.State == worker.WorkerIdle {
				continue
			}
			if w.State == worker.WorkerDone || w.State == worker.WorkerFailed {
				doneIDs = append(doneIDs, id)
			} else {
				activeIDs = append(activeIDs, id)
			}
		}
		sort.Slice(activeIDs, func(i, j int) bool { return activeIDs[i] < activeIDs[j] })
		sort.Slice(doneIDs, func(i, j int) bool { return doneIDs[i] < doneIDs[j] })
		m.workerTabOrder = append(activeIDs, doneIDs...)

		var tabParts []string
		for tabIdx, id := range m.workerTabOrder {
			w := workers[id]
			marker := ""
			if id == m.activeWorkerView {
				marker = "▸"
			}
			roleStr := ""
			if w.Role != "" {
				roleStr = " " + string(w.Role)
			}
			tabParts = append(tabParts, fmt.Sprintf("%s%d:%s%s[%s]", marker, tabIdx+1, w.StoryID, roleStr, w.State))
		}
		workerTabStr = strings.Join(tabParts, " │ ")
	}
	claudePanel := renderClaudePanel(
		&m.claudeVP,
		m.spinner,
		m.claudeContent,
		claudeRunning,
		m.activePanel == panelClaude,
		m.width,
		claudeHeight,
		workerTabStr,
	)

	parts := []string{header, topRow, claudePanel}
	if m.phase == phaseInteractive || m.phase == phaseParallel {
		parts = append(parts, renderTaskInput(m.taskInput, m.width, m.taskInputActive, m.clarifyQuestions, m.clarifyIndex))
	}
	if m.stuckAlert != nil {
		parts = append(parts, renderStuckBar(m.stuckAlert, m.width, m.hintActive))
	}
	if m.hintActive {
		parts = append(parts, renderHintInput(m.hintInput, m.width))
	}
	if m.statusText != "" {
		parts = append(parts, renderStatusLine(m.statusText, m.statusLevel, m.width))
	}
	parts = append(parts, footer)

	output := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Overlay sprite mascot before line clamping
	if m.mascot != nil {
		output = m.mascot.Overlay(output)
	}

	// Clamp to exactly terminal height to prevent scrolling/jitter
	lines := strings.Split(output, "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// renderResumePrompt renders the checkpoint resume prompt as a full-screen view.
func (m *Model) renderResumePrompt() string {
	cp := m.loadedCheckpoint

	var b strings.Builder
	header := renderHeader(m, m.width)
	b.WriteString(header)
	b.WriteString("\n")

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFD700")).Render("  Checkpoint Found — Resume Previous Run?")
	b.WriteString(title)
	b.WriteString("\n\n")

	// Mode
	b.WriteString(fmt.Sprintf("  Mode: %s\n\n", stylePanelTitle.Render(cp.Phase)))

	// Completed stories
	b.WriteString(fmt.Sprintf("  %s (%d)\n", styleSuccess.Render("Completed"), len(cp.CompletedStories)))
	for _, id := range cp.CompletedStories {
		b.WriteString(fmt.Sprintf("    %s %s\n", styleStoryPassed.Render("✓"), id))
	}
	if len(cp.CompletedStories) == 0 {
		b.WriteString("    (none)\n")
	}
	b.WriteString("\n")

	// Failed stories
	b.WriteString(fmt.Sprintf("  %s (%d)\n", styleDanger.Render("Failed"), len(cp.FailedStories)))
	for id, fs := range cp.FailedStories {
		errSummary := fs.LastError
		if len(errSummary) > 60 {
			errSummary = errSummary[:60] + "..."
		}
		b.WriteString(fmt.Sprintf("    %s %s (retries: %d) %s\n", styleStoryFailed.Render("✗"), id, fs.Retries, styleMuted.Render(errSummary)))
	}
	if len(cp.FailedStories) == 0 {
		b.WriteString("    (none)\n")
	}
	b.WriteString("\n")

	// Remaining count
	totalKnown := len(cp.CompletedStories) + len(cp.FailedStories) + len(cp.InProgress)
	if p, err := prd.Load(m.cfg.PRDFile); err == nil {
		remaining := p.TotalCount() - len(cp.CompletedStories)
		b.WriteString(fmt.Sprintf("  %s %d\n\n", styleMuted.Render("Remaining:"), remaining))
	} else {
		b.WriteString(fmt.Sprintf("  %s %d+ stories tracked\n\n", styleMuted.Render("Total:"), totalKnown))
	}

	// Prompt
	b.WriteString("  Press " + styleKey.Render("y") + " to resume, " + styleKey.Render("n") + " to start fresh, " + styleKey.Render("q") + " to quit\n")

	return b.String()
}

// rebuildStoryDisplayInfos reconstructs display infos from cached PRD stories.
// Called on fast tick (500ms) but uses cached data to avoid disk I/O.
func (m *Model) rebuildStoryDisplayInfos() {
	if len(m.cachedPRDStories) == 0 && !m.isClarifying() {
		return
	}
	prevCount := len(m.storyDisplayInfos)
	var coordIface interface {
		Workers() map[worker.WorkerID]*worker.Worker
	}
	if m.coord != nil {
		coordIface = m.coord
	}
	m.storyDisplayInfos = BuildStoryDisplayInfos(m.cachedPRDStories, m.currentStoryID, coordIface, m.phase, m.iteration, string(m.currentRole))
	// Inject clarifying task as a temporary display entry
	if m.isClarifying() {
		m.storyDisplayInfos = append(m.storyDisplayInfos, StoryDisplayInfo{
			ID:            "T-?",
			Title:         m.clarifyingTask,
			IsInteractive: true,
			Status:        "clarifying",
		})
	}
	// Auto-scroll to show newly added tasks
	if len(m.storyDisplayInfos) > prevCount {
		m.storiesVP.GotoBottom()
	}
}

// clampLines truncates or pads a string to exactly n lines.
// tailTruncate returns the last maxLen bytes of s, or s unchanged if shorter.
func tailTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[len(s)-maxLen:]
}

func clampLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func renderStuckBar(info *runner.StuckInfo, width int, hintActive bool) string {
	icon := " ⚠ STUCK "
	detail := fmt.Sprintf(" %s — %s (%dx) ", info.StoryID, info.Pattern, info.Count)
	if len(info.Commands) > 0 {
		cmd := info.Commands[0]
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		detail += "→ " + cmd + " "
	}
	hint := ""
	if !hintActive {
		hint = " [i: inject hint] "
	}
	content := styleStuckBar.Render(icon) + styleStuckBarDetail.Render(detail+hint)
	// Pad to full width with the danger background
	contentWidth := lipgloss.Width(content)
	if contentWidth < width {
		content += styleStuckBarDetail.Render(strings.Repeat(" ", width-contentWidth))
	}
	return content
}

func renderStatusLine(text string, level statusLevel, width int) string {
	style := styleStatusInfo
	switch level {
	case statusWarn:
		style = styleStatusWarn
	case statusError:
		style = styleStatusError
	}
	content := style.Render(" " + text + " ")
	contentWidth := lipgloss.Width(content)
	if contentWidth < width {
		content += style.Render(strings.Repeat(" ", width-contentWidth))
	}
	return content
}

func renderHintInput(ti textarea.Model, width int) string {
	label := styleStuckBar.Render(" HINT ")
	esc := styleFooter.Render(" esc: cancel  enter: submit")
	return label + " " + ti.View() + esc
}

func renderTaskInput(ti textarea.Model, width int, active bool, clarifyQuestions []string, clarifyIndex int) string {
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render("⚡ Task:")
	if len(clarifyQuestions) > 0 && active {
		// Show current question above the input
		qIdx := clarifyIndex
		if qIdx >= len(clarifyQuestions) {
			qIdx = len(clarifyQuestions) - 1
		}
		qLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).
			Render(fmt.Sprintf("  Q%d. %s", qIdx+1, clarifyQuestions[qIdx]))
		help := styleFooter.Render(" esc: cancel task  enter: submit answer")
		return qLabel + "\n" + label + " " + ti.View() + help
	}
	if active {
		help := styleFooter.Render(" esc: cancel  enter: submit  tab: switch")
		return label + " " + ti.View() + help
	}
	placeholder := styleFooter.Render(" press t to add a task")
	return label + placeholder
}

func renderFooter(width int, confirmQuit bool, done bool, idle bool, parallel bool, review bool, qualityPrompt bool, resumePrompt bool, paused bool, interactive bool) string {
	if interactive {
		return "  " + styleKey.Render("arrows") + styleFooter.Render(": move  ") +
			styleKey.Render("space") + styleFooter.Render(": jump  ") +
			styleKey.Render("p/esc") + styleFooter.Render(": exit play mode  ") +
			styleKey.Render("q") + styleFooter.Render(": quit")
	}
	if paused {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": resume  ") +
			styleKey.Render("q") + styleFooter.Render(": quit")
	}
	if resumePrompt {
		return "  " + styleKey.Render("y") + styleFooter.Render(": resume  ") +
			styleKey.Render("n") + styleFooter.Render(": start fresh  ") +
			styleKey.Render("q") + styleFooter.Render(": quit")
	}
	if confirmQuit {
		return "  " + styleQuitConfirm.Render("Press q again to quit, any other key to cancel")
	}
	baseHelp := styleKey.Render("q") + styleFooter.Render(": quit  ") +
		styleKey.Render("tab") + styleFooter.Render(": panel  ") +
		styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
		styleKey.Render("j/k") + styleFooter.Render(": scroll  ") +
		styleKey.Render("s") + styleFooter.Render(": settings  ") +
		styleKey.Render("m") + styleFooter.Render(": monitor")
	if parallel {
		baseHelp += "  " + styleKey.Render("</>") + styleFooter.Render(": worker") +
			"  " + styleKey.Render("t") + styleFooter.Render(": task")
	} else if done {
		baseHelp += "  " + styleKey.Render("t") + styleFooter.Render(": new task")
	}
	if qualityPrompt {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": continue fixing  ") +
			styleKey.Render("q") + styleFooter.Render(": finish  ") +
			styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
	}
	if review {
		return "  " + styleKey.Render("enter") + styleFooter.Render(": execute  ") +
			styleKey.Render("q") + styleFooter.Render(": quit  ") +
			styleKey.Render("[/]") + styleFooter.Render(": context tab  ") +
			styleKey.Render("j/k") + styleFooter.Render(": scroll")
	}
	if idle {
		return "  " + styleMuted.Render("Idle — ") + baseHelp
	}
	if done {
		return "  " + styleSuccess.Render("Run complete — ") + baseHelp
	}
	return "  " + baseHelp
}

// generateCompletionReport builds a human-readable report of the run outcome,
// explaining which stories were completed, which were not, and why.
func (m *Model) generateCompletionReport() string {
	var sb strings.Builder

	sb.WriteString("\n── Completion Report ──\n\n")
	sb.WriteString(fmt.Sprintf("Reason: %s\n", m.completionReason))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", time.Since(m.startTime).Truncate(time.Second)))
	sb.WriteString(fmt.Sprintf("Iterations used: %d\n\n", m.iteration))

	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		sb.WriteString(fmt.Sprintf("Could not load prd.json: %v\n", err))
		return sb.String()
	}

	var passed, incomplete []prd.UserStory
	for _, s := range p.UserStories {
		if s.Passes {
			passed = append(passed, s)
		} else {
			incomplete = append(incomplete, s)
		}
	}

	sb.WriteString(fmt.Sprintf("Stories: %d/%d completed\n\n", len(passed), len(p.UserStories)))

	if len(passed) > 0 {
		sb.WriteString("Completed:\n")
		for _, s := range passed {
			sb.WriteString(fmt.Sprintf("  ✓ %s: %s\n", s.ID, s.Title))
		}
		sb.WriteString("\n")
	}

	if len(incomplete) > 0 {
		sb.WriteString("Incomplete:\n")
		for _, s := range incomplete {
			reason := m.inferStorySkipReason(s.ID)
			sb.WriteString(fmt.Sprintf("  ✗ %s: %s\n    Reason: %s\n", s.ID, s.Title, reason))
		}
	}

	// Write report to log file
	reportPath := filepath.Join(m.cfg.LogDir, "completion-report.log")
	_ = os.WriteFile(reportPath, []byte(sb.String()), 0o644)

	return sb.String()
}

// inferStorySkipReason tries to determine why a story was not completed.
func (m *Model) inferStorySkipReason(storyID string) string {
	// Check parallel coordinator state
	if m.coord != nil {
		if m.coord.IsCompleted(storyID) {
			return "Marked completed by coordinator but passes=false in prd.json (possible judge rejection)"
		}
		if m.coord.IsFailed(storyID) {
			if errMsg := m.coord.FailedError(storyID); errMsg != "" {
				return fmt.Sprintf("Worker failed: %s", errMsg)
			}
			return "Worker failed (no error details)"
		}
		// Check if it was blocked by a failed dependency
		if blocked, dep := m.coord.IsBlockedByFailure(storyID); blocked {
			return fmt.Sprintf("Blocked: dependency %q failed", dep)
		}
		return "Not scheduled (may have been unreachable in DAG)"
	}

	// Check events log for clues about this story
	evts, err := events.Load(m.cfg.ProjectDir)
	if err == nil {
		for i := len(evts) - 1; i >= 0; i-- {
			e := evts[i]
			if e.StoryID != storyID {
				continue
			}
			switch e.Type {
			case events.EventStuck:
				return fmt.Sprintf("Got stuck: %s", e.Summary)
			case events.EventContextExhausted:
				return "Claude ran out of context window"
			case events.EventStoryFailed:
				return fmt.Sprintf("Failed: %s", e.Summary)
			}
		}
	}

	return "Not attempted (run ended before this story was reached)"
}

// showCompletionReport generates and displays the completion report in the Claude panel.
func (m *Model) showCompletionReport() {
	m.persistRunHistory()
	m.saveInteractiveSession()
	m.notifier.RunComplete(m.ctx, m.completedStories, m.totalStories, m.totalCost())
	report := m.generateCompletionReport()
	debuglog.Log("completion report:\n%s", report)
	m.claudeContent += report
	m.claudeVP.SetContent(m.claudeContent)
	m.claudeVP.GotoBottom()
	m.prevClaudeLen = len(m.claudeContent)
}

// saveInteractiveSession saves interactive tasks to a session file on clean exit.
func (m *Model) saveInteractiveSession() {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		debuglog.Log("saveInteractiveSession: failed to load PRD: %v", err)
		return
	}
	path, err := interactive.SaveSession(m.cfg.ProjectDir, p.UserStories)
	if err != nil {
		debuglog.Log("saveInteractiveSession: failed to save session: %v", err)
		return
	}
	if path != "" {
		debuglog.Log("saveInteractiveSession: saved to %s", path)
	}
}

// persistRunHistory computes a RunSummary from the current state and appends it to run-history.json.
func (m *Model) persistRunHistory() {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		debuglog.Log("persistRunHistory: failed to load PRD: %v", err)
		return
	}

	var completed, failed int
	for _, s := range p.UserStories {
		if s.Passes {
			completed++
		}
	}

	totalIterations := m.iteration
	if m.coord != nil {
		totalIterations = m.coord.IterationCount()
		failed = m.coord.FailedCount()
	} else {
		failed = len(p.UserStories) - completed
	}

	var avgIter float64
	if completed > 0 {
		avgIter = float64(totalIterations) / float64(completed)
	}

	// Compute judge rejection rate and per-story judge rejects from events
	var judgeTotal, judgeRejections, stuckCount int
	judgeRejectsPerStory := make(map[string]int)
	evts, err := events.Load(m.cfg.ProjectDir)
	if err == nil {
		for _, e := range evts {
			switch e.Type {
			case events.EventJudgeResult:
				judgeTotal++
				if e.Meta["verdict"] == "fail" {
					judgeRejections++
					judgeRejectsPerStory[e.StoryID]++
				}
			case events.EventStuck:
				stuckCount++
			}
		}
	}

	var rejectionRate float64
	if judgeTotal > 0 {
		rejectionRate = float64(judgeRejections) / float64(judgeTotal)
	}

	// Collect cost and model data from RunCosting
	var totalCost float64
	var totalInputTokens, totalOutputTokens int
	var cacheHitRate float64
	modelsSet := make(map[string]bool)
	storyIterCounts := make(map[string]int)
	storyModels := make(map[string]string)

	if m.runCosting != nil {
		snap := m.runCosting.Snapshot()
		totalCost = snap.TotalCost
		totalInputTokens = snap.TotalInputTokens
		totalOutputTokens = snap.TotalOutputTokens
		cacheHitRate = m.runCosting.CacheHitRate()

		for storyID, sc := range snap.Stories {
			storyIterCounts[storyID] = len(sc.Iterations)
			for _, ic := range sc.Iterations {
				if ic.TokenUsage.Model != "" {
					modelsSet[ic.TokenUsage.Model] = true
					storyModels[storyID] = ic.TokenUsage.Model // last model wins
				}
			}
		}
	}

	var modelsUsed []string
	for model := range modelsSet {
		modelsUsed = append(modelsUsed, model)
	}

	// Compute first-pass rate
	var firstPassRate float64
	if m.coord != nil {
		pq := m.coord.GetPlanQuality()
		if pq.TotalStories > 0 {
			firstPassRate = float64(pq.FirstPassCount) / float64(pq.TotalStories)
		}
	} else if completed > 0 {
		// Serial mode: stories with zero judge rejections are first-pass
		firstPass := 0
		for _, s := range p.UserStories {
			if s.Passes && judgeRejectsPerStory[s.ID] == 0 {
				firstPass++
			}
		}
		firstPassRate = float64(firstPass) / float64(len(p.UserStories))
	}

	// Build per-story details
	var storyDetails []costs.StorySummary
	for _, s := range p.UserStories {
		storyDetails = append(storyDetails, costs.StorySummary{
			StoryID:      s.ID,
			Title:        s.Title,
			Iterations:   storyIterCounts[s.ID],
			Passed:       s.Passes,
			JudgeRejects: judgeRejectsPerStory[s.ID],
			Model:        storyModels[s.ID],
		})
	}

	durationMinutes := time.Since(m.startTime).Minutes()

	summary := costs.RunSummary{
		PRD:                   p.Project,
		Date:                  time.Now().Format(time.RFC3339),
		StoriesTotal:          len(p.UserStories),
		StoriesCompleted:      completed,
		StoriesFailed:         failed,
		TotalCost:             totalCost,
		DurationMinutes:       durationMinutes,
		TotalIterations:       totalIterations,
		AvgIterationsPerStory: avgIter,
		StuckCount:            stuckCount,
		JudgeRejectionRate:    rejectionRate,
		FirstPassRate:         firstPassRate,
		ModelsUsed:            modelsUsed,
		TotalInputTokens:      totalInputTokens,
		TotalOutputTokens:     totalOutputTokens,
		CacheHitRate:          cacheHitRate,
		StoryDetails:          storyDetails,
		Workers:               m.cfg.Workers,
	}

	if err := costs.AppendRun(m.cfg.ProjectDir, summary); err != nil {
		debuglog.Log("persistRunHistory: failed to append run: %v", err)
	} else {
		debuglog.Log("persistRunHistory: appended run summary for %s", p.Project)
	}
}

// buildRunSummary computes a RunSummary from the current model state for use by synthesis.
func (m *Model) buildRunSummary() costs.RunSummary {
	p, err := prd.Load(m.cfg.PRDFile)
	if err != nil {
		return costs.RunSummary{}
	}

	var completed, failed int
	for _, s := range p.UserStories {
		if s.Passes {
			completed++
		}
	}

	totalIterations := m.iteration
	if m.coord != nil {
		totalIterations = m.coord.IterationCount()
		failed = m.coord.FailedCount()
	} else {
		failed = len(p.UserStories) - completed
	}

	var avgIter float64
	if completed > 0 {
		avgIter = float64(totalIterations) / float64(completed)
	}

	var judgeTotal, judgeRejections, stuckCount int
	evts, err := events.Load(m.cfg.ProjectDir)
	if err == nil {
		for _, e := range evts {
			switch e.Type {
			case events.EventJudgeResult:
				judgeTotal++
				if e.Meta["verdict"] == "fail" {
					judgeRejections++
				}
			case events.EventStuck:
				stuckCount++
			}
		}
	}

	var rejectionRate float64
	if judgeTotal > 0 {
		rejectionRate = float64(judgeRejections) / float64(judgeTotal)
	}

	return costs.RunSummary{
		PRD:                   p.Project,
		Date:                  time.Now().Format(time.RFC3339),
		StoriesTotal:          len(p.UserStories),
		StoriesCompleted:      completed,
		StoriesFailed:         failed,
		TotalCost:             m.totalCost(),
		DurationMinutes:       time.Since(m.startTime).Minutes(),
		TotalIterations:       totalIterations,
		AvgIterationsPerStory: avgIter,
		StuckCount:            stuckCount,
		JudgeRejectionRate:    rejectionRate,
	}
}

// isClarifying returns true if the model is in clarification Q&A mode.
func (m *Model) isClarifying() bool {
	return len(m.clarifyQuestions) > 0
}

// clearClarifyState resets all clarification state fields.
// dispatchInteractiveTask creates a story from the task text and dispatches it to a worker.
// Returns a tea.Cmd to listen for worker updates, or nil if dispatch prerequisites aren't met.
func (m *Model) dispatchInteractiveTask(taskText string) tea.Cmd {
	if m.livePRD == nil || m.storyDAG == nil {
		debuglog.Log("dispatchInteractiveTask: skipped — livePRD or storyDAG is nil")
		return nil
	}
	// Bootstrap coordinator if none exists (pure interactive / post-PRD mode)
	if m.coord == nil {
		m.coord = coordinator.New(m.cfg, m.storyDAG, m.cfg.Workers, nil)
		m.coord.SetRunCosting(m.runCosting)
		m.coord.SetNotifier(m.notifier)
		debuglog.Log("bootstrapped coordinator for interactive task dispatch")
	}
	story := m.storyCreator.CreateAndAppend(taskText, "", m.livePRD, m.storyDAG)
	m.coord.AddStory(&story)
	m.totalStories++
	m.coord.ScheduleReady(m.ctx)
	m.claudeContent += tsLog("── Interactive task %s dispatched ──\n", story.ID)
	m.claudeVP.SetContent(m.claudeContent)
	m.claudeVP.GotoBottom()
	m.prevClaudeLen = len(m.claudeContent)
	debuglog.Log("dispatched interactive task %s: %s", story.ID, story.Title)
	// Switch to phaseParallel since there's now an active worker
	if m.phase == phaseInteractive {
		m.phase = phaseParallel
		debuglog.Log("switching from phaseInteractive to phaseParallel for interactive task dispatch")
	}
	// Listen for worker updates
	if m.coord.ActiveCount() > 0 {
		return m.coord.ListenCmd()
	}
	return nil
}

func (m *Model) clearClarifyState() {
	m.clarifyingTask = ""
	m.clarifyQuestions = nil
	m.clarifyAnswers = nil
	m.clarifyIndex = 0
}

// buildClarifyDescription bundles the original task with Q&A into the format:
// Task: {original}\n\nQ: {q1}\nA: {a1}\n\nQ: {q2}\nA: {a2}
func (m *Model) buildClarifyDescription() string {
	var sb strings.Builder
	sb.WriteString("Task: ")
	sb.WriteString(m.clarifyingTask)
	for i, q := range m.clarifyQuestions {
		sb.WriteString("\n\nQ: ")
		sb.WriteString(q)
		sb.WriteString("\nA: ")
		if i < len(m.clarifyAnswers) {
			sb.WriteString(m.clarifyAnswers[i])
		}
	}
	return sb.String()
}
