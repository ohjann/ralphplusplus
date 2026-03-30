package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eoghanhynes/ralph/internal/debuglog"
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
	StatusPort         int    // --status-port <port>: remote status page (0 = disabled)
	NotifyTopic        string // --notify <topic>: ntfy.sh topic for push notifications
	NtfyServer         string // --ntfy-server <url>: self-hosted ntfy server URL
	EnableMonitoring   bool   // --enable-monitoring: activate ntfy + status page from .env
	HistoryCommand     bool   // true when "history" subcommand is used
	HistoryAll         bool   // --all flag for history subcommand
	HistoryStats       bool   // --stats flag for history subcommand
	HistoryCompare     bool   // --compare flag for history subcommand
	NoArchitect        bool   // --no-architect: globally skip architect phase for all stories
	SpriteEnabled      bool   // sprite mascot overlay (default true)
	NoPRD              bool   // true when no prd.json exists (interactive-only mode)
	ModelOverride      string // --model: override model for all roles
	ArchitectModel     string // --architect-model: override model for architect role only
	ImplementerModel   string // --implementer-model: override model for implementer role only
	UtilityModel       string // --utility-model: model for DAG analysis and other utility tasks (default: haiku)
	StoryTimeout       int    // --story-timeout: max minutes per story before cancellation (default: 0 = no limit)
	NoSimplify         bool   // --no-simplify: skip per-story simplify pass
	NoFusion           bool   // --no-fusion: disable automatic fusion mode for complex stories
	FusionWorkers      int    // --fusion-workers N: competing implementations per complex story (default: 2)

	// Derived paths
	PRDFile        string
	ProgressFile   string
	ArchiveDir     string
	LastBranchFile string
	LogDir         string
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
		FusionWorkers:      2,
		Memory: DefaultMemoryConfig(),
	}

	// Check for "history" subcommand as first argument.
	if len(args) > 0 && args[0] == "history" {
		cfg.HistoryCommand = true
		// Parse optional flags
		for j := 1; j < len(args); j++ {
			switch args[j] {
			case "--all":
				cfg.HistoryAll = true
			case "--stats":
				cfg.HistoryStats = true
			case "--compare":
				cfg.HistoryCompare = true
			case "--dir":
				if j+1 < len(args) {
					cfg.ProjectDir = args[j+1]
					j++
				}
			default:
				if strings.HasPrefix(args[j], "--dir=") {
					cfg.ProjectDir = args[j][len("--dir="):]
				}
			}
		}
		cfg.RalphHome = resolveRalphHome()
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
		cfg.RalphHome = resolveRalphHome()
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
				return nil, fmt.Errorf("--workers requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--workers: invalid number %q", args[i+1])
			}
			if n < 1 {
				n = 1
			}
			cfg.Workers = n
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
		case "--status-port":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--status-port requires a port number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return nil, fmt.Errorf("--status-port: invalid number %q", args[i+1])
			}
			cfg.StatusPort = n
			i += 2
		case "--notify":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--notify requires a topic string")
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
			cfg.EnableMonitoring = true
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
				n, err := strconv.Atoi(args[i][len("--workers="):])
				if err != nil {
					return nil, fmt.Errorf("--workers: invalid number %q", args[i][len("--workers="):])
				}
				if n < 1 {
					n = 1
				}
				cfg.Workers = n
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
			if strings.HasPrefix(args[i], "--status-port=") {
				n, err := strconv.Atoi(args[i][len("--status-port="):])
				if err != nil {
					return nil, fmt.Errorf("--status-port: invalid number %q", args[i][len("--status-port="):])
				}
				cfg.StatusPort = n
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--notify=") {
				cfg.NotifyTopic = args[i][len("--notify="):]
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

	// Resolve RALPH_HOME: directory of the binary, or parent of binary dir, or $RALPH_HOME
	cfg.RalphHome = resolveRalphHome()

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
	if c.StatusPort == 0 {
		if v := getEnv("RALPH_STATUS_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.StatusPort = n
			}
		}
	}

	// --enable-monitoring activates monitoring using env values with sensible defaults.
	if c.EnableMonitoring {
		if c.StatusPort == 0 {
			c.StatusPort = 8080
		}
		// NotifyTopic must come from env — we can't guess a topic name.
		// NtfyServer defaults to https://ntfy.sh (handled downstream in notify package).
	}
}

// MonitoringSummary returns a human-readable string describing the active monitoring config.
// Returns empty string if no monitoring is enabled.
func (c *Config) MonitoringSummary() string {
	var lines []string
	if c.NotifyTopic != "" {
		server := c.NtfyServer
		if server == "" {
			server = "https://ntfy.sh"
		}
		lines = append(lines, fmt.Sprintf("  Notifications: %s/%s", server, c.NotifyTopic))
	}
	if c.StatusPort > 0 {
		lines = append(lines, fmt.Sprintf("  Status page:   http://localhost:%d", c.StatusPort))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Monitoring:\n" + strings.Join(lines, "\n")
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

	if c.JudgeEnabled {
		if c.RalphHome == "" {
			return fmt.Errorf("cannot locate ralph home directory (for judge-prompt.md)")
		}
		judgePath := filepath.Join(c.RalphHome, "judge-prompt.md")
		if _, err := os.Stat(judgePath); os.IsNotExist(err) {
			return fmt.Errorf("judge-prompt.md not found at %s", judgePath)
		}
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

func resolveRalphHome() string {
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		dir := filepath.Dir(exe)

		// Check binary's own directory
		if hasPromptFiles(dir) {
			return dir
		}
		// Check parent (handles build/ralph)
		parent := filepath.Dir(dir)
		if hasPromptFiles(parent) {
			return parent
		}
	}

	// Fall back to RALPH_HOME env var
	if env := os.Getenv("RALPH_HOME"); env != "" {
		if hasPromptFiles(env) {
			return env
		}
	}

	return ""
}

func hasPromptFiles(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "ralph-prompt.md"))
	return err == nil
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
  --workers <n>                   Number of parallel workers (default: 1 = serial)
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
  --status-port <port>           Start remote status page on given port (disabled by default)
  --notify <topic>               Send push notifications via ntfy.sh to given topic
  --ntfy-server <url>            Self-hosted ntfy server URL (default: https://ntfy.sh)
  --enable-monitoring            Enable ntfy + status page using .ralph/.env config

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
  ralph --enable-monitoring       Use .ralph/.env for ntfy + status page config

History Subcommand:
  ralph history                          Show last 10 runs
  ralph history --all                    Show all runs

Memory Subcommands:
  ralph memory stats                     Show memory file sizes and entry counts
`)
}
