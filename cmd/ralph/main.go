package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ohjann/ralphplusplus/internal/assets"
	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/coordinator"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/daemon"
	"github.com/ohjann/ralphplusplus/internal/dag"
	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/history"
	"github.com/ohjann/ralphplusplus/internal/memory"
	"github.com/ohjann/ralphplusplus/internal/notify"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/retro"
	"github.com/ohjann/ralphplusplus/internal/runner"
	"github.com/ohjann/ralphplusplus/internal/tui"
	"github.com/ohjann/ralphplusplus/internal/viewer"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

var Version = "dev"

func main() {
	// install-skill runs without a config (no prd.json needed); handle it
	// before Parse so it works in any cwd.
	if len(os.Args) > 1 && os.Args[1] == "install-skill" {
		if err := runInstallSkill(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// viewer is the local web UI; like install-skill it runs without a
	// config so it works in any cwd and is dispatched before Parse.
	if len(os.Args) > 1 && os.Args[1] == "viewer" {
		if err := runViewer(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Handle history subcommand before validation (no prd.json needed).
	if cfg.HistoryCommand {
		fp, err := history.Fingerprint(cfg.ProjectDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving repo fingerprint: %v\n", err)
			os.Exit(1)
		}
		// Opportunistic migration for the current repo: first invocation of a
		// post-IH-005 binary copies <projectDir>/.ralph/run-history.json to the
		// user-level location. Failures here are non-fatal — the CLI should
		// still be able to render whatever user-level history exists.
		if mErr := costs.MigrateLegacyHistory(cfg.ProjectDir, fp); mErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: migrate run-history: %v\n", mErr)
		}
		switch {
		case cfg.HistoryStats:
			err = printStats(fp, cfg.HistoryAll, cfg.HistoryAllKinds)
		case cfg.HistoryCompare:
			err = printCompare(fp, cfg.HistoryBy, cfg.HistoryAll, cfg.HistoryAllKinds)
		default:
			err = printHistory(fp, cfg.HistoryAll, cfg.HistoryAllKinds)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle memory subcommand before validation (no prd.json needed).
	if cfg.MemoryCommand != "" {
		var err error
		switch cfg.MemoryCommand {
		case "stats":
			err = printMemoryStats(cfg.RalphHome, cfg.ProjectDir)
		case "consolidate":
			err = runMemoryConsolidate(cfg)
		case "reset":
			err = runMemoryReset(cfg.ProjectDir, cfg.RalphHome)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle --kill early (no prd.json or validation needed).
	if cfg.KillDaemon {
		os.Exit(killDaemon(cfg.ProjectDir))
	}

	// Handle retro subcommand (local analysis, no daemon needed).
	if cfg.SubCommand == "retro" {
		os.Exit(runRetro(cfg))
	}

	// Handle client subcommands (connect to running daemon).
	if cfg.SubCommand != "" {
		os.Exit(runSubCommand(cfg))
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Flip any stale "running" manifests from previous crashed runs whose PIDs
	// are no longer alive on this host. Best-effort: do not block startup.
	if sweepErr := history.SweepInterrupted(); sweepErr != nil {
		debuglog.Log("history: sweep failed: %v", sweepErr)
	}

	// Open a history run for the workhorse processes (daemon child, retro,
	// memory-consolidate). The parent TUI process does not run Claude directly
	// once a daemon is attached, so it skips OpenRun here; runRetro and
	// runMemoryConsolidate each open their own run before reaching this line.
	if cfg.DaemonMode {
		hr, runErr := history.OpenRun(cfg.ProjectDir, cfg.PRDFile, Version, history.RunOpts{Kind: history.KindDaemon})
		if runErr != nil {
			debuglog.Log("history: open run failed: %v", runErr)
		} else {
			cfg.HistoryRun = hr
			defer func() {
				_ = hr.Finalize(history.StatusComplete, hr.ComputeTotals(), nil)
				if err := history.UpdateLastRunID(hr.RepoFP(), hr.ID()); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: history.UpdateLastRunID: %v\n", err)
				}
			}()
		}
	}

	// Record this repo in the user-level metadata dir. Failures are
	// non-fatal — history tracking must not block a run.
	repoFP, _, err := history.TouchRepo(cfg.ProjectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: history.TouchRepo: %v\n", err)
	}
	// Copy any pre-IH-005 <projectDir>/.ralph/run-history.json into the
	// user-level location on first run of the new binary per repo. The legacy
	// file is left in place and a .migrated sibling marker is written.
	if repoFP != "" {
		if mErr := costs.MigrateLegacyHistory(cfg.ProjectDir, repoFP); mErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: migrate run-history: %v\n", mErr)
		}
	}

	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directories: %v\n", err)
		os.Exit(1)
	}

	// When no prd.json exists, create an empty one for interactive mode.
	if cfg.NoPRD {
		projectName := filepath.Base(cfg.ProjectDir)
		branchName := fmt.Sprintf("ralph/interactive-%s", randomWords())
		emptyPRD := &prd.PRD{
			Project:     projectName,
			BranchName:  branchName,
			UserStories: []prd.UserStory{},
		}
		if err := prd.Save(cfg.PRDFile, emptyPRD); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating empty prd.json: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize debug log
	if err := debuglog.Init(cfg.LogDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init debug log: %v\n", err)
	}
	defer debuglog.Close()
	debuglog.Log("ralph starting, version=%s, workers=%d", Version, cfg.Workers)

	// --web: ensure the singleton viewer is running and print its URL.
	if cfg.WebEnabled {
		if err := launchViewer(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not launch web viewer: %v\n", err)
		}
	}

	// Handle --daemon: run as background daemon (no TUI).
	if cfg.DaemonMode {
		runDaemonMode(cfg)
		return
	}

	// Default mode: auto-fork logic.
	// Check if a daemon is already running, start one if needed, then attach TUI.
	sockPath := filepath.Join(cfg.ProjectDir, ".ralph", "daemon.sock")
	pidPath := filepath.Join(cfg.ProjectDir, ".ralph", "daemon.pid")

	switch checkDaemon(sockPath, pidPath) {
	case daemonAlive:
		fmt.Fprintln(os.Stderr, "Reconnecting to running session...")
	case daemonStale:
		// Clean up stale files and start fresh
		if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale socket: %v\n", err)
		}
		_ = os.Remove(pidPath)
		if err := forkDaemon(cfg, sockPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
			os.Exit(1)
		}
	case daemonAbsent:
		if err := forkDaemon(cfg, sockPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
			os.Exit(1)
		}
	}

	client, err := daemon.Connect(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not connect to daemon at %s: %v\n", sockPath, err)
		client = nil
	}

	os.Exit(runTUI(cfg, client))
}

// runTUI owns the TUI parent process lifecycle: it opens an ad-hoc history run
// (so clarify, utility, and user-initiated architect/implementer Claude calls
// are captured alongside daemon runs), drives the Bubble Tea program, and
// finalizes the run on exit. Returning an exit code (rather than calling
// os.Exit inline) lets the deferred Finalize fire on any return path.
func runTUI(cfg *config.Config, client *daemon.DaemonClient) int {
	var teaErr error
	if hr := openAdHocRun(cfg, Version); hr != nil {
		defer func() {
			status := history.StatusComplete
			if teaErr != nil {
				status = history.StatusFailed
			}
			_ = hr.Finalize(status, hr.ComputeTotals(), nil)
		}()
	}

	model := tui.NewModel(cfg, Version, client)
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	teaErr = err
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		return 1
	}
	if m, ok := finalModel.(*tui.Model); ok {
		return m.ExitCode()
	}
	return 0
}

// openAdHocRun opens a KindAdHoc history run for the TUI parent process and
// assigns it to cfg.HistoryRun. OpenRun failure is non-fatal: a warning is
// logged and nil is returned so RunClaudeForIteration transparently no-ops.
// Unlike the daemon/retro/memory-consolidate paths, ad-hoc runs deliberately
// skip history.UpdateLastRunID — RepoMeta.LastRunID tracks substantive runs.
func openAdHocRun(cfg *config.Config, version string) *history.Run {
	hr, err := history.OpenRun(cfg.ProjectDir, cfg.PRDFile, version, history.RunOpts{Kind: history.KindAdHoc})
	if err != nil {
		debuglog.Log("history: open ad-hoc run failed: %v", err)
		return nil
	}
	cfg.HistoryRun = hr
	return hr
}

// daemonStatus represents the state of the daemon.
type daemonStatus int

const (
	daemonAbsent daemonStatus = iota
	daemonAlive
	daemonStale
)

// checkDaemon determines if a daemon is running, stale, or absent.
func checkDaemon(sockPath, pidPath string) daemonStatus {
	// Check if socket exists
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		return daemonAbsent
	}

	// Check if PID file exists and process is alive
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		// Socket exists but no PID file — stale
		return daemonStale
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return daemonStale
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return daemonStale
	}

	// Signal 0 tests if process exists without actually sending a signal
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return daemonStale
	}

	// Process is alive — verify the socket actually responds
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	resp, err := client.Get("http://daemon/api/state")
	if err != nil {
		return daemonStale
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return daemonStale
	}

	return daemonAlive
}

// forkDaemon starts a new daemon process in the background and waits for its
// socket to become available.
func forkDaemon(cfg *config.Config, sockPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Build daemon args: forward all CLI flags, add --daemon
	daemonArgs := buildDaemonArgs(os.Args[1:])

	cmd := exec.Command(exePath, daemonArgs...)
	cmd.Dir = cfg.ProjectDir

	// Redirect stdout/stderr to daemon.log
	logPath := filepath.Join(cfg.ProjectDir, ".ralph", "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Detach from parent process group so daemon survives TUI exit
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}

	// Don't wait for the daemon — it runs independently
	go func() {
		_ = cmd.Wait()
		logFile.Close()
	}()

	// Wait up to 5 seconds for the socket to appear and respond
	pollClient := &http.Client{
		Timeout: 1 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sockPath); err == nil {
			resp, err := pollClient.Get("http://daemon/api/state")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}

	return fmt.Errorf("daemon did not start within 5 seconds (check %s)", logPath)
}

func launchViewer() error {
	lockPath, err := viewer.LockPath()
	if err != nil {
		return err
	}
	token, err := viewer.LoadOrCreateToken()
	if err != nil {
		return fmt.Errorf("viewer token: %w", err)
	}

	printURL := func(info *viewer.LockInfo) {
		fmt.Fprintf(os.Stderr, "✦ Ralph web viewer: http://127.0.0.1:%d/?token=%s\n", info.Port, token)
	}

	if info, ok := readLiveViewerLock(lockPath); ok {
		printURL(info)
		return nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(exePath, "viewer")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn viewer: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if info, ok := readLiveViewerLock(lockPath); ok {
			printURL(info)
			return nil
		}
	}
	return fmt.Errorf("viewer did not become ready within 5 seconds")
}

func readLiveViewerLock(path string) (*viewer.LockInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var info viewer.LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, false
	}
	if info.PID <= 0 || info.Port <= 0 {
		return nil, false
	}
	if err := syscall.Kill(info.PID, 0); err != nil {
		return nil, false
	}
	return &info, true
}

