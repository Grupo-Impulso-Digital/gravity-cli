package cli

import (
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

func ciMode(gf globalFlags) bool {
	if gf.ci {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CI"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func logWriter(cmd *cobra.Command) io.Writer {
	return cmd.ErrOrStderr()
}

var asciiReplacer = strings.NewReplacer(
	"└─ ", "`- ",
	"└ ", "`- ",
	"├─ ", "|- ",
	"│", "|",
	"←", "<-",
	"→", "->",
	"…", "...",
	"＋", "+",
	"✓", "ok",
	"✗", "x",
)

type plainOut struct {
	w io.Writer
}

func plainWriter(w io.Writer) io.Writer {
	if _, ok := w.(plainOut); ok {
		return w
	}
	return plainOut{w: w}
}

func rawWriter(w io.Writer) io.Writer {
	if p, ok := w.(plainOut); ok {
		return p.w
	}
	return w
}

func (p plainOut) Write(b []byte) (int, error) {
	if _, err := io.WriteString(p.w, toPlain(string(b))); err != nil {
		return 0, err
	}
	return len(b), nil
}

func toPlain(s string) string {
	s = asciiReplacer.Replace(s)
	return strings.Map(func(r rune) rune {
		if isEmoji(r) {
			return -1
		}
		return r
	}, s)
}

func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r == 0xFE0F || r == 0xFE0E || r == 0x200D:
		return true
	case r >= 0x1F1E6 && r <= 0x1F1FF:
		return true
	}
	return unicode.Is(unicode.So, r) && r > 0x2000 && r != '—'
}
