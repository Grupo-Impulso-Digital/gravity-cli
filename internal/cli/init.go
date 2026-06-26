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
		space     string
		role      string
		siteAlias string
		urlAlias  string
		nonInt    bool
		outDir    string
		confirm   bool
		migrate   bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a project-local .gravity.yaml",
		Long: `Create a .gravity.yaml in the current directory: a committable, non-secret
manifest describing how this repo feeds the Gravity docs platform — its site,
product/multi-repo identity, spaces, and (commented) source/document mappings.

A token is never written here; provide it via GRAVITY_TOKEN (CI) or
` + "`gravity auth login`" + ` (local). Use --migrate to upgrade a legacy file in place.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := outDir
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return Fail(CodeError, fmt.Errorf("get working dir: %w", err))
				}
				dir = wd
			}
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("resolve dir: %w", err))
			}
			path := filepath.Join(dir, config.ProjectFileName)

			// Seed from the global --site/--api-url flags (and their hidden
			// aliases), then env.
			site := firstNonEmpty(siteAlias, gf.site, os.Getenv(config.EnvSite))
			apiURL := firstNonEmpty(urlAlias, gf.apiURL, os.Getenv(config.EnvAPIURL))
			sp := firstNonEmpty(space, os.Getenv(config.EnvSpace))
			repo := filepath.Base(absDir)

			if migrate {
				existing, err := config.LoadProject(dir)
				if err != nil {
					return Fail(CodeError, err)
				}
				if existing == nil {
					return Failf(CodeError, "no %s to migrate in %s", config.ProjectFileName, dir)
				}
				site = firstNonEmpty(site, existing.Site)
				apiURL = firstNonEmpty(apiURL, existing.APIURL)
				sp = firstNonEmpty(sp, existing.Spaces.Default)
				role = firstNonEmpty(role, existing.Product.Role)
				if existing.Product.Repo != "" {
					repo = existing.Product.Repo
				}
			} else {
				reader := bufio.NewReader(cmd.InOrStdin())
				if !nonInt {
					site = prompt(cmd, reader, "Site slug", site)
					apiURL = prompt(cmd, reader, "API URL", firstNonEmpty(apiURL, config.DefaultAPIURL))
					sp = prompt(cmd, reader, "Default space (optional)", sp)
				}
				// Refuse to clobber an existing config unless --force is set.
				if !confirm {
					if _, statErr := os.Stat(path); statErr == nil {
						return Failf(CodeError, "%s already exists; pass --force to overwrite or --migrate to upgrade it", path)
					}
				}
			}

			if apiURL == "" {
				apiURL = config.DefaultAPIURL
			}
			if site == "" {
				return Failf(CodeError, "site is required (pass --site or answer the prompt)")
			}

			content := renderScaffold(scaffoldParams{
				Site:   site,
				APIURL: apiURL,
				Space:  sp,
				Role:   role,
				Repo:   repo,
			})
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return Fail(CodeError, fmt.Errorf("write %s: %w", path, err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "default space to record")
	cmd.Flags().StringVar(&role, "role", "", "this repo's role in the product (api|service|frontend|docs)")
	cmd.Flags().BoolVarP(&nonInt, "yes", "y", false, "non-interactive; use flags/env without prompting")
	cmd.Flags().StringVar(&outDir, "dir", "", "directory to write .gravity.yaml in (default: cwd)")
	cmd.Flags().BoolVar(&confirm, "force", false, "overwrite an existing .gravity.yaml")
	cmd.Flags().BoolVar(&migrate, "migrate", false, "upgrade a legacy .gravity.yaml in place, preserving its values")
	// Hidden back-compat aliases for the pre-rename flag names.
	cmd.Flags().StringVar(&siteAlias, "site-slug", "", "")
	cmd.Flags().StringVar(&urlAlias, "url", "", "")
	_ = cmd.Flags().MarkHidden("site-slug")
	_ = cmd.Flags().MarkHidden("url")
	return cmd
}

type scaffoldParams struct {
	Site   string
	APIURL string
	Space  string
	Role   string
	Repo   string
}

// renderScaffold produces a rich, commented .gravity.yaml. Active sections are
// filled from params; advanced mappings are scaffolded as commented examples.
func renderScaffold(p scaffoldParams) string {
	productSlug := p.Site
	defaultSpace := firstNonEmpty(p.Space, p.Repo)
	releaseSpace := firstNonEmpty(p.Space, "changelog")
	roleLine := "  # role: api            # informational: api | service | frontend | docs"
	if p.Role != "" {
		roleLine = "  role: " + p.Role
	}

	return fmt.Sprintf(`# .gravity.yaml — committed, non-secret CI config for the Gravity docs platform.
# NEVER put a token here. Use the %s env var (CI) or `+"`gravity auth login`"+` (local).
version: %d

# --- Connection ---
site: %s
apiUrl: %s

# --- Product / multi-repo identity ---
# A product is one or more repos pointing at the same site, composing shared
# docs and knowledge. `+"`repo`"+` is this repo's unique name within the product.
product:
  slug: %s
  repo: %s
%s

# --- Spaces ---
spaces:
  default: %s
  # shared: [changelog]   # spaces co-owned with sibling repos (pages get slug-prefixed)

# --- Machine-owned, code-derived doc blocks (authored by `+"`gravity sync`"+`) ---
# sources:
#   - source: openapi/openapi.yaml   # repo-relative spec or code file
#     kind: openapi                  # openapi | code
#     space: api
#     page: api-reference
#     title: API Reference

# --- Verbatim Markdown documents -> pages or releases ---
# documents:
#   - file: docs/getting-started.md
#     page: getting-started
#     ownership: machine             # machine | hybrid | human
#     as: page                       # page | release

# --- Release notes (git-derived) ---
releaseNotes:
  space: %s
  changelog: CHANGELOG.md

# --- Nucleus knowledge namespace (shared across a product's repos) ---
knowledge:
  namespace: %s
  scope: %s
`,
		config.EnvToken, config.SchemaVersion,
		p.Site, p.APIURL,
		productSlug, p.Repo, roleLine,
		defaultSpace,
		releaseSpace,
		productSlug, p.Repo,
	)
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
