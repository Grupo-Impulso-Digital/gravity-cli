package eval

import (
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
)

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

// Markdown renders the report for people.
func Markdown(r *Report) string {
	var b strings.Builder
	title := r.Model
	if title == "" {
		title = "(configured model)"
	}
	fmt.Fprintf(&b, "# Model eval: %s\n\n", title)
	verdict := "PASS"
	switch {
	case r.Status == StatusFailed:
		verdict = "FAILED: " + r.Reason
	case !r.Pass:
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "Verdict: **%s**\n\n", verdict)

	b.WriteString("| Metric | Value |\n| ------ | ----- |\n")
	row := func(k, v string) { fmt.Fprintf(&b, "| %s | %s |\n", k, mdCell(v)) }
	row("Model", title)
	if r.Generation != nil {
		row("Served by", fmt.Sprintf("%s · %s · instructions %s", r.Generation.Model, r.Generation.Provider, short(r.Generation.InstructionsVersion)))
	}
	if r.MaxOutput > 0 {
		row("Max output", fmt.Sprintf("%d tokens", r.MaxOutput))
	}
	if r.RunID != "" {
		row("Run", r.RunID)
	}
	row("Pages", fmt.Sprintf("%d", len(r.Pages)))
	row("Checks passed", fmt.Sprintf("%d/%d (%.0f%%)", r.Checks.Passed, r.Checks.Total, r.Checks.PassRate*100))
	row("Public endpoints covered", fmt.Sprintf("%d/%d", r.Coverage.Covered, r.Coverage.Total))
	row("Covered in detail", fmt.Sprintf("%d/%d (≥ %d words)", r.Coverage.Detailed, r.Coverage.Total, r.Coverage.DetailWords))
	row("Words per endpoint section", fmt.Sprintf("min %d · median %d · mean %.1f", r.Words.Min, r.Words.Median, r.Words.Mean))
	row("Internal leaks", fmt.Sprintf("%d", len(r.Leaks)))
	row("Turns", fmt.Sprintf("%d", r.Turns))
	row("Tokens", fmt.Sprintf("%d (input %d, output %d)", r.TotalTokens, r.InputTokens, r.OutputTokens))
	row("Duration", fmt.Sprintf("%.1f s", r.DurationSeconds))
	if r.CostUSD > 0 {
		row("Cost", fmt.Sprintf("$%.4f", r.CostUSD))
	}

	if len(r.Failures) > 0 {
		b.WriteString("\n## Failures\n\n")
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	if len(r.Checks.ByCode) > 0 {
		b.WriteString("\n## Checks\n\n| Check | Passed |\n| ----- | ------ |\n")
		for _, code := range append(append([]string(nil), PageChecks...), agent.CheckCoverage) {
			if c, ok := r.Checks.ByCode[code]; ok {
				fmt.Fprintf(&b, "| %s | %d/%d |\n", code, c.Passed, c.Total)
			}
		}
	}

	if len(r.Coverage.Endpoints) > 0 {
		b.WriteString("\n## Endpoints\n\n| Endpoint | Covered | Words | Detailed |\n| -------- | ------- | ----- | -------- |\n")
		for _, e := range r.Coverage.Endpoints {
			fmt.Fprintf(&b, "| `%s` | %s | %d | %s |\n", e.Endpoint, yesNo(e.Covered), e.Words, yesNo(e.Detailed))
		}
	}

	if len(r.Leaks) > 0 {
		b.WriteString("\n## Internal leaks\n\n")
		for _, l := range r.Leaks {
			fmt.Fprintf(&b, "- `%s` on %s [%s]\n", l.Endpoint, l.Page, l.BlockKey)
		}
	}

	if len(r.Pages) > 0 {
		b.WriteString("\n## Pages\n\n| Page | Op | Blocks | Words | Headings | Code blocks |\n| ---- | -- | ------ | ----- | -------- | ----------- |\n")
		for _, p := range r.Pages {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d |\n", mdCell(p.Name()), p.Op, p.Blocks, p.Words, p.Headings, p.CodeBlocks)
		}
		for _, p := range r.Pages {
			if len(p.Findings) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n### Findings on %s\n\n", p.Name())
			for _, f := range p.Findings {
				fmt.Fprintf(&b, "- %s\n", f.String())
			}
		}
	}

	if len(r.Warnings) > 0 {
		b.WriteString("\n## Pass warnings\n\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}

func short(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
