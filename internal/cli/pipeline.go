package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func notYet(name string) error {
	return &ExitError{
		Code:    CodeError,
		Err:     errorf("gravity %s is not available in this build yet: the pass engine ships in a later 1.0 milestone (use gravity v0.3 meanwhile)", name),
		ErrCode: "not_available",
	}
}

func newRunCmd(*app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the documentation pipeline for this repository (not available in this build)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return notYet("run")
		},
	}
	f := cmd.Flags()
	f.StringSlice("pass", nil, "run only these passes")
	f.String("trigger", "", "override the detected trigger")
	f.String("branch", "", "override the detected branch")
	f.String("from", "", "start of an explicit range")
	f.String("to", "", "end of an explicit range")
	f.String("note", "", "a note for reviewers and the AI")
	f.Bool("dry-run", false, "plan and report without writing")
	f.Duration("lease-timeout", 20*time.Minute, "how long to wait for another run on the same branch")
	f.Int("parallel", 1, "run up to N independent passes concurrently")
	f.Bool("no-comment", false, "do not post the pull request comment")
	f.Bool("strict", false, "treat skipped passes as failures")
	return cmd
}

func newPreviewCmd(*app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Show what every pass would write for your working tree, without writing (not available in this build)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return notYet("preview")
		},
	}
	f := cmd.Flags()
	f.StringSlice("pass", nil, "preview only these passes")
	f.String("from", "", "start of the range")
	f.String("to", "", "end of the range")
	f.Bool("committed", false, "preview HEAD instead of the working tree")
	f.String("note", "", "a note for the AI")
	f.String("format", "text", "text, diff or json")
	f.Bool("open", false, "open the target pages")
	return cmd
}

func newCheckCmd(*app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "The pull request gate: drift, coverage, claims and doc impact (not available in this build)",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "api" || args[0] == "docs") {
				return removedPointer("check "+args[0], "gravity check")
			}
			return notYet("check")
		},
	}
	f := cmd.Flags()
	f.StringSlice("pass", nil, "check only these passes")
	f.String("from", "", "start of the range")
	f.String("to", "", "end of the range")
	f.StringSlice("fail-on", nil, "drift, coverage, claims, verbatim")
	f.String("annotate", "auto", "auto, github, gitlab, azure or none")
	f.Bool("comment", false, "post the doc-impact comment")
	return cmd
}
