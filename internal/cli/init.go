package cli

import (
	"context"
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

	"github.com/impulso/gravity-cli/internal/api"
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

			// client is built best-effort for the interactive wizard so it can
			// list the org's sites/spaces; it stays nil offline/unauthenticated.
			var client *api.Client
			interactive := false

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
				interactive = true
				// Best-effort: resolve a token (env or the user-level config only —
				// never the project file) plus the API URL so the wizard can list
				// the org's sites/spaces. A resolve error leaves client nil and the
				// wizard falls back to free-text entry.
				if cfg, cerr := config.Resolve(config.Flags{Token: gf.token, APIURL: apiURL, Site: site, Space: sp}, dir); cerr == nil {
					apiURL = firstNonEmpty(apiURL, cfg.APIURL)
					if cfg.Token != "" && cfg.APIURL != "" {
						client = api.New(cfg.APIURL, cfg.Token)
					}
				}
				params, write, werr := runInitWizard(cmd, client, initSeed{
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

			// On the interactive path, idempotently create the spaces the manifest
			// references so they exist on the platform now (not only after the first
			// `gravity sync`). Best-effort: never fails the command.
			if interactive && client != nil && site != "" {
				ensureDeclaredSpaces(cmd, client, site, params)
			}

			if len(sources) == 0 && len(documents) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No source/document mappings declared yet — edit the file or re-run `gravity init`, then `gravity sync`.")
			} else if interactive {
				fmt.Fprintln(cmd.OutOrStdout(), "Note: `gravity sync` authors content as a draft + open proposal — pages appear under review until approved, not as live pages.")
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

// runInitWizard drives the huh forms, detecting candidate specs/docs in ScanDir
// and offering to map them. When client is non-nil it lists the org's sites and
// the chosen site's spaces so the user selects rather than types them; offline
// it falls back to free-text inputs. The second return is the user's final
// write/abort choice.
func runInitWizard(cmd *cobra.Command, client *api.Client, seed initSeed) (wizardResult, bool, error) {
	ctx := cmd.Context()
	in := cmd.InOrStdin()
	errOut := cmd.ErrOrStderr()

	site := seed.Site
	apiURL := seed.APIURL
	defaultSpace := seed.Space
	role := seed.Role
	repo := seed.Repo
	apiSpace := "api"

	// --- Form 1: pick (or type) the target site. Sites are fetched up front so
	// the picker has real options; errors degrade to a free-text input. ---
	siteFld := siteField(ctx, client, errOut, &site, seed.Site)
	form1 := huh.NewForm(huh.NewGroup(siteFld)).WithInput(in).WithOutput(errOut)
	if err := form1.Run(); err != nil {
		return wizardResult{}, false, err
	}
	if site == siteManualSentinel {
		site = ""
		if err := runInput(cmd, "Site slug", "The Gravity site this repo documents.", &site, requiredField); err != nil {
			return wizardResult{}, false, err
		}
	}
	site = strings.TrimSpace(site)
	productSlug := site // default the product id to the chosen site; editable below

	// With the site known, fetch its spaces so the space picker is real too.
	spaces := fetchSpaces(ctx, client, site, errOut)

	specs, mds := detectDocSources(seed.ScanDir)
	selectedSpecs := append([]string(nil), specs...) // preselect all detected
	selectedDocs := append([]string(nil), mds...)

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().Title("API URL").Value(&apiURL).Validate(optionalURL),
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
			spaceField("Default space", "Where this repo's pages live.", spaces, &defaultSpace, seed.Space),
		),
	}

	if len(specs) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("OpenAPI specs to document").
				Description("Each becomes machine-owned `api` blocks authored by `gravity sync`.").
				Options(toOptions(specs)...).
				Value(&selectedSpecs),
			spaceField("API docs space", "", spaces, &apiSpace, "api"),
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

	form := huh.NewForm(groups...).WithInput(in).WithOutput(errOut)
	if err := form.Run(); err != nil {
		return wizardResult{}, false, err
	}

	// Resolve "new space…" choices into a typed slug.
	if defaultSpace == spaceNewSentinel {
		defaultSpace = seed.Space
		if err := runInput(cmd, "New space slug", "Where this repo's pages live.", &defaultSpace, requiredField); err != nil {
			return wizardResult{}, false, err
		}
	}
	if apiSpace == spaceNewSentinel {
		apiSpace = "api"
		if err := runInput(cmd, "New API docs space slug", "", &apiSpace, requiredField); err != nil {
			return wizardResult{}, false, err
		}
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
			File: m,
			Page: slugFromPath(m),
			// Hand-authored docs are editable in Gravity (seeded once from the repo,
			// then human-owned). Use machine only for a verbatim/code mirror.
			Ownership: "human",
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

// Sentinel option values for the site/space pickers. They can't collide with a
// real slug (slugs are ^[a-z0-9-]+$), so a selected sentinel unambiguously means
// "let me type one instead".
const (
	siteManualSentinel = "\x00manual-site"
	spaceNewSentinel   = "\x00new-space"
)

// siteField returns the site picker: a Select over the org's sites (plus a
// manual-entry sentinel) when they can be listed, otherwise a free-text Input.
// It binds the chosen value into *binding and seeds the default selection.
func siteField(ctx context.Context, client *api.Client, notice io.Writer, binding *string, seed string) huh.Field {
	sites, ok := fetchSites(ctx, client, notice)
	if !ok {
		if *binding == "" {
			*binding = seed
		}
		return huh.NewInput().Title("Site slug").
			Description("The Gravity site this repo documents.").
			Value(binding).Validate(requiredField)
	}
	opts := make([]huh.Option[string], 0, len(sites)+1)
	for _, s := range sites {
		opts = append(opts, huh.NewOption(siteLabel(s), s.Slug))
	}
	opts = append(opts, huh.NewOption("✏️  enter a slug manually", siteManualSentinel))
	*binding = pickDefaultSite(sites, seed)
	return huh.NewSelect[string]().Title("Site").
		Description("The Gravity site this repo documents.").
		Options(opts...).Value(binding)
}

// spaceField returns a space picker: a Select over the site's existing spaces
// (plus a "new space" sentinel) when spaces are known, otherwise a free-text
// Input. seed preselects/prefills the default.
func spaceField(title, desc string, spaces []api.Space, binding *string, seed string) huh.Field {
	if len(spaces) == 0 {
		if *binding == "" {
			*binding = seed
		}
		return huh.NewInput().Title(title).Description(desc).Value(binding).Validate(requiredField)
	}
	opts := make([]huh.Option[string], 0, len(spaces)+1)
	for _, s := range spaces {
		opts = append(opts, huh.NewOption(spaceLabel(s), s.Slug))
	}
	opts = append(opts, huh.NewOption("＋ new space…", spaceNewSentinel))
	if slugInSpaces(seed, spaces) {
		*binding = seed
	} else {
		*binding = spaceNewSentinel
	}
	return huh.NewSelect[string]().Title(title).Description(desc).Options(opts...).Value(binding)
}

// runInput runs a one-field form to collect a single value (used to resolve the
// manual-site / new-space sentinels).
func runInput(cmd *cobra.Command, title, desc string, binding *string, validate func(string) error) error {
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Description(desc).Value(binding).Validate(validate),
	)).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr()).Run()
}

