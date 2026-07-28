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

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
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

			site := firstNonEmpty(siteAlias, gf.site, os.Getenv(config.EnvSite))
			apiURL := firstNonEmpty(urlAlias, gf.apiURL, os.Getenv(config.EnvAPIURL))
			sp := firstNonEmpty(space, os.Getenv(config.EnvSpace))
			repo := filepath.Base(absDir)
			productSlug := ""
			parent, home := "", ""
			var shared []string

			var sources []config.SourceMap
			var documents []config.DocMap

			var client *api.Client
			interactive := false

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
				parent = existing.Spaces.Parent
				home = existing.Spaces.Home
				shared = existing.Spaces.Shared
				role = firstNonEmpty(role, existing.Product.Role)
				productSlug = existing.Product.Slug
				if existing.Product.Repo != "" {
					repo = existing.Product.Repo
				}
				sources = existing.Sources
				documents = existing.Documents

			case nonInt || !isInteractive(cmd.InOrStdin()):

			default:
				interactive = true
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
				parent, home = params.Parent, params.Home
				if params.Shared && sp != "" {
					shared = []string{sp}
				}
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
				Parent:      parent,
				Home:        home,
				Shared:      shared,
				Role:        role,
				Repo:        repo,
				ProductSlug: firstNonEmpty(productSlug, site),
				Sources:     sources,
				Documents:   documents,
			}
			if err := projectFromScaffold(params).Validate(path); err != nil {
				return Fail(CodeError, err)
			}

			content := renderScaffold(params)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return Fail(CodeError, fmt.Errorf("write %s: %w", path, err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)

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
	cmd.Flags().StringVar(&siteAlias, "site-slug", "", "")
	cmd.Flags().StringVar(&urlAlias, "url", "", "")
	_ = cmd.Flags().MarkHidden("site-slug")
	_ = cmd.Flags().MarkHidden("url")
	return cmd
}

type initSeed struct {
	Site    string
	APIURL  string
	Space   string
	Role    string
	Repo    string
	ScanDir string
}

