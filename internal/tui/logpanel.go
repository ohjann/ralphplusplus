package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
)

func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = stylePhaseActive
	return s
}

func renderLogPanel(sp spinner.Model, content string, running bool, width int) string {
	title := stylePanelTitle.Render("Claude")

	innerWidth := width - 4
	if innerWidth < 0 {
		innerWidth = 0
	}

	// Truncate lines to fit width
	lines := strings.Split(content, "\n")
	var truncated []string
	for _, line := range lines {
		if len(line) > innerWidth && innerWidth > 3 {
			line = line[:innerWidth-3] + "..."
		}
		truncated = append(truncated, line)
	}
	body := strings.Join(truncated, "\n")

	prefix := title
	if running {
		prefix = fmt.Sprintf("%s %s", title, sp.View())
	}

	panel := prefix + "  " + body

	return styleLogPanel.Width(innerWidth).Render(panel)
}
