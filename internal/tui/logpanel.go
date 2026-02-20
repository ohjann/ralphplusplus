package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
)

func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = stylePhaseActive
	return s
}

func newClaudeViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

func renderClaudePanel(vp viewport.Model, sp spinner.Model, content string, running bool, active bool, width, height int) string {
	title := stylePanelTitle.Render("Claude Activity")
	if running {
		title = fmt.Sprintf("%s %s", title, sp.View())
	}

	style := stylePanelBorder
	if active {
		style = stylePanelBorderActive
	}

	innerWidth := width - 4
	if innerWidth < 0 {
		innerWidth = 0
	}
	innerHeight := height - 3
	if innerHeight < 0 {
		innerHeight = 0
	}

	vp.Width = innerWidth
	vp.Height = innerHeight
	vp.SetContent(content)

	body := title + "\n" + vp.View()

	return style.Width(innerWidth).Height(innerHeight + 1).Render(body)
}
