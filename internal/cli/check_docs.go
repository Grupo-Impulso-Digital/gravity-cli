package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

func newCheckDocsCmd(gf *globalFlags) *cobra.Command {
	var (
		site    string
		space   string
		from    string
		to      string
		useAI   bool
		format  string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Check docs completeness against code",
		Long: `Pull published page snapshots for this repo's pages and verify machine/hybrid
blocks whose source binding has a hash and a repo-resident ref by recomputing
sha256, flagging stale mismatches. Blocks without a verifiable binding are
skipped and counted.

Only this repo's pages are checked: the pages the platform attributes to it, or,
before any attribution exists, the spaces .gravity.yaml declares (--space
overrides). The whole site is never checked by default.

With --ai, additionally run the docs-gap agent over the diff(from..to) plus the
text of this repo's pages; it reports gaps via report_findings. Deterministic
and AI findings are merged. An empty commit range skips the AI pass.

Exit codes: 0 no findings, 1 findings, 2 error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format = jsonFormat(format, jsonOut)
			if err := validateFormat(format); err != nil {
				return err
			}
			if site != "" {
				gf.site = site
			}
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			siteSlug, err := e.requireSite()
			if err != nil {
				return err
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, err)
			}

			all, err := e.client.Pages(cmd.Context(), siteSlug, "")
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch pages: %w", err))
			}

			myRemoteKey := ""
			if ref := repoRefFor(cmd.Context(), repo, e.proj); ref != nil {
				myRemoteKey = ref.RemoteKey
			}
			scope, err := resolveRepoScope(myRemoteKey, attributedTo(pageRemoteKeys(all), myRemoteKey), e.proj, space)
			if err != nil {
				return Fail(CodeError, err)
			}
			verifySet := all
			if scope.bySpaces() {
				verifySet = scope.pages(all)
			}
			mine := scope.pages(all)
			res := verifyDocsBindings(repo.Root, verifySet, siteSlug, mappedRefs(e.proj), myRemoteKey)
			res.Notes = append([]string{scopeNote(scope, len(mine), len(all), "page(s)")}, res.Notes...)

			if useAI {
				rng, err := repo.ResolveRange(cmd.Context(), from, to)
				if err != nil {
					return Fail(CodeError, fmt.Errorf("resolve range: %w", err))
				}
				commits, err := repo.Log(cmd.Context(), rng.From, rng.To, 1)
				if err != nil {
					return Fail(CodeError, err)
				}
				if len(commits) == 0 {
					res.Notes = append(res.Notes, fmt.Sprintf("AI gap pass skipped: no commits in range %s", rng.String()))
					return renderCheck(cmd, res, format)
				}
				aiFindings, err := runDocsGapAgent(cmd.Context(), e.client, repo, rng, mine, logWriter(cmd), &api.MessagesContext{Site: siteSlug, Space: e.cfg.Space, Namespace: e.cfg.Namespace})
				if err != nil {
					return Fail(CodeError, err)
				}
				res.Findings = append(res.Findings, aiFindings...)
				res.Notes = append(res.Notes, fmt.Sprintf("AI gap pass over %s produced %d finding(s)", rng.String(), len(aiFindings)))
			}

			return renderCheck(cmd, res, format)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&space, "space", "", "check only this space (default: this repo's pages)")
	cmd.Flags().StringVar(&from, "from", "", "start git ref for the --ai diff (default: latest tag/first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref for the --ai diff (default: HEAD)")
	cmd.Flags().BoolVar(&useAI, "ai", false, "run the AI docs-gap pass")
	cmd.Flags().StringVar(&format, "format", output.FormatText, "text|json|github")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "shorthand for --format json")
	return cmd
}

func mappedRefs(proj *config.Project) map[string]bool {
	if proj == nil {
		return nil
	}
	refs := make(map[string]bool, len(proj.Documents)+len(proj.Sources))
	for _, d := range proj.Documents {
		refs[d.File] = true
	}
	for _, s := range proj.Sources {
		refs[s.Source] = true
	}
	return refs
}

func verifyDocsBindings(repoRoot string, pages []api.Page, siteSlug string, mapped map[string]bool, myRemoteKey string) output.Result {
	res := output.Result{Command: "check docs", Site: siteSlug}
	verified := 0
	foreign := 0
	mineAttributed := attributedTo(pageRemoteKeys(pages), myRemoteKey)
	type staleKey struct{ page, ref string }
	staleBlocks := map[staleKey]int{}
	staleChecks := map[staleKey]checks.BindingCheck{}
	staleTypes := map[staleKey]string{}
	for _, p := range pages {
		if foreignPage(p, myRemoteKey, mineAttributed) {
			if hasBoundBlocks(p) {
				foreign++
			}
			continue
		}
		for _, blk := range p.Blocks {
			if blk.Ownership != "machine" && blk.Ownership != "hybrid" {
				continue
			}
			check := checks.VerifyBinding(repoRoot, blk.SourceBinding)
			if check.Skipped {
				res.Skipped++
				continue
			}
			verified++
			if check.Stale {
				k := staleKey{page: pageLabel(p), ref: check.Ref}
				staleBlocks[k]++
				staleChecks[k] = check
				staleTypes[k] = blk.Type
			}
		}
	}
	keys := make([]staleKey, 0, len(staleBlocks))
	for k := range staleBlocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].page != keys[j].page {
			return keys[i].page < keys[j].page
		}
		return keys[i].ref < keys[j].ref
	})
	for _, k := range keys {
		check := staleChecks[k]
		count := staleBlocks[k]
		if mapped[k.ref] {
			res.Notes = append(res.Notes, fmt.Sprintf(
				"stale but mapped: %s changed and page %q lags it (%d block(s)) — the next sync on the live branch refreshes it automatically",
				k.ref, k.page, count,
			))
			continue
		}
		res.Findings = append(res.Findings, output.Finding{
			Severity: output.SeverityError,
			Kind:     "stale",
			Title:    fmt.Sprintf("stale: %s block(s) on page %q (%d affected)", staleTypes[k], k.page, count),
			Detail: fmt.Sprintf("source %s changed; recorded hash %s but file hashes to %s",
				k.ref, shortHash(check.Want), shortHash(check.Got)),
			Location:      k.ref,
			SuggestedPage: k.page,
		})
	}
	res.Notes = append(res.Notes, fmt.Sprintf("verified %d machine/hybrid block(s), skipped %d without a verifiable binding", verified, res.Skipped))
	if foreign > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("skipped %d page(s) with bound blocks that %s did not write (another repo's, or unattributed)", foreign, myRemoteKey))
	}
	return res
}

func foreignPage(p api.Page, myRemoteKey string, mineAttributed bool) bool {
	if myRemoteKey == "" {
		return false
	}
	if mineAttributed {
		return !writtenByRepo(p, myRemoteKey)
	}
	return p.RepoRemoteKey != nil && *p.RepoRemoteKey != "" && *p.RepoRemoteKey != myRemoteKey
}

func hasBoundBlocks(p api.Page) bool {
	for _, blk := range p.Blocks {
		if (blk.Ownership == "machine" || blk.Ownership == "hybrid") && blk.SourceBinding != nil {
			return true
		}
	}
	return false
}

func pageLabel(p api.Page) string {
	if p.SpaceSlug == "" {
		return p.Slug
	}
	return p.SpaceSlug + "/" + p.Slug
}

func runDocsGapAgent(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, pages []api.Page, logw io.Writer, mctx *api.MessagesContext) ([]output.Finding, error) {
	tools := append(agent.GitToolsAt(repo, rng.To), agent.ReportFindingsTool())
	runner := &agent.Runner{
		Client:  client,
		System:  resolvePrompt(ctx, client, prompts.NameDocsGap, logw),
		Tools:   tools,
		Context: mctx,
		Log:     logw,
	}
	kickoff := fmt.Sprintf(
		"Review the code changes between %s and %s for documentation gaps. "+
			"Use git_diff(from=%q, to=%q) to see what changed. "+
			"Here is the current text of this repository's documentation pages:\n\n%s\n\n"+
			"Identify genuine gaps where code changed but docs did not. A page may describe behavior "+
			"that another repository of the product implements; that is not a gap. "+
			"Call report_findings when done.",
		rng.From, rng.To, rng.From, rng.To, docsDigest(pages),
	)
	if mctx != nil {
		kickoff = enrichKickoff(ctx, client, mctx.Namespace, "docs gaps "+rng.String(), kickoff, mctx)
	}
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return nil, err
	}
	if res.TerminalTool != agent.ToolReportFindings {
		if res.Stopped {
			return nil, fmt.Errorf("AI docs-gap pass did not finish: %s", res.StopReason)
		}
		return nil, errors.New("AI docs-gap pass ended without calling report_findings")
	}
	parsed, err := agent.ParseFindings(res.TerminalInput)
	if err != nil {
		return nil, fmt.Errorf("parse AI findings: %w", err)
	}
	var findings []output.Finding
	for _, f := range parsed.Findings {
		findings = append(findings, output.Finding{
			Severity:      output.NormalizeSeverity(f.Severity),
			Title:         f.Title,
			Detail:        f.Detail,
			SuggestedPage: f.SuggestedPage,
		})
	}
	return findings, nil
}

const (
	gapDigestPerPage = 4 * 1024
	gapDigestTotal   = 64 * 1024
)

func docsDigest(pages []api.Page) string {
	if len(pages) == 0 {
		return "(no published pages)"
	}
	var b strings.Builder
	for i, p := range pages {
		header := fmt.Sprintf("### page %q (space %s, slug %s)\n", p.Title, p.SpaceSlug, p.Slug)
		body := docs.PageDigest(p.Blocks, gapDigestPerPage)
		if strings.TrimSpace(body) == "" {
			body = "(no text blocks)\n"
		}
		if b.Len()+len(header)+len(body) > gapDigestTotal {
			fmt.Fprintf(&b, "\n[... %d more page(s) omitted to stay within the digest budget; titles: ", len(pages)-i)
			titles := make([]string, 0, len(pages)-i)
			for _, rest := range pages[i:] {
				titles = append(titles, rest.SpaceSlug+"/"+rest.Slug)
			}
			b.WriteString(docs.ClipText(strings.Join(titles, ", "), 4*1024))
			b.WriteString("]\n")
			return b.String()
		}
		b.WriteString(header)
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func renderCheck(cmd *cobra.Command, res output.Result, format string) error {
	out := cmd.OutOrStdout()
	if format != output.FormatText {
		out = rawWriter(out)
	}
	if err := output.Render(out, res, format); err != nil {
		return Fail(CodeError, err)
	}
	if len(res.Findings) > 0 {
		return Fail(CodeFindings, nil)
	}
	return nil
}
