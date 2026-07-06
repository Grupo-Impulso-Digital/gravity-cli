package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
)

func newCheckAPICmd(gf *globalFlags) *cobra.Command {
	var (
		site    string
		openapi string
		ci      bool
		format  string
	)
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Check API-doc drift between the spec/code and documented api blocks",
		Long: `Pull the documented api blocks for the site and compare them to the source.

With --openapi, diff the spec operations against the documented blocks:
  undocumented  in the spec but not documented
  orphaned      documented but not in the spec
  changed       documented but the summary/params differ

Without --openapi, verify each block whose source binding has a hash and a
repo-resident ref by recomputing its sha256, flagging stale mismatches. Blocks
without a verifiable binding are skipped and counted (never silently).

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

			blocks, err := e.client.APIBlocks(cmd.Context(), siteSlug)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch api blocks: %w", err))
			}

			res := output.Result{Command: "check api", Site: siteSlug}

			if openapi != "" {
				res, err = diffAgainstSpec(openapi, blocks, siteSlug)
				if err != nil {
					return Fail(CodeError, err)
				}
			} else {
				repo, err := git.Open(cmd.Context(), ".")
				if err != nil {
					return Fail(CodeError, err)
				}
				res = verifyAPIBindings(repo.Root, blocks, siteSlug)
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
	cmd.Flags().StringVar(&openapi, "openapi", "", "path to an OpenAPI spec to diff against")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().StringVar(&format, "format", output.FormatText, "text|json|github")
	return cmd
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
