package ui

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/muesli/termenv"
)

// Tones color human output.
const (
	ToneOK     = "ok"
	ToneWarn   = "warn"
	ToneFail   = "fail"
	ToneInfo   = "info"
	ToneDim    = "dim"
	ToneAccent = "accent"
)

// Paint renders s in a tone on a color terminal.
func (p *Printer) Paint(tone, s string) string {
	switch tone {
	case ToneOK:
		return p.paint("32", s)
	case ToneWarn:
		return p.paint("33", s)
	case ToneFail:
		return p.paint("31", s)
	case ToneInfo:
		return p.paint("36", s)
	case ToneDim:
		return p.paint("2", s)
	case ToneAccent:
		return p.paint("1;35", s)
	}
	return s
}

// Glyph returns a symbol on a terminal and its ASCII stand-in elsewhere.
func (p *Printer) Glyph(unicode, ascii string) string {
	if p.mode == ModeTTY {
		return unicode
	}
	return ascii
}

// Section prints a heading that opens a block of output.
func (p *Printer) Section(title, subtitle string) {
	if p.quiet {
		return
	}
	line := p.Paint(ToneAccent, p.Glyph("▍", "#")) + " " + p.Bold(title)
	if subtitle != "" {
		line += "  " + p.Dim(subtitle)
	}
	p.Println("")
	p.Println("%s", line)
}

// TreeNode is one line of a rendered tree.
type TreeNode struct {
	Label    string
	Children []TreeNode
}

// Tree prints nodes with box-drawing branches on a terminal and ASCII branches elsewhere.
func (p *Printer) Tree(indent string, root TreeNode) {
	if p.quiet {
		return
	}
	p.Println("%s%s", indent, root.Label)
	p.treeChildren(indent, root.Children)
}

func (p *Printer) treeChildren(prefix string, nodes []TreeNode) {
	mid, last, bar, gap := p.Glyph("├── ", "|-- "), p.Glyph("└── ", "`-- "), p.Glyph("│   ", "|   "), "    "
	for i, n := range nodes {
		branch, next := mid, bar
		if i == len(nodes)-1 {
			branch, next = last, gap
		}
		p.Println("%s%s%s", prefix, p.Dim(branch), n.Label)
		p.treeChildren(prefix+p.Dim(next), n.Children)
	}
}

// Grid prints a table with a header row: bordered on a terminal, aligned columns elsewhere.
func (p *Printer) Grid(indent string, headers []string, rows [][]string) {
	if p.quiet || len(rows) == 0 {
		return
	}
	if p.mode != ModeTTY {
		tw := tabwriter.NewWriter(p.human, 0, 2, 2, ' ', 0)
		up := make([]string, len(headers))
		for i, h := range headers {
			up[i] = strings.ToUpper(h)
		}
		fmt.Fprintln(tw, indent+strings.Join(up, "\t"))
		for _, r := range rows {
			fmt.Fprintln(tw, indent+strings.Join(r, "\t"))
		}
		_ = tw.Flush()
		return
	}
	r := lipgloss.NewRenderer(p.human)
	if !p.color {
		r.SetColorProfile(termenv.Ascii)
	}
	head := r.NewStyle().Bold(true).Padding(0, 1)
	cell := r.NewStyle().Padding(0, 1)
	border := r.NewStyle()
	if p.color {
		border = border.Foreground(lipgloss.Color("8"))
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(border).
		BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return head
			}
			return cell
		})
	for _, line := range strings.Split(t.Render(), "\n") {
		p.Println("%s%s", indent, line)
	}
}

// Note prints an indented line with a mark.
func (p *Printer) Note(indent, mark, format string, args ...any) {
	p.Println("%s%s %s", indent, p.Mark(mark), fmt.Sprintf(format, args...))
}
