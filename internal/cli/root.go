// Package cli wires the cobra command tree, resolves configuration, and constructs the API client and agent harness for each command.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

type globalFlags struct {
	token  string
	apiURL string
	site   string
	ci     bool
	track  *envTracker
}

type envTracker struct {
	env *env
}

type env struct {
	cfg         config.Config
	proj        *config.Project
	client      *api.Client
	featureSet  map[string]bool
	targetSpace string
}

func resolveEnv(gf globalFlags, spaceFlag string) (*env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, Fail(CodeError, fmt.Errorf("get working dir: %w", err))
	}
	cfg, proj, err := config.ResolveWithProject(config.Flags{
		Token:  gf.token,
		APIURL: gf.apiURL,
		Site:   gf.site,
		Space:  spaceFlag,
	}, cwd)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	e := &env{cfg: cfg, proj: proj, client: api.New(cfg.APIURL, cfg.Token)}
	if gf.track != nil {
		gf.track.env = e
	}
	return e, nil
}

func (e *env) requireAuth() error {
	if e.cfg.Token == "" {
		return Failf(CodeError, "no API token: set %s (CI secret) or run `gravity auth login`", config.EnvToken)
	}
	if e.cfg.APIURL == "" {
		return Failf(CodeError, "no API URL: set %s or add `apiUrl` to %s", config.EnvAPIURL, config.ProjectFileName)
	}
	return nil
}

func (e *env) requireSite() (string, error) {
	if e.cfg.Site == "" {
		return "", Failf(CodeError, "no site configured: pass --site, set GRAVITY_SITE, or add `site:` to .gravity.yaml")
	}
	return e.cfg.Site, nil
}

// NewRootCommand assembles the full command tree.
func NewRootCommand() *cobra.Command {
	gf := &globalFlags{track: &envTracker{}}

	root := &cobra.Command{
		Use:           "gravity",
		Short:         "Gravity — CI companion for the Gravity docs platform",
		Long:          "gravity is a CI/pipeline companion for the Gravity docs platform.\nIt generates release notes, checks API-doc drift, and checks docs completeness against code.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if ciMode(*gf) {
				root := cmd.Root()
				root.SetOut(plainWriter(cmd.OutOrStdout()))
				root.SetErr(plainWriter(cmd.ErrOrStderr()))
			}
		},
	}

	root.PersistentFlags().StringVar(&gf.token, "token", "", "API token (sk_live_...); overrides env and config")
	root.PersistentFlags().StringVar(&gf.apiURL, "api-url", "", "Gravity API base URL; overrides env and config")
	root.PersistentFlags().StringVar(&gf.site, "site", "", "site slug; overrides env and config")
	root.PersistentFlags().BoolVar(&gf.ci, "ci", false, "CI mode: never prompt, plain ASCII output without emoji (also on when CI=true)")

	root.AddCommand(
		newVersionCmd(),
		newInitCmd(gf),
		newAuthCmd(gf),
		newDoctorCmd(gf),
		newPingCmd(gf),
		newReposCmd(gf),
		newReleaseNotesCmd(gf),
		newCheckCmd(gf),
		newCoverageCmd(gf),
		newSpacesCmd(gf),
		newSyncCmd(gf),
		newDocsCmd(gf),
		newCaptureCmd(gf),
		newNucleusCmd(gf),
	)
	explainErrors(root, gf.track)
	return root
}

func explainErrors(cmd *cobra.Command, track *envTracker) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := run(c, args)
			if err != nil && track != nil {
				err = explainNotFound(c.Context(), track.env, err)
			}
			return err
		}
	}
	for _, sub := range cmd.Commands() {
		explainErrors(sub, track)
	}
}

// Execute runs the root command and returns the process exit code.
func Execute(ctx context.Context) int {
	root := NewRootCommand()
	err := root.ExecuteContext(ctx)
	if err != nil {
		PrintError(err)
		return CodeFor(err)
	}
	return CodeOK
}
