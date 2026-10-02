package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

type previewPage struct {
	Pass  string `json:"pass"`
	Op    string `json:"op"`
	Page  string `json:"page"`
	Title string `json:"title,omitempty"`
	Diff  string `json:"diff,omitempty"`
	Note  string `json:"note,omitempty"`
}

type previewData struct {
	*engine.Result
	Pages        []previewPage                     `json:"pages"`
	Instructions map[string][]api.InstructionLayer `json:"instructions"`
	CostUSD      float64                           `json:"costUsd"`
}

func newPreviewCmd(a *app) *cobra.Command {
	var f pipelineFlags
	var committed, open bool
	var format string
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Show what every pass would write for your working tree, without writing",
		Long:  "Run every pass as a dry run over your uncommitted changes (or HEAD with --committed): the pages each pass would change, as a diff, the instructions the app composes for each pass, and what it cost.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch format {
			case "text", "diff", "json":
			default:
				return Failf(CodeError, "--format %q must be text, diff or json", format)
			}
			ctx := cmd.Context()
			s, err := a.pipelineSession(ctx, modePreview, f, committed)
			if err != nil {
				return err
			}
			res, err := engine.Execute(ctx, a.runEnv(s), s.opts)
			if err != nil {
				if res != nil && res.Plan != nil {
					a.printRun(res, s.info)
				}
				return runError(err)
			}
			data := previewData{Result: res, Instructions: map[string][]api.InstructionLayer{}, Pages: []previewPage{}}
			for _, p := range res.Passes {
				data.CostUSD += p.CostUSD
			}
			if res.Plan != nil {
				for _, pp := range res.Plan.Passes {
					if pp.Applies && len(pp.Instructions.Layers) > 0 {
						data.Instructions[pp.Name] = pp.Instructions.Layers
					}
				}
			}
			data.Pages = a.previewPages(ctx, s, res, format != "text")
			if format != "json" || !a.ui.JSON() {
				a.printPreview(res, s.info, data, format)
			}
			if open && a.openBrowser != nil && res.Plan != nil {
				for _, pp := range res.Plan.Passes {
					if pp.Applies && pp.Target.ViewerURL != "" {
						_ = a.openBrowser(pp.Target.ViewerURL)
					}
				}
			}
			if code := res.ExitCode(false); code == CodeError || code == CodeLicense {
				return a.finishPipeline(res, false, nil)
			}
			return a.ui.Result(data)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&f.passes, "pass", nil, "preview only these passes")
	fl.StringVar(&f.from, "from", "", "start of the range (default HEAD, diffing the working tree)")
	fl.StringVar(&f.to, "to", "", "end of the range")
	fl.BoolVar(&committed, "committed", false, "preview HEAD instead of the working tree")
	fl.StringVar(&f.note, "note", "", "a note for the AI")
	fl.StringVar(&format, "format", "text", "text, diff or json")
	fl.BoolVar(&open, "open", false, "open the target pages")
	return cmd
}

func spaceOf(res *engine.Result, pass string) string {
	if res.Plan == nil {
		return ""
	}
	for _, pp := range res.Plan.Passes {
		if pp.Name == pass && pp.Target.Space != nil {
			return pp.Target.Space.ID
		}
	}
	return ""
}

func (a *app) previewPages(ctx context.Context, s *pipelineSession, res *engine.Result, withDiff bool) []previewPage {
	out := []previewPage{}
	for _, p := range res.Passes {
		if p.Report == nil || p.Report.Recorded == nil {
			continue
		}
		rec := p.Report.Recorded
		for _, req := range rec.Changes {
			pg := previewPage{Pass: p.Name, Op: req.Op, Page: firstNonEmpty(req.Target.Slug, req.Target.PageID), Title: req.Title}
			var current *api.PageContent
			if req.Target.PageID != "" {
				c, err := s.client.Page(ctx, req.Target.PageID, api.PageQuery{State: "draft", Format: "json"})
				if err == nil {
					current = c
					pg.Page, pg.Title = c.Page.Slug, firstNonEmpty(pg.Title, c.Page.Title)
				}
			}
			if withDiff {
				pg.Diff = report.ChangeDiff(current, req)
			}
			pg.Note = req.Summary
			out = append(out, pg)
		}
		for _, req := range rec.Verbatim {
			pg := previewPage{Pass: p.Name, Op: api.OpImport, Page: req.Page.Slug, Title: req.Page.Title, Note: req.File.Path}
			if withDiff {
				var current *api.PageContent
				if space := spaceOf(res, p.Name); space != "" {
					if c, err := s.client.PageBySlug(ctx, space, req.Page.Slug, api.PageQuery{State: "draft", Format: "json"}); err == nil {
						current = c
					}
				}
				pg.Diff = report.VerbatimDiff(current, req.Blocks)
			}
			out = append(out, pg)
		}
		for _, d := range rec.Deletions {
			out = append(out, previewPage{Pass: p.Name, Op: api.OpDelete, Page: d.Path, Note: d.Reason})
		}
		for _, m := range rec.Memories {
			out = append(out, previewPage{Pass: p.Name, Op: "memory", Page: m.Title, Note: m.Body})
		}
		for _, h := range rec.Hints {
			out = append(out, previewPage{Pass: p.Name, Op: "hint", Page: firstNonEmpty(h.UnitKey, h.PageID), Note: h.Claim})
		}
	}
	return out
}

func (a *app) printPreview(res *engine.Result, info *repoInfo, data previewData, format string) {
	a.printRun(res, info)
	p := a.ui
	if len(data.Pages) == 0 {
		p.Println("No page would change.")
	}
	for _, pg := range data.Pages {
		title := pg.Page
		if pg.Title != "" && pg.Title != pg.Page {
			title += " (" + pg.Title + ")"
		}
		p.Println("%s %s %s: %s", p.Bold(pg.Op), pg.Pass, title, pg.Note)
		if format == "diff" && pg.Diff != "" {
			for _, line := range strings.Split(strings.TrimRight(pg.Diff, "\n"), "\n") {
				p.Println("    %s", line)
			}
		}
	}
	if len(data.Instructions) > 0 && format != "diff" {
		p.Println("%s", p.Bold("Instructions"))
		for _, name := range sortedKeys(keysOf(data.Instructions)) {
			p.Println("  %s", name)
			for i, l := range data.Instructions[name] {
				p.Println("    %d. %s: %s", i+1, l.Label, oneLine(l.Text, 100))
			}
		}
	}
	p.Println("Cost: $%.2f", data.CostUSD)
}

func keysOf(m map[string][]api.InstructionLayer) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