// buildDaemonArgs takes the original CLI args, filters out TUI-only flags,
// and appends --daemon.
func buildDaemonArgs(args []string) []string {
	var result []string
	// TUI-only flags to filter out (no value)
	tuiOnlyFlags := map[string]bool{
		"--no-guy": true,
		"--kill":   true,
		"--web":    true,
	}

	i := 0
	for i < len(args) {
		if tuiOnlyFlags[args[i]] {
			i++
			continue
		}
		result = append(result, args[i])
		i++
	}

	result = append(result, "--daemon")
	return result
}

// runDaemonMode runs ralph as a background daemon (no TUI).
// This is the entry point when --daemon is passed.
func runDaemonMode(cfg *config.Config) {
	debuglog.Log("ralph daemon mode starting, pid=%d", os.Getpid())

	// Load PRD. When --plan is set, prd.json may not exist yet (the TUI
	// generates it from the plan file) — tolerate that and start idle.
	p, err := prd.Load(cfg.PRDFile)
	if err != nil {
		if cfg.PlanFile != "" && errors.Is(err, fs.ErrNotExist) {
			p = &prd.PRD{}
		} else {
			fmt.Fprintf(os.Stderr, "daemon: failed to load prd.json: %v\n", err)
			os.Exit(1)
		}
	}

	// Filter to incomplete stories
	var incomplete []prd.UserStory
	for _, s := range p.UserStories {
		if !s.Passes {
			incomplete = append(incomplete, s)
		}
	}

	if len(incomplete) == 0 && !cfg.IdleMode && cfg.PlanFile == "" {
		fmt.Fprintln(os.Stderr, "daemon: no incomplete stories")
		os.Exit(0)
	}

	cfg.ResolveAutoWorkers(len(incomplete))

	// Create coordinator without a DAG — the DAG is built in the daemon's
	// Prepare phase (after the API socket is serving) so the parent process
	// doesn't time out waiting for dependency analysis to finish.
	coord := coordinator.New(cfg, nil, cfg.Workers, incomplete)
	rc := costs.NewRunCosting()
	coord.SetRunCosting(rc)

	n := notify.NewNotifier(cfg.NotifyTopic, cfg.NtfyServer)
	n.SetDisabled(!cfg.NotifyEnabled)
	coord.SetNotifier(n)

	// Create and run daemon
	d := daemon.New(cfg, coord, daemon.DaemonOpts{
		Notifier:     n,
		RunCosting:   rc,
		Version:      Version,
		TotalStories: len(incomplete),
		Prepare: func(ctx context.Context) error {
			if len(incomplete) == 0 {
				return nil
			}
			coord.SetDAG(dag.BuildDAG(ctx, cfg.ProjectDir, p, incomplete, cfg.UtilityModel))
			return nil
		},
	})

	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		os.Exit(1)
	}
}

