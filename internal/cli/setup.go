package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/setup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type setupOptions struct {
	yes         bool
	product     string
	site        string
	noStructure bool
	noRun       bool
	noCI        bool
}

type setupManifest struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content"`
}

type setupData struct {
	Product   string            `json:"product"`
	Site      api.StructureSite `json:"site"`
	Structure *api.Structure    `json:"structure"`
	Passes    []config.Pass     `json:"passes"`
	Kept      []string          `json:"kept"`
	Manifest  setupManifest     `json:"manifest"`
	Written   []string          `json:"written"`
	Issues    []issue           `json:"issues"`
	Detected  *detect.Result    `json:"detected"`
	SignedIn  bool              `json:"signedIn"`
	Proposal  bool              `json:"proposal"`
	Granted   []string          `json:"granted,omitempty"`
}

const (
	answerNew  = "__new__"
	answerSkip = "__skip__"
)

func newSetupCmd(a *app) *cobra.Command {
	var o setupOptions
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set this repository up: product, site, structure, passes, .gravity.yaml, then a dry run and CI",
		Long: "The guided front door. setup signs you in if needed, detects the repository (languages, packages, READMEs and docs folders, OpenAPI specs, changelog, CI), asks for the product and the site, drafts the docs structure, suggests passes, writes .gravity.yaml, validates it and shows the result.\n" +
			"Then it offers to create the structure in Gravity, to dry-run the passes and to wire CI. Re-running it is safe: what .gravity.yaml already declares is kept and only gaps are filled.\n" +
			"Without a terminal pass --yes to accept every suggestion. --json alone prints the proposal and writes nothing; --json --yes writes it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runSetup(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.yes, "yes", "y", false, "accept every suggestion without asking (needed without a terminal)")
	f.StringVar(&o.product, "product", "", "product slug this repository belongs to")
	f.StringVar(&o.site, "site", "", "site slug the docs go to")
	f.BoolVar(&o.noStructure, "no-structure", false, "do not draft a structure")
	f.BoolVar(&o.noRun, "no-run", false, "do not offer a dry run at the end")
	f.BoolVar(&o.noCI, "no-ci", false, "do not offer to wire CI at the end")
	return cmd
}

type setupRun struct {
	a        *app
	o        setupOptions
	ctx      context.Context
	info     *repoInfo
	path     string
	existing *config.Manifest
	client   *api.Client
	who      *api.WhoAmI
	products []api.ProductSummary
	sites    []api.Site
	layout   setup.Layout
	data     setupData
	auto     bool
}

func (a *app) runSetup(ctx context.Context, o setupOptions) error {
	if o.product != "" && normalize.ProductSlug(o.product) != o.product {
		return Failf(CodeError, "--product %q must be a lowercase slug (try %q)", o.product, normalize.ProductSlug(o.product))
	}
	proposal := a.ui.JSON() && !o.yes
	if !a.ui.Interactive() && !o.yes && !proposal {
		return &ExitError{Code: CodeError, ErrCode: "needs_terminal", Err: errors.New("setup asks a few questions and needs a terminal; pass --yes to accept the suggestions, or --json to print the proposal")}
	}
	r := &setupRun{a: a, o: o, ctx: ctx, auto: o.yes || !a.ui.Interactive(), data: setupData{Passes: []config.Pass{}, Kept: []string{}, Written: []string{}, Issues: []issue{}, Proposal: proposal}}
	steps := []func() error{r.load, r.signIn, r.detect, r.chooseProduct, r.chooseSite, r.compose}
	for _, step := range steps {
		if err := step(); err != nil {
			if errors.Is(err, ui.ErrAborted) {
				a.ui.Println("Canceled; nothing was written.")
				return a.ui.Result(r.data)
			}
			return err
		}
	}
	if proposal {
		return a.ui.Result(r.data)
	}
	if err := r.write(); err != nil {
		if errors.Is(err, ui.ErrAborted) {
			a.ui.Println("Canceled; nothing was written.")
			return a.ui.Result(r.data)
		}
		return err
	}
	return r.finish()
}

