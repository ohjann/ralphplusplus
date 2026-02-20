package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	MaxIterations      int
	ProjectDir         string
	RalphHome          string
	JudgeEnabled       bool
	JudgeMaxRejections int
	IdleMode           bool

	// Derived paths
	PRDFile        string
	ProgressFile   string
	ArchiveDir     string
	LastBranchFile string
	LogDir         string
}

func Parse(args []string) (*Config, error) {
	cfg := &Config{
		MaxIterations:      10,
		JudgeMaxRejections: 2,
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
		case "--judge":
			cfg.JudgeEnabled = true
			i++
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
		default:
			// Check for --dir=value and --judge-max-rejections=value
			if len(args[i]) > 6 && args[i][:6] == "--dir=" {
				cfg.ProjectDir = args[i][6:]
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
			// Positional: max_iterations
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return nil, fmt.Errorf("unknown argument %q. Use --help for usage", args[i])
			}
			cfg.MaxIterations = n
			i++
		}
	}

	// Resolve RALPH_HOME: directory of the binary, or parent of binary dir, or $RALPH_HOME
	cfg.RalphHome = resolveRalphHome()

	// Resolve PROJECT_DIR
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

	// Derive paths
	cfg.PRDFile = filepath.Join(cfg.ProjectDir, "prd.json")
	cfg.ProgressFile = filepath.Join(cfg.ProjectDir, "progress.txt")
	cfg.ArchiveDir = filepath.Join(cfg.ProjectDir, ".ralph", "archive")
	cfg.LastBranchFile = filepath.Join(cfg.ProjectDir, ".ralph", ".last-branch")
	cfg.LogDir = filepath.Join(cfg.ProjectDir, ".ralph", "logs")

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.IdleMode {
		return nil
	}
	if _, err := os.Stat(c.PRDFile); os.IsNotExist(err) {
		return fmt.Errorf("no prd.json found in %s\nUse the /ralph skill in Claude Code to create one from a PRD", c.ProjectDir)
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
	fmt.Print(`Usage: ralph [options] [max_iterations]

Run the Ralph autonomous agent loop against a prd.json in the current directory.

Options:
  --dir <path>                    Project directory containing prd.json (default: current directory)
  --idle                          Launch TUI in idle mode (no execution, just display layout)
  --judge                         Enable LLM-as-Judge verification (requires gemini CLI)
  --judge-max-rejections <n>      Max judge rejections per story before auto-passing (default: 2)
  --help, -h                      Show this help message

Arguments:
  max_iterations                  Maximum loop iterations (default: 10)

Examples:
  ralph                           Run with 10 iterations, prd.json in current dir
  ralph 5                         Run with 5 iterations
  ralph --dir ~/myapp             Run against prd.json in ~/myapp
  ralph --idle                    Launch TUI without executing the loop
  ralph --judge                   Run with Gemini judge verification
  ralph --judge --judge-max-rejections 3   Allow up to 3 rejections per story
`)
}