// runRetro runs a design retrospective on the completed work.
func runRetro(cfg *config.Config) int {
	prdFile := filepath.Join(cfg.ProjectDir, "prd.json")
	if _, err := os.Stat(prdFile); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "No prd.json found. Run a Ralph loop first.")
		return 1
	}

	logDir := filepath.Join(cfg.ProjectDir, ".ralph", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating log dir: %v\n", err)
		return 1
	}

	if sweepErr := history.SweepInterrupted(); sweepErr != nil {
		debuglog.Log("history: sweep failed: %v", sweepErr)
	}
	if hr, runErr := history.OpenRun(cfg.ProjectDir, prdFile, Version, history.RunOpts{Kind: history.KindRetro}); runErr != nil {
		debuglog.Log("history: open retro run failed: %v", runErr)
	} else {
		cfg.HistoryRun = hr
		defer func() {
			_ = hr.Finalize(history.StatusComplete, hr.ComputeTotals(), nil)
			if err := history.UpdateLastRunID(hr.RepoFP(), hr.ID()); err != nil {
				debuglog.Log("history: UpdateLastRunID: %v", err)
			}
		}()
	}

	fmt.Fprintln(os.Stderr, "Running design retrospective...")
	result, err := retro.RunRetrospective(context.Background(), cfg, cfg.ProjectDir, logDir, prdFile, cfg.UtilityModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Print(retro.FormatSummary(result))

	// Print path to saved result
	retroDir := filepath.Join(cfg.ProjectDir, ".ralph", "retro")
	fmt.Fprintf(os.Stderr, "\nSaved to %s/\n", retroDir)
	return 0
}

