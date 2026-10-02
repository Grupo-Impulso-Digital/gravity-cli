package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
)

const maxCoverageList = 10

func newCoverageCmd(gf *globalFlags) *cobra.Command {
	var (
		site     string
		repoKey  string
		kind     string
		format   string
		minRatio float64
		all      bool
		require  bool
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Report documentation coverage against this repo's feature inventory",
		Long: `Compare what this repo says it contains (the feature inventory published by
` + "`gravity docs generate`" + `) against what is actually documented on the site.

By default only this repo's coverage is reported — a sibling repo publishing to
the same site is its own problem. Pass --all for the whole site, or --repo to
name another repo by its remote key.

Units count as documented when a live page covers them, stale when the sources
they are bound to changed since that page was written, and undocumented when no
page covers them at all. Stale units are documented: they do not fail the bar.

Exit codes: 0 at or above the bar, 1 below it (or a required page missing),
2 on an auth/network/config error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format = jsonFormat(format, jsonOut)
			if err := validateFormat(format); err != nil {
				return err
			}
			if err := validateUnitKind(kind); err != nil {
				return err
			}
			if all && repoKey != "" {
				return Failf(CodeError, "--all and --repo are mutually exclusive")
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

			opts := coverageOpts{
				site:      siteSlug,
				remoteKey: repoKey,
				kind:      kind,
				min:       coverageBar(e.proj, minRatio, cmd.Flags().Changed("min")),
				required:  requiredPages(e.proj),
				format:    format,
				require:   require,
			}
			if opts.min < 0 || opts.min > 1 {
				return Failf(CodeError, "invalid coverage bar %.2f (want 0..1)", opts.min)
			}
			if !all && opts.remoteKey == "" {
				if ref := localRepoRef(cmd.Context(), e.proj); ref != nil {
					opts.remoteKey = ref.RemoteKey
				}
			}
			if skip, gerr := e.gateFeature(cmd.Context(), featureCoverage, "coverage reporting", logWriter(cmd), require); skip || gerr != nil {
				return gerr
			}
			return runCoverage(cmd.Context(), e.client, opts, logWriter(cmd), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&repoKey, "repo", "", "report on this repo (remote key) instead of the current one")
	cmd.Flags().StringVar(&kind, "kind", "", "only count units of this kind: feature|service|system|api|capability")
	cmd.Flags().Float64Var(&minRatio, "min", 0, "minimum documented ratio 0..1 (default: coverage.min from .gravity.yaml)")
	cmd.Flags().BoolVar(&all, "all", false, "report every repo publishing to the site, not just this one")
	cmd.Flags().StringVar(&format, "format", output.FormatText, "text|json|github")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "shorthand for --format json")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable coverage reporting as a hard error (exit 2)")
	return cmd
}

type coverageOpts struct {
	site      string
	remoteKey string
	kind      string
	min       float64
	required  []requiredPage
	format    string
	require   bool
}

type requiredPage struct {
	name string
	slug string
}

type coverageView struct {
	Site     string             `json:"site"`
	Repo     string             `json:"repo,omitempty"`
	Kind     string             `json:"kind,omitempty"`
	Min      float64            `json:"min"`
	Repos    []api.RepoCoverage `json:"repos"`
	Findings []output.Finding   `json:"findings"`
}

func validateUnitKind(kind string) error {
	switch kind {
	case "", api.UnitKindFeature, api.UnitKindService, api.UnitKindSystem, api.UnitKindAPI, api.UnitKindCapability:
		return nil
	}
	return Failf(CodeError, "invalid --kind %q (want feature|service|system|api|capability)", kind)
}

func coverageBar(proj *config.Project, flagVal float64, flagSet bool) float64 {
	if flagSet {
		return flagVal
	}
	if proj != nil {
		return proj.Coverage.Min
	}
	return 0
}

func requiredPages(proj *config.Project) []requiredPage {
	if proj == nil || len(proj.Coverage.Require) == 0 {
		return nil
	}
	out := make([]requiredPage, 0, len(proj.Coverage.Require))
	for _, name := range proj.Coverage.Require {
		_, slug, _ := proj.PageTarget("", name, "")
		out = append(out, requiredPage{name: name, slug: slug})
	}
	return out
}

func runCoverage(ctx context.Context, client *api.Client, opts coverageOpts, logw, out io.Writer) error {
	cov, err := client.Coverage(ctx, opts.site, opts.remoteKey, opts.kind)
	if err != nil {
		if skipped, ferr := skippableFeature(err, "coverage reporting", logw, opts.require); skipped || ferr != nil {
			return ferr
		}
		return Fail(CodeError, fmt.Errorf("fetch coverage: %w", classifyAuthErr(err)))
	}

	var published map[string]bool
	if len(opts.required) > 0 {
		pages, perr := client.ListPages(ctx, opts.site, api.PageListOptions{})
		if perr != nil {
			return Fail(CodeError, fmt.Errorf("fetch pages: %w", classifyAuthErr(perr)))
		}
		published = make(map[string]bool, len(pages))
		for _, p := range pages {
			published[p.Slug] = true
		}
	}

	findings := evaluateCoverage(cov, opts.min, opts.required, published)
	res := output.Result{
		Command:  "coverage",
		Site:     opts.site,
		Findings: findings,
		Notes:    coverageNotes(cov, opts),
	}

	switch opts.format {
	case output.FormatJSON:
		enc := json.NewEncoder(rawWriter(out))
		enc.SetIndent("", "  ")
		view := coverageView{
			Site: opts.site, Repo: opts.remoteKey, Kind: opts.kind, Min: opts.min,
			Repos: cov.Repos, Findings: findings,
		}
		if view.Repos == nil {
			view.Repos = []api.RepoCoverage{}
		}
		if view.Findings == nil {
			view.Findings = []output.Finding{}
		}
		if err := enc.Encode(view); err != nil {
			return Fail(CodeError, err)
		}
	case output.FormatGitHub:
		if err := output.Render(rawWriter(out), res, output.FormatGitHub); err != nil {
			return Fail(CodeError, err)
		}
	default:
		printCoverage(out, cov, opts)
		if err := output.Render(out, res, output.FormatText); err != nil {
			return Fail(CodeError, err)
		}
	}

	if len(findings) > 0 {
		return Fail(CodeFindings, nil)
	}
	return nil
}

func evaluateCoverage(cov *api.CoverageResponse, bar float64, required []requiredPage, published map[string]bool) []output.Finding {
	var findings []output.Finding
	if bar > 0 && cov != nil {
		for _, r := range cov.Repos {
			if r.Totals.Units == 0 {
				continue
			}
			ratio := coverageRatio(r.Totals)
			if ratio >= bar {
				continue
			}
			findings = append(findings, output.Finding{
				Severity: output.SeverityWarning,
				Kind:     "coverage",
				Title: fmt.Sprintf("%s documents %s of its units, below the %s bar",
					repoLabel(r), pct(ratio), pct(bar)),
				Detail: fmt.Sprintf("%d of %d unit(s) documented (%d undocumented, %d stale)",
					r.Totals.Documented, r.Totals.Units, r.Totals.Undocumented, r.Totals.Stale),
				Location: r.RemoteKey,
			})
		}
	}
	if published != nil {
		for _, req := range required {
			if published[req.slug] || published[req.name] {
				continue
			}
			findings = append(findings, output.Finding{
				Severity:      output.SeverityError,
				Kind:          "missing-page",
				Title:         fmt.Sprintf("required page %q does not exist", req.name),
				Detail:        "coverage.require lists this page; author it (a `documents` mapping or `gravity docs generate`) and re-run",
				SuggestedPage: req.slug,
			})
		}
	}
	return findings
}

func coverageRatio(t api.CoverageTotals) float64 {
	if t.Units <= 0 {
		return 1
	}
	return float64(t.Documented) / float64(t.Units)
}

func coverageNotes(cov *api.CoverageResponse, opts coverageOpts) []string {
	var notes []string
	if cov == nil || len(cov.Repos) == 0 {
		if opts.remoteKey != "" {
			notes = append(notes, fmt.Sprintf(
				"no inventory published for %s yet — run `gravity docs generate` to record one", opts.remoteKey,
			))
		} else {
			notes = append(notes, "no repo has published an inventory for this site yet — run `gravity docs generate` to record one")
		}
	}
	if opts.min == 0 {
		notes = append(notes, "no coverage bar is set — add `coverage.min` to .gravity.yaml or pass --min to fail below it")
	}
	return notes
}

func printCoverage(out io.Writer, cov *api.CoverageResponse, opts coverageOpts) {
	fmt.Fprintf(out, "Site:  %s\n", firstNonEmpty(cov.SiteSlug, opts.site))
	if opts.remoteKey != "" {
		fmt.Fprintf(out, "Repo:  %s\n", opts.remoteKey)
	} else {
		fmt.Fprintln(out, "Repo:  (all repos on this site)")
	}
	if opts.kind != "" {
		fmt.Fprintf(out, "Kind:  %s\n", opts.kind)
	}
	if opts.min > 0 {
		fmt.Fprintf(out, "Bar:   %s\n", pct(opts.min))
	}
	for _, r := range cov.Repos {
		fmt.Fprintf(out, "\n%s\n", repoLabel(r))
		fmt.Fprintf(out, "  %-11s %3d/%-3d  %4s   %d stale   %d undocumented\n",
			"total", r.Totals.Documented, r.Totals.Units, pct(coverageRatio(r.Totals)),
			r.Totals.Stale, r.Totals.Undocumented)
		for _, k := range r.ByKind {
			if k.Units == 0 {
				continue
			}
			ratio := coverageRatio(api.CoverageTotals{Units: k.Units, Documented: k.Documented})
			fmt.Fprintf(out, "  %-11s %3d/%-3d  %4s   %d stale   %d undocumented\n",
				k.Kind, k.Documented, k.Units, pct(ratio), k.Stale, k.Undocumented)
		}
		printCoverageUnits(out, r)
		printUncoveredPages(out, r)
	}
}

func printCoverageUnits(out io.Writer, r api.RepoCoverage) {
	var undocumented, stale []string
	for _, u := range r.Units {
		switch u.State {
		case api.UnitStateUndocumented:
			undocumented = append(undocumented, u.Key)
		case api.UnitStateStale:
			stale = append(stale, u.Key)
		}
	}
	if len(undocumented) > 0 {
		fmt.Fprintf(out, "  undocumented: %s\n", capList(undocumented))
	}
	if len(stale) > 0 {
		fmt.Fprintf(out, "  stale:        %s\n", capList(stale))
	}
}

func printUncoveredPages(out io.Writer, r api.RepoCoverage) {
	if len(r.UncoveredPages) == 0 {
		return
	}
	slugs := make([]string, 0, len(r.UncoveredPages))
	for _, p := range r.UncoveredPages {
		slugs = append(slugs, p.SpaceSlug+"/"+p.Slug)
	}
	fmt.Fprintf(out, "  unclaimed pages: %s\n", capList(slugs))
}

func capList(vals []string) string {
	if len(vals) <= maxCoverageList {
		return strings.Join(vals, ", ")
	}
	return fmt.Sprintf("%s, … (%d more)", strings.Join(vals[:maxCoverageList], ", "), len(vals)-maxCoverageList)
}

func repoLabel(r api.RepoCoverage) string {
	switch {
	case r.Name != "" && r.RemoteKey != "":
		return fmt.Sprintf("%s  (%s)", r.Name, r.RemoteKey)
	case r.Name != "":
		return r.Name
	default:
		return orUnset(r.RemoteKey)
	}
}

func pct(ratio float64) string {
	return fmt.Sprintf("%.0f%%", ratio*100)
}
