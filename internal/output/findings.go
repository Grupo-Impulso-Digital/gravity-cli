// Package output renders findings and structured results in the formats CI systems consume.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Severity levels for findings, ordered most-to-least severe.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Finding is a single issue reported by a check or by the AI gap pass.
type Finding struct {
	Severity      string `json:"severity"`
	Kind          string `json:"kind,omitempty"`
	Title         string `json:"title"`
	Detail        string `json:"detail,omitempty"`
	Location      string `json:"location,omitempty"`
	SuggestedPage string `json:"suggestedPage,omitempty"`
}

// Result is the full output of a check command.
type Result struct {
	Command  string    `json:"command"`
	Site     string    `json:"site"`
	Findings []Finding `json:"findings"`
	Skipped  int       `json:"skipped"`
	Notes    []string  `json:"notes,omitempty"`
}

// Format identifiers.
const (
	FormatText   = "text"
	FormatJSON   = "json"
	FormatGitHub = "github"
)

// ValidFormat reports whether f is a recognized output format.
func ValidFormat(f string) bool {
	switch f {
	case FormatText, FormatJSON, FormatGitHub:
		return true
	}
	return false
}

// NormalizeSeverity maps AI severities (high/medium/low) onto the canonical error/warning/info scale.
func NormalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high", "critical", "error", "blocker":
		return SeverityError
	case "medium", "warning", "warn":
		return SeverityWarning
	case "low", "info", "minor", "nit", "":
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}

// Render writes the result to w in the requested format.
func Render(w io.Writer, r Result, format string) error {
	sorted := make([]Finding, len(r.Findings))
	copy(sorted, r.Findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := severityRank(sorted[i].Severity), severityRank(sorted[j].Severity); a != b {
			return a < b
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].Title < sorted[j].Title
	})
	r.Findings = sorted

	switch format {
	case FormatJSON:
		return renderJSON(w, r)
	case FormatGitHub:
		return renderGitHub(w, r)
	case FormatText, "":
		return renderText(w, r)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

func renderJSON(w io.Writer, r Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return enc.Encode(r)
}

func renderText(w io.Writer, r Result) error {
	var b strings.Builder
	if len(r.Findings) == 0 {
		fmt.Fprintf(&b, "%s: no findings", r.Command)
		if r.Site != "" {
			fmt.Fprintf(&b, " for site %q", r.Site)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "%s: %d finding(s)", r.Command, len(r.Findings))
		if r.Site != "" {
			fmt.Fprintf(&b, " for site %q", r.Site)
		}
		b.WriteString("\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "  [%s] %s\n", strings.ToUpper(f.Severity), f.Title)
			if f.Location != "" {
				fmt.Fprintf(&b, "        at: %s\n", f.Location)
			}
			if f.Detail != "" {
				fmt.Fprintf(&b, "        %s\n", indent(f.Detail))
			}
			if f.SuggestedPage != "" {
				fmt.Fprintf(&b, "        suggested page: %s\n", f.SuggestedPage)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	if r.Skipped > 0 {
		fmt.Fprintf(&b, "note: skipped %d block(s) without a verifiable source binding\n", r.Skipped)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func renderGitHub(w io.Writer, r Result) error {
	var b strings.Builder
	for _, f := range r.Findings {
		level := "notice"
		switch f.Severity {
		case SeverityError:
			level = "error"
		case SeverityWarning:
			level = "warning"
		}
		title := f.Title
		msg := f.Title
		if f.Detail != "" {
			msg = f.Title + " — " + f.Detail
		}
		if f.SuggestedPage != "" {
			msg += " (suggested page: " + f.SuggestedPage + ")"
		}
		props := "title=" + escapeProp(title)
		if f.Location != "" {
			props += ",file=" + escapeProp(f.Location)
		}
		fmt.Fprintf(&b, "::%s %s::%s\n", level, props, escapeData(msg))
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "::notice::%s\n", escapeData(n))
	}
	if r.Skipped > 0 {
		fmt.Fprintf(&b, "::notice::skipped %d block(s) without a verifiable source binding\n", r.Skipped)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n        ")
}

func escapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

func escapeProp(s string) string {
	s = escapeData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}