// runSubCommand handles CLI client subcommands that connect to a running daemon.
func runSubCommand(cfg *config.Config) int {
	sockPath := filepath.Join(cfg.ProjectDir, ".ralph", "daemon.sock")

	client, err := daemon.Connect(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to daemon: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is a daemon running? Start one with: ralph --daemon")
		return 1
	}
	defer client.Close()

	switch cfg.SubCommand {
	case "status":
		return cmdStatus(client)
	case "logs":
		return cmdLogs(client)
	case "hint":
		return cmdHint(client, cfg)
	case "pause":
		return cmdPauseResume(client, true)
	case "resume":
		return cmdPauseResume(client, false)
	}
	return 1
}

func cmdStatus(client *daemon.DaemonClient) int {
	state, err := client.GetState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Summary line
	activeWorkers := 0
	for _, w := range state.Workers {
		if w.State == "running" {
			activeWorkers++
		}
	}
	pausedStr := ""
	if state.Paused {
		pausedStr = " (PAUSED)"
	}
	fmt.Printf("Phase: %s%s | Workers: %d active | Stories: %d/%d complete",
		state.Phase, pausedStr, activeWorkers, state.CompletedCount, state.TotalStories)
	if state.FailedCount > 0 {
		fmt.Printf(" | %d failed", state.FailedCount)
	}
	if state.CostTotals.TotalCost > 0 {
		fmt.Printf(" | Cost: $%.2f", state.CostTotals.TotalCost)
	}
	fmt.Println()

	if state.AllDone {
		fmt.Println("\nAll stories complete.")
		return 0
	}

	// Worker table
	if len(state.Workers) > 0 {
		fmt.Printf("\n%-8s  %-10s  %-30s  %-10s  %s\n",
			"WORKER", "STORY", "TITLE", "STATE", "ITER")
		fmt.Printf("%-8s  %-10s  %-30s  %-10s  %s\n",
			"────────", "──────────", "──────────────────────────────", "──────────", "────")
		for _, w := range state.Workers {
			title := w.StoryTitle
			if len(title) > 30 {
				title = title[:27] + "..."
			}
			fmt.Printf("%-8d  %-10s  %-30s  %-10s  %d\n",
				w.ID, w.StoryID, title, w.State, w.Iteration)
		}
	}

	return 0
}

func cmdLogs(client *daemon.DaemonClient) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	evtCh := client.StreamEvents(ctx)
	for evt := range evtCh {
		switch evt.Type {
		case daemon.EventLogLine:
			var log daemon.LogLineEvent
			if err := json.Unmarshal(evt.Data, &log); err == nil {
				fmt.Printf("[%s] %s\n", log.Timestamp.Format("15:04:05"), log.Line)
			}
		case daemon.EventMergeResult:
			var mr daemon.MergeResultEvent
			if err := json.Unmarshal(evt.Data, &mr); err == nil {
				if mr.Success {
					fmt.Printf("[merge] %s: merged successfully\n", mr.StoryID)
				} else {
					fmt.Printf("[merge] %s: FAILED: %s\n", mr.StoryID, mr.Error)
				}
			}
		case daemon.EventStuckAlert:
			var sa daemon.StuckAlertEvent
			if err := json.Unmarshal(evt.Data, &sa); err == nil {
				fmt.Printf("[stuck] worker %d (%s): %s\n", sa.WorkerID, sa.StoryID, sa.StuckReason)
			}
		case daemon.EventDaemonState:
			var s daemon.DaemonStateEvent
			if err := json.Unmarshal(evt.Data, &s); err == nil {
				active := 0
				for _, w := range s.Workers {
					if w.State == "running" {
						active++
					}
				}
				fmt.Printf("[state] %d/%d complete | %d active workers\n",
					s.CompletedCount, s.TotalStories, active)
			}
		case "error":
			fmt.Fprintf(os.Stderr, "Connection error: %s\n", string(evt.Data))
			return 1
		}
	}
	return 0
}

func cmdHint(client *daemon.DaemonClient, cfg *config.Config) int {
	wID := worker.WorkerID(cfg.HintWorkerID)
	if err := client.SendHint(wID, cfg.HintText); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Printf("Hint sent to worker %d\n", cfg.HintWorkerID)
	return 0
}

func cmdPauseResume(client *daemon.DaemonClient, pause bool) int {
	var err error
	if pause {
		err = client.Pause()
	} else {
		err = client.Resume()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if pause {
		fmt.Println("Workers paused")
	} else {
		fmt.Println("Workers resumed")
	}
	return 0
}

// killDaemon sends SIGTERM to a running daemon and waits for it to exit.
// Returns the exit code.
func killDaemon(projectDir string) int {
	pidPath := filepath.Join(projectDir, ".ralph", "daemon.pid")
	sockPath := filepath.Join(projectDir, ".ralph", "daemon.sock")

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No daemon running")
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "No daemon running")
		_ = os.Remove(pidPath)
		return 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No daemon running")
		_ = os.Remove(pidPath)
		return 0
	}

	// Check if process is alive
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Fprintln(os.Stderr, "No daemon running")
		_ = os.Remove(pidPath)
		_ = os.Remove(sockPath)
		return 0
	}

	// Send SIGTERM
	fmt.Fprintf(os.Stderr, "Sending SIGTERM to daemon (PID %d)...\n", pid)
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending signal: %v\n", err)
		return 1
	}

	// Wait for the socket to be removed (indicates clean shutdown)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "Daemon stopped")
			return 0
		}
		// Also check if process is still alive
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process died
			_ = os.Remove(sockPath)
			_ = os.Remove(pidPath)
			fmt.Fprintln(os.Stderr, "Daemon stopped")
			return 0
		}
	}

	fmt.Fprintln(os.Stderr, "Daemon did not stop within 10 seconds")
	return 1
}

