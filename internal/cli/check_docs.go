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
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

func newCheckDocsCmd(gf *globalFlags) *cobra.Command {
	var (
		site   string
		from   string
		to     string
		useAI  bool
		ci     bool
		format string
	)
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Check docs completeness against code",
		Long: `Pull published page snapshots for the site and verify machine/hybrid blocks
whose source binding has a hash and a repo-resident ref by recomputing sha256,
flagging stale mismatches. Blocks without a verifiable binding are skipped and
counted.

With --ai, additionally run the docs-gap agent over the diff(from..to) plus a
digest of current docs; it reports gaps via report_findings. Deterministic and
AI findings are merged.

Exit codes: 0 no findings, 1 findings, 2 error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			pages, err := e.client.Pages(cmd.Context(), siteSlug, "")
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch pages: %w", err))
			}

			res := verifyDocsBindings(repo.Root, pages, siteSlug, mappedRefs(e.proj))

			if useAI {
				rng, err := repo.ResolveRange(cmd.Context(), from, to)
				if err != nil {
					return Fail(CodeError, fmt.Errorf("resolve range: %w", err))
				}
				aiFindings, err := runDocsGapAgent(cmd.Context(), e.client, repo, rng, pages, logWriter(cmd, ci), &api.MessagesContext{Site: siteSlug, Space: e.cfg.Space, Namespace: e.cfg.Namespace})
				if err != nil {
					return Fail(CodeError, err)
				}
				res.Findings = append(res.Findings, aiFindings...)
				res.Notes = append(res.Notes, fmt.Sprintf("AI gap pass over %s produced %d finding(s)", rng.String(), len(aiFindings)))
			}

			if err := output.Render(cmd.OutOrStdout(), res, format); err != nil {
				return Fail(CodeError, err)
			}
			if len(res.Findings) > 0 {
				return Fail(CodeFindings, nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&from, "from", "", "start git ref for the --ai diff (default: latest tag/first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref for the --ai diff (default: HEAD)")
	cmd.Flags().BoolVar(&useAI, "ai", false, "run the AI docs-gap pass")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().StringVar(&format, "format", output.FormatText, "text|json|github")
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

func verifyDocsBindings(repoRoot string, pages []api.Page, siteSlug string, mapped map[string]bool) output.Result {
	res := output.Result{Command: "check docs", Site: siteSlug}
	verified := 0
	type staleKey struct{ page, ref string }
	staleBlocks := map[staleKey]int{}
	staleChecks := map[staleKey]checks.BindingCheck{}
	staleTypes := map[staleKey]string{}
	for _, p := range pages {
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
				k := staleKey{page: p.Slug, ref: check.Ref}
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
	return res
}

func runDocsGapAgent(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, pages []api.Page, logw io.Writer, mctx *api.MessagesContext) ([]output.Finding, error) {
	tools := append(agent.GitTools(repo), agent.ReportFindingsTool())
	runner := &agent.Runner{
		Client:  client,
		System:  resolvePrompt(ctx, client, prompts.NameDocsGap, logw),
		Tools:   tools,
		Context: mctx,
		Log:     logw,
	}
	digest := docsDigest(pages)
	kickoff := fmt.Sprintf(
		"Review the code changes between %s and %s for documentation gaps. "+
			"Use git_diff(from=%q, to=%q) to see what changed. "+
			"Here is a digest of the currently published docs:\n\n%s\n\n"+
			"Identify genuine gaps where code changed but docs did not. Call report_findings when done.",
		rng.From, rng.To, rng.From, rng.To, digest,
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

func docsDigest(pages []api.Page) string {
	var b strings.Builder
	if len(pages) == 0 {
		return "(no published pages)"
	}
	for _, p := range pages {
		fmt.Fprintf(&b, "- page %q (slug: %s, space: %s)\n", p.Title, p.Slug, p.SpaceSlug)
		types := map[string]int{}
		for _, blk := range p.Blocks {
			types[blk.Type]++
		}
		if len(types) > 0 {
			names := make([]string, 0, len(types))
			for t := range types {
				names = append(names, t)
			}
			sort.Strings(names)
			parts := make([]string, 0, len(names))
			for _, t := range names {
				parts = append(parts, fmt.Sprintf("%s×%d", t, types[t]))
			}
			fmt.Fprintf(&b, "    blocks: %s\n", strings.Join(parts, ", "))
		}
	}
	return b.String()
}
