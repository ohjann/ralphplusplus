package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/history"
)

// MemoryConfig holds configuration for the markdown-based memory system.
type MemoryConfig struct {
	Disabled           bool // --memory-disable: skip memory injection entirely
	DreamEveryNRuns    int  // how often to run dream consolidation (default: 5)
	MaxEntries         int  // max entries per memory file (default: 50)
	WarnTokenThreshold int  // warn when memory files exceed this token count (default: 50000)
}

// DefaultMemoryConfig returns the default memory configuration values.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		DreamEveryNRuns:    5,
		MaxEntries:         50,
		WarnTokenThreshold: 50000,
	}
}

type Config struct {
	ProjectDir         string
	RalphHome          string
	JudgeEnabled       bool
	JudgeMaxRejections int
	IdleMode           bool
	Workers            int    // --workers N, default 1 (serial)
	WorkspaceBase      string // default /tmp/ralph-workspaces
	PlanFile           string // --plan <path> to generate prd.json from a plan file
	QualityReview      bool   // enabled by default; --no-quality-review disables
	QualityWorkers     int    // --quality-workers N: parallel reviewers (default: 3)
	QualityMaxIters    int    // --quality-max-iterations N: review-fix cycles (default: 2)
	Memory             MemoryConfig
	MemoryCommand      string // "stats", "consolidate", "reset" (empty = normal TUI mode)
	NotifyTopic        string // --notify-topic <topic>: ntfy.sh topic for push notifications
	NtfyServer         string // --ntfy-server <url>: self-hosted ntfy server URL
	WebEnabled         bool   // --web: spawn the web viewer and print its URL
	NotifyEnabled      bool   // --notify: enable push notifications via ntfy
	EnableMonitoring   bool   // --enable-monitoring: DEPRECATED — alias for --web --notify
	HistoryCommand     bool   // true when "history" subcommand is used
	HistoryAll         bool   // --all flag for history subcommand: aggregate across every fingerprint dir
	HistoryAllKinds    bool   // --all-kinds flag: bypass the daemon-only filter
	HistoryStats       bool   // --stats flag for history subcommand
	HistoryCompare     bool   // --compare flag for history subcommand
	HistoryBy          string // --by value for history --compare ("model" or "flags"); default "model"
	NoArchitect        bool   // --no-architect: globally skip architect phase for all stories
	SpriteEnabled      bool   // sprite mascot overlay (default true)
	NoPRD              bool   // true when no prd.json exists (interactive-only mode)
	ModelOverride      string // --model: override model for all roles
	ArchitectModel     string // --architect-model: override model for architect role only
	ImplementerModel   string // --implementer-model: override model for implementer role only
	UtilityModel       string // --utility-model: model for DAG analysis and other utility tasks (default: haiku)
	StoryTimeout       int    // --story-timeout: max minutes per story before cancellation (default: 0 = no limit)
	WorkersAuto        bool   // --workers auto: scale workers to DAG width (capped at AutoMaxWorkers)
	AutoMaxWorkers     int    // cap for auto worker scaling (default: 5)
	NoSimplify         bool   // --no-simplify: skip per-story simplify pass
	NoFusion           bool   // --no-fusion: disable automatic fusion mode for complex stories
	FusionWorkers      int    // --fusion-workers N: competing implementations per complex story (default: 2)
	DaemonMode         bool          // --daemon: run as background daemon (no TUI)
	KillDaemon         bool          // --kill: send SIGTERM to running daemon and exit
	IdleTimeout        time.Duration // --idle-timeout: auto-shutdown after this duration idle (default: 5m, 0 = disabled)
	RetroEnabled       bool          // --retro: run retrospective after summary generation
	SubCommand         string        // client subcommand: "status", "logs", "hint", "pause", "resume", "retro"
	HintWorkerID       int           // worker ID for "hint" subcommand
	HintText           string        // hint text for "hint" subcommand

	// Derived paths
	PRDFile        string
	ProgressFile   string
	ArchiveDir     string
	LastBranchFile string
	LogDir         string

	// HistoryRun, if non-nil, is the active per-process run that iteration
	// writers are attached to. Populated in cmd/ralph/main.go after cfg.Validate().
	HistoryRun *history.Run

	// UtilityIter is a monotonic counter used to synthesize iteration indices
	// for utility calls (DAG analysis, dream/synthesis, memory consolidate)
	// that have no natural per-story iteration number.
	UtilityIter atomic.Int64
}