// loadHistoryForCLI loads history for this repo's fp, or aggregates across
// every fingerprint dir under <userdata>/repos when aggregate is true.
func loadHistoryForCLI(fp string, aggregate bool) (costs.RunHistory, error) {
	if aggregate {
		return costs.LoadAllHistory()
	}
	return costs.LoadHistory(fp)
}

// filterDaemonRuns keeps only Kind=="daemon" or Kind=="" entries (legacy
// pre-IH-005 entries are grandfathered as daemon).
func filterDaemonRuns(runs []costs.RunSummary) []costs.RunSummary {
	out := make([]costs.RunSummary, 0, len(runs))
	for _, r := range runs {
		if r.IsDaemon() {
			out = append(out, r)
		}
	}
	return out
}

func printHistory(fp string, aggregate, allKinds bool) error {
	h, err := loadHistoryForCLI(fp, aggregate)
	if err != nil {
		return err
	}
	if !allKinds {
		h.Runs = filterDaemonRuns(h.Runs)
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	runs := h.Runs
	if !aggregate && len(runs) > 10 {
		runs = runs[len(runs)-10:]
	}

	// Print header
	fmt.Printf("%-19s  %-20s  %9s  %7s  %8s  %8s  %9s  %8s  %-10s\n",
		"DATE", "PRD", "STORIES", "WORKERS", "COST", "DURATION", "AVG ITER", "1ST PASS", "MODEL")
	fmt.Printf("%-19s  %-20s  %9s  %7s  %8s  %8s  %9s  %8s  %-10s\n",
		"───────────────────", "────────────────────", "─────────", "───────", "────────", "────────", "─────────", "────────", "──────────")

	for _, r := range runs {
		// Truncate date to first 19 chars (YYYY-MM-DDTHH:MM:SS)
		date := r.Date
		if len(date) > 19 {
			date = date[:19]
		}
		// Truncate PRD name to 20 chars
		prdName := r.PRD
		if len(prdName) > 20 {
			prdName = prdName[:17] + "..."
		}

		stories := fmt.Sprintf("%d/%d", r.StoriesCompleted, r.StoriesTotal)
		cost := fmt.Sprintf("$%.2f", r.TotalCost)
		duration := fmt.Sprintf("%.0f min", r.DurationMinutes)
		avgIter := fmt.Sprintf("%.1f", r.AvgIterationsPerStory)

		firstPass := "-"
		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			firstPass = fmt.Sprintf("%.0f%%", r.FirstPassRate*100)
		}

		workers := "-"
		if r.Workers > 0 {
			workers = fmt.Sprintf("%d", r.Workers)
		}

		model := "-"
		if len(r.ModelsUsed) == 1 {
			model = shortModelName(r.ModelsUsed[0])
		} else if len(r.ModelsUsed) > 1 {
			model = "mixed"
		}

		fmt.Printf("%-19s  %-20s  %9s  %7s  %8s  %8s  %9s  %8s  %-10s\n",
			date, prdName, stories, workers, cost, duration, avgIter, firstPass, model)
	}

	if !aggregate && len(h.Runs) > 10 {
		fmt.Printf("\nShowing last 10 of %d runs. Use --all to see everything.\n", len(h.Runs))
	}
	return nil
}

// shortModelName extracts a readable name from a full model ID.
// e.g. "claude-opus-4-6" → "opus", "claude-sonnet-4-20250514" → "sonnet"
func shortModelName(model string) string {
	for _, name := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(model, name) {
			return name
		}
	}
	if strings.Contains(model, "gemini") {
		return "gemini"
	}
	if len(model) > 10 {
		return model[:10]
	}
	return model
}

