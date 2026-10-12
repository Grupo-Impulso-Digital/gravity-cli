package verbatim

import (
	"sort"

	"github.com/yuin/goldmark/ast"
)

type locator struct {
	src    []byte
	starts []int
	base   []int
}

func newLocator(src []byte, base []int) *locator {
	starts := []int{0}
	for i, ch := range src {
		if ch == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	return &locator{src: src, starts: starts, base: base}
}

func (l *locator) line(offset int) int {
	return sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
}

func (l *locator) span(n ast.Node) lineSpan {
	first, last := -1, -1
	note := func(from, to int) {
		if from < 0 || to < from {
			return
		}
		if first < 0 || from < first {
			first = from
		}
		if to > last {
			last = to
		}
	}
	if p := n.Pos(); p >= 0 {
		note(p, p)
	}
	_ = ast.Walk(n, func(m ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || m.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		if p := m.Pos(); p >= 0 {
			note(p, p)
		}
		lines := m.Lines()
		if lines.Len() > 0 {
			note(lines.At(0).Start, max(lines.At(lines.Len()-1).Stop-1, lines.At(lines.Len()-1).Start))
		}
		if hb, ok := m.(*ast.HTMLBlock); ok && hb.HasClosure() {
			note(hb.ClosureLine.Start, max(hb.ClosureLine.Stop-1, hb.ClosureLine.Start))
		}
		return ast.WalkContinue, nil
	})
	if first < 0 {
		return noSpan
	}
	start, end := l.line(first), l.line(last)
	if _, ok := n.(*ast.FencedCodeBlock); ok {
		end = l.closingFence(end)
	}
	return spanOf(l.base, start, end)
}

func (l *locator) closingFence(end int) int {
	next := end + 1
	if next >= len(l.starts) {
		return end
	}
	stop := len(l.src)
	if next+1 < len(l.starts) {
		stop = l.starts[next+1]
	}
	if fenceRE.Match(l.src[l.starts[next]:stop]) {
		return next
	}
	return end
}
