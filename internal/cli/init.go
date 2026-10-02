package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

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
		force     bool
		migrate   bool
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a project-local .gravity.yaml",
		Long: `Create a .gravity.yaml in the current directory: a committable, non-secret
manifest describing how this repo feeds the Gravity docs platform — its site,
product/multi-repo identity, default space, and the OpenAPI/Markdown mappings
that ` + "`gravity sync`" + ` authors onto the platform.

Run with no flags for an interactive wizard. With -y/--yes (or --ci, or a
non-interactive shell) it skips the questions: it detects OpenAPI specs and the
repo's documentation Markdown (README.md and docs/**, never AGENTS.md,
CONTRIBUTING, LICENSE, CHANGELOG, .github/** and the like) and maps them for you.
Nothing is created on the platform: ` + "`gravity sync`" + ` creates the spaces.

--dry-run prints the file instead of writing it. --migrate upgrades an existing
file in place without losing any setting (removed keys are dropped and reported).

A token is never written here; provide it via GRAVITY_TOKEN (CI) or
` + "`gravity auth login`" + ` (local). The API URL is only recorded when you pass
--api-url (self-hosted platforms).`,
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
			out := cmd.OutOrStdout()

			explicit := initInputs{
				site:   firstNonEmpty(siteAlias, gf.site, os.Getenv(config.EnvSite)),
				apiURL: firstNonEmpty(urlAlias, gf.apiURL),
				space:  firstNonEmpty(space, os.Getenv(config.EnvSpace)),
				role:   role,
				repo:   filepath.Base(absDir),
			}
			if explicit.apiURL != "" {
				if err := config.CheckAPIURL(explicit.apiURL); err != nil {
					return Fail(CodeError, err)
				}
			}

			if !force && !migrate && !dryRun {
				if _, statErr := os.Stat(path); statErr == nil {
					return Failf(CodeError, "%s already exists; pass --force to overwrite or --migrate to upgrade it", path)
				}
			}

			var proj *config.Project
			var notes []string
			switch {
			case migrate:
				proj, notes, err = migrateManifest(path, explicit)
				if err != nil {
					return Fail(CodeError, err)
				}

			case nonInt || ciMode(*gf) || !isInteractive(cmd.InOrStdin()):
				if explicit.site == "" {
					return Failf(CodeError, "site is required: pass --site (or set %s)", config.EnvSite)
				}
				det := detectDocSources(absDir)
				proj = projectFromDetection(explicit, det)
				notes = detectionNotes(det)

			default:
				var client *api.Client
				if cfg, cerr := config.Resolve(config.Flags{Token: gf.token, APIURL: explicit.apiURL, Site: explicit.site, Space: explicit.space}, dir); cerr == nil {
					if cfg.Token != "" && cfg.APIURL != "" {
						client = api.New(cfg.APIURL, cfg.Token)
					}
				}
				var write bool
				proj, write, err = runInitWizard(cmd, client, explicit, absDir)
				if err != nil {
					return Fail(CodeError, err)
				}
				if !write {
					fmt.Fprintln(out, "Aborted; nothing written.")
					return nil
				}
			}

			if proj.Site == "" {
				return Failf(CodeError, "site is required (pass --site or answer the prompt)")
			}
			if err := validateManifest(proj, absDir, path); err != nil {
				return Fail(CodeError, err)
			}

			content := renderManifest(proj)
			if dryRun {
				fmt.Fprint(out, content)
				for _, n := range notes {
					fmt.Fprintln(cmd.ErrOrStderr(), "note: "+n)
				}
				return nil
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return Fail(CodeError, fmt.Errorf("write %s: %w", path, err))
			}
			fmt.Fprintf(out, "Wrote %s\n", path)
			for _, n := range notes {
				fmt.Fprintln(out, "  "+n)
			}
			if len(proj.Sources) == 0 && len(proj.Documents) == 0 {
				fmt.Fprintln(out, "No OpenAPI spec or documentation Markdown found to map — add `sources`/`documents` to the file, then run `gravity sync`.")
			} else if !migrate {
				fmt.Fprintln(out, "Next: `gravity sync` creates the spaces and authors these pages as a draft + open proposal for review.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "default space to record (default: the repo name)")
	cmd.Flags().StringVar(&role, "role", "", "this repo's role in the product, which sets its default unit kind (api|service|frontend|docs)")
	cmd.Flags().BoolVarP(&nonInt, "yes", "y", false, "non-interactive: detect specs and docs and write without prompting")
	cmd.Flags().StringVar(&outDir, "dir", "", "directory to write .gravity.yaml in (default: cwd)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .gravity.yaml")
	cmd.Flags().BoolVar(&migrate, "migrate", false, "upgrade an existing .gravity.yaml in place, preserving every value")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the file that would be written without writing it")
	cmd.Flags().StringVar(&siteAlias, "site-slug", "", "")
	cmd.Flags().StringVar(&urlAlias, "url", "", "")
	_ = cmd.Flags().MarkHidden("site-slug")
	_ = cmd.Flags().MarkHidden("url")
	return cmd
}

type initInputs struct {
	site   string
	apiURL string
	space  string
	role   string
	repo   string
}

func newManifest(in initInputs, productSlug string) *config.Project {
	p := &config.Project{
		Version: config.SchemaVersion,
		Site:    in.site,
		Product: config.Product{Slug: firstNonEmpty(productSlug, in.site), Repo: in.repo, Role: in.role},
		Spaces:  config.Spaces{Default: firstNonEmpty(in.space, in.repo)},
	}
	if in.apiURL != "" && strings.TrimRight(in.apiURL, "/") != config.DefaultAPIURL {
		p.APIURL = in.apiURL
	}
	return p
}

func projectFromDetection(in initInputs, det docDetection) *config.Project {
	p := newManifest(in, "")
	p.Sources = specSources(det.specs, "")
	p.Documents = docMappings(det.selected)
	return p
}

func detectionNotes(det docDetection) []string {
	var notes []string
	if len(det.specs) > 0 {
		notes = append(notes, fmt.Sprintf("mapped %d OpenAPI spec(s): %s", len(det.specs), strings.Join(det.specs, ", ")))
	}
	if len(det.selected) > 0 {
		notes = append(notes, fmt.Sprintf("mapped %d Markdown doc(s): %s", len(det.selected), strings.Join(det.selected, ", ")))
	}
	if skipped := len(det.docs) - len(det.selected); skipped > 0 {
		notes = append(notes, fmt.Sprintf("%d other Markdown file(s) left unmapped; add them under `documents` if they belong in the docs", skipped))
	}
	return notes
}

func specSources(specs []string, space string) []config.SourceMap {
	out := make([]config.SourceMap, 0, len(specs))
	used := map[string]bool{}
	for _, s := range specs {
		out = append(out, config.SourceMap{
			Source: s,
			Kind:   "openapi",
			Space:  space,
			Page:   uniqueSlug(specPageSlug(s), s, used),
			Title:  "API Reference",
		})
	}
	return out
}

func docMappings(files []string) []config.DocMap {
	out := make([]config.DocMap, 0, len(files))
	used := map[string]bool{}
	for _, f := range files {
		out = append(out, config.DocMap{
			File:      f,
			Page:      uniqueSlug(slugFromPath(f), f, used),
			Ownership: "human",
			As:        "page",
		})
	}
	return out
}

func uniqueSlug(slug, file string, used map[string]bool) string {
	if !used[slug] {
		used[slug] = true
		return slug
	}
	dir := filepath.Base(filepath.Dir(file))
	cand := slugFromPath(dir + "-" + filepath.Base(file))
	for i := 2; used[cand]; i++ {
		cand = fmt.Sprintf("%s-%d", slug, i)
	}
	used[cand] = true
	return cand
}

func validateManifest(p *config.Project, absDir, path string) error {
	clone := *p
	data := renderManifest(&clone)
	parsed, err := config.ParseProject([]byte(data), path)
	if err != nil {
		return err
	}
	parsed.ApplyDefaults(absDir)
	return parsed.Validate(path)
}

func migrateManifest(path string, in initInputs) (*config.Project, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("no %s to migrate in %s", config.ProjectFileName, filepath.Dir(path))
		}
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	p, notes, err := config.ParseProjectForMigration(data, path)
	if err != nil {
		return nil, nil, err
	}
	if p.LegacySpace != "" {
		if p.Spaces.Default == "" {
			p.Spaces.Default = p.LegacySpace
		}
		if p.ReleaseNotes.Space == "" {
			p.ReleaseNotes.Space = p.LegacySpace
		}
		notes = append(notes, fmt.Sprintf("moved the legacy top-level `space: %s` to spaces.default", p.LegacySpace))
		p.LegacySpace = ""
	}
	if p.Version < config.SchemaVersion {
		p.Version = config.SchemaVersion
	}
	if in.site != "" {
		p.Site = in.site
	}
	if in.apiURL != "" {
		p.APIURL = in.apiURL
	}
	if in.space != "" {
		p.Spaces.Default = in.space
	}
	if in.role != "" {
		p.Product.Role = in.role
	}
	return p, notes, nil
}