func printStats(fp string, aggregate, allKinds bool) error {
	h, err := loadHistoryForCLI(fp, aggregate)
	if err != nil {
		return err
	}
	if !allKinds {
		h.Runs = filterDaemonRuns(h.Runs)
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	var (
		totalRuns         = len(h.Runs)
		totalStories      int
		totalCompleted    int
		totalFailed       int
		totalIterations   int
		totalInputTokens  int
		totalOutputTokens int
		totalDuration     float64
		firstPassSum      float64
		firstPassCount    int
		cacheHitSum       float64
		cacheHitCount     int
	)

	// Per-story aggregation for most-retried
	type storyAgg struct {
		id       string
		title    string
		rejects  int
		appears  int
	}
	storyMap := make(map[string]*storyAgg)

	for _, r := range h.Runs {
		totalStories += r.StoriesTotal
		totalCompleted += r.StoriesCompleted
		totalFailed += r.StoriesFailed
		totalIterations += r.TotalIterations
		totalInputTokens += r.TotalInputTokens
		totalOutputTokens += r.TotalOutputTokens
		totalDuration += r.DurationMinutes

		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			firstPassSum += r.FirstPassRate
			firstPassCount++
		}
		if r.CacheHitRate > 0 {
			cacheHitSum += r.CacheHitRate
			cacheHitCount++
		}

		for _, sd := range r.StoryDetails {
			agg, ok := storyMap[sd.StoryID]
			if !ok {
				agg = &storyAgg{id: sd.StoryID, title: sd.Title}
				storyMap[sd.StoryID] = agg
			}
			agg.rejects += sd.JudgeRejects
			agg.appears++
		}
	}

	// Print summary
	fmt.Println("── Run Statistics ──")
	fmt.Printf("  Total runs:          %d\n", totalRuns)
	fmt.Printf("  Total stories:       %d completed, %d failed (of %d)\n", totalCompleted, totalFailed, totalStories)
	fmt.Printf("  Total iterations:    %d\n", totalIterations)
	fmt.Printf("  Total duration:      %.0f min\n", totalDuration)
	fmt.Printf("  Total tokens:        %dk in / %dk out\n", totalInputTokens/1000, totalOutputTokens/1000)

	if firstPassCount > 0 {
		fmt.Printf("  Avg first-pass rate: %.0f%%\n", (firstPassSum/float64(firstPassCount))*100)
	}
	if totalCompleted > 0 {
		fmt.Printf("  Avg iterations/story: %.1f\n", float64(totalIterations)/float64(totalCompleted))
	}
	if cacheHitCount > 0 {
		fmt.Printf("  Avg cache hit rate:  %.0f%%\n", (cacheHitSum/float64(cacheHitCount))*100)
	}

	// Last 5 runs trend
	fmt.Println("\n── Recent Trend (last 5 runs) ──")
	start := 0
	if len(h.Runs) > 5 {
		start = len(h.Runs) - 5
	}
	for _, r := range h.Runs[start:] {
		date := r.Date
		if len(date) > 10 {
			date = date[:10]
		}
		fp := "-"
		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			fp = fmt.Sprintf("%.0f%%", r.FirstPassRate*100)
		}
		fmt.Printf("  %s  %d/%d stories  avg %.1f iter  %s 1st-pass\n",
			date, r.StoriesCompleted, r.StoriesTotal, r.AvgIterationsPerStory, fp)
	}

	// Most-retried stories
	type ranked struct {
		id      string
		title   string
		rejects int
	}
	var retried []ranked
	for _, agg := range storyMap {
		if agg.rejects > 0 {
			retried = append(retried, ranked{id: agg.id, title: agg.title, rejects: agg.rejects})
		}
	}
	if len(retried) > 0 {
		// Sort by rejects descending
		sort.Slice(retried, func(i, j int) bool {
			return retried[i].rejects > retried[j].rejects
		})
		fmt.Println("\n── Most Judge-Rejected Stories ──")
		limit := 5
		if len(retried) < limit {
			limit = len(retried)
		}
		for _, r := range retried[:limit] {
			title := r.title
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			fmt.Printf("  %s  %-40s  %d rejects\n", r.id, title, r.rejects)
		}
	}

	return nil
}

type modelGroup struct {
	model         string
	runs          int
	firstPassSum  float64
	firstPassN    int
	avgIterSum    float64
	avgIterN      int
	inputTokens   int
	outputTokens  int
	durationMin   float64
	storiesTotal  int
	storiesDone   int
}

func printCompare(fp, by string, aggregate, allKinds bool) error {
	h, err := loadHistoryForCLI(fp, aggregate)
	if err != nil {
		return err
	}
	if !allKinds {
		h.Runs = filterDaemonRuns(h.Runs)
	}
	if len(h.Runs) == 0 {
		fmt.Println("No run history yet.")
		return nil
	}

	switch by {
	case "flags":
		return printCompareByFlags(h)
	case "", "model":
		// fall through to model grouping below
	default:
		return fmt.Errorf("unknown --by value %q (want \"model\" or \"flags\")", by)
	}

	// Group runs by primary model
	groups := make(map[string]*modelGroup)

	for _, r := range h.Runs {
		model := "unknown"
		if len(r.ModelsUsed) == 1 {
			model = shortModelName(r.ModelsUsed[0])
		} else if len(r.ModelsUsed) > 1 {
			model = "mixed"
		}

		g, ok := groups[model]
		if !ok {
			g = &modelGroup{model: model}
			groups[model] = g
		}
		g.runs++
		g.inputTokens += r.TotalInputTokens
		g.outputTokens += r.TotalOutputTokens
		g.durationMin += r.DurationMinutes
		g.storiesTotal += r.StoriesTotal
		g.storiesDone += r.StoriesCompleted

		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			g.firstPassSum += r.FirstPassRate
			g.firstPassN++
		}
		if r.StoriesCompleted > 0 {
			g.avgIterSum += r.AvgIterationsPerStory
			g.avgIterN++
		}
	}

	if len(groups) < 2 {
		fmt.Println("── Model Comparison ──")
		fmt.Println("  Only one model group found. Run PRDs with different models to compare.")
		fmt.Println()
		// Still show the single group
		for _, g := range groups {
			printModelGroup(g)
		}
		return nil
	}

	fmt.Println("── Model Comparison ──")
	fmt.Println()

	// Print header
	fmt.Printf("  %-12s  %5s  %9s  %8s  %10s  %10s\n",
		"MODEL", "RUNS", "1ST PASS", "AVG ITER", "IN TOKENS", "OUT TOKENS")
	fmt.Printf("  %-12s  %5s  %9s  %8s  %10s  %10s\n",
		"────────────", "─────", "─────────", "────────", "──────────", "──────────")

	for _, g := range groups {
		fp := "-"
		if g.firstPassN > 0 {
			fp = fmt.Sprintf("%.0f%%", (g.firstPassSum/float64(g.firstPassN))*100)
		}
		ai := "-"
		if g.avgIterN > 0 {
			ai = fmt.Sprintf("%.1f", g.avgIterSum/float64(g.avgIterN))
		}
		fmt.Printf("  %-12s  %5d  %9s  %8s  %9dk  %9dk\n",
			g.model, g.runs, fp, ai, g.inputTokens/1000, g.outputTokens/1000)
	}

	return nil
}

