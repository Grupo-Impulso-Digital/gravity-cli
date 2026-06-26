package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/config"
	"github.com/impulso/gravity-cli/internal/docs"
	"github.com/impulso/gravity-cli/internal/git"
)

// syncTarget is one resolved authoring action: either a page upsert or a
// release-notes upsert.
type syncTarget struct {
	kind    string // "page" | "release"
	space   string
	label   string
	page    api.PageUpsertRequest
	release api.ReleaseNotesRequest
}

func newSyncCmd(gf *globalFlags) *cobra.Command {
	var (
		only   string
		page   string
		output string
		dryRun bool
		ci     bool
		space  string
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Author docs to Gravity from .gravity.yaml (API blocks + Markdown)",
		Long: `Author the doc mappings declared in .gravity.yaml onto the Gravity platform:

  sources    OpenAPI/code files -> machine-owned, drift-locked api blocks
  documents  Markdown files     -> native blocks on a page, or a release

Machine blocks are bound to their source file by sha256, so they change only when
the code changes and are verifiable by ` + "`gravity check api`" + ` / ` + "`gravity check docs`" + `.
Every write creates a draft + open proposal — nothing is published directly.

Use --only to author just one kind, and --page to target a single mapping.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|stdout)", output)
			}
			switch only {
			case "", "api", "docs":
			default:
				return Failf(CodeError, "invalid --only %q (want api|docs)", only)
			}

			e, err := resolveEnv(*gf, space)
			if err != nil {
				return err
			}
			if e.proj == nil {
				return Failf(CodeError, "no %s found; run `gravity init` to create one", config.ProjectFileName)
			}
			if len(e.proj.Sources) == 0 && len(e.proj.Documents) == 0 {
				return Failf(CodeError, "%s declares no `sources` or `documents` to sync", config.ProjectFileName)
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, fmt.Errorf("sync must run inside a git repo: %w", err))
			}
			generator := "gravity sync v" + version

			targets, err := buildSyncTargets(e.proj, repo.Root, generator, only, page)
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to sync (no matching mappings)")
				return nil
			}

			out := cmd.OutOrStdout()
			if output == outputStdout {
				printSyncStdout(out, targets)
				return nil
			}
			if dryRun {
				return printSyncDryRun(out, targets)
			}

			if err := e.requireAuth(); err != nil {
				return err
			}
			site, err := e.requireSite()
			if err != nil {
				return err
			}
			return runSync(cmd.Context(), e.client, site, targets, logWriter(cmd, ci), out)
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "author only one kind: api|docs")
	cmd.Flags().StringVar(&page, "page", "", "author only the mapping(s) for this page slug")
	cmd.Flags().StringVar(&space, "space", "", "override the target space for all mappings")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without calling the API")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	return cmd
}

// buildSyncTargets turns the manifest mappings into concrete authoring actions.
func buildSyncTargets(proj *config.Project, repoRoot, generator, only, pageFilter string) ([]syncTarget, error) {
	var targets []syncTarget

	if only == "" || only == "api" {
		for _, s := range proj.Sources {
			if pageFilter != "" && s.Page != pageFilter {
				continue
			}
			blocks, err := docs.APIBlocks(repoRoot, s.Source, generator)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Source, err)
			}
			sp, slug := proj.PageTarget(s.Space, s.Page)
			title := firstNonEmpty(s.Title, s.Page)
			targets = append(targets, syncTarget{
				kind:  "page",
				space: sp,
				label: fmt.Sprintf("api %s -> %s/%s", s.Source, sp, slug),
				page:  api.PageUpsertRequest{SpaceSlug: sp, Slug: slug, Title: title, Blocks: blocks},
			})
		}
	}

	if only == "" || only == "docs" {
		for _, d := range proj.Documents {
			if pageFilter != "" && d.Page != pageFilter {
				continue
			}
			if d.As == "release" {
				body, title, err := docs.ReadVerbatim(repoRoot, d.File)
				if err != nil {
					return nil, fmt.Errorf("document %s: %w", d.File, err)
				}
				title = firstNonEmpty(d.Title, d.Version, title)
				sp := d.Space
				if sp == "" {
					sp = proj.ReleaseNotes.Space
				}
				targets = append(targets, syncTarget{
					kind:    "release",
					space:   sp,
					label:   fmt.Sprintf("release %s -> %s", d.File, sp),
					release: api.ReleaseNotesRequest{SpaceSlug: sp, Title: title, BodyMarkdown: body},
				})
				continue
			}
			blocks, title, err := docs.MarkdownPage(repoRoot, d.File, d.Ownership, generator)
			if err != nil {
				return nil, fmt.Errorf("document %s: %w", d.File, err)
			}
			title = firstNonEmpty(d.Title, title)
			sp, slug := proj.PageTarget(d.Space, d.Page)
			targets = append(targets, syncTarget{
				kind:  "page",
				space: sp,
				label: fmt.Sprintf("doc %s -> %s/%s", d.File, sp, slug),
				page:  api.PageUpsertRequest{SpaceSlug: sp, Slug: slug, Title: title, Blocks: blocks},
			})
		}
	}

	return targets, nil
}

// runSync ensures each unique space then performs every upsert.
func runSync(ctx context.Context, client *api.Client, site string, targets []syncTarget, logw, out io.Writer) error {
	ensured := map[string]bool{}
	for _, t := range targets {
		if t.space == "" || ensured[t.space] {
			continue
		}
		if _, err := client.EnsureSpace(ctx, site, api.SpaceUpsertRequest{
			Slug:        t.space,
			Description: "Maintained by the gravity CLI.",
		}); err != nil {
			return syncAPIError(err, fmt.Sprintf("ensure space %q", t.space))
		}
		ensured[t.space] = true
	}

	for _, t := range targets {
		fmt.Fprintf(logw, "sync: %s\n", t.label)
		var resp *api.ReleaseNotesResponse
		var err error
		switch t.kind {
		case "release":
			resp, err = client.CreateReleaseNotes(ctx, site, t.release)
		default:
			resp, err = client.UpsertPage(ctx, site, t.page)
		}
		if err != nil {
			return syncAPIError(err, t.label)
		}
		fmt.Fprintf(out, "Proposed: %s  status=%s  proposal=%s\n", resp.PageSlug, resp.Status, resp.ProposalID)
		if resp.ReviewURL != "" {
			fmt.Fprintf(out, "  review: %s\n", resp.ReviewURL)
		}
	}
	return nil
}

func printSyncStdout(out io.Writer, targets []syncTarget) {
	for _, t := range targets {
		if t.kind == "release" {
			fmt.Fprintf(out, "# %s  (release -> space %s)\n%s\n\n", t.release.Title, t.release.SpaceSlug, t.release.BodyMarkdown)
			continue
		}
		docs.WritePageText(out, t.page)
	}
}

func printSyncDryRun(out io.Writer, targets []syncTarget) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	for _, t := range targets {
		fmt.Fprintf(out, "--- %s ---\n", t.label)
		var payload any
		if t.kind == "release" {
			payload = t.release
		} else {
			payload = t.page
		}
		if err := enc.Encode(payload); err != nil {
			return Fail(CodeError, err)
		}
	}
	return nil
}

// syncAPIError surfaces a helpful auth hint for *api.APIError.
func syncAPIError(err error, what string) error {
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsAuth() {
		return Failf(CodeError, "%s: %s (check %s and that the key is authorized for this site)", what, ae.Message, config.EnvToken)
	}
	return Fail(CodeError, fmt.Errorf("%s: %w", what, err))
}
