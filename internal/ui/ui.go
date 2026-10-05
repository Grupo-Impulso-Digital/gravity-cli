// Package ui renders CLI output for a terminal, for CI logs, or as a single --json envelope.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

// Modes of output.
const (
	ModeTTY   = "tty"
	ModePlain = "plain"
	ModeJSON  = "json"
)

// Options configure a Printer.
type Options struct {
	JSON     bool
	NoColor  bool
	Quiet    bool
	Verbose  bool
	CI       bool
	Terminal bool
}

// Warning is a non-fatal notice carried in the --json envelope.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorInfo is the error object of the --json envelope.
type ErrorInfo struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

// Envelope is the single JSON document printed on stdout in --json mode.
type Envelope struct {
	OK       bool       `json:"ok"`
	Command  string     `json:"command"`
	Version  string     `json:"version"`
	Data     any        `json:"data"`
	Warnings []Warning  `json:"warnings"`
	Error    *ErrorInfo `json:"error"`
}

// Printer writes human output and the --json envelope.
type Printer struct {
	stdout   io.Writer
	human    io.Writer
	errw     io.Writer
	mode     string
	color    bool
	quiet    bool
	verbose  bool
	command  string
	warnings []Warning
	emitted  bool
}

// IsTerminal reports whether w is an interactive terminal.
func IsTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// New builds a Printer; human output goes to stdout, or to stderr in --json mode.
func New(stdout, stderr io.Writer, opts Options) *Printer {
	p := &Printer{stdout: stdout, errw: stderr, quiet: opts.Quiet, verbose: opts.Verbose}
	switch {
	case opts.JSON:
		p.mode = ModeJSON
	case opts.Terminal && !opts.CI:
		p.mode = ModeTTY
	default:
		p.mode = ModePlain
	}
	p.color = p.mode == ModeTTY && !opts.NoColor
	p.human = stdout
	if p.mode == ModeJSON {
		p.human = stderr
	}
	if p.mode != ModeTTY {
		p.human = asciiWriter{p.human}
		p.errw = asciiWriter{p.errw}
	}
	return p
}

// SetCommand records the command name for the --json envelope.
func (p *Printer) SetCommand(name string) { p.command = name }

// Mode returns tty, plain or json.
func (p *Printer) Mode() string { return p.mode }

// JSON reports whether --json is active.
func (p *Printer) JSON() bool { return p.mode == ModeJSON }

// Interactive reports whether prompts and live progress are allowed.
func (p *Printer) Interactive() bool { return p.mode == ModeTTY }

// Human returns the writer for human-readable output.
func (p *Printer) Human() io.Writer { return p.human }

// Println writes one line of human output unless --quiet.
func (p *Printer) Println(format string, args ...any) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.human, format+"\n", args...)
}

// Print writes human output unless --quiet.
func (p *Printer) Print(s string) {
	if p.quiet {
		return
	}
	fmt.Fprint(p.human, s)
}

// Always writes a line of human output even with --quiet.
func (p *Printer) Always(format string, args ...any) {
	fmt.Fprintf(p.human, format+"\n", args...)
}

// Debugf writes a line to stderr with --verbose.
func (p *Printer) Debugf(format string, args ...any) {
	if p.verbose {
		fmt.Fprintf(p.errw, "debug: "+format+"\n", args...)
	}
}

// Warn records a warning for the envelope and prints it to stderr.
func (p *Printer) Warn(code, message string) {
	for _, w := range p.warnings {
		if w.Code == code && w.Message == message {
			return
		}
	}
	p.warnings = append(p.warnings, Warning{Code: code, Message: message})
	fmt.Fprintf(p.errw, "%s %s\n", p.Mark(MarkWarn), message)
}

// Warnings returns the recorded warnings.
func (p *Printer) Warnings() []Warning { return p.warnings }

// Marks used in human output.
const (
	MarkOK   = "ok"
	MarkSkip = "skip"
	MarkFail = "fail"
	MarkWarn = "warn"
	MarkInfo = "info"
)

// Mark returns the symbol for a status, colored on a terminal.
func (p *Printer) Mark(kind string) string {
	sym, col := "•", ""
	switch kind {
	case MarkOK:
		sym, col = "✓", "32"
	case MarkSkip:
		sym, col = "–", "2"
	case MarkFail:
		sym, col = "✗", "31"
	case MarkWarn:
		sym, col = "!", "33"
	}
	if p.mode != ModeTTY {
		sym = asciiReplacer.Replace(sym)
	}
	return p.paint(col, sym)
}

// Bold renders s in bold on a color terminal.
func (p *Printer) Bold(s string) string { return p.paint("1", s) }

// Dim renders s dimmed on a color terminal.
func (p *Printer) Dim(s string) string { return p.paint("2", s) }

func (p *Printer) paint(code, s string) string {
	if !p.color || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Table writes aligned rows of human output.
func (p *Printer) Table(indent string, rows [][]string) {
	if p.quiet || len(rows) == 0 {
		return
	}
	tw := tabwriter.NewWriter(p.human, 0, 2, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, indent+strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

// Result prints the success envelope in --json mode; it is a no-op otherwise.
func (p *Printer) Result(data any) error {
	if p.mode != ModeJSON || p.emitted {
		return nil
	}
	p.emitted = true
	return p.writeEnvelope(Envelope{OK: true, Data: data})
}

// Failure prints the failure envelope in --json mode; it is a no-op otherwise.
func (p *Printer) Failure(info ErrorInfo, data any) error {
	if p.mode != ModeJSON || p.emitted {
		return nil
	}
	p.emitted = true
	return p.writeEnvelope(Envelope{OK: false, Data: data, Error: &info})
}

func (p *Printer) writeEnvelope(env Envelope) error {
	env.Command = p.command
	env.Version = version.String()
	env.Warnings = p.warnings
	if env.Warnings == nil {
		env.Warnings = []Warning{}
	}
	enc := json.NewEncoder(p.stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// Error prints a human error line to stderr.
func (p *Printer) Error(msg string) {
	fmt.Fprintln(p.errw, "gravity: "+msg)
}

var asciiReplacer = strings.NewReplacer(
	"✓", "ok", "–", "-", "—", "-", "✗", "x", "›", ">", "·", "-", "…", "...",
	"←", "<-", "→", "->", "•", "*", "“", `"`, "”", `"`, "‘", "'", "’", "'",
	"↓", "v", "↳", "+", "▍", "#", "●", "*",
)

type asciiWriter struct{ w io.Writer }

func (a asciiWriter) Write(b []byte) (int, error) {
	s := asciiReplacer.Replace(string(b))
	if _, err := io.WriteString(a.w, s); err != nil {
		return 0, err
	}
	return len(b), nil
}