func (r *setupRun) load() error {
	info, err := r.a.inspectRepo(r.ctx)
	if err != nil {
		return err
	}
	r.info = info
	r.path = config.ManifestPath(info.root, r.a.manifestOverride())
	r.data.Manifest.Path = relPath(info.root, r.path)
	m, err := config.Load(r.path)
	if err != nil {
		return Fail(CodeError, fmt.Errorf("%w; fix it, or move it away and run gravity setup again", err))
	}
	r.existing = m
	if m != nil && r.o.product != "" && m.Product != "" && m.Product != r.o.product {
		return Failf(CodeError, "%s declares product %s; edit it or drop --product", r.data.Manifest.Path, m.Product)
	}
	return nil
}

func (r *setupRun) signIn() error {
	a := r.a
	apiURL := ""
	if r.existing != nil {
		apiURL = r.existing.APIURL
	}
	creds, err := a.credentials(apiURL)
	if err != nil {
		return err
	}
	if creds.Token == "" && a.ui.Interactive() {
		if creds.APIURLSource == auth.SourceManifest && !auth.SameAPIURL(creds.APIURL, config.DefaultAPIURL) {
			return &ExitError{Code: CodeError, ErrCode: "manifest_api_url", Err: fmt.Errorf("not signed in, and %s points at %s; gravity only signs you in to a host you name yourself: run `gravity login --api-url %s` if you trust it", r.data.Manifest.Path, creds.APIURL, creds.APIURL)}
		}
		if _, err := a.login(r.ctx, loginOptions{}); err != nil {
			return err
		}
		if creds, err = a.credentials(apiURL); err != nil {
			return err
		}
	}
	if creds.Token == "" {
		a.ui.Warn("not_signed_in", "not signed in: setup proposes from the repository alone (run `gravity login` for products, sites and server checks)")
		return nil
	}
	r.client = a.client(creds)
	who, err := r.client.WhoAmI(r.ctx)
	if err != nil {
		return explainAPI(err)
	}
	if err := requirePipelines(who.Features); err != nil {
		return err
	}
	r.who = who
	r.data.SignedIn = true
	a.ui.Note("", ui.MarkOK, "signed in as %s", accountLabel(who))
	if who.Principal == nil || who.Principal.Kind != api.TokenKindRepo {
		if r.products, err = r.client.Products(r.ctx); err != nil {
			if api.IsLicenseError(err) {
				return explainAPI(err)
			}
			a.ui.Debugf("products: %v", err)
		}
		if r.sites, err = r.client.Sites(r.ctx); err != nil {
			a.ui.Debugf("sites: %v", err)
		}
	}
	return nil
}

func (r *setupRun) detect() error {
	l, err := r.a.layout(r.ctx, r.info.root, true)
	if err != nil {
		return err
	}
	r.layout = l
	r.data.Detected = l.Detect
	r.a.ui.Note("", ui.MarkOK, "%s", detectionLine(r.info.remoteKey, l.Detect, setup.DocsFiles(l.Files)))
	return nil
}

func detectionLine(remoteKey string, d *detect.Result, docsFiles []config.VerbatimFile) string {
	parts := []string{remoteKey}
	for i, l := range d.Languages {
		if i >= 2 {
			break
		}
		parts = append(parts, l.Name)
	}
	if n := len(d.OpenAPI); n > 0 {
		ops := 0
		for _, o := range d.OpenAPI {
			ops += o.Operations
		}
		parts = append(parts, fmt.Sprintf("%s (%d operations)", plural(n, "OpenAPI document", "OpenAPI documents"), ops))
	}
	pkgs := 0
	for _, f := range docsFiles {
		if strings.HasSuffix(f.Include, "/README.md") {
			pkgs++
		}
	}
	if pkgs > 0 {
		parts = append(parts, plural(pkgs, "package README", "package READMEs"))
	}
	if d.UIRoutes > 0 {
		parts = append(parts, fmt.Sprintf("%s UI (%d routes)", d.UIFramework, d.UIRoutes))
	}
	if d.MarkdownFiles > 0 {
		parts = append(parts, plural(d.MarkdownFiles, "Markdown doc", "Markdown docs"))
	}
	if d.ReleaseTags > 0 {
		parts = append(parts, plural(d.ReleaseTags, "release", "releases"))
	}
	for _, c := range d.CI {
		parts = append(parts, cisetup.Label(c))
	}
	return strings.Join(parts, " · ")
}