// fetchSites lists the org's sites best-effort. It returns ok=false (after a
// one-line notice) when there's no client, the call fails, or the org has no
// sites — so the caller falls back to free-text entry.
func fetchSites(ctx context.Context, client *api.Client, notice io.Writer) ([]api.SiteSummary, bool) {
	if client == nil {
		fmt.Fprintln(notice, "Not signed in — type values manually. Run `gravity auth login` to pick from your sites.")
		return nil, false
	}
	sites, err := client.Sites(ctx)
	if err != nil {
		fmt.Fprintf(notice, "Couldn't list sites (%v) — type values manually.\n", err)
		return nil, false
	}
	if len(sites) == 0 {
		return nil, false
	}
	return sites, true
}

// fetchSpaces lists a site's spaces best-effort; any failure yields nil so the
// caller falls back to free-text space entry.
func fetchSpaces(ctx context.Context, client *api.Client, site string, notice io.Writer) []api.Space {
	if client == nil || site == "" {
		return nil
	}
	tree, err := client.SiteTree(ctx, site)
	if err != nil {
		fmt.Fprintf(notice, "Couldn't list spaces for %q (%v) — type the space manually.\n", site, err)
		return nil
	}
	return tree.Spaces
}

// ensureDeclaredSpaces idempotently creates (or confirms) every space the new
// manifest references, so a freshly-`init`'d repo's spaces exist on the platform
// immediately rather than only after the first `gravity sync`. Best-effort: a
// permission (403) or not-yet-available failure is reported and skipped, never
// fatal — the written file is the command's real output.
func ensureDeclaredSpaces(cmd *cobra.Command, client *api.Client, site string, p scaffoldParams) {
	out := cmd.OutOrStdout()
	seen := map[string]bool{}
	add := func(s string) []string {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return nil
		}
		seen[s] = true
		return []string{s}
	}
	var slugs []string
	slugs = append(slugs, add(firstNonEmpty(p.Space, p.Repo))...)
	for _, src := range p.Sources {
		slugs = append(slugs, add(src.Space)...)
	}
	for _, slug := range slugs {
		_, err := client.EnsureSpace(cmd.Context(), site, api.SpaceUpsertRequest{Slug: slug})
		if err == nil {
			fmt.Fprintf(out, "Ensured space %q on site %q.\n", slug, site)
			continue
		}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && (apiErr.IsAuth() || apiErr.IsUnavailable()) {
			fmt.Fprintf(out, "Note: couldn't create space %q on %q (%s) — `gravity sync` will create it later.\n", slug, site, apiErr.Message)
			continue
		}
		fmt.Fprintf(out, "Note: couldn't create space %q on %q: %v\n", slug, site, err)
	}
}

func siteLabel(s api.SiteSummary) string {
	if s.Name != "" && s.Name != s.Slug {
		return s.Slug + " — " + s.Name
	}
	return s.Slug
}

func spaceLabel(s api.Space) string {
	if s.Name != "" && s.Name != s.Slug {
		return s.Slug + " — " + s.Name
	}
	return s.Slug
}

// pickDefaultSite chooses the initially-selected site: the seed when it is a
// listed site, otherwise the first site.
func pickDefaultSite(sites []api.SiteSummary, seed string) string {
	for _, s := range sites {
		if s.Slug == seed {
			return seed
		}
	}
	if len(sites) > 0 {
		return sites[0].Slug
	}
	return seed
}

// slugInSpaces reports whether slug names one of the spaces.
func slugInSpaces(slug string, spaces []api.Space) bool {
	if slug == "" {
		return false
	}
	for _, s := range spaces {
		if s.Slug == slug {
			return true
		}
	}
	return false
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
#     ownership: human               # human: editable in Gravity, seeded once (default)
#                                    # machine: verbatim/code mirror, drift-locked, not editable
#                                    # hybrid: repo stays source, machine fields refreshed
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