func runInitWizard(cmd *cobra.Command, client *api.Client, in initInputs, scanDir string) (*config.Project, bool, error) {
	ctx := cmd.Context()
	stdin := cmd.InOrStdin()
	errOut := cmd.ErrOrStderr()

	site := in.site
	defaultSpace := firstNonEmpty(in.space, in.repo)
	role := in.role
	repo := in.repo

	siteFld := siteField(ctx, client, errOut, &site, in.site)
	form1 := huh.NewForm(huh.NewGroup(siteFld)).WithInput(stdin).WithOutput(errOut)
	if err := form1.Run(); err != nil {
		return nil, false, err
	}
	if site == siteManualSentinel {
		site = ""
		if err := runInput(cmd, "Site slug", "The Gravity site this repo documents.", &site, requiredField); err != nil {
			return nil, false, err
		}
	}
	site = strings.TrimSpace(site)
	productSlug := site

	spaces := fetchSpaces(ctx, client, site, errOut)

	det := detectDocSources(scanDir)
	selectedSpecs := append([]string(nil), det.specs...)
	selectedDocs := append([]string(nil), det.selected...)

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().Title("Product slug").
				Description("Logical product id shared by the repos that document one product; defaults to the site slug.").
				Value(&productSlug),
			huh.NewInput().Title("Repo name").
				Description("This repo's unique name within the product.").
				Value(&repo),
			huh.NewSelect[string]().Title("Role").
				Description("What this repo is in the product; it sets the default unit kind `gravity docs generate` documents.").
				Options(
					huh.NewOption("(none — document features)", ""),
					huh.NewOption("api — document services", "api"),
					huh.NewOption("service — document services", "service"),
					huh.NewOption("frontend — document features", "frontend"),
					huh.NewOption("docs — a docs-only repo", "docs"),
				).Value(&role),
		),
		huh.NewGroup(
			spaceField("Default space", "Where this repo's pages live. `gravity sync` creates it if needed.", spaces, &defaultSpace, defaultSpace),
		),
	}

	if len(det.specs) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("OpenAPI specs to document").
				Description("Each becomes machine-owned `api` blocks on an api-reference page in the default space.").
				Options(toOptions(det.specs)...).
				Value(&selectedSpecs),
		))
	}
	if len(det.docs) > 0 {
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Markdown docs to publish as pages").
				Description("README.md and docs/** are preselected; tick anything else that belongs in the docs.").
				Options(toOptions(det.docs)...).
				Value(&selectedDocs),
		))
	}

	form := huh.NewForm(groups...).WithInput(stdin).WithOutput(errOut)
	if err := form.Run(); err != nil {
		return nil, false, err
	}

	if defaultSpace == spaceNewSentinel {
		defaultSpace = firstNonEmpty(in.space, in.repo)
		if err := runInput(cmd, "New space slug", "Where this repo's pages live.", &defaultSpace, requiredField); err != nil {
			return nil, false, err
		}
	}

	res := wizardResult{
		Space: strings.TrimSpace(defaultSpace),
	}
	res.Documents = docMappings(selectedDocs)

	if hierarchySupported(ctx, client) {
		if err := runHierarchySteps(cmd, spaces, &res); err != nil {
			return nil, false, err
		}
	}

	p := newManifest(initInputs{
		site:   site,
		apiURL: in.apiURL,
		space:  res.Space,
		role:   role,
		repo:   firstNonEmpty(strings.TrimSpace(repo), in.repo),
	}, strings.TrimSpace(productSlug))
	p.Sources = specSources(selectedSpecs, "")
	p.Documents = res.Documents
	p.Spaces.Parent = res.Parent
	p.Spaces.Home = res.Home
	if res.Shared && res.Space != "" {
		p.Spaces.Shared = []string{res.Space}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\n--- %s preview ---\n%s\n", config.ProjectFileName, renderManifest(p))

	write := true
	confirmForm := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Write %s?", config.ProjectFileName)).Value(&write),
	)).WithInput(stdin).WithOutput(errOut)
	if err := confirmForm.Run(); err != nil {
		return nil, false, err
	}
	return p, write, nil
}

