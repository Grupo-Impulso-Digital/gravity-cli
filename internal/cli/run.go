package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func newRunCmd(a *app) *cobra.Command {
	var f pipelineFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the documentation pipeline for this repository",
		Long: "Plan the passes for this trigger, resolve each pass's commit range, skip passes whose scope did not change, then run the rest inside one Gravity run whose changes are reviewed as a bundle.\n\n" +
			"A run reads committed history only (up to HEAD, or --to): uncommitted changes are never sent, because every write cites the commit it comes from. Use `gravity preview` to see what your working tree would change.\n\n" +
			"Outside CI the trigger is manual. A manual run runs the passes whose triggers allow it; `--pass <name>` runs that pass whatever its triggers (on servers that support it), and the output names the passes a manual run left out.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validAnnotate(f.annotate); err != nil {
				return err
			}
			ctx := cmd.Context()
			s, err := a.pipelineSession(ctx, modeRun, f, false)
			if errors.Is(err, errForkPR) {
				return a.forkPR(s)
			}
			if err != nil {
				return err
			}
			if !s.ci.IsCI() {
				if dirty, derr := s.info.repo.Dirty(ctx); derr == nil && dirty {
					a.ui.Warn("uncommitted_changes", "uncommitted changes are not part of this run, which reads committed history only; commit them, or use `gravity preview` to see what they would change")
				}
			}
			res, err := a.execute(ctx, s)
			if res != nil && res.Plan != nil {
				a.printRun(res, s.info)
			}
			if err != nil {
				return runError(err)
			}
			if s.opts.Trigger == config.TriggerPR || s.ci.IsCI() {
				a.publishReport(ctx, s, res, !f.noComment, f.annotate)
			}
			return a.finishPipeline(res, f.strict, nil)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&f.passes, "pass", nil, "run only these passes (on a manual run, whatever their triggers)")
	fl.StringVar(&f.trigger, "trigger", "", "override the detected trigger (pr, push, release, schedule, manual)")
	fl.StringVar(&f.branch, "branch", "", "override the detected branch")
	fl.StringVar(&f.from, "from", "", "start of an explicit range (manual runs)")
	fl.StringVar(&f.to, "to", "", "end of an explicit range (manual runs)")
	fl.StringVar(&f.note, "note", "", "a note for reviewers and the AI")
	fl.BoolVar(&f.dryRun, "dry-run", false, "plan and report without writing")
	fl.DurationVar(&f.leaseTimeout, "lease-timeout", 20*time.Minute, "how long to wait for another run on the same branch")
	fl.IntVar(&f.parallel, "parallel", 1, "run up to N independent passes concurrently (max 4)")
	fl.BoolVar(&f.noComment, "no-comment", false, "do not post the pull request comment")
	fl.BoolVar(&f.strict, "strict", false, "treat unapproved targets, missing scopes and unlicensed modules as failures")
	fl.StringVar(&f.annotate, "annotate", "auto", "finding annotations: auto, github, gitlab, azure or none")
	return cmd
}
