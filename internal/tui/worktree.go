package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
)

func newWorktreeViewport(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return vp
}

func renderWorktreePanel(vp viewport.Model, content string, active bool, width, height int) string {
	title := stylePanelTitle.Render("Working Tree (jj st)")

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