type wizardResult struct {
	Space     string
	Parent    string
	Home      string
	Shared    bool
	Documents []config.DocMap
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

var excludedDocPrefixes = []string{
	"agents", "claude", "contributing", "code_of_conduct", "code-of-conduct",
	"license", "security", "changelog", "history", "pull_request_template", "issue_template",
	"licence", //nolint:misspell // British spelling of LICENSE files
}

const (
	maxDetectedSpecs = 10
	maxDetectedDocs  = 25
	maxSelectedDocs  = 15
)

type docDetection struct {
	specs    []string
	docs     []string
	selected []string
}

func detectDocSources(dir string) docDetection {
	var det docDetection
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
				det.specs = append(det.specs, rel)
			}
		case ".md", ".markdown":
			if !excludedDoc(stem) {
				det.docs = append(det.docs, rel)
			}
		}
		return nil
	})
	sort.Strings(det.specs)
	sort.SliceStable(det.docs, func(i, j int) bool {
		ri, rj := docRank(det.docs[i]), docRank(det.docs[j])
		if ri != rj {
			return ri < rj
		}
		return det.docs[i] < det.docs[j]
	})
	if len(det.specs) > maxDetectedSpecs {
		det.specs = det.specs[:maxDetectedSpecs]
	}
	if len(det.docs) > maxDetectedDocs {
		det.docs = det.docs[:maxDetectedDocs]
	}
	for _, d := range det.docs {
		if sensibleDoc(d) && len(det.selected) < maxSelectedDocs {
			det.selected = append(det.selected, d)
		}
	}
	return det
}

func excludedDoc(stem string) bool {
	for _, p := range excludedDocPrefixes {
		if stem == p || strings.HasPrefix(stem, p+"-") || strings.HasPrefix(stem, p+"_") || strings.HasPrefix(stem, p+".") {
			return true
		}
	}
	return false
}

func sensibleDoc(rel string) bool {
	lower := strings.ToLower(rel)
	if lower == "readme.md" {
		return true
	}
	return strings.HasPrefix(lower, "docs/") || strings.HasPrefix(lower, "doc/")
}

func docRank(rel string) int {
	switch {
	case strings.EqualFold(rel, "README.md"):
		return 0
	case sensibleDoc(rel):
		return 1
	default:
		return 2
	}
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
