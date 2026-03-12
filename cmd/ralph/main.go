package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eoghanhynes/ralph/internal/config"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"github.com/eoghanhynes/ralph/internal/prd"
	"github.com/eoghanhynes/ralph/internal/tui"
)

var Version = "dev"

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directories: %v\n", err)
		os.Exit(1)
	}

	// Default max iterations to 1.5x the number of stories (headroom for retries)
	if cfg.MaxIterations == 0 && !cfg.IdleMode {
		if p, err := prd.Load(cfg.PRDFile); err == nil {
			n := p.TotalCount()
			cfg.MaxIterations = n + n/2
		}
	}

	// Initialize debug log
	if err := debuglog.Init(cfg.LogDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init debug log: %v\n", err)
	}
	defer debuglog.Close()
	debuglog.Log("ralph starting, version=%s, workers=%d, maxIterations=%d", Version, cfg.Workers, cfg.MaxIterations)

	model := tui.NewModel(cfg, Version)

	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	if m, ok := finalModel.(*tui.Model); ok {
		os.Exit(m.ExitCode())
	}
}
