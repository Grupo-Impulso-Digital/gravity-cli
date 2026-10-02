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

// NoChanges is the run summary when a run changed no documentation.
const NoChanges = "No documentation changes from this run."

// Handoff is a unit role moving between repositories.
type Handoff struct {
	UnitKey string `json:"unitKey"`
	Role    string `json:"role"`
	From    string `json:"from"`
	To      string `json:"to"`
	Status  string `json:"status"`
}

// Competing is a page another run is changing at the same time.
type Competing struct {
	Pass    string   `json:"pass"`
	Page    string   `json:"page"`
	Repos   []string `json:"repos,omitempty"`
	Runs    []string `json:"runs,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Pending bool     `json:"pending,omitempty"`
}

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

// Doc is everything the doc-impact report (pull requests) or run summary (other triggers) shows.
type Doc struct {
	Repo      string      `json:"repo"`
	PR        int         `json:"pr,omitempty"`
	Heading   string      `json:"heading,omitempty"`
	Applied   bool        `json:"applied,omitempty"`
	RunURL    string      `json:"runUrl,omitempty"`
	BundleURL string      `json:"bundleUrl,omitempty"`
	Survey    bool        `json:"survey,omitempty"`
	Passes    []Pass      `json:"passes"`
	Handoffs  []Handoff   `json:"handoffs,omitempty"`
	Competing []Competing `json:"competing,omitempty"`
}

// Marker is the hidden line that identifies this repository's comment on a pull request.
func Marker(repo string) string {
	return "<!-- gravity:doc-impact repo=" + repo + " -->"
}

// HasImpact reports whether any pass would change documentation or found something, or ownership moved.
func (d Doc) HasImpact() bool {
	if len(d.Handoffs) > 0 || len(d.Competing) > 0 {
		return true
	}
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

func impactCell(p Pass, applied bool) string {
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
		switch {
		case applied && len(p.Impact) == 1:
			noun = "page changed"
		case applied:
			noun = "pages changed"
		case len(p.Impact) == 1:
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
	if d.Heading != "" {
		title = "### " + d.Heading
	}
	b.WriteString(title + "\n\n")
	if !d.HasImpact() {
		if d.Applied || d.Heading != "" {
			b.WriteString(NoChanges + "\n")
		} else {
			b.WriteString(NoImpact + "\n")
		}
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
		fmt.Fprintf(&b, "| %s | %s | %s |\n", escape(p.Name), escape(firstOf(p.Target, "-")), impactCell(p, d.Applied))
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
	writeCompeting(&b, d.Competing)
	writeHandoffs(&b, d.Handoffs)
	switch {
	case d.BundleURL != "":
		fmt.Fprintf(&b, "\n[Review the bundle in Gravity](%s)\n", d.BundleURL)
	case d.RunURL != "":
		fmt.Fprintf(&b, "\n[Open run in Gravity](%s)\n", d.RunURL)
	}
	return b.String()
}

func writeCompeting(b *strings.Builder, list []Competing) {
	if len(list) == 0 {
		return
	}
	var parts []string
	for _, c := range list {
		who := strings.Join(c.Repos, ", ")
		if who == "" {
			who = "run " + strings.Join(c.Runs, ", ")
		}
		if c.Pending {
			parts = append(parts, fmt.Sprintf("%s already has an open change from %s", escape(c.Page), who))
			continue
		}
		s := fmt.Sprintf("%s also changed by %s", escape(c.Page), who)
		if len(c.Keys) > 0 {
			s += " (" + plural(len(c.Keys), "block", "blocks") + ")"
		}
		parts = append(parts, s)
	}
	fmt.Fprintf(b, "\n**Competing changes**: %s. The review shows both versions side by side.\n", strings.Join(parts, "; "))
}

func writeHandoffs(b *strings.Builder, list []Handoff) {
	if len(list) == 0 {
		return
	}
	var parts []string
	for _, h := range list {
		when := "detected"
		if h.Status == "expected" {
			when = "once merged"
		}
		parts = append(parts, fmt.Sprintf("`%s` %s moves from %s to %s (%s)", h.UnitKey, h.Role, h.From, h.To, when))
	}
	fmt.Fprintf(b, "\n**Handoffs**: %s.\n", strings.Join(parts, "; "))
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
