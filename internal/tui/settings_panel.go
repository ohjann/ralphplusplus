package tui

import (
	"fmt"
	"strings"

	"github.com/eoghanhynes/ralph/internal/config"
)

// settingType describes the kind of value a setting holds.
type settingType int

const (
	settingBool settingType = iota
	settingInt
	settingFloat
)

// settingEntry is a single tunable setting.
type settingEntry struct {
	Label      string      // display label
	Type       settingType
	BoolVal    bool
	IntVal     int
	FloatVal   float64
	DefaultB   bool
	DefaultI   int
	DefaultF   float64
	Min        int     // min for int
	Max        int     // max for int (0 = no upper limit)
	FloatMin   float64
	FloatMax   float64
	FloatStep  float64
}

// settingsState holds the list of tunable settings and cursor position.
type settingsState struct {
	Entries     []settingEntry
	SelectedIdx int
	Dirty       bool
}

// newSettingsState builds the settings list from the current config.
func newSettingsState(cfg *config.Config) settingsState {
	return settingsState{
		Entries: []settingEntry{
			{Label: "Judge Enabled", Type: settingBool, BoolVal: cfg.JudgeEnabled, DefaultB: true},
			{Label: "Judge Max Rejections", Type: settingInt, IntVal: cfg.JudgeMaxRejections, DefaultI: 2, Min: 0},
			{Label: "Workers", Type: settingInt, IntVal: cfg.Workers, DefaultI: 1, Min: 1},
			{Label: "Quality Review", Type: settingBool, BoolVal: cfg.QualityReview, DefaultB: true},
			{Label: "Quality Workers", Type: settingInt, IntVal: cfg.QualityWorkers, DefaultI: 3, Min: 1},
			{Label: "Quality Max Iterations", Type: settingInt, IntVal: cfg.QualityMaxIters, DefaultI: 2, Min: 1},
			{Label: "Memory Disable", Type: settingBool, BoolVal: cfg.Memory.Disabled, DefaultB: false},
			{Label: "Skip Architect", Type: settingBool, BoolVal: cfg.NoArchitect, DefaultB: false},
			{Label: "Sprite Enabled", Type: settingBool, BoolVal: cfg.SpriteEnabled, DefaultB: true},
		},
	}
}

func (s *settingsState) moveUp() {
	if s.SelectedIdx > 0 {
		s.SelectedIdx--
	}
}

func (s *settingsState) moveDown() {
	if s.SelectedIdx < len(s.Entries)-1 {
		s.SelectedIdx++
	}
}

func (s *settingsState) toggle() {
	e := &s.Entries[s.SelectedIdx]
	if e.Type == settingBool {
		e.BoolVal = !e.BoolVal
		s.Dirty = true
	}
}

func (s *settingsState) increment() {
	e := &s.Entries[s.SelectedIdx]
	switch e.Type {
	case settingInt:
		e.IntVal++
		if e.Max > 0 && e.IntVal > e.Max {
			e.IntVal = e.Max
		}
		s.Dirty = true
	case settingFloat:
		e.FloatVal += e.FloatStep
		if e.FloatVal > e.FloatMax {
			e.FloatVal = e.FloatMax
		}
		// Round to avoid float drift
		e.FloatVal = float64(int(e.FloatVal*100+0.5)) / 100
		s.Dirty = true
	case settingBool:
		e.BoolVal = !e.BoolVal
		s.Dirty = true
	}
}

func (s *settingsState) decrement() {
	e := &s.Entries[s.SelectedIdx]
	switch e.Type {
	case settingInt:
		e.IntVal--
		if e.IntVal < e.Min {
			e.IntVal = e.Min
		}
		s.Dirty = true
	case settingFloat:
		e.FloatVal -= e.FloatStep
		if e.FloatVal < e.FloatMin {
			e.FloatVal = e.FloatMin
		}
		e.FloatVal = float64(int(e.FloatVal*100+0.5)) / 100
		s.Dirty = true
	case settingBool:
		e.BoolVal = !e.BoolVal
		s.Dirty = true
	}
}

// applyTo writes current settings values back to the Config.
func (s *settingsState) applyTo(cfg *config.Config) {
	for i, e := range s.Entries {
		switch i {
		case 0:
			cfg.JudgeEnabled = e.BoolVal
		case 1:
			cfg.JudgeMaxRejections = e.IntVal
		case 2:
			cfg.Workers = e.IntVal
		case 3:
			cfg.QualityReview = e.BoolVal
		case 4:
			cfg.QualityWorkers = e.IntVal
		case 5:
			cfg.QualityMaxIters = e.IntVal
		case 6:
			cfg.Memory.Disabled = e.BoolVal
		case 7:
			cfg.NoArchitect = e.BoolVal
		case 8:
			cfg.SpriteEnabled = e.BoolVal
		}
	}
}

// refreshFrom reloads entry values from the config (e.g. after external change).
func (s *settingsState) refreshFrom(cfg *config.Config) {
	fresh := newSettingsState(cfg)
	for i := range s.Entries {
		if i < len(fresh.Entries) {
			s.Entries[i].BoolVal = fresh.Entries[i].BoolVal
			s.Entries[i].IntVal = fresh.Entries[i].IntVal
			s.Entries[i].FloatVal = fresh.Entries[i].FloatVal
		}
	}
}

// renderSettingsPanel draws the settings form.
func renderSettingsPanel(state settingsState, width int) string {
	var sb strings.Builder

	// Header
	header := "  Settings"
	if state.Dirty {
		header += "  " + styleMuted.Render("[unsaved]")
	}
	sb.WriteString(header + "\n")
	sb.WriteString("  " + strings.Repeat("─", min(width-4, 50)) + "\n")

	// Entries
	for i, e := range state.Entries {
		cursor := "  "
		if i == state.SelectedIdx {
			cursor = styleTagActive.Render("> ")
		}

		label := e.Label
		val := formatSettingValue(e)
		def := formatSettingDefault(e)

		// Pad label to align values
		padLen := 26 - len(label)
		if padLen < 2 {
			padLen = 2
		}
		pad := strings.Repeat(" ", padLen)

		line := fmt.Sprintf("%s%s%s%s", cursor, label, pad, val)
		if def != "" {
			line += "  " + styleMuted.Render("("+def+")")
		}

		sb.WriteString(line + "\n")
	}

	// Footer help
	sb.WriteString("\n")
	sb.WriteString("  " + styleMuted.Render("enter: toggle  +/-: adjust  ctrl+s: save to config.toml"))
	sb.WriteString("\n")

	return sb.String()
}

func formatSettingValue(e settingEntry) string {
	switch e.Type {
	case settingBool:
		if e.BoolVal {
			return styleSuccess.Render("true")
		}
		return styleDanger.Render("false")
	case settingInt:
		return fmt.Sprintf("%d", e.IntVal)
	case settingFloat:
		return fmt.Sprintf("%.2f", e.FloatVal)
	}
	return ""
}

func formatSettingDefault(e settingEntry) string {
	switch e.Type {
	case settingBool:
		return fmt.Sprintf("default: %t", e.DefaultB)
	case settingInt:
		return fmt.Sprintf("default: %d", e.DefaultI)
	case settingFloat:
		return fmt.Sprintf("default: %.2f", e.DefaultF)
	}
	return ""
}
