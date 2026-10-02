package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	stepPending = iota
	stepRunning
	stepDone
	stepFailed
	stepSkipped
	stepWarned
)

type step struct {
	label  string
	state  int
	detail string
}

// Progress shows one line per step: a live spinner view on a terminal, finished steps as plain lines elsewhere.
type Progress struct {
	p         *Printer
	title     string
	mu        sync.Mutex
	steps     []step
	prog      *tea.Program
	finished  chan struct{}
	transient bool
	stopped   bool
	human     io.Writer
	errw      io.Writer
}

type progressModel struct {
	g       *Progress
	spin    spinner.Model
	r       *lipgloss.Renderer
	closing bool
}

type refreshMsg struct{}

type closeMsg struct{}

// StartProgress begins a progress view; transient views disappear when stopped.
func (p *Printer) StartProgress(title string, transient bool) *Progress {
	g := &Progress{p: p, title: title, transient: transient}
	if p.mode != ModeTTY || p.quiet {
		if title != "" && !p.quiet {
			p.Println("%s", p.Bold(title))
		}
		return g
	}
	r := lipgloss.NewRenderer(p.human)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if p.color {
		sp.Style = r.NewStyle().Foreground(lipgloss.Color("6"))
	}
	g.human, g.errw = p.human, p.errw
	g.prog = tea.NewProgram(progressModel{g: g, spin: sp, r: r}, tea.WithOutput(p.human), tea.WithInput(nil), tea.WithoutSignalHandler())
	live := &liveWriter{prog: g.prog}
	p.human, p.errw = live, live
	g.finished = make(chan struct{})
	go func() {
		_, _ = g.prog.Run()
		close(g.finished)
	}()
	return g
}

// Add appends a pending step and returns its index.
func (g *Progress) Add(label string) int {
	g.mu.Lock()
	g.steps = append(g.steps, step{label: label})
	i := len(g.steps) - 1
	g.mu.Unlock()
	g.refresh()
	return i
}

// Begin marks a step as running.
func (g *Progress) Begin(i int) { g.set(i, stepRunning, "") }

// Done marks a step as finished.
func (g *Progress) Done(i int, detail string) { g.set(i, stepDone, detail) }

// Fail marks a step as failed.
func (g *Progress) Fail(i int, detail string) { g.set(i, stepFailed, detail) }

// Skip marks a step as skipped.
func (g *Progress) Skip(i int, detail string) { g.set(i, stepSkipped, detail) }

// Warn marks a step as finished with a warning.
func (g *Progress) Warn(i int, detail string) { g.set(i, stepWarned, detail) }

func (g *Progress) set(i, state int, detail string) {
	g.mu.Lock()
	if i < 0 || i >= len(g.steps) {
		g.mu.Unlock()
		return
	}
	g.steps[i].state = state
	g.steps[i].detail = detail
	s := g.steps[i]
	g.mu.Unlock()
	if g.prog == nil {
		if state != stepRunning && state != stepPending {
			g.p.Println("  %s %s", g.p.Mark(stateMark(state)), joinDetail(s.label, s.detail))
		}
		return
	}
	g.refresh()
}

func (g *Progress) refresh() {
	if g.prog != nil {
		g.prog.Send(refreshMsg{})
	}
}

// Stop ends the live view and restores normal output.
func (g *Progress) Stop() {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	g.stopped = true
	g.mu.Unlock()
	if g.prog == nil {
		return
	}
	g.prog.Send(closeMsg{})
	<-g.finished
	g.p.human, g.p.errw = g.human, g.errw
}

func stateMark(state int) string {
	switch state {
	case stepDone:
		return MarkOK
	case stepFailed:
		return MarkFail
	case stepSkipped:
		return MarkSkip
	case stepWarned:
		return MarkWarn
	}
	return MarkInfo
}

func joinDetail(label, detail string) string {
	if detail == "" {
		return label
	}
	return label + "  " + detail
}

func (m progressModel) Init() tea.Cmd { return m.spin.Tick }

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case closeMsg:
		m.closing = true
		return m, tea.Quit
	case refreshMsg:
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.closing && m.g.transient {
		return ""
	}
	g := m.g
	g.mu.Lock()
	steps := append([]step(nil), g.steps...)
	g.mu.Unlock()
	var b strings.Builder
	if g.title != "" {
		b.WriteString(g.p.Bold(g.title) + "\n")
	}
	for _, s := range steps {
		mark := g.p.Mark(stateMark(s.state))
		switch s.state {
		case stepPending:
			mark = g.p.Dim("·")
		case stepRunning:
			mark = m.spin.View()
		}
		detail := s.detail
		if detail != "" {
			detail = "  " + g.p.Dim(detail)
		}
		fmt.Fprintf(&b, "  %s %s%s\n", mark, s.label, detail)
	}
	return b.String()
}

type liveWriter struct {
	prog *tea.Program
	mu   sync.Mutex
	buf  []byte
}

func (w *liveWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		w.prog.Println(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(b), nil
}