func Parse(args []string) (*Config, error) {
	cfg := &Config{
		JudgeEnabled:       true,
		JudgeMaxRejections: 2,
		Workers:            1,
		WorkspaceBase:      "/tmp/ralph-workspaces",
		QualityReview:      true,
		QualityWorkers:     3,
		QualityMaxIters:    2,
		SpriteEnabled:      true,
		UtilityModel:       "haiku",
		AutoMaxWorkers:     5,
		FusionWorkers:      2,
		IdleTimeout:        5 * time.Minute,
		Memory: DefaultMemoryConfig(),
	}

	// Check for client subcommands (connect to running daemon).
	if len(args) > 0 {
		switch args[0] {
		case "status", "logs", "pause", "resume", "hint", "retro":
			cfg.SubCommand = args[0]
			// Parse optional --dir and subcommand-specific args
			for j := 1; j < len(args); j++ {
				switch args[j] {
				case "--dir":
					if j+1 < len(args) {
						cfg.ProjectDir = args[j+1]
						j++
					}
				default:
					if strings.HasPrefix(args[j], "--dir=") {
						cfg.ProjectDir = args[j][len("--dir="):]
					} else if cfg.SubCommand == "hint" && cfg.HintWorkerID == 0 {
						n, err := strconv.Atoi(args[j])
						if err != nil {
							return nil, fmt.Errorf("hint: invalid worker ID %q", args[j])
						}
						cfg.HintWorkerID = n
					} else if cfg.SubCommand == "hint" && cfg.HintText == "" {
						cfg.HintText = args[j]
					}
				}
			}
			if cfg.SubCommand == "hint" && (cfg.HintWorkerID == 0 || cfg.HintText == "") {
				return nil, fmt.Errorf("usage: ralph hint <worker-id> \"hint text\"")
			}
			if cfg.ProjectDir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return nil, fmt.Errorf("cannot determine working directory: %w", err)
				}
				cfg.ProjectDir = cwd
			}
			abs, err := filepath.Abs(cfg.ProjectDir)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve project dir: %w", err)
			}
			cfg.ProjectDir = abs
			return cfg, nil
		}
	}

	// Check for "history" subcommand as first argument.
	if len(args) > 0 && args[0] == "history" {
		cfg.HistoryCommand = true
		// Parse optional flags
		for j := 1; j < len(args); j++ {
			switch args[j] {
			case "--all":
				cfg.HistoryAll = true
			case "--all-kinds":
				cfg.HistoryAllKinds = true
			case "--stats":
				cfg.HistoryStats = true
			case "--compare":
				cfg.HistoryCompare = true
			case "--by":
				if j+1 < len(args) {
					cfg.HistoryBy = args[j+1]
					j++
				}
			case "--dir":
				if j+1 < len(args) {
					cfg.ProjectDir = args[j+1]
					j++
				}
			default:
				if strings.HasPrefix(args[j], "--dir=") {
					cfg.ProjectDir = args[j][len("--dir="):]
				} else if strings.HasPrefix(args[j], "--by=") {
					cfg.HistoryBy = args[j][len("--by="):]
				}
			}
		}
		cfg.RalphHome = os.Getenv("RALPH_HOME")
		if cfg.ProjectDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("cannot determine working directory: %w", err)
			}
			cfg.ProjectDir = cwd
		}
		abs, err := filepath.Abs(cfg.ProjectDir)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve project dir: %w", err)
		}
		cfg.ProjectDir = abs
		return cfg, nil
	}

	// Check for "memory" subcommand as first argument.
	if len(args) > 0 && args[0] == "memory" {
		if len(args) < 2 {
			return nil, fmt.Errorf("usage: ralph memory <stats|consolidate|reset>")
		}
		switch args[1] {
		case "stats":
			cfg.MemoryCommand = "stats"
		case "consolidate":
			cfg.MemoryCommand = "consolidate"
		case "reset":
			cfg.MemoryCommand = "reset"
		default:
			return nil, fmt.Errorf("unknown memory command %q. Use: stats, consolidate, reset", args[1])
		}
		// Resolve paths needed for memory commands.
		cfg.RalphHome = os.Getenv("RALPH_HOME")
		if cfg.ProjectDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("cannot determine working directory: %w", err)
			}
			cfg.ProjectDir = cwd
		}
		abs, err := filepath.Abs(cfg.ProjectDir)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve project dir: %w", err)
		}
		cfg.ProjectDir = abs
		cfg.LogDir = filepath.Join(cfg.ProjectDir, ".ralph", "logs")

		// Load .ralph/.env so VOYAGE_API_KEY etc. are available for memory commands
		envFile := filepath.Join(cfg.ProjectDir, ".ralph", ".env")
		dotEnv, _ := loadDotEnv(envFile)
		cfg.applyEnvDefaults(dotEnv)

		return cfg, nil
	}

	// Pre-scan for --dir so we can resolve ProjectDir early and load config.toml.
	for j := 0; j < len(args); j++ {
		if args[j] == "--dir" && j+1 < len(args) {
			cfg.ProjectDir = args[j+1]
			break
		}
		if len(args[j]) > 6 && args[j][:6] == "--dir=" {
			cfg.ProjectDir = args[j][6:]
			break
		}
	}

	// Resolve ProjectDir early for config.toml loading.
	if cfg.ProjectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.ProjectDir = cwd
	}
	earlyAbs, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve project dir: %w", err)
	}
	cfg.ProjectDir = earlyAbs

	// Load config.toml (priority: CLI flags > config.toml > defaults).
	tc, tcErr := loadTomlConfig(cfg.ProjectDir)
	if tcErr != nil {
		debuglog.Log("config.toml: error loading: %v", tcErr)
	} else if tc != nil {
		tc.applyTo(cfg)
	}

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		case "--dir":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--dir requires a path argument")
			}
			cfg.ProjectDir = args[i+1]
			i += 2
		case "--idle":
			cfg.IdleMode = true
			i++
		case "--no-judge":
			cfg.JudgeEnabled = false
			i++
		case "--no-guy":
			cfg.SpriteEnabled = false
			i++
		case "--workers":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--workers requires a number or 'auto'")
			}
			if args[i+1] == "auto" {
				cfg.WorkersAuto = true
				cfg.Workers = 1 // will be resolved after DAG analysis
			} else {
				n, err := strconv.Atoi(args[i+1])
				if err != nil {
					return nil, fmt.Errorf("--workers: invalid value %q (use a number or 'auto')", args[i+1])
				}
				if n < 1 {
					n = 1
				}
				cfg.Workers = n
			}
			i += 2
		case "--plan":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--plan requires a file path argument")
			}
			cfg.PlanFile = args[i+1]
			i += 2
		case "--workspace-base":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--workspace-base requires a path")
			}
			cfg.WorkspaceBase = args[i+1]
			i += 2
		case "--no-quality-review":
			cfg.QualityReview = false
			i++
		case "--quality-workers":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--quality-workers requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--quality-workers: invalid number %q", args[i+1])
			}
			if n < 1 {
				n = 1
			}
			cfg.QualityWorkers = n
			i += 2
		case "--quality-max-iterations":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--quality-max-iterations requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--quality-max-iterations: invalid number %q", args[i+1])
			}
			if n < 1 {
				n = 1
			}
			cfg.QualityMaxIters = n
			i += 2
		case "--judge-max-rejections":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--judge-max-rejections requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--judge-max-rejections: invalid number %q", args[i+1])
			}
			cfg.JudgeMaxRejections = n
			i += 2
		case "--memory-disable":
			cfg.Memory.Disabled = true
			i++
		case "--web":
			cfg.WebEnabled = true
			i++
		case "--notify":
			cfg.NotifyEnabled = true
			i++
		case "--notify-topic":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--notify-topic requires a topic string")
			}
			cfg.NotifyTopic = args[i+1]
			i += 2
		case "--ntfy-server":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--ntfy-server requires a URL")
			}
			cfg.NtfyServer = args[i+1]
			i += 2
		case "--enable-monitoring":
			fmt.Fprintln(os.Stderr, "warning: --enable-monitoring is deprecated, use --web and --notify")
			cfg.EnableMonitoring = true
			cfg.WebEnabled = true
			cfg.NotifyEnabled = true
			i++
		case "--no-architect":
			cfg.NoArchitect = true
			i++
		case "--no-simplify":
			cfg.NoSimplify = true
			i++
		case "--no-fusion":
			cfg.NoFusion = true
			i++
		case "--daemon":
			cfg.DaemonMode = true
			i++
		case "--retro":
			cfg.RetroEnabled = true
			i++
		case "--kill":
			cfg.KillDaemon = true
			i++
		case "--idle-timeout":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--idle-timeout requires a duration (e.g. 5m, 30s, 0)")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--idle-timeout: invalid duration %q", args[i+1])
			}
			cfg.IdleTimeout = d
			i += 2
		case "--fusion-workers":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--fusion-workers requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--fusion-workers: invalid number %q", args[i+1])
			}
			if n < 2 {
				n = 2
			}
			cfg.FusionWorkers = n
			i += 2
		case "--model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--model requires a model name")
			}
			cfg.ModelOverride = args[i+1]
			i += 2
		case "--architect-model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--architect-model requires a model name")
			}
			cfg.ArchitectModel = args[i+1]
			i += 2
		case "--implementer-model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--implementer-model requires a model name")
			}
			cfg.ImplementerModel = args[i+1]
			i += 2
		case "--utility-model":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--utility-model requires a model name")
			}
			cfg.UtilityModel = args[i+1]
			i += 2

		case "--story-timeout":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--story-timeout requires a number of minutes")
			}
			val, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--story-timeout must be a number: %w", err)
			}
			cfg.StoryTimeout = val
			i += 2

		default:
			// Check for --key=value forms
			if len(args[i]) > 6 && args[i][:6] == "--dir=" {
				cfg.ProjectDir = args[i][6:]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--plan=") {
				cfg.PlanFile = args[i][len("--plan="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--workers=") {
				val := args[i][len("--workers="):]
				if val == "auto" {
					cfg.WorkersAuto = true
					cfg.Workers = 1
				} else {
					n, err := strconv.Atoi(val)
					if err != nil {
						return nil, fmt.Errorf("--workers: invalid value %q (use a number or 'auto')", val)
					}
					if n < 1 {
						n = 1
					}
					cfg.Workers = n
				}
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--workspace-base=") {
				cfg.WorkspaceBase = args[i][len("--workspace-base="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--quality-workers=") {
				n, err := strconv.Atoi(args[i][len("--quality-workers="):])
				if err != nil {
					return nil, fmt.Errorf("--quality-workers: invalid number %q", args[i][len("--quality-workers="):])
				}
				if n < 1 {
					n = 1
				}
				cfg.QualityWorkers = n
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--quality-max-iterations=") {
				n, err := strconv.Atoi(args[i][len("--quality-max-iterations="):])
				if err != nil {
					return nil, fmt.Errorf("--quality-max-iterations: invalid number %q", args[i][len("--quality-max-iterations="):])
				}
				if n < 1 {
					n = 1
				}
				cfg.QualityMaxIters = n
				i++
				continue
			}
			if len(args[i]) > 25 && args[i][:25] == "--judge-max-rejections=" {
				n, err := strconv.Atoi(args[i][25:])
				if err != nil {
					return nil, fmt.Errorf("--judge-max-rejections: invalid number %q", args[i][25:])
				}
				cfg.JudgeMaxRejections = n
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--notify-topic=") {
				cfg.NotifyTopic = args[i][len("--notify-topic="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--ntfy-server=") {
				cfg.NtfyServer = args[i][len("--ntfy-server="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--model=") {
				cfg.ModelOverride = args[i][len("--model="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--architect-model=") {
				cfg.ArchitectModel = args[i][len("--architect-model="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--implementer-model=") {
				cfg.ImplementerModel = args[i][len("--implementer-model="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--utility-model=") {
				cfg.UtilityModel = args[i][len("--utility-model="):]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--fusion-workers=") {
				n, err := strconv.Atoi(args[i][len("--fusion-workers="):])
				if err != nil {
					return nil, fmt.Errorf("--fusion-workers: invalid number %q", args[i][len("--fusion-workers="):])
				}
				if n < 2 {
					n = 2
				}
				cfg.FusionWorkers = n
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--idle-timeout=") {
				d, err := time.ParseDuration(args[i][len("--idle-timeout="):])
				if err != nil {
					return nil, fmt.Errorf("--idle-timeout: invalid duration %q", args[i][len("--idle-timeout="):])
				}
				cfg.IdleTimeout = d
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--story-timeout=") {
				val, err := strconv.Atoi(args[i][len("--story-timeout="):])
				if err != nil {
					return nil, fmt.Errorf("--story-timeout must be a number: %w", err)
				}
				cfg.StoryTimeout = val
				i++
				continue
			}

			return nil, fmt.Errorf("unknown argument %q. Use --help for usage", args[i])
		}
	}

	cfg.RalphHome = os.Getenv("RALPH_HOME")

	// Re-resolve PROJECT_DIR in case --dir was set in the flag loop.
	if cfg.ProjectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.ProjectDir = cwd
	}
	abs, absErr := filepath.Abs(cfg.ProjectDir)
	if absErr != nil {
		return nil, fmt.Errorf("cannot resolve project dir: %w", absErr)
	}
	cfg.ProjectDir = abs

	// Load .ralph/.env if it exists (values only apply where flags weren't explicitly set)
	envFile := filepath.Join(cfg.ProjectDir, ".ralph", ".env")
	dotEnv, envErr := loadDotEnv(envFile)
	if envErr != nil {
		debuglog.Log("dotenv: could not load %s: %v", envFile, envErr)
	} else {
		debuglog.Log("dotenv: loaded %d vars from %s", len(dotEnv), envFile)
	}

	// Apply env vars (dotenv -> OS env -> flag). Flags always win.
	cfg.applyEnvDefaults(dotEnv)

	// Derive paths
	cfg.PRDFile = filepath.Join(cfg.ProjectDir, "prd.json")
	cfg.ProgressFile = filepath.Join(cfg.ProjectDir, "progress.md")
	cfg.ArchiveDir = filepath.Join(cfg.ProjectDir, ".ralph", "archive")
	cfg.LastBranchFile = filepath.Join(cfg.ProjectDir, ".ralph", ".last-branch")
	cfg.LogDir = filepath.Join(cfg.ProjectDir, ".ralph", "logs")

	return cfg, nil
}

// applyEnvDefaults fills in config values from .env file and OS environment variables.
// Priority: CLI flags > OS env vars > .env file values.
func (c *Config) applyEnvDefaults(dotEnv map[string]string) {
	// Export all .env vars into the OS environment (don't clobber existing values).
	// This ensures keys like VOYAGE_API_KEY and ANTHROPIC_API_KEY are available
	// to downstream code that reads them via os.Getenv.
	for k, v := range dotEnv {
		if existing := os.Getenv(k); existing == "" {
			os.Setenv(k, v)
			debuglog.Log("dotenv: set %s (len=%d)", k, len(v))
		} else {
			debuglog.Log("dotenv: skipped %s (already set in env)", k)
		}
	}

	// Helper: get value from OS env (now includes .env values from above).
	getEnv := func(key string) string {
		return os.Getenv(key)
	}

	if c.NotifyTopic == "" {
		c.NotifyTopic = getEnv("RALPH_NOTIFY_TOPIC")
	}
	if c.NtfyServer == "" {
		c.NtfyServer = getEnv("RALPH_NTFY_SERVER")
	}
	// --notify without a topic falls back to "ralph" so the flag is self-contained.
	if c.NotifyEnabled && c.NotifyTopic == "" {
		c.NotifyTopic = "ralph"
	}
}

// ResolveAutoWorkers sets the Workers count when --workers auto is active.
// Called after DAG analysis with the total number of stories. Workers is set
// to min(storyCount, AutoMaxWorkers) — the DAG scheduling loop naturally
// limits concurrency to actually-ready stories each cycle.
func (c *Config) ResolveAutoWorkers(storyCount int) {
	if !c.WorkersAuto {
		return
	}
	n := storyCount
	if n > c.AutoMaxWorkers {
		n = c.AutoMaxWorkers
	}
	if n < 1 {
		n = 1
	}
	c.Workers = n
}

func (c *Config) Validate() error {
	if c.IdleMode {
		return nil
	}
	if c.PlanFile != "" {
		// Resolve plan file path
		if !filepath.IsAbs(c.PlanFile) {
			c.PlanFile = filepath.Join(c.ProjectDir, c.PlanFile)
		}
		if _, err := os.Stat(c.PlanFile); os.IsNotExist(err) {
			return fmt.Errorf("plan file not found: %s", c.PlanFile)
		}
		// Don't require prd.json when --plan is used (it will be generated)
	} else if _, err := os.Stat(c.PRDFile); os.IsNotExist(err) {
		c.NoPRD = true
	}

	return nil
}

func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(filepath.Join(c.ProjectDir, ".ralph"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(c.ProjectDir, ".ralph", "memory"), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(c.LogDir, 0o755)
}


func printUsage() {
	fmt.Print(`Usage: ralph [options]

Run the Ralph autonomous agent loop against a prd.json in the current directory.

General:
  --dir <path>                    Project directory containing prd.json (default: current directory)
  --plan <path>                   Generate prd.json from a plan file before executing
  --idle                          Launch TUI in idle mode (no execution, just display layout)
  --help, -h                      Show this help message

Execution:
  --workers <n|auto>              Number of parallel workers, or 'auto' to scale to DAG width (default: 1 = serial)
  --workspace-base <path>         Base directory for workspaces (default: /tmp/ralph-workspaces)
  --no-architect                  Skip architect phase for all stories (go straight to implementer)
  --no-simplify                   Skip per-story simplify pass (code quality review before judge)
  --no-fusion                     Disable automatic fusion mode for complex stories
  --fusion-workers <n>            Competing implementations per complex story (default: 2)
  --utility-model <name>          Model for DAG analysis and utility tasks (default: haiku)
  --story-timeout <minutes>       Max wall clock minutes per story before cancellation (default: 0 = no limit)

Model Selection:
  --model <name>                  Override model for all roles (e.g. opus, sonnet, haiku)
  --architect-model <name>        Override model for architect role only
  --implementer-model <name>      Override model for implementer role only
                                  Precedence: role-specific flag > --model > role default

Judge:
  --no-judge                      Disable LLM-as-Judge verification (enabled by default)
  --judge-max-rejections <n>      Max judge rejections per story before auto-passing (default: 2)

Quality:
  --no-quality-review             Disable final quality review (enabled by default)
  --quality-workers <n>           Parallel quality reviewers (default: 3)
  --quality-max-iterations <n>    Max review-fix cycles (default: 2)

Memory:
  --memory-disable               Disable memory injection

Monitoring:
  --web                          Launch the web viewer (singleton per host) and print its URL
  --notify                       Enable push notifications via ntfy
  --notify-topic <topic>         ntfy topic to publish to (default: read from RALPH_NOTIFY_TOPIC)
  --ntfy-server <url>            Self-hosted ntfy server URL (default: https://ntfy.sh)
  --enable-monitoring            DEPRECATED: alias for --web --notify (prints a warning)

Daemon:
  --daemon                        Run as a background daemon (no TUI, coordination loop + API only)
  --kill                          Send SIGTERM to a running daemon and wait for exit
  --idle-timeout <duration>       Auto-shutdown after idle (no work + no clients) for duration (default: 5m, 0 = disabled)
  --retro                         Run design retrospective automatically after completion

Client Commands (connect to running daemon):
  ralph status                    Show current daemon state
  ralph logs                      Stream daemon events to stdout
  ralph hint <worker-id> "text"   Send a hint to a worker
  ralph pause                     Pause all workers
  ralph resume                    Resume paused workers
  ralph retro                     Run design retrospective on completed work

Display:
  --no-guy                        Disable sprite mascot overlay

Examples:
  ralph                           Run until all stories are complete
  ralph --dir ~/myapp             Run against prd.json in ~/myapp
  ralph --idle                    Launch TUI without executing the loop
  ralph --no-judge                Run without Gemini judge verification
  ralph --judge-max-rejections 3  Allow up to 3 rejections per story
  ralph --plan .claude/plans/my-plan.md   Generate prd.json from plan, then execute
  ralph --no-quality-review       Run without final quality gate
  ralph --web --notify            Launch the web viewer and enable push notifications

History Subcommand:
  ralph history                          Show last 10 runs
  ralph history --all                    Show all runs
  ralph history --stats                  Show aggregate statistics across all runs
  ralph history --compare                Compare runs grouped by model
  ralph history --compare --by flags     Compare runs grouped by feature-flag configuration

Memory Subcommands:
  ralph memory stats                     Show memory file sizes and entry counts
`)
}
