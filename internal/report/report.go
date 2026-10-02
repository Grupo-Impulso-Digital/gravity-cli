// Package report renders run results for pull requests: the doc-impact comment, CI step summaries, annotations and page diffs.
package report

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// NoImpact is the comment body when no pass has documentation impact.
const NoImpact = "No documentation impact from this pull request."

// Pass is one row of the doc-impact report.
type Pass struct {
	Name       string               `json:"name"`
	Kind       string               `json:"kind"`
	Target     string               `json:"target"`
	Status     string               `json:"status"`
	SkipReason string               `json:"skipReason,omitempty"`
	Summary    string               `json:"summary,omitempty"`
	Error      string               `json:"error,omitempty"`
	Impact     []api.Impact         `json:"impact,omitempty"`
	Findings   []api.Finding        `json:"findings,omitempty"`
	Notes      []api.Note           `json:"notes,omitempty"`
	Claims     []agent.ClaimFinding `json:"claims,omitempty"`
}

// Doc is everything the doc-impact report shows.
type Doc struct {
	Repo   string `json:"repo"`
	PR     int    `json:"pr,omitempty"`
	RunURL string `json:"runUrl,omitempty"`
	Survey bool   `json:"survey,omitempty"`
	Passes []Pass `json:"passes"`
}

// Marker is the hidden line that identifies this repository's comment on a pull request.
func Marker(repo string) string {
	return "<!-- gravity:doc-impact repo=" + repo + " -->"
}

// HasImpact reports whether any pass would change documentation or found something.
func (d Doc) HasImpact() bool {
	for _, p := range d.Passes {
		if len(p.Impact) > 0 || len(p.Findings) > 0 {
			return true
		}
	}
	return false
}

// Findings returns every finding of the report.
func (d Doc) Findings() []api.Finding {
	var out []api.Finding
	for _, p := range d.Passes {
		out = append(out, p.Findings...)
	}
	return out
}

func impactCell(p Pass) string {
	switch {
	case p.Status == api.StatusSkipped:
		return "skipped: " + strings.ReplaceAll(p.SkipReason, "_", " ")
	case p.Status == api.StatusFailed:
		return "failed: " + escape(p.Error)
	case len(p.Impact) == 0 && len(p.Findings) == 0:
		if p.Kind == "check" {
			return "no findings"
		}
		return "no impact"
	}
	var parts []string
	if len(p.Impact) > 0 {
		var pages []string
		for i, im := range p.Impact {
			if i == 5 {
				pages = append(pages, fmt.Sprintf("… %d more", len(p.Impact)-i))
				break
			}
			name := firstOf(im.Page.Slug, im.Page.Title, im.Page.ID)
			action := im.Action
			if action == api.OpCreate {
				action = "new"
			}
			pages = append(pages, fmt.Sprintf("%s (%s)", escape(name), action))
		}
		noun := "pages would change"
		if len(p.Impact) == 1 {
			noun = "page would change"
		}
		parts = append(parts, fmt.Sprintf("%d %s: %s", len(p.Impact), noun, strings.Join(pages, ", ")))
	}
	if n := len(p.Findings); n > 0 {
		parts = append(parts, plural(n, "finding", "findings"))
	}
	return strings.Join(parts, "; ")
}

// Markdown renders the doc-impact report (§11.7), starting with the repository marker.
func Markdown(d Doc) string {
	var b strings.Builder
	b.WriteString(Marker(d.Repo) + "\n")
	title := "### Gravity · doc impact"
	if d.PR > 0 {
		title += fmt.Sprintf(" for #%d", d.PR)
	}
	b.WriteString(title + "\n\n")
	if !d.HasImpact() {
		b.WriteString(NoImpact + "\n")
		if d.RunURL != "" {
			fmt.Fprintf(&b, "\n[Open run in Gravity](%s)\n", d.RunURL)
		}
		return b.String()
	}
	if d.Survey {
		b.WriteString("_Surveyed recent history: no earlier run to compare with._\n\n")
	}
	b.WriteString("| Pass | Target | Impact |\n| --- | --- | --- |\n")
	for _, p := range d.Passes {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", escape(p.Name), escape(firstOf(p.Target, "-")), impactCell(p))
	}
	findings := d.Findings()
	if len(findings) > 0 {
		var titles []string
		for i, f := range findings {
			if i == 3 {
				titles = append(titles, fmt.Sprintf("and %d more", len(findings)-i))
				break
			}
			t := f.Title
			if f.Verdict == api.VerdictContradicted {
				t += " (contradicted here)"
			}
			titles = append(titles, t)
		}
		fmt.Fprintf(&b, "\n**%s**: %s.\n", plural(len(findings), "finding", "findings"), strings.Join(titles, "; "))
	}
	elsewhere := map[string]bool{}
	nElsewhere, nNotes := 0, 0
	for _, p := range d.Passes {
		for _, c := range p.Claims {
			if c.Verdict == api.VerdictTrueElsewhere {
				nElsewhere++
				if c.Repo != "" {
					elsewhere[c.Repo] = true
				}
			}
		}
		nNotes += len(p.Notes)
	}
	var extra []string
	if nElsewhere > 0 {
		repos := make([]string, 0, len(elsewhere))
		for r := range elsewhere {
			repos = append(repos, r)
		}
		sort.Strings(repos)
		s := plural(nElsewhere, "claim", "claims") + " true elsewhere"
		if len(repos) > 0 {
			s += " (" + strings.Join(repos, ", ") + ")"
		}
		extra = append(extra, s)
	}
	if nNotes > 0 {
		extra = append(extra, plural(nNotes, "note", "notes"))
	}
	if len(extra) > 0 {
		b.WriteString(strings.Join(extra, " · ") + ".\n")
	}
	if d.RunURL != "" {
		fmt.Fprintf(&b, "\n[Open run in Gravity](%s)\n", d.RunURL)
	}
	return b.String()
}

// AppendFile appends a report to a step-summary file.
func AppendFile(path, body string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(body + "\n"); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// GitHubAnnotations renders findings as GitHub workflow commands.
func GitHubAnnotations(findings []api.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		level := "error"
		switch f.Severity {
		case api.SeverityWarning:
			level = "warning"
		case api.SeverityInfo:
			level = "notice"
		}
		var props []string
		if f.File != "" {
			props = append(props, "file="+propEscape(f.File))
			if f.Line > 0 {
				props = append(props, fmt.Sprintf("line=%d", f.Line))
			}
		}
		props = append(props, "title="+propEscape("Gravity: "+f.Title))
		msg := firstOf(f.Detail, f.Title)
		if f.Page != nil {
			msg += " (page " + firstOf(f.Page.Slug, f.Page.ID) + ")"
		}
		out = append(out, fmt.Sprintf("::%s %s::%s", level, strings.Join(props, ","), dataEscape(msg)))
	}
	return out
}

func dataEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func propEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

func escape(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
