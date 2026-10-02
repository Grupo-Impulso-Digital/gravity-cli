package cli

import (
	"errors"

	"github.com/spf13/cobra"

	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

var failOnCategories = map[string]bool{"drift": true, "coverage": true, "claims": true, "verbatim": true}

func newCheckCmd(a *app) *cobra.Command {
	var f pipelineFlags
	cmd := &cobra.Command{
		Use:   "check",
		Short: "The pull request gate: drift, coverage, claims and doc impact",
		Long:  "Run the check passes (or a built-in drift and coverage check when none is declared) plus every other pass's dry impact for this pull request. Exits 1 when a finding falls in --fail-on.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "api" || args[0] == "docs") {
				return removedPointer("check "+args[0], "gravity check")
			}
			if len(args) > 0 {
				return Failf(CodeError, "unexpected argument %q", args[0])
			}
			for _, c := range f.failOn {
				if !failOnCategories[c] {
					return Failf(CodeError, "--fail-on %q must be drift, coverage, claims or verbatim", c)
				}
			}
			switch f.annotate {
			case "auto", "github", "gitlab", "azure", "none":
			default:
				return Failf(CodeError, "--annotate %q must be auto, github, gitlab, azure or none", f.annotate)
			}
			ctx := cmd.Context()
			s, err := a.pipelineSession(ctx, modeCheck, f, false)
			if errors.Is(err, errForkPR) {
				return a.forkPR(s)
			}
			if err != nil {
				return err
			}
			res, err := engine.Execute(ctx, a.runEnv(s), s.opts)
			if res != nil && res.Plan != nil {
				a.printRun(res, s.info)
			}
			if err != nil {
				return runError(err)
			}
			comment := f.comment || a.env("GITHUB_TOKEN") != ""
			a.publishPR(ctx, s, res, comment, f.annotate)
			return a.finishPipeline(res, false, nil)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&f.passes, "pass", nil, "check only these passes")
	fl.StringVar(&f.from, "from", "", "compare from this commit instead of the merge base")
	fl.StringVar(&f.to, "to", "", "compare up to this commit instead of HEAD")
	fl.StringSliceVar(&f.failOn, "fail-on", nil, "finding categories that fail the check: drift, coverage, claims, verbatim")
	fl.StringVar(&f.annotate, "annotate", "auto", "auto, github, gitlab, azure or none")
	fl.BoolVar(&f.comment, "comment", false, "post the doc-impact comment (also posted when GITHUB_TOKEN is set)")
	return cmd
}
