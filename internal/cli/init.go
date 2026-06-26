package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/config"
)

func newInitCmd(gf *globalFlags) *cobra.Command {
	var (
		site    string
		apiURL  string
		space   string
		nonInt  bool
		outDir  string
		confirm bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a project-local .gravity.yaml",
		Long:  "Create a .gravity.yaml in the current directory recording the site and API URL.\nValues default to flags/env; missing ones are prompted for unless --yes is set.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := outDir
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return Fail(CodeError, fmt.Errorf("get working dir: %w", err))
				}
				dir = wd
			}

			// Seed from flags, then env, then global flags.
			if site == "" {
				site = firstNonEmpty(gf.site, os.Getenv(config.EnvSite))
			}
			if apiURL == "" {
				apiURL = firstNonEmpty(gf.apiURL, os.Getenv(config.EnvAPIURL))
			}
			if space == "" {
				space = os.Getenv(config.EnvSpace)
			}

			reader := bufio.NewReader(cmd.InOrStdin())
			if !nonInt {
				site = prompt(cmd, reader, "Site slug", site)
				apiURL = prompt(cmd, reader, "API URL", firstNonEmpty(apiURL, "https://app.gravity.dev"))
				space = prompt(cmd, reader, "Default space (optional)", space)
			}
			if site == "" {
				return Failf(CodeError, "site is required (pass --site or answer the prompt)")
			}
			if apiURL == "" {
				return Failf(CodeError, "api URL is required (pass --api-url or answer the prompt)")
			}

			// Refuse to clobber an existing config unless --force is set.
			if !confirm {
				if _, statErr := os.Stat(filepath.Join(dir, config.ProjectFileName)); statErr == nil {
					return Failf(CodeError, "%s already exists; pass --force to overwrite", filepath.Join(dir, config.ProjectFileName))
				}
			}

			path, err := config.WriteProjectConfig(dir, config.ProjectConfig{
				Site:   site,
				APIURL: apiURL,
				Space:  space,
			})
			if err != nil {
				return Fail(CodeError, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site-slug", "", "site slug to record")
	cmd.Flags().StringVar(&apiURL, "url", "", "API URL to record")
	cmd.Flags().StringVar(&space, "space", "", "default space to record")
	cmd.Flags().BoolVarP(&nonInt, "yes", "y", false, "non-interactive; use flags/env without prompting")
	cmd.Flags().StringVar(&outDir, "dir", "", "directory to write .gravity.yaml in (default: cwd)")
	cmd.Flags().BoolVar(&confirm, "force", false, "overwrite an existing .gravity.yaml")
	return cmd
}

func prompt(cmd *cobra.Command, r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