type flagsGroup struct {
	config       string
	runs         int
	firstPassSum float64
	firstPassN   int
	avgIterSum   float64
	avgIterN     int
	durationMin  float64
}

// flagsConfigKey returns a stable display string for a run's flag configuration.
// Uses CLI-style flag names so users can recognise them.
func flagsConfigKey(r costs.RunSummary) string {
	var parts []string
	if r.NoArchitect {
		parts = append(parts, "--no-architect")
	}
	if r.NoFusion {
		parts = append(parts, "--no-fusion")
	}
	if r.NoSimplify {
		parts = append(parts, "--no-simplify")
	}
	if !r.QualityReview {
		parts = append(parts, "--no-quality-review")
	}
	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, " ")
}

func printCompareByFlags(h costs.RunHistory) error {
	groups := make(map[string]*flagsGroup)
	var order []string

	for _, r := range h.Runs {
		key := flagsConfigKey(r)
		g, ok := groups[key]
		if !ok {
			g = &flagsGroup{config: key}
			groups[key] = g
			order = append(order, key)
		}
		g.runs++
		g.durationMin += r.DurationMinutes
		if r.FirstPassRate > 0 || r.StoriesCompleted > 0 {
			g.firstPassSum += r.FirstPassRate
			g.firstPassN++
		}
		if r.StoriesCompleted > 0 {
			g.avgIterSum += r.AvgIterationsPerStory
			g.avgIterN++
		}
	}

	fmt.Println("── Flag Configuration Comparison ──")
	fmt.Println()

	if len(groups) < 2 {
		fmt.Println("  Only one flag configuration found. Run PRDs with different flags to compare.")
		fmt.Println()
	}

	const configCol = 28
	fmt.Printf("  %-*s  %5s  %9s  %8s  %8s\n",
		configCol, "CONFIG", "RUNS", "1ST PASS", "AVG ITER", "DURATION")
	fmt.Printf("  %-*s  %5s  %9s  %8s  %8s\n",
		configCol, strings.Repeat("─", configCol), "─────", "─────────", "────────", "────────")

	for _, key := range order {
		g := groups[key]
		fp := "-"
		if g.firstPassN > 0 {
			fp = fmt.Sprintf("%.0f%%", (g.firstPassSum/float64(g.firstPassN))*100)
		}
		ai := "-"
		if g.avgIterN > 0 {
			ai = fmt.Sprintf("%.1f", g.avgIterSum/float64(g.avgIterN))
		}
		cfg := g.config
		if len(cfg) > configCol {
			cfg = cfg[:configCol-1] + "…"
		}
		fmt.Printf("  %-*s  %5d  %9s  %8s  %6.0fm\n",
			configCol, cfg, g.runs, fp, ai, g.durationMin)
	}

	return nil
}

func printModelGroup(g *modelGroup) {
	fp := "-"
	if g.firstPassN > 0 {
		fp = fmt.Sprintf("%.0f%%", (g.firstPassSum/float64(g.firstPassN))*100)
	}
	ai := "-"
	if g.avgIterN > 0 {
		ai = fmt.Sprintf("%.1f", g.avgIterSum/float64(g.avgIterN))
	}
	fmt.Printf("  Model: %s\n", g.model)
	fmt.Printf("    Runs:           %d\n", g.runs)
	fmt.Printf("    Stories:        %d/%d completed\n", g.storiesDone, g.storiesTotal)
	fmt.Printf("    Avg 1st-pass:   %s\n", fp)
	fmt.Printf("    Avg iterations: %s\n", ai)
	fmt.Printf("    Total tokens:   %dk in / %dk out\n", g.inputTokens/1000, g.outputTokens/1000)
	fmt.Printf("    Total duration: %.0f min\n", g.durationMin)
}