func (r *setupRun) chooseProduct() error {
	a := r.a
	switch {
	case r.o.product != "":
		r.data.Product = r.o.product
	case r.existing != nil && r.existing.Product != "":
		r.data.Product = r.existing.Product
	case r.who != nil && r.who.Principal != nil && r.who.Principal.Repo != nil && r.who.Principal.Repo.Product != nil:
		r.data.Product = r.who.Principal.Repo.Product.Slug
	default:
		options := setup.RankProducts(r.products, r.info.remoteKey, r.info.name)
		r.data.Product = options[0].Slug
		if len(r.products) > 0 && !r.auto {
			choices := make([]ui.Choice, 0, len(options))
			for _, o := range options {
				choices = append(choices, ui.Choice{Key: o.Slug, Label: o.Label()})
			}
			slug, err := a.prompter().Select("Product", "Which product does "+r.info.name+" document?", choices, options[0].Slug)
			if err != nil {
				return err
			}
			r.data.Product = slug
		}
	}
	a.ui.Note("", ui.MarkOK, "product %s", r.data.Product)
	return nil
}

func (r *setupRun) productOption() setup.ProductOption {
	for _, p := range r.products {
		if p.Slug == r.data.Product {
			return setup.ProductOption{Slug: p.Slug, Name: p.Name}
		}
	}
	return setup.ProductOption{Slug: r.data.Product, Name: humanize(r.data.Product), New: true}
}

func (r *setupRun) chooseSite() error {
	a := r.a
	switch {
	case r.o.site != "":
		r.data.Site = api.StructureSite{Slug: r.o.site}
	case r.existing != nil && r.existing.Structure != nil:
		r.data.Site = r.existing.Structure.Site
	default:
		def := setup.DefaultSite(r.productOption(), r.products, r.sites)
		r.data.Site = api.StructureSite{Slug: def.Slug, Name: def.Name}
		if !r.auto && len(r.sites) > 0 {
			choices := make([]ui.Choice, 0, len(r.sites)+1)
			newSlug := r.data.Product
			exists := false
			for _, s := range r.sites {
				choices = append(choices, ui.Choice{Key: s.Slug, Label: firstNonEmpty(s.Name, s.Slug)})
				exists = exists || s.Slug == newSlug
			}
			if !exists {
				choices = append(choices, ui.Choice{Key: answerNew, Label: "New site: " + humanize(newSlug)})
			}
			key, err := a.prompter().Select("Site", "Where should the docs of "+r.info.name+" live?", choices, def.Slug)
			if err != nil {
				return err
			}
			if key == answerNew {
				r.data.Site = api.StructureSite{Slug: newSlug, Name: humanize(newSlug)}
			} else {
				for _, s := range r.sites {
					if s.Slug == key {
						r.data.Site = api.StructureSite{Slug: s.Slug, Name: firstNonEmpty(s.Name, s.Slug)}
					}
				}
			}
		}
	}
	for _, s := range r.sites {
		if s.Slug == r.data.Site.Slug && r.data.Site.Name == "" {
			r.data.Site.Name = s.Name
		}
	}
	if r.data.Site.Name == "" {
		r.data.Site.Name = humanize(r.data.Site.Slug)
	}
	a.ui.Note("", ui.MarkOK, "site %s (%s)", r.data.Site.Name, r.data.Site.Slug)
	return nil
}

