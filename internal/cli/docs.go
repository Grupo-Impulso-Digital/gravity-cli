package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/agent"
	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/docs"
	"github.com/impulso/gravity-cli/internal/git"
	"github.com/impulso/gravity-cli/internal/prompts"
)

func newDocsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation from the codebase (preview)",
		Long: `Scan the repository with the AI harness and author documentation pages for
three audiences — public, users, and developers — as draft proposals on the
Gravity platform.

This is a preview: it gates on platform support and, until the server features
are live, reports unavailability and exits 0 (pass --require to fail instead).`,
	}
	cmd.AddCommand(newDocsGenerateCmd(gf))
	return cmd
}

func newDocsGenerateCmd(gf *globalFlags) *cobra.Command {
	var (
		site      string
		space     string
		audiences []string
		pageSlug  string
		output    string
		dryRun    bool
		ci        bool
		require   bool
		from      string
		save      string
	)
	cmd := &cobra.Command{
		Use:     "generate",
		Aliases: []string{"init"},
		Short:   "Author multi-audience docs from the codebase",
		Long: `Two-phase AI authoring: first plan the pages, then author each page's blocks
for the public/users/developers audiences. Existing pages are read first so a
re-run updates blocks in place instead of duplicating them. Every write is a
draft + open proposal — nothing is published directly.

Audience is a per-block attribute: one page can carry blocks for different
audiences, and the platform renders the blocks matching the viewer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|stdout)", output)
			}
			if err := docs.ValidAudiences(audiences); err != nil {
				return Failf(CodeError, "%v", err)
			}
			if len(audiences) == 0 {
				return Failf(CodeError, "no audiences selected")
			}
			if site != "" {
				gf.site = site
			}
			e, err := resolveEnv(*gf, space)
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

			// Replay: sync a previously authored (and saved) set with no AI cost.
			// The AI phases already ran; only the sync — the one late-failing step —
			// needs retrying, so skip planning/authoring entirely.
			if from != "" {
				targets, err := loadTargets(from)
				if err != nil {
					return Fail(CodeError, fmt.Errorf("load %s: %w", from, err))
				}
				return finishDocs(cmd, e, siteSlug, targets, output, dryRun, ci, from)
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, err)
			}
			logw := logWriter(cmd, ci)

			// Preview gate plus block-audience degradation, from one whoami call.
			who, err := e.client.WhoAmI(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("whoami: %w", err))
			}
			if !who.Features[featureDocsGenerate] {
				if require {
					return Failf(CodeError, "docs generation is not yet available on this platform")
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "note: docs generation is not yet available on this platform; skipping")
				return nil
			}
			audienceOK := who.Features[featureBlockAudience]
			if !audienceOK {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: block-level audiences not yet supported by this platform; blocks will render to everyone")
			}

			mctx := &api.MessagesContext{Site: siteSlug, Space: e.cfg.Space, Namespace: e.cfg.Namespace}
			planPrompt := resolvePrompt(cmd.Context(), e.client, prompts.NameDocsPlan, logw)
			authorPrompt := resolvePrompt(cmd.Context(), e.client, prompts.NameDocsAuthor, logw)

			plan, err := runDocsPlan(cmd.Context(), e.client, repo, planPrompt, audiences, logw, mctx)
			if err != nil {
				return Fail(CodeError, err)
			}

			existing, err := e.client.Pages(cmd.Context(), siteSlug, "")
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch pages: %w", err))
			}
			byPage := make(map[string][]api.ContentBlock, len(existing))
			for _, p := range existing {
				byPage[p.SpaceSlug+"\x00"+p.Slug] = p.Blocks
			}

			generator := "gravity docs generate v" + version
			var targets []syncTarget
			for _, pg := range plan.Pages {
				if pageSlug != "" && pg.Slug != pageSlug {
					continue
				}
				aud := pg.Audiences
				if len(aud) == 0 {
					aud = audiences
				} else {
					aud = intersect(aud, audiences)
				}
				if len(aud) == 0 {
					continue
				}
				pg.Audiences = aud
				sp := firstNonEmpty(space, pg.Space, e.cfg.Space, "docs")

				authored, stopped, err := runDocsAuthor(cmd.Context(), e.client, repo, authorPrompt, pg, byPage[sp+"\x00"+pg.Slug], logw, mctx)
				if err != nil {
					return Fail(CodeError, err)
				}
				if stopped {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: skipped page %q (author run hit a cap before completing)\n", pg.Slug)
					continue
				}
				blocks, err := docs.AssembleBlocks(repo.Root, generator, toAuthored(authored))
				if err != nil {
					return Fail(CodeError, fmt.Errorf("page %s: %w", pg.Slug, err))
				}
				if !audienceOK {
					for i := range blocks {
						blocks[i].Audiences = nil
					}
				}
				title := firstNonEmpty(authored.Title, pg.Title, pg.Slug)
				targets = append(targets, syncTarget{
					kind:  "page",
					space: sp,
					label: fmt.Sprintf("docs %s -> %s/%s", strings.Join(pg.Audiences, "+"), sp, pg.Slug),
					page:  api.PageUpsertRequest{SpaceSlug: sp, Slug: pg.Slug, Title: title, Blocks: blocks},
				})
			}

			artifact := save
			if artifact == "" {
				artifact = defaultDocsArtifact(repo.Root)
			}
			return finishDocs(cmd, e, siteSlug, targets, output, dryRun, ci, artifact)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&space, "space", "", "override the target space for all pages")
	cmd.Flags().StringSliceVar(&audiences, "audiences",
		[]string{api.AudiencePublic, api.AudienceUsers, api.AudienceDevelopers},
		"audiences to author: public,users,developers")
	cmd.Flags().StringVar(&pageSlug, "page", "", "author only the planned page with this slug")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without calling the API")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable docs generation as a hard error (exit 2)")
	cmd.Flags().StringVar(&from, "from", "", "replay a saved doc set through sync, skipping the AI phases (use the file printed by a prior run)")
	cmd.Flags().StringVar(&save, "save", "", "path to persist the authored doc set for replay (default .gravity/generated/docs.json)")
	return cmd
}

// defaultDocsArtifact is where `docs generate` persists its authored set for
// replay: a stable per-repo path so a failed sync can be retried with --from
// without paying the AI cost again.
func defaultDocsArtifact(repoRoot string) string {
	return filepath.Join(repoRoot, ".gravity", "generated", "docs.json")
}

// finishDocs emits the authored targets: to stdout, as a dry-run, or by syncing.
// Before a real sync it persists the set to artifactPath so a run whose sync
// fails late — after all the AI tokens are already spent — can be replayed with
// `gravity docs generate --from <artifactPath>` at no further cost. On any sync
// failure it points the user back at that file. This is the durability guarantee:
// authored content is never lost to a late API error.
func finishDocs(cmd *cobra.Command, e *env, siteSlug string, targets []syncTarget, output string, dryRun, ci bool, artifactPath string) error {
	out := cmd.OutOrStdout()
	if len(targets) == 0 {
		fmt.Fprintln(out, "no pages authored")
		return nil
	}
	if output == outputStdout {
		printSyncStdout(out, targets)
		return nil
	}
	if dryRun {
		return printSyncDryRun(out, targets)
	}
	if artifactPath != "" {
		if err := saveTargets(artifactPath, targets); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save authored docs to %s (%v); proceeding to sync\n", artifactPath, err)
			artifactPath = ""
		} else {
			fmt.Fprintf(out, "Saved authored docs to %s\n", artifactPath)
		}
	}
	err := runSync(cmd.Context(), e.client, siteSlug, targets, true, logWriter(cmd, ci), out)
	if err != nil && artifactPath != "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: authored docs are saved at %s — fix the cause and replay with `gravity docs generate --from %s` (no AI re-run)\n",
			artifactPath, artifactPath)
	}
	return err
}

// runDocsPlan runs phase A: survey the repo and propose the page list.
func runDocsPlan(ctx context.Context, client *api.Client, repo *git.Repo, system string, audiences []string, logw io.Writer, mctx *api.MessagesContext) (agent.DocPlanInput, error) {
	tools := append(agent.GitTools(repo), agent.SubmitDocPlanTool())
	runner := &agent.Runner{
		Client:        client,
		System:        system,
		Tools:         tools,
		Context:       mctx,
		Log:           logw,
		MaxIterations: agent.DefaultAuthorMaxIterations,
	}
	kickoff := fmt.Sprintf(
		"Survey this repository and propose documentation pages covering these audiences: %s. "+
			"Start with list_files and read the README and main entrypoints, then call submit_doc_plan.",
		strings.Join(audiences, ", "),
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return agent.DocPlanInput{}, err
	}
	if res.TerminalTool != agent.ToolSubmitDocPlan {
		if res.Stopped {
			return agent.DocPlanInput{}, fmt.Errorf("docs plan did not finish: %s", res.StopReason)
		}
		return agent.DocPlanInput{}, errors.New("docs plan ended without calling submit_doc_plan")
	}
	return agent.ParseDocPlan(res.TerminalInput)
}

// runDocsAuthor runs phase B for one page. The bool return is true when the run
// hit a cap before submitting, so the caller skips the page rather than upserting
// a partial block set (which would propose deleting real blocks).
func runDocsAuthor(ctx context.Context, client *api.Client, repo *git.Repo, system string, page agent.DocPlanPage, existing []api.ContentBlock, logw io.Writer, mctx *api.MessagesContext) (agent.PageDocInput, bool, error) {
	tools := append(agent.GitTools(repo), agent.SubmitPageDocTool())
	runner := &agent.Runner{
		Client:        client,
		System:        system,
		Tools:         tools,
		Context:       mctx,
		Log:           logw,
		MaxTokens:     agent.DefaultAuthorMaxTokens,
		MaxIterations: agent.DefaultAuthorMaxIterations,
	}
	kickoff := fmt.Sprintf(
		"Author the documentation page %q (slug %q) for audiences: %s.\n"+
			"Intended contents: %s\n"+
			"Relevant source files: %s\n\n"+
			"Existing blocks on this page:\n%s\n\n"+
			"Read the relevant source, then call submit_page_doc with the complete block set.",
		page.Title, page.Slug, strings.Join(page.Audiences, ", "),
		firstNonEmpty(page.Summary, "(none given)"),
		firstNonEmpty(strings.Join(page.Sources, ", "), "(none given)"),
		pageBlockDigest(existing),
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return agent.PageDocInput{}, false, err
	}
	if res.TerminalTool != agent.ToolSubmitPageDoc {
		if res.Stopped {
			return agent.PageDocInput{}, true, nil
		}
		return agent.PageDocInput{}, false, errors.New("docs author ended without calling submit_page_doc")
	}
	parsed, err := agent.ParsePageDoc(res.TerminalInput)
	return parsed, false, err
}

// pageBlockDigest summarizes a page's existing blocks for the author run so the
// model reuses keys (update) or omits them (remove) instead of churning.
func pageBlockDigest(blocks []api.ContentBlock) string {
	if len(blocks) == 0 {
		return "(none — this is a new page)"
	}
	var b strings.Builder
	for _, blk := range blocks {
		aud := "everyone"
		if len(blk.Audiences) > 0 {
			aud = strings.Join(blk.Audiences, ",")
		}
		key := blk.Key
		if key == "" {
			key = "(server returned no key)"
		}
		fmt.Fprintf(&b, "- key=%s type=%s ownership=%s audiences=%s\n", key, blk.Type, blk.Ownership, aud)
	}
	return b.String()
}

func toAuthored(in agent.PageDocInput) []docs.AuthoredBlock {
	out := make([]docs.AuthoredBlock, 0, len(in.Blocks))
	for _, b := range in.Blocks {
		out = append(out, docs.AuthoredBlock{
			Key:       b.Key,
			Type:      b.Type,
			Ownership: b.Ownership,
			Audiences: b.Audiences,
			Content:   b.Content,
			Sources:   b.Sources,
		})
	}
	return out
}

// intersect returns the values in a that also appear in b, preserving a's order.
func intersect(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, x := range b {
		set[x] = true
	}
	var out []string
	for _, x := range a {
		if set[x] {
			out = append(out, x)
		}
	}
	return out
}