type wizardResult struct {
	Site        string
	APIURL      string
	Space       string
	Parent      string
	Home        string
	Shared      bool
	Role        string
	Repo        string
	ProductSlug string
	Sources     []config.SourceMap
	Documents   []config.DocMap
}

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
	productSlug := site

	spaces := fetchSpaces(ctx, client, site, errOut)

	specs, mds := detectDocSources(seed.ScanDir)
	selectedSpecs := append([]string(nil), specs...)
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
			File:      m,
			Page:      slugFromPath(m),
			Ownership: "human",
			As:        "page",
		})
	}

	if hierarchySupported(ctx, client) {
		if err := runHierarchySteps(cmd, spaces, &res); err != nil {
			return wizardResult{}, false, err
		}
	}

	out := cmd.OutOrStdout()
	var sharedList []string
	if res.Shared && res.Space != "" {
		sharedList = []string{res.Space}
	}
	fmt.Fprintf(out, "\n--- %s preview ---\n%s\n", config.ProjectFileName, renderScaffold(scaffoldParams{
		Site:        res.Site,
		APIURL:      firstNonEmpty(res.APIURL, config.DefaultAPIURL),
		Space:       res.Space,
		Parent:      res.Parent,
		Home:        res.Home,
		Shared:      sharedList,
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

const (
	siteManualSentinel = "\x00manual-site"
	spaceNewSentinel   = "\x00new-space"
)

func hierarchySupported(ctx context.Context, client *api.Client) bool {
	if client == nil {
		return false
	}
	who, err := client.WhoAmI(ctx)
	return err == nil && who.Features[featureSpaceHierarchy]
}

func runHierarchySteps(cmd *cobra.Command, spaces []api.Space, res *wizardResult) error {
	var parentOpts []huh.Option[string]
	parentOpts = append(parentOpts, huh.NewOption("(none — top-level space)", ""))
	seedParent := ""
	for _, s := range spaces {
		if s.ParentSpaceID != nil || s.Slug == res.Space {
			continue
		}
		parentOpts = append(parentOpts, huh.NewOption(spaceLabel(s), s.Slug))
	}
	if cur := findSpace(spaces, res.Space); cur != nil && cur.ParentSpaceID != nil {
		for _, s := range spaces {
			if s.ID == *cur.ParentSpaceID {
				seedParent = s.Slug
			}
		}
	}
	parentOpts = append(parentOpts, huh.NewOption("＋ new parent space…", spaceNewSentinel))
	parent := seedParent

	shared := res.Shared
	fields := []huh.Field{
		huh.NewSelect[string]().Title("Parent space").
			Description(fmt.Sprintf("Nest %q as a subspace of a product space (e.g. modules of one platform).", res.Space)).
			Options(parentOpts...).Value(&parent),
		huh.NewConfirm().Title("Shared space?").
			Description("Do sibling repos of this product also publish into this space?\nTheir pages are then kept apart per repo (a collection per repo).").
			Value(&shared),
	}

	home := false
	homeSlug := homeCandidate(res.Documents)
	if homeSlug != "" {
		fields = append(fields, huh.NewConfirm().
			Title(fmt.Sprintf("Pin %q as the space home page?", homeSlug)).
			Description("Readers landing on the space see this page first.").
			Value(&home))
	}

	form := huh.NewForm(huh.NewGroup(fields...)).
		WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr())
	if err := form.Run(); err != nil {
		return err
	}
	if parent == spaceNewSentinel {
		parent = ""
		if err := runInput(cmd, "New parent space slug", "The top-level product space to nest under.", &parent, requiredField); err != nil {
			return err
		}
	}
	res.Parent = strings.TrimSpace(parent)
	res.Shared = shared
	if home {
		res.Home = homeSlug
	}
	return nil
}

func homeCandidate(docs []config.DocMap) string {
	for _, d := range docs {
		if d.Page == "overview" {
			return "overview"
		}
	}
	if len(docs) == 1 && docs[0].Page != "" {
		return docs[0].Page
	}
	return ""
}

func findSpace(spaces []api.Space, slug string) *api.Space {
	for i := range spaces {
		if spaces[i].Slug == slug {
			return &spaces[i]
		}
	}
	return nil
}

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

func spaceField(title, desc string, spaces []api.Space, binding *string, seed string) huh.Field {
	if len(spaces) == 0 {
		if *binding == "" {
			*binding = seed
		}
		return huh.NewInput().Title(title).Description(desc).Value(binding).Validate(requiredField)
	}
	opts := make([]huh.Option[string], 0, len(spaces)+1)
	for _, s := range orderSpacesForPicker(spaces) {
		label := spaceLabel(s)
		if s.ParentSpaceID != nil {
			label = "  └ " + label
		}
		opts = append(opts, huh.NewOption(label, s.Slug))
	}
	opts = append(opts, huh.NewOption("＋ new space…", spaceNewSentinel))
	if slugInSpaces(seed, spaces) {
		*binding = seed
	} else {
		*binding = spaceNewSentinel
	}
	return huh.NewSelect[string]().Title(title).Description(desc).Options(opts...).Value(binding)
}

func orderSpacesForPicker(spaces []api.Space) []api.Space {
	out := make([]api.Space, 0, len(spaces))
	emitted := make(map[string]bool, len(spaces))
	for _, s := range spaces {
		if s.ParentSpaceID != nil {
			continue
		}
		out = append(out, s)
		emitted[s.ID] = true
		for _, c := range spaces {
			if c.ParentSpaceID != nil && *c.ParentSpaceID == s.ID {
				out = append(out, c)
				emitted[c.ID] = true
			}
		}
	}
	for _, s := range spaces {
		if !emitted[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func runInput(cmd *cobra.Command, title, desc string, binding *string, validate func(string) error) error {
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Description(desc).Value(binding).Validate(validate),
	)).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr()).Run()
}

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

func ensureDeclaredSpaces(cmd *cobra.Command, client *api.Client, site string, p scaffoldParams) {
	out := cmd.OutOrStdout()
	withParent := p.Parent != "" && hierarchySupported(cmd.Context(), client)

	type ensure struct{ slug, parent string }
	seen := map[string]bool{}
	var plan []ensure
	add := func(slug, parent string) {
		slug = strings.TrimSpace(slug)
		if slug == "" || seen[slug] {
			return
		}
		seen[slug] = true
		plan = append(plan, ensure{slug: slug, parent: parent})
	}
	defaultSpace := firstNonEmpty(p.Space, p.Repo)
	if withParent {
		add(p.Parent, "")
		add(defaultSpace, p.Parent)
	} else {
		add(defaultSpace, "")
	}
	for _, src := range p.Sources {
		add(src.Space, "")
	}
	for _, e := range plan {
		_, err := client.EnsureSpace(cmd.Context(), site, api.SpaceUpsertRequest{Slug: e.slug, Parent: e.parent})
		if err == nil {
			if e.parent != "" {
				fmt.Fprintf(out, "Ensured space %q (subspace of %q) on site %q.\n", e.slug, e.parent, site)
			} else {
				fmt.Fprintf(out, "Ensured space %q on site %q.\n", e.slug, site)
			}
			continue
		}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && (apiErr.IsAuth() || apiErr.IsUnavailable()) {
			fmt.Fprintf(out, "Note: couldn't create space %q on %q (%s) — `gravity sync` will create it later.\n", e.slug, site, apiErr.Message)
			continue
		}
		fmt.Fprintf(out, "Note: couldn't create space %q on %q: %v\n", e.slug, site, err)
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
		return nil
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

func detectDocSources(dir string) (specs, mds []string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries
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
			return nil //nolint:nilerr // skip unrelativizable paths
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
	Parent      string
	Home        string
	Shared      []string
	Role        string
	Repo        string
	ProductSlug string
	Sources     []config.SourceMap
	Documents   []config.DocMap
}

func projectFromScaffold(p scaffoldParams) *config.Project {
	return &config.Project{
		Version: config.SchemaVersion,
		Site:    p.Site,
		APIURL:  p.APIURL,
		Product: config.Product{Slug: firstNonEmpty(p.ProductSlug, p.Site), Repo: p.Repo, Role: p.Role},
		Spaces: config.Spaces{
			Default: firstNonEmpty(p.Space, p.Repo),
			Parent:  p.Parent,
			Home:    p.Home,
			Shared:  p.Shared,
		},
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

func renderScaffold(p scaffoldParams) string {
	productSlug := firstNonEmpty(p.ProductSlug, p.Site)
	defaultSpace := firstNonEmpty(p.Space, p.Repo)
	releaseSpace := firstNonEmpty(p.Space, "changelog")
	roleLine := "  # role: api            # informational: api | service | frontend | docs"
	if p.Role != "" {
		roleLine = "  role: " + p.Role
	}

	return fmt.Sprintf(
		`# .gravity.yaml — committed, non-secret CI config for the Gravity docs platform.
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
%s

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
		renderSpacesBlock(defaultSpace, p.Parent, p.Home, p.Shared),
		renderSourcesBlock(p.Sources),
		renderDocumentsBlock(p.Documents),
		releaseSpace,
		productSlug, p.Repo,
	)
}

func renderSpacesBlock(defaultSpace, parent, home string, shared []string) string {
	var b strings.Builder
	b.WriteString("spaces:\n")
	fmt.Fprintf(&b, "  default: %s\n", yamlScalar(defaultSpace))
	if parent != "" {
		fmt.Fprintf(&b, "  parent: %s\n", yamlScalar(parent))
	} else {
		b.WriteString("  # parent: platform      # nest `default` as a subspace of this top-level space\n")
	}
	if home != "" {
		fmt.Fprintf(&b, "  home: %s\n", yamlScalar(home))
	} else {
		b.WriteString("  # home: overview        # page slug pinned as the space's home page\n")
	}
	if len(shared) > 0 {
		quoted := make([]string, len(shared))
		for i, s := range shared {
			quoted[i] = yamlScalar(s)
		}
		fmt.Fprintf(&b, "  shared: [%s]\n", strings.Join(quoted, ", "))
	} else {
		b.WriteString("  # shared: [changelog]   # spaces co-fed by sibling repos (pages grouped per repo)")
	}
	return strings.TrimRight(b.String(), "\n")
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
