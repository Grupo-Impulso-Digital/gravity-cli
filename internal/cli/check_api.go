package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

func newCheckAPICmd(gf *globalFlags) *cobra.Command {
	var (
		site    string
		space   string
		openapi string
		format  string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Check API-doc drift between the spec and the documented api blocks",
		Long: `Pull the documented api blocks for the site and compare them to the source.

Every OpenAPI spec mapped in .gravity.yaml sources[] is diffed against the api
blocks on its page (--openapi diffs one spec of your choice instead):
  undocumented  in the spec but not documented
  orphaned      documented but not in the spec
  changed       documented but the summary differs

With no spec at all, verify each block whose source binding has a hash and a
repo-resident ref by recomputing its sha256, flagging stale mismatches. Blocks
without a verifiable binding are skipped and counted (never silently).

Only this repo's blocks are checked: the pages the platform attributes to it, or,
before any attribution exists, the spaces .gravity.yaml declares (--space
overrides).

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

			blocks, err := e.client.APIBlocks(cmd.Context(), siteSlug)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch api blocks: %w", err))
			}
			tree, err := e.client.SiteTree(cmd.Context(), siteSlug)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("read site %q: %w", siteSlug, err))
			}
			myRemoteKey := ""
			if ref := repoRefFor(cmd.Context(), repo, e.proj); ref != nil {
				myRemoteKey = ref.RemoteKey
			}
			scope, err := resolveRepoScope(myRemoteKey, attributedTo(pageRefRemoteKeys(tree.Pages), myRemoteKey), e.proj, space)
			if err != nil {
				return Fail(CodeError, err)
			}
			scoped := scopeAPIBlocks(blocks, tree.Pages, scope)
			note := scopeNote(scope, len(scoped), len(blocks), "api block(s)")

			specs := apiSpecs(e.proj, openapi)
			var res output.Result
			if len(specs) > 0 {
				res, err = diffSpecs(repo.Root, specs, scoped, siteSlug, openapi != "")
				if err != nil {
					return Fail(CodeError, err)
				}
			} else {
				res = verifyAPIBindings(repo.Root, scoped, siteSlug)
			}
			res.Notes = append([]string{note}, res.Notes...)
			return renderCheck(cmd, res, format)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&space, "space", "", "check only this space's api blocks (default: this repo's pages)")
	cmd.Flags().StringVar(&openapi, "openapi", "", "diff this OpenAPI spec instead of the sources[] specs in .gravity.yaml")
	cmd.Flags().StringVar(&format, "format", output.FormatText, "text|json|github")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "shorthand for --format json")
	return cmd
}

type apiSpec struct {
	path  string
	space string
	page  string
}

func apiSpecs(proj *config.Project, flag string) []apiSpec {
	if flag != "" {
		return []apiSpec{{path: flag}}
	}
	if proj == nil {
		return nil
	}
	var out []apiSpec
	for _, s := range proj.Sources {
		if s.Kind != "" && s.Kind != "openapi" {
			continue
		}
		sp, slug, _ := proj.PageTarget(s.Space, s.Page, s.Collection)
		out = append(out, apiSpec{path: s.Source, space: sp, page: slug})
	}
	return out
}

func scopeAPIBlocks(blocks []api.APIBlock, pages []api.PageRef, scope repoScope) []api.APIBlock {
	owner := make(map[string]*string, len(pages))
	for i := range pages {
		owner[pages[i].ID] = pages[i].RepoRemoteKey
	}
	out := make([]api.APIBlock, 0, len(blocks))
	for _, b := range blocks {
		if scope.includes(b.SpaceSlug, b.PageSlug, owner[b.PageID]) {
			out = append(out, b)
		}
	}
	return out
}

func (s apiSpec) owns(b api.APIBlock) bool {
	if s.space == "" && s.page == "" {
		return true
	}
	if b.SourceBinding != nil && b.SourceBinding.Ref == s.path {
		return true
	}
	return b.SpaceSlug == s.space && b.PageSlug == s.page
}

func diffSpecs(repoRoot string, specs []apiSpec, blocks []api.APIBlock, siteSlug string, explicit bool) (output.Result, error) {
	res := output.Result{Command: "check api", Site: siteSlug}
	for _, spec := range specs {
		path := spec.path
		if !explicit {
			clean, err := pathsafe.Rel(spec.path)
			if err != nil {
				return output.Result{}, fmt.Errorf("sources: %s: %w", spec.path, err)
			}
			path = filepath.Join(repoRoot, clean)
		}
		var mine []api.APIBlock
		for _, b := range blocks {
			if spec.owns(b) {
				mine = append(mine, b)
			}
		}
		one, err := diffAgainstSpec(path, mine, siteSlug)
		if err != nil {
			return output.Result{}, err
		}
		for i := range one.Notes {
			one.Notes[i] = spec.path + ": " + one.Notes[i]
		}
		res.Findings = append(res.Findings, one.Findings...)
		res.Notes = append(res.Notes, one.Notes...)
	}
	return res, nil
}

func diffAgainstSpec(specPath string, blocks []api.APIBlock, siteSlug string) (output.Result, error) {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return output.Result{}, fmt.Errorf("read openapi spec %s: %w", specPath, err)
	}
	ops, err := checks.ParseOpenAPI(spec)
	if err != nil {
		return output.Result{}, err
	}

	documented := make([]checks.DocumentedOp, 0, len(blocks))
	for _, b := range blocks {
		documented = append(documented, checks.DocumentedOp{
			Method:   b.Content.Method,
			Path:     b.Content.Path,
			Summary:  b.Content.Summary,
			PageSlug: b.PageSlug,
			BlockID:  b.BlockID,
		})
	}

	findings := checks.DiffOperations(ops, documented)
	res := output.Result{Command: "check api", Site: siteSlug}
	res.Notes = append(res.Notes,
		fmt.Sprintf("compared %d spec operation(s) against %d documented block(s)", len(ops), len(documented)))
	for _, f := range findings {
		sev := output.SeverityWarning
		if f.Kind == "undocumented" {
			sev = output.SeverityError
		}
		res.Findings = append(res.Findings, output.Finding{
			Severity:      sev,
			Kind:          f.Kind,
			Title:         fmt.Sprintf("%s: %s %s", f.Kind, f.Method, f.Path),
			Detail:        f.Detail,
			Location:      f.Method + " " + f.Path,
			SuggestedPage: f.PageSlug,
		})
	}
	return res, nil
}

func verifyAPIBindings(repoRoot string, blocks []api.APIBlock, siteSlug string) output.Result {
	res := output.Result{Command: "check api", Site: siteSlug}
	for _, b := range blocks {
		check := checks.VerifyBinding(repoRoot, b.SourceBinding)
		if check.Skipped {
			res.Skipped++
			continue
		}
		if check.Stale {
			res.Findings = append(res.Findings, output.Finding{
				Severity: output.SeverityError,
				Kind:     "stale",
				Title:    fmt.Sprintf("stale: %s %s", b.Content.Method, b.Content.Path),
				Detail: fmt.Sprintf("source %s changed; recorded hash %s but file hashes to %s",
					check.Ref, shortHash(check.Want), shortHash(check.Got)),
				Location:      check.Ref,
				SuggestedPage: b.PageSlug,
			})
		}
	}
	res.Notes = append(res.Notes, fmt.Sprintf("verified %d block(s), skipped %d without a verifiable binding", len(blocks)-res.Skipped, res.Skipped))
	return res
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