// printMemoryStats shows file sizes, entry counts, last consolidation date, and runs since.
func printMemoryStats(ralphHome, projectDir string) error {
	fmt.Println("── Memory Stats ──")

	stats := memory.MemoryStats(projectDir, ralphHome)
	for _, s := range stats {
		if !s.Exists {
			fmt.Printf("  %s: (not created yet)\n", s.Name)
			continue
		}
		estTokens := s.SizeBytes * 4 / 3 // rough estimate: ~0.75 bytes per token
		fmt.Printf("  %s: %d bytes (~%d tokens), %d entries\n", s.Name, s.SizeBytes, estTokens, s.EntryCount)
		fmt.Printf("    → %s\n", s.Path)
	}

	meta, err := memory.LoadRunMeta(projectDir)
	if err != nil {
		fmt.Printf("\n  Last consolidation: unknown (error reading run-meta.json: %v)\n", err)
	} else if meta.LastDream == "" {
		fmt.Printf("\n  Last consolidation: never\n")
		fmt.Printf("  Runs since last consolidation: %d\n", meta.RunCount)
	} else {
		fmt.Printf("\n  Last consolidation: %s\n", meta.LastDream)
		fmt.Printf("  Runs since last consolidation: %d\n", meta.RunCount)
	}

	return nil
}

// runMemoryConsolidate manually triggers the dream consolidation cycle.
func runMemoryConsolidate(cfg *config.Config) error {
	fmt.Println("Running dream consolidation...")

	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	if sweepErr := history.SweepInterrupted(); sweepErr != nil {
		debuglog.Log("history: sweep failed: %v", sweepErr)
	}
	if hr, runErr := history.OpenRun(cfg.ProjectDir, cfg.PRDFile, Version, history.RunOpts{Kind: history.KindMemoryConsolidate}); runErr != nil {
		debuglog.Log("history: open memory-consolidate run failed: %v", runErr)
	} else {
		cfg.HistoryRun = hr
		defer func() {
			_ = hr.Finalize(history.StatusComplete, hr.ComputeTotals(), nil)
			if err := history.UpdateLastRunID(hr.RepoFP(), hr.ID()); err != nil {
				debuglog.Log("history: UpdateLastRunID: %v", err)
			}
		}()
	}

	runClaude := func(ctx context.Context, projectDir, prompt, logFilePath string) error {
		iter := int(cfg.UtilityIter.Add(1))
		_, err := runner.RunClaudeForIteration(ctx, cfg, projectDir, prompt, logFilePath, runner.IterationOpts{
			StoryID: "_memory-consolidate",
			Role:    "memory-consolidate",
			Iter:    iter,
			Model:   cfg.UtilityModel,
		})
		return err
	}

	ctx := context.Background()
	if err := memory.RunDream(ctx, cfg.ProjectDir, cfg.RalphHome, cfg.LogDir, cfg.Memory.MaxEntries, cfg.Memory.DreamEveryNRuns, runClaude); err != nil {
		return fmt.Errorf("dream consolidation failed: %w", err)
	}

	fmt.Println("Dream consolidation complete.")

	// Print summary of what was consolidated
	stats := memory.MemoryStats(cfg.ProjectDir, cfg.RalphHome)
	for _, s := range stats {
		if s.Exists {
			fmt.Printf("  %s: %d entries (%d bytes)\n", s.Name, s.EntryCount, s.SizeBytes)
		}
	}
	return nil
}

// runMemoryReset clears all memory files after confirmation.
func runMemoryReset(projectDir, ralphHome string) error {
	// Show what will be deleted
	stats := memory.MemoryStats(projectDir, ralphHome)
	hasFiles := false
	for _, s := range stats {
		if s.Exists {
			fmt.Printf("  Will delete: %s\n    → %s\n", s.Name, s.Path)
			hasFiles = true
		}
	}
	if !hasFiles {
		fmt.Println("No memory files to reset.")
		return nil
	}

	// Confirmation prompt
	fmt.Print("\nType 'yes' to confirm reset: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "yes" {
		fmt.Println("Reset cancelled.")
		return nil
	}

	for _, s := range stats {
		if !s.Exists {
			continue
		}
		if err := os.Remove(s.Path); err != nil {
			return fmt.Errorf("removing %s: %w", s.Name, err)
		}
		fmt.Printf("  Deleted: %s\n", s.Path)
	}

	fmt.Println("Memory reset complete.")
	return nil
}

// runInstallSkill writes the embedded skills/ralph/ tree to
// ~/.claude/skills/ralph/. It is idempotent: existing files are overwritten
// with current embedded contents, additional local files are left untouched.
func runInstallSkill() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	dest := filepath.Join(home, ".claude", "skills", "ralph")

	skillFS := assets.SkillFS()
	walkErr := fs.WalkDir(skillFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := fs.ReadFile(skillFS, p)
		if readErr != nil {
			return fmt.Errorf("reading embedded %s: %w", p, readErr)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		return fmt.Errorf("installing skill to %s: %w", dest, walkErr)
	}
	fmt.Printf("Installed ralph skill to %s\n", dest)
	return nil
}

// randomWords generates a short random identifier like "swift-oak-river".
func randomWords() string {
	words := []string{
		"swift", "calm", "bold", "warm", "deep",
		"oak", "elm", "fox", "owl", "bee",
		"river", "stone", "cloud", "leaf", "dawn",
	}
	a := words[rand.Intn(5)]
	b := words[5+rand.Intn(5)]
	c := words[10+rand.Intn(5)]
	return fmt.Sprintf("%s-%s-%s", a, b, c)
}
