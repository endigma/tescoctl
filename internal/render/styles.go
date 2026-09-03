package render

import "github.com/charmbracelet/lipgloss"

// Styles is the palette. Colours are ANSI 256 so they adapt to the user's
// theme rather than fighting it.
type Styles struct {
	Title  lipgloss.Style
	Header lipgloss.Style
	Muted  lipgloss.Style
	Price  lipgloss.Style
	Offer  lipgloss.Style
	Error  lipgloss.Style
	Border lipgloss.Style
}

func newStyles(r *lipgloss.Renderer) Styles {
	return Styles{
		Title:  r.NewStyle().Bold(true),
		Header: r.NewStyle().Bold(true).Foreground(lipgloss.Color("14")),
		Muted:  r.NewStyle().Foreground(lipgloss.Color("8")),
		Price:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		Offer:  r.NewStyle().Foreground(lipgloss.Color("11")),
		Error:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
		Border: r.NewStyle().Foreground(lipgloss.Color("8")),
	}
}
