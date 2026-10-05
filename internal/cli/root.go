// Package cli wires the gravity command tree, global flags, output modes and exit codes.
package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "gravity",
		Short:         "Gravity keeps your documentation in step with your code",
		Long:          "gravity connects a repository to Gravity: the repository declares its docs structure and passes in .gravity.yaml, gravity applies them and runs the passes, and the results land as a change request reviewed in the app.\nStart with `gravity setup`; `gravity show` and `gravity validate` explain the manifest; `gravity run --dry-run` shows what a run would write.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.setupUI(strings.TrimPrefix(cmd.CommandPath(), "gravity "))
		},
	}
	root.SetVersionTemplate(versionLine() + "\n")
	pf := root.PersistentFlags()
	pf.StringVar(&a.gf.profile, "profile", "", "credential profile to use (env GRAVITY_PROFILE)")
	pf.StringVar(&a.gf.apiURL, "api-url", "", "Gravity API base URL (env GRAVITY_API_URL)")
	pf.StringVar(&a.gf.token, "token", "", "API token (else env GRAVITY_REPO_TOKEN, then GRAVITY_TOKEN, then the profile); never read from .gravity.yaml")
	pf.StringVar(&a.gf.manifest, "manifest", "", "manifest path (env GRAVITY_MANIFEST, default .gravity.yaml)")
	pf.StringVarP(&a.gf.dir, "dir", "C", "", "run as if gravity was started in this directory")
	pf.BoolVar(&a.gf.json, "json", false, "print exactly one JSON document on stdout; human output goes to stderr")
	pf.BoolVar(&a.gf.noColor, "no-color", false, "disable colors (also NO_COLOR)")
	pf.BoolVarP(&a.gf.quiet, "quiet", "q", false, "print only what is essential")
	pf.BoolVarP(&a.gf.verbose, "verbose", "v", false, "print debug details to stderr")

	root.AddCommand(
		newLoginCmd(a),
		newLogoutCmd(a),
		newWhoamiCmd(a),
		newOrgCmd(a),
		newSetupCmd(a),
		newShowCmd(a),
		newValidateCmd(a),
		newStructureCmd(a),
		newRunCmd(a),
		newReviewCmd(a),
		newRunsCmd(a),
		newApproveCmd(a),
		newCICmd(a),
		newStatusCmd(a),
		newCheckCmd(a),
		newExplainCmd(a),
		newAgentCmd(a),
		newVersionCmd(a),
	)
	return root
}

// Execute runs the CLI with the process streams and returns the exit code.
func Execute(ctx context.Context) int {
	return run(ctx, newApp(), nil)
}

func run(ctx context.Context, a *app, args []string) int {
	root := newRootCommand(a)
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	if args != nil {
		root.SetArgs(args)
	}
	err := root.ExecuteContext(ctx)
	if a.ui == nil {
		a.setupUI("")
	}
	if err == nil {
		return CodeOK
	}
	code := CodeFor(err)
	var ee *ExitError
	silent := errors.As(err, &ee) && ee.Err == nil
	msg := messageOf(err)
	if !silent {
		msg += a.credentialHint(err)
	}
	if a.ui.JSON() {
		_ = a.ui.Failure(ui.ErrorInfo{Code: errorCode(err), Message: msg, ExitCode: code}, nil)
	}
	if !silent {
		a.ui.Error(msg)
	}
	return code
}

func messageOf(err error) string {
	var ee *ExitError
	if errors.As(err, &ee) && ee.Err == nil {
		return ""
	}
	return err.Error()
}
