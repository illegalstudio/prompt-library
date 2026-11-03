package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func renderMarkdown(body string, width int, dark bool) (string, error) {
	style := styles.LightStyleConfig
	if dark {
		style = styles.DarkStyleConfig
	}
	// The surrounding UI supplies the margin and spacing.
	margin := uint(0)
	style.Document.Margin = &margin
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(max(1, width)),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return "", err
	}
	rendered, err := renderer.Render(body)
	return strings.Trim(rendered, "\n"), err
}

func (m *model) renderViewer() {
	if m.viewing == nil {
		return
	}
	rendered, err := renderMarkdown(m.viewing.Body, m.viewer.Width, m.darkBackground)
	if err != nil {
		rendered = m.viewing.Body
		m.status = "Markdown preview unavailable; showing plain text: " + err.Error()
	}
	offset := m.viewer.YOffset
	m.viewer.SetContent(rendered)
	m.viewer.SetYOffset(offset)
	m.viewer.SetXOffset(0)
}

func (m model) viewerView() string {
	title := accent.Render(ansi.Truncate(strings.Join(strings.Fields(m.viewing.Title), " "), max(1, m.width-4), "..."))
	footer := []string{
		fmt.Sprintf("Up/Down scroll  |  PgUp/PgDn page  |  Left/Right pan  %.0f%%", m.viewer.ScrollPercent()*100),
		"Enter copy  |  Esc/v/q back",
	}
	for i := range footer {
		footer[i] = "  " + ansi.Truncate(footer[i], max(1, m.width-4), "...")
	}
	return "  " + title + "\n\n" + lipgloss.NewStyle().MarginLeft(2).Render(m.viewer.View()) + "\n\n" + muted.Render(strings.Join(footer, "\n"))
}
