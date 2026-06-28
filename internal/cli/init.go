package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"

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
product/multi-repo identity, spaces, and the source/document mappings that
` + "`gravity sync`" + ` authors onto the platform.

Run with no flags for an interactive wizard; it detects OpenAPI specs and
Markdown docs in the repo and offers to map them. Use -y/--yes (or a
non-interactive shell) to skip the wizard and rely on flags/env.

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
			productSlug := ""

			var sources []config.SourceMap
			var documents []config.DocMap

			// Refuse to clobber an existing config unless --force/--migrate is set.
			// Done up front so the wizard never runs only to fail at write time.
			if !confirm && !migrate {
				if _, statErr := os.Stat(path); statErr == nil {
					return Failf(CodeError, "%s already exists; pass --force to overwrite or --migrate to upgrade it", path)
				}
			}

			switch {
			case migrate:
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
				productSlug = existing.Product.Slug
				if existing.Product.Repo != "" {
					repo = existing.Product.Repo
				}
				// Preserve declared mappings — never silently drop them.
				sources = existing.Sources
				documents = existing.Documents

			case nonInt || !isInteractive(cmd.InOrStdin()):
				// Non-interactive: rely on flags/env, no detection.

			default:
				params, write, werr := runInitWizard(cmd, initSeed{
					Site:    site,
					APIURL:  firstNonEmpty(apiURL, config.DefaultAPIURL),
					Space:   firstNonEmpty(sp, repo),
					Role:    role,
					Repo:    repo,
					ScanDir: absDir,
				})
				if werr != nil {
					return Fail(CodeError, werr)
				}
				if !write {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted; nothing written.")
					return nil
				}
				site, apiURL, sp = params.Site, params.APIURL, params.Space
				role, repo, productSlug = params.Role, params.Repo, params.ProductSlug
				sources, documents = params.Sources, params.Documents
			}

			if apiURL == "" {
				apiURL = config.DefaultAPIURL
			}
			if site == "" {
				return Failf(CodeError, "site is required (pass --site or answer the prompt)")
			}

			params := scaffoldParams{
				Site:        site,
				APIURL:      apiURL,
				Space:       sp,
				Role:        role,
				Repo:        repo,
				ProductSlug: firstNonEmpty(productSlug, site),
				Sources:     sources,
				Documents:   documents,
			}
			// Validate the manifest we are about to write so init never produces a
			// file that `sync`/`check` would later reject.
			if err := projectFromScaffold(params).Validate(path); err != nil {
				return Fail(CodeError, err)
			}

			content := renderScaffold(params)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return Fail(CodeError, fmt.Errorf("write %s: %w", path, err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			if len(sources) == 0 && len(documents) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No source/document mappings declared yet — edit the file or re-run `gravity init`, then `gravity sync`.")
			}
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

// initSeed carries the pre-filled defaults into the interactive wizard.
type initSeed struct {
	Site    string
	APIURL  string
	Space   string
	Role    string
	Repo    string
	ScanDir string
}

// wizardResult is the manifest assembled from the wizard answers.
type wizardResult struct {
	Site        string
	APIURL      string
	Space       string
	Role        string
	Repo        string
	ProductSlug string
	Sources     []config.SourceMap
	Documents   []config.DocMap
}

// runInitWizard drives the huh form, detecting candidate specs/docs in ScanDir
// and offering to map them. The second return is the user's final write/abort
// choice.
func runInitWizard(cmd *cobra.Command, seed initSeed) (wizardResult, bool, error) {
	site := seed.Site
	apiURL := seed.APIURL
	defaultSpace := seed.Space
	role := seed.Role
	productSlug := seed.Site
	repo := seed.Repo
	apiSpace := "api"

	specs, mds := detectDocSources(seed.ScanDir)
	selectedSpecs := append([]string(nil), specs...) // preselect all detected
	selectedDocs := append([]string(nil), mds...)

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().Title("Site slug").
				Description("The Gravity site this repo documents.").
				Value(&site).Validate(requiredField),
			huh.NewInput().Title("API URL").Value(&apiURL).Validate(optionalURL),
		),
		huh.NewGroup(
			huh.NewInput().Title("Product slug").
				Description("Logical product id; defaults to the site slug.").
				Value(&productSlug),
			huh.NewInput().Title("Repo name").
				Description("This repo's unique name within the product.").
				Value(&repo),
			huh.NewSelect[string]().Title("Role (informational)").
				Options(
					huh.NewOption("(none)", ""),
					huh.NewOption("api", "api"),
					huh.NewOption("service", "service"),
					huh.NewOption("frontend", "frontend"),
					huh.NewOption("docs", "docs"),
				).Value(&role),
		),
		huh.NewGroup(
			huh.NewInput().Title("Default space").
				Description("Where this repo's pages live.").
				Value(&defaultSpace).Validate(requiredField),
		),
	}

	if len(specs) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("OpenAPI specs to document").
				Description("Each becomes machine-owned `api` blocks authored by `gravity sync`.").
				Options(toOptions(specs)...).
				Value(&selectedSpecs),
			huh.NewInput().Title("API docs space").Value(&apiSpace),
		))
	}
	if len(mds) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Markdown docs to publish as pages").
				Description("Each becomes a native-block page authored by `gravity sync`.").
				Options(toOptions(mds)...).
				Value(&selectedDocs),
		))
	}

	form := huh.NewForm(groups...).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr())
	if err := form.Run(); err != nil {
		return wizardResult{}, false, err
	}

	if strings.TrimSpace(productSlug) == "" {
		productSlug = site
	}
	res := wizardResult{
		Site:        strings.TrimSpace(site),
		APIURL:      strings.TrimSpace(apiURL),
		Space:       strings.TrimSpace(defaultSpace),
		Role:        role,
		Repo:        strings.TrimSpace(repo),
		ProductSlug: strings.TrimSpace(productSlug),
	}
	for _, s := range selectedSpecs {
		res.Sources = append(res.Sources, config.SourceMap{
			Source: s,
			Kind:   "openapi",
			Space:  strings.TrimSpace(apiSpace),
			Page:   specPageSlug(s),
			Title:  "API Reference",
		})
	}
	for _, m := range selectedDocs {
		res.Documents = append(res.Documents, config.DocMap{
			File:      m,
			Page:      slugFromPath(m),
			Ownership: "machine",
			As:        "page",
		})
	}

	// Preview, then a final write confirmation.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n--- %s preview ---\n%s\n", config.ProjectFileName, renderScaffold(scaffoldParams{
		Site:        res.Site,
		APIURL:      firstNonEmpty(res.APIURL, config.DefaultAPIURL),
		Space:       res.Space,
		Role:        res.Role,
		Repo:        res.Repo,
		ProductSlug: res.ProductSlug,
		Sources:     res.Sources,
		Documents:   res.Documents,
	}))

	write := true
	confirmForm := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Write %s?", config.ProjectFileName)).Value(&write),
	)).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr())
	if err := confirmForm.Run(); err != nil {
		return wizardResult{}, false, err
	}
	return res, write, nil
}

