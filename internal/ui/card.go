package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Link is a labeled URL on a summary card.
type Link struct {
	Label string
	URL   string
}

// Card prints a summary: a bordered box on a terminal, an indented block elsewhere.
func (p *Printer) Card(title string, lines []string, links []Link) {
	if p.quiet {
		return
	}
	body := make([]string, 0, len(lines)+len(links))
	body = append(body, lines...)
	width := 0
	for _, l := range links {
		if len(l.Label) > width {
			width = len(l.Label)
		}
	}
	for _, l := range links {
		if l.URL == "" {
			continue
		}
		body = append(body, l.Label+strings.Repeat(" ", width-len(l.Label))+"  "+l.URL)
	}
	if p.mode != ModeTTY {
		p.Println("%s", title)
		for _, l := range body {
			p.Println("  %s", l)
		}
		return
	}
	r := lipgloss.NewRenderer(p.human)
	if !p.color {
		r.SetColorProfile(termenv.Ascii)
	}
	head := r.NewStyle().Bold(true)
	box := r.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if p.color {
		box = box.BorderForeground(lipgloss.Color("6"))
	}
	content := head.Render(title)
	if len(body) > 0 {
		content += "\n" + strings.Join(body, "\n")
	}
	p.Println("%s", box.Render(content))
}