func (r *setupRun) compose() error {
	a := r.a
	p := a.ui
	var declared *api.Structure
	if r.existing != nil {
		declared = r.existing.Structure
	}
	live := a.liveStructure(r.ctx, r.client, r.data.Site.Slug)
	var draft setup.Draft
	if !r.o.noStructure {
		draft = setup.DraftStructure(setup.DraftInput{Site: r.data.Site, Declared: declared, Live: live, Layouts: []setup.Layout{r.layout}})
		st := draft.Structure
		r.data.Structure = &st
	} else if declared != nil {
		r.data.Structure = declared
	}
	existingNames := map[string]bool{}
	if r.existing != nil {
		for _, ep := range r.existing.Passes {
			existingNames[ep.Name] = true
			r.data.Kept = append(r.data.Kept, ep.Name)
		}
	}
	site := setup.Site{Slug: r.data.Site.Slug, Name: r.data.Site.Name, New: live == nil}
	var tree *api.SiteTree
	if r.client != nil && live != nil {
		tree, _ = r.client.SiteTree(r.ctx, r.data.Site.Slug)
	}
	memory := r.who != nil && r.who.Modules["memory"]
	sugg := setup.Suggest(setup.Inputs{Detect: r.layout.Detect, Site: site, Tree: tree, MemoryModule: memory})
	var candidates []setup.Suggestion
	if draft.Pass != nil && !hasVerbatim(r.existing) {
		candidates = append(candidates, setup.Suggestion{Pass: *draft.Pass, Target: r.data.Site.Name + " › docs", Source: "README and docs files (verbatim, locked)", Selected: true})
	}
	for _, s := range sugg {
		if s.Pass.Kind == config.KindVerbatim && draft.Pass != nil {
			continue
		}
		candidates = append(candidates, s)
	}
	var fresh []setup.Suggestion
	for _, c := range candidates {
		if !existingNames[c.Pass.Name] {
			fresh = append(fresh, c)
		}
	}
	if r.data.Structure != nil && !r.o.noStructure {
		p.Section("Proposed structure", "spaces, collections and pages for site "+r.data.Site.Slug)
		p.Tree("  ", structureTree(p, r.data.Structure, nil))
		if !r.auto {
			ans, err := a.prompter().Select("Use this structure?", "you can edit structure: in "+r.data.Manifest.Path+" afterwards", []ui.Choice{{Key: "use", Label: "Use it"}, {Key: answerSkip, Label: "Skip the structure for now"}}, "use")
			if err != nil {
				return err
			}
			if ans == answerSkip {
				r.data.Structure = declared
			}
		}
	}
	chosen := selected(fresh, nil)
	if len(fresh) > 0 && !r.auto {
		choices := make([]ui.Choice, 0, len(fresh))
		var pre []string
		for _, s := range fresh {
			label := fmt.Sprintf("%-18s %-10s %s", s.Pass.Name, s.Pass.Kind, s.Target)
			if s.Source != "" {
				label += "  ← " + s.Source
			}
			choices = append(choices, ui.Choice{Key: s.Pass.Name, Label: label})
			if s.Selected {
				pre = append(pre, s.Pass.Name)
			}
		}
		picked, err := a.prompter().MultiSelect("Which passes should keep the docs up to date?", "verbatim imports files as they are; guides and changelogs are written by AI and reviewed", choices, pre)
		if err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, k := range picked {
			keep[k] = true
		}
		chosen = selected(fresh, keep)
	}
	for _, s := range chosen {
		r.data.Passes = append(r.data.Passes, s.Pass)
		if r.data.Structure != nil && s.Create != nil {
			ensureStructureSpace(r.data.Structure, api.StructureSpace{Slug: s.Create.Space, Name: s.Create.Name, Type: s.Create.Type, Visibility: visibilityOf(s.Create.Visibility)})
		}
	}
	return r.render()
}

func visibilityOf(v string) string {
	if v == "private" || v == "public" {
		return v
	}
	return "public"
}

func ensureStructureSpace(st *api.Structure, sp api.StructureSpace) {
	for _, have := range st.Spaces {
		if have.Slug == sp.Slug {
			return
		}
	}
	st.Spaces = append(st.Spaces, sp)
}

func hasVerbatim(m *config.Manifest) bool {
	if m == nil {
		return false
	}
	for _, p := range m.Passes {
		if p.Kind == config.KindVerbatim {
			return true
		}
	}
	return false
}

func selected(list []setup.Suggestion, keep map[string]bool) []setup.Suggestion {
	var out []setup.Suggestion
	for _, s := range list {
		if (keep == nil && s.Selected) || keep[s.Pass.Name] {
			out = append(out, s)
		}
	}
	return out
}