func requiredField(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func optionalURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil // defaulted later
	}
	if u, err := url.Parse(s); err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("must be an absolute http(s) URL")
	}
	return nil
}

func toOptions(vals []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(vals))
	for i, v := range vals {
		opts[i] = huh.NewOption(v, v)
	}
	return opts
}

// isInteractive reports whether r is a terminal we can drive a form on. In CI
// or piped input it returns false so init falls back to flags/env.
func isInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

var skipScanDir = map[string]bool{
	"node_modules": true, "vendor": true, "bin": true, "dist": true,
	"build": true, "target": true, "out": true, "testdata": true,
}

// detectDocSources walks dir (bounded) for OpenAPI specs and Markdown files
// worth offering as doc mappings. Hidden, vendored, and build dirs are skipped.
func detectDocSources(dir string) (specs, mds []string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipScanDir[name] || walkDepth(dir, path) > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return nil //nolint:nilerr // skip paths we can't relativize
		}
		rel = filepath.ToSlash(rel)
		name := strings.ToLower(d.Name())
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		switch ext {
		case ".yaml", ".yml", ".json":
			if strings.Contains(stem, "openapi") || strings.Contains(stem, "swagger") {
				specs = append(specs, rel)
			}
		case ".md":
			if name != "changelog.md" {
				mds = append(mds, rel)
			}
		}
		return nil
	})
	sort.Strings(specs)
	sort.Strings(mds)
	if len(specs) > 10 {
		specs = specs[:10]
	}
	if len(mds) > 25 {
		mds = mds[:25]
	}
	return specs, mds
}

func walkDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

// slugFromPath derives a stable page slug from a file path.
func slugFromPath(p string) string {
	base := strings.ToLower(filepath.Base(p))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "readme" {
		return "overview"
	}
	var b strings.Builder
	dash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "page"
	}
	return s
}

// specPageSlug maps a spec path to a conventional api page slug.
func specPageSlug(p string) string {
	s := slugFromPath(p)
	if strings.Contains(s, "openapi") || strings.Contains(s, "swagger") || s == "api" {
		return "api-reference"
	}
	return s
}

type scaffoldParams struct {
	Site        string
	APIURL      string
	Space       string
	Role        string
	Repo        string
	ProductSlug string
	Sources     []config.SourceMap
	Documents   []config.DocMap
}

// projectFromScaffold builds a typed Project mirroring what renderScaffold emits,
// so the result can be validated before it is written to disk.
func projectFromScaffold(p scaffoldParams) *config.Project {
	return &config.Project{
		Version: config.SchemaVersion,
		Site:    p.Site,
		APIURL:  p.APIURL,
		Product: config.Product{Slug: firstNonEmpty(p.ProductSlug, p.Site), Repo: p.Repo, Role: p.Role},
		Spaces:  config.Spaces{Default: firstNonEmpty(p.Space, p.Repo)},
		Sources: p.Sources, Documents: p.Documents,
		ReleaseNotes: config.ReleaseNotes{Space: firstNonEmpty(p.Space, "changelog"), Changelog: "CHANGELOG.md"},
	}
}

const commentedSourcesExample = `# sources:
#   - source: openapi/openapi.yaml   # repo-relative spec or code file
#     kind: openapi                  # openapi | code
#     space: api
#     page: api-reference
#     title: API Reference`

const commentedDocumentsExample = `# documents:
#   - file: docs/getting-started.md
#     page: getting-started
#     ownership: machine             # machine | hybrid | human
#     as: page                       # page | release`

// renderScaffold produces a rich, commented .gravity.yaml. Connection/identity
// sections are filled from params; sources/documents are rendered as active YAML
// when present, or as commented examples when the user declared none.
func renderScaffold(p scaffoldParams) string {
	productSlug := firstNonEmpty(p.ProductSlug, p.Site)
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
%s

# --- Verbatim Markdown documents -> pages or releases ---
%s

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
		renderSourcesBlock(p.Sources),
		renderDocumentsBlock(p.Documents),
		releaseSpace,
		productSlug, p.Repo,
	)
}

func renderSourcesBlock(sources []config.SourceMap) string {
	if len(sources) == 0 {
		return commentedSourcesExample
	}
	var b strings.Builder
	b.WriteString("sources:\n")
	for _, s := range sources {
		fmt.Fprintf(&b, "  - source: %s\n", yamlScalar(s.Source))
		if s.Kind != "" {
			fmt.Fprintf(&b, "    kind: %s\n", yamlScalar(s.Kind))
		}
		if s.Space != "" {
			fmt.Fprintf(&b, "    space: %s\n", yamlScalar(s.Space))
		}
		fmt.Fprintf(&b, "    page: %s\n", yamlScalar(s.Page))
		if s.Title != "" {
			fmt.Fprintf(&b, "    title: %s\n", yamlScalar(s.Title))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderDocumentsBlock(documents []config.DocMap) string {
	if len(documents) == 0 {
		return commentedDocumentsExample
	}
	var b strings.Builder
	b.WriteString("documents:\n")
	for _, d := range documents {
		fmt.Fprintf(&b, "  - file: %s\n", yamlScalar(d.File))
		if d.Space != "" {
			fmt.Fprintf(&b, "    space: %s\n", yamlScalar(d.Space))
		}
		if d.Page != "" {
			fmt.Fprintf(&b, "    page: %s\n", yamlScalar(d.Page))
		}
		if d.Title != "" {
			fmt.Fprintf(&b, "    title: %s\n", yamlScalar(d.Title))
		}
		if d.Ownership != "" {
			fmt.Fprintf(&b, "    ownership: %s\n", yamlScalar(d.Ownership))
		}
		if d.As != "" {
			fmt.Fprintf(&b, "    as: %s\n", yamlScalar(d.As))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// yamlScalar renders s as a safely-quoted YAML scalar (plain when possible).
func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return s
	}
	return strings.TrimSpace(string(out))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
