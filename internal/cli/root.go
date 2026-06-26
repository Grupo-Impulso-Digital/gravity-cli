// Package cli wires the cobra command tree, resolves configuration, and
// constructs the API client and agent harness for each command.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/config"
)

// version is overridable at build time via -ldflags.
var version = "0.1.0-dev"

// SetVersion lets main override the version string.
func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

// globalFlags are persistent flags available on every command.
type globalFlags struct {
	token  string
	apiURL string
	site   string
}

// env bundles the resolved config and constructed clients for a command run.
type env struct {
	cfg    config.Config
	client *api.Client
}

// resolveEnv builds the runtime environment from global flags, env, and files.
func resolveEnv(gf globalFlags, spaceFlag string) (*env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, Fail(CodeError, fmt.Errorf("get working dir: %w", err))
	}
	cfg, err := config.Resolve(config.Flags{
		Token:  gf.token,
		APIURL: gf.apiURL,
		Site:   gf.site,
		Space:  spaceFlag,
	}, cwd)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	return &env{cfg: cfg, client: api.New(cfg.APIURL, cfg.Token)}, nil
}

// requireAuth ensures a token and API URL are present.
func (e *env) requireAuth() error {
	if e.cfg.Token == "" {
		return Failf(CodeError, "no token configured: run `gravity auth login --token <sk_live_...> --api-url <url>` or set GRAVITY_TOKEN")
	}
	if e.cfg.APIURL == "" {
		return Failf(CodeError, "no API URL configured: pass --api-url or set GRAVITY_API_URL")
	}
	return nil
}

// requireSite returns the configured site or an error.
func (e *env) requireSite() (string, error) {
	if e.cfg.Site == "" {
		return "", Failf(CodeError, "no site configured: pass --site, set GRAVITY_SITE, or add `site:` to .gravity.yaml")
	}
	return e.cfg.Site, nil
}

// NewRootCommand assembles the full command tree.
func NewRootCommand() *cobra.Command {
	gf := &globalFlags{}

	root := &cobra.Command{
		Use:           "gravity",
		Short:         "Gravity — CI companion for the Gravity docs platform",
		Long:          "gravity is a CI/pipeline companion for the Gravity docs platform.\nIt generates release notes, checks API-doc drift, and checks docs completeness against code.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	root.PersistentFlags().StringVar(&gf.token, "token", "", "API token (sk_live_...); overrides env and config")
	root.PersistentFlags().StringVar(&gf.apiURL, "api-url", "", "Gravity API base URL; overrides env and config")
	root.PersistentFlags().StringVar(&gf.site, "site", "", "site slug; overrides env and config")

	root.AddCommand(
		newVersionCmd(),
		newInitCmd(gf),
		newAuthCmd(),
		newDoctorCmd(gf),
		newReleaseNotesCmd(gf),
		newCheckCmd(gf),
		newSelfdocCmd(gf),
	)
	return root
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
