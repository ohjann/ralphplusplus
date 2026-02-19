package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
)

func newProgressViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

func renderProgressPanel(vp viewport.Model, active bool, width, height int) string {
	title := stylePanelTitle.Render("Progress")

	style := stylePanelBorder
	if active {
		style = stylePanelBorderActive
	}

	// Account for border and padding in sizing
	innerWidth := width - 4 // 2 for border + 2 for padding
	if innerWidth < 0 {
		innerWidth = 0
	}
	innerHeight := height - 3 // 2 for border + 1 for title
	if innerHeight < 0 {
		innerHeight = 0
	}

	vp.Width = innerWidth
	vp.Height = innerHeight

	content := title + "\n" + vp.View()

	return style.Width(innerWidth).Height(innerHeight + 1).Render(content)
}