func (r *setupRun) render() error {
	var data []byte
	action := "create"
	if r.existing != nil {
		data = r.existing.YAML
		action = "keep"
	} else {
		data = []byte("version: 2\n")
	}
	out := data
	var err error
	if r.existing == nil || r.existing.Product == "" {
		if out, err = config.SetKey(out, "product", r.data.Product); err != nil {
			return Fail(CodeError, err)
		}
	}
	if r.existing == nil {
		if paths := r.layout.Detect.OpenAPIPaths(); len(paths) > 0 {
			if out, err = config.SetKey(out, "code", config.Code{OpenAPI: paths}); err != nil {
				return Fail(CodeError, err)
			}
		}
	}
	if r.data.Structure != nil && (r.existing == nil || r.existing.Structure == nil || !r.o.noStructure) {
		if out, err = config.SetKey(out, "structure", r.data.Structure); err != nil {
			return Fail(CodeError, err)
		}
	}
	for _, p := range r.data.Passes {
		if out, err = config.UpsertPass(out, p); err != nil {
			return Fail(CodeError, err)
		}
	}
	if _, err := config.Parse(out); err != nil {
		return Fail(CodeError, fmt.Errorf("the manifest setup would write is invalid: %w", err))
	}
	if r.existing != nil && string(out) != string(r.existing.YAML) {
		action = "update"
	}
	r.data.Manifest.Action = action
	r.data.Manifest.Content = string(out)
	return nil
}

func (r *setupRun) write() error {
	a := r.a
	p := a.ui
	m := r.data.Manifest
	if m.Action == "keep" {
		p.Note("", ui.MarkOK, "%s already declares everything setup would add", m.Path)
	} else {
		verb := map[string]string{"create": "Create", "update": "Update"}[m.Action]
		p.Section(verb+" "+m.Path, plural(strings.Count(m.Content, "\n"), "line", "lines"))
		for _, line := range strings.Split(strings.TrimRight(m.Content, "\n"), "\n") {
			p.Println("  %s", p.Dim(line))
		}
		if !r.auto {
			ans, err := a.prompter().Select(verb+" "+m.Path+"?", "nothing is committed or pushed", []ui.Choice{{Key: "write", Label: "Write it"}, {Key: "cancel", Label: "Cancel"}}, "write")
			if err != nil {
				return err
			}
			if ans != "write" {
				return ui.ErrAborted
			}
		}
		if err := os.WriteFile(r.path, []byte(m.Content), 0o644); err != nil {
			return Fail(CodeError, err)
		}
		r.data.Written = append(r.data.Written, m.Path)
		p.Note("", ui.MarkOK, "wrote %s", m.Path)
	}
	if r.client != nil {
		man, err := config.Load(r.path)
		if err == nil {
			c, _ := a.detectCI(r.ctx, r.info.repo)
			req := a.connectRequest(r.info, man, api.ContextInit, origin(c), false)
			req.Product = r.data.Product
			if conn, err := r.client.Connect(r.ctx, req); err != nil {
				a.ui.Warn("connect_failed", "could not register the repository yet: "+err.Error())
			} else if conn.Repo.Created {
				p.Note("", ui.MarkOK, "registered %s under %s", r.info.name, firstNonEmpty(conn.Repo.Product.Name, conn.Repo.Product.Slug))
			}
		}
	}
	return nil
}

func (r *setupRun) finish() error {
	a := r.a
	p := a.ui
	m, err := a.collect(r.ctx, modelOptions{server: r.client != nil, plan: r.client != nil})
	if err != nil {
		return err
	}
	r.data.Issues = m.issues
	if m.manifest != nil {
		renderShow(p, a.showData(m, nil))
	}
	if m.errors() > 0 {
		renderIssues(p, m.issues, nil)
	}
	interactive := p.Interactive() && !r.o.yes
	next := []string{}
	if m.manifest != nil && m.manifest.Structure != nil && r.client != nil {
		if interactive && a.confirm("Create the missing spaces and collections in Gravity now?", "pages with a source are left for the import") {
			if err := a.structureApply(r.ctx, false); err != nil {
				a.ui.Warn("structure_apply_failed", err.Error())
			}
		} else {
			next = append(next, "gravity structure apply --dry-run   then   gravity structure apply")
		}
	}
	if r.client != nil {
		next = append(next, r.offerGrants(interactive)...)
	}
	if !r.o.noRun && m.errors() == 0 && r.client != nil {
		if interactive && a.confirm("Dry-run the passes now?", "everything is computed (model calls included) and nothing is written") {
			if err := a.runPipeline(r.ctx, runFlags{pipelineFlags: pipelineFlags{dryRun: true, leaseTimeout: defaultLeaseTimeout, annotate: "none", parallel: 1}, allowDirty: true}); err != nil {
				a.ui.Warn("dry_run_failed", err.Error())
			}
		} else {
			next = append(next, "gravity run --dry-run")
		}
	}
	if !r.o.noCI && r.client != nil {
		if interactive && a.confirm("Wire CI now?", "writes the workflow and installs the repository token") {
			if err := a.ciSetup(r.ctx, "", false); err != nil {
				a.ui.Warn("ci_setup_failed", err.Error())
			}
		} else {
			next = append(next, "gravity ci setup")
		}
	}
	lines := []string{"Product " + r.data.Product + " · site " + r.data.Site.Slug}
	if len(r.data.Written) > 0 {
		lines = append(lines, "Commit "+strings.Join(r.data.Written, ", ")+" when you are happy with it")
	}
	if m.errors() > 0 {
		lines = append(lines, p.Paint(ui.ToneFail, plural(m.errors(), "error", "errors")+" to fix first: gravity validate"))
	}
	for _, n := range next {
		lines = append(lines, "Next: "+n)
	}
	if r.client == nil {
		lines = append(lines, "Next: gravity login, then gravity setup again")
	}
	p.Println("")
	p.Card("Setup done", lines, nil)
	return a.ui.Result(r.data)
}

func (r *setupRun) offerGrants(interactive bool) []string {
	a := r.a
	p := a.ui
	repo := repoParam(r.who, r.info)
	pending, err := r.client.Approvals(r.ctx, repo)
	if err != nil {
		p.Debugf("approvals: %v", err)
		return nil
	}
	repoName := firstNonEmpty(pending.Repo.Name, r.info.name)
	var spaces, next []string
	for _, pa := range pending.Pending {
		if !pa.MayApprove {
			next = append(next, fmt.Sprintf("ask someone with write access to %s to run gravity approve %s", pa.Target, pa.Target))
			continue
		}
		if interactive && !a.confirm(fmt.Sprintf("Allow %s to write to %s?", repoName, pa.Target), firstNonEmpty(pendingWhy(pa), "CI runs need it")) {
			next = append(next, "gravity approve "+pa.Target)
			continue
		}
		if !interactive && !r.o.yes {
			next = append(next, "gravity approve "+pa.Target)
			continue
		}
		spaces = append(spaces, pa.Target)
	}
	if len(spaces) == 0 {
		return next
	}
	res, err := r.client.Approve(r.ctx, repo, api.ApproveRequest{Spaces: spaces})
	if err != nil {
		a.ui.Warn("approve_failed", err.Error())
		return append(next, "gravity approve "+strings.Join(spaces, " "))
	}
	for _, o := range res.Approved {
		r.data.Granted = append(r.data.Granted, o.Target)
		p.Note("", ui.MarkOK, "%s may now write to %s", repoName, o.Target)
	}
	for _, o := range res.Refused {
		p.Note("", ui.MarkFail, "%s: %s", firstNonEmpty(o.Target, o.Pass), firstNonEmpty(o.Message, o.Code, "refused"))
		next = append(next, "gravity approve "+firstNonEmpty(o.Target, o.Pass))
	}
	return next
}

func (a *app) confirm(title, desc string) bool {
	ans, err := a.prompter().Select(title, desc, []ui.Choice{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "Not now"}}, "yes")
	return err == nil && ans == "yes"
}
