package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/setup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type initPlan struct {
	content    string
	parsed     *config.Manifest
	appPasses  []config.Pass
	scopes     []string
	ci         *cisetup.Plan
	installer  *cisetup.Installer
	canMint    bool
	mintReason string
}

func (a *app) prompter() ui.Prompter {
	if a.prompts != nil {
		return a.prompts
	}
	accessible := a.env("ACCESSIBLE") != "" || a.env("TERM") == "dumb"
	return &ui.HuhPrompter{In: a.stdin, Out: a.stderr, Accessible: accessible}
}

func (r *initRun) auto() bool {
	return r.o.yes || !r.a.ui.Interactive()
}

func (r *initRun) ask() error {
	r.data.Mode = r.mode()
	r.prompt = r.a.prompter()
	if err := r.askProduct(); err != nil {
		return err
	}
	switch r.data.Mode {
	case modeConvert:
		return r.askConversion()
	case modeFresh:
		return r.askPasses()
	}
	return nil
}

func (r *initRun) productNamed(slug string) setup.ProductOption {
	for _, p := range r.products {
		if p.Slug == slug {
			return setup.ProductOption{Slug: p.Slug, Name: p.Name}
		}
	}
	return setup.ProductOption{Slug: slug, New: true}
}

func (r *initRun) askProduct() error {
	if fixed := firstNonEmpty(r.o.product, r.manifestProduct()); fixed != "" {
		r.product = r.productNamed(fixed)
	} else if r.conn.Repo.ID != "" && !r.conn.Repo.Created && r.conn.Repo.Product.Slug != "" {
		p := r.conn.Repo.Product
		r.product = setup.ProductOption{Slug: p.Slug, Name: p.Name}
	} else if r.isRepoPrincipal() && r.who.Principal.Repo != nil && r.who.Principal.Repo.Product != nil {
		p := r.who.Principal.Repo.Product
		r.product = setup.ProductOption{Slug: p.Slug, Name: p.Name}
	} else {
		options := setup.RankProducts(r.products, r.info.remoteKey, r.info.name)
		r.product = options[0]
		if len(r.products) > 0 && !r.auto() {
			choices := make([]ui.Choice, 0, len(options))
			for _, o := range options {
				choices = append(choices, ui.Choice{Key: o.Slug, Label: o.Label()})
			}
			r.data.Questions++
			slug, err := r.prompt.Select("Product", "Which product does "+r.info.name+" belong to?", choices, options[0].Slug)
			if err != nil {
				return err
			}
			for _, o := range options {
				if o.Slug == slug {
					r.product = o
				}
			}
		}
	}
	r.data.Product = r.product.Slug
	if r.data.Questions == 0 {
		r.a.ui.Println("%s Product %s", r.a.ui.Mark(ui.MarkOK), r.productLabel())
	}
	return nil
}

func (r *initRun) productLabel() string {
	if r.product.New {
		return r.product.Slug + " (new)"
	}
	return firstNonEmpty(r.product.Name, r.product.Slug)
}

func (r *initRun) siteTree(site setup.Site) *api.SiteTree {
	if site.New || site.Slug == "" {
		return nil
	}
	tree, err := r.client.SiteTree(r.ctx, site.Slug)
	if err != nil {
		r.a.ui.Debugf("site %s: %v", site.Slug, err)
		return nil
	}
	return tree
}

func (r *initRun) askPasses() error {
	site := setup.DefaultSite(r.product, r.products, r.sites)
	memory := r.who.Modules["memory"]
	var keep map[string]bool
	asked := false
	for {
		sugg := setup.Suggest(setup.Inputs{Detect: r.det, Site: site, Tree: r.siteTree(site), MemoryModule: memory})
		if r.auto() {
			r.chosen = selectedSuggestions(sugg, nil)
			break
		}
		approval := r.needsApproval(sugg)
		choices := make([]ui.Choice, 0, len(sugg)+1)
		var pre []string
		for _, s := range sugg {
			label := s.Label()
			if approval[s.Pass.Name] {
				label += "  (needs approval)"
			}
			choices = append(choices, ui.Choice{Key: s.Pass.Name, Label: label})
			if (keep == nil && s.Selected) || keep[s.Pass.Name] {
				pre = append(pre, s.Pass.Name)
			}
		}
		choices = append(choices, ui.Choice{Key: answerSite, Label: "Change site… (now " + site.Name + ")"})
		if !asked {
			r.data.Questions++
			asked = true
		}
		picked, err := r.prompt.MultiSelect("What should this repository keep up to date?", "site: "+site.Name+" · change site… at the end of the list", choices, pre)
		if err != nil {
			return err
		}
		keep = map[string]bool{}
		change := false
		for _, k := range picked {
			if k == answerSite {
				change = true
				continue
			}
			keep[k] = true
		}
		if !change {
			r.chosen = selectedSuggestions(sugg, keep)
			break
		}
		next, err := r.pickSite(site)
		if err != nil {
			return err
		}
		site = next
	}
	r.data.Site = &site
	return nil
}

func (r *initRun) needsApproval(sugg []setup.Suggestion) map[string]bool {
	out := map[string]bool{}
	var passes []config.Pass
	for _, s := range sugg {
		if s.Pass.Target != "" {
			passes = append(passes, s.Pass)
		}
	}
	if len(passes) == 0 {
		return out
	}
	content, err := r.minimalManifest(nil, "", passes)
	if err != nil {
		return out
	}
	m, err := config.Parse([]byte(content))
	if err != nil {
		r.a.ui.Debugf("approval probe: %v", err)
		return out
	}
	req := r.connectRequest(m, true)
	req.CreateTargets = setup.CreateTargets(sugg)
	conn, err := r.client.Connect(r.ctx, req)
	if err != nil {
		r.a.ui.Debugf("approval probe: %v", err)
		return out
	}
	for _, w := range conn.Manifest.Warnings {
		if w.Code != "target_unapproved" {
			continue
		}
		var i int
		if _, err := fmt.Sscanf(w.Path, "passes[%d]", &i); err == nil && i >= 0 && i < len(passes) {
			out[passes[i].Name] = true
		}
	}
	return out
}

func selectedSuggestions(sugg []setup.Suggestion, keep map[string]bool) []setup.Suggestion {
	var out []setup.Suggestion
	for _, s := range sugg {
		if (keep == nil && s.Selected) || keep[s.Pass.Name] {
			out = append(out, s)
		}
	}
	return out
}

func (r *initRun) pickSite(current setup.Site) (setup.Site, error) {
	choices := make([]ui.Choice, 0, len(r.sites)+1)
	for _, s := range r.sites {
		choices = append(choices, ui.Choice{Key: s.Slug, Label: firstNonEmpty(s.Name, s.Slug)})
	}
	newSite := setup.Site{Slug: r.product.Slug, Name: firstNonEmpty(r.product.Name, r.product.Slug), New: true}
	exists := false
	for _, s := range r.sites {
		if s.Slug == newSite.Slug {
			exists = true
		}
	}
	if !exists {
		choices = append(choices, ui.Choice{Key: answerNew, Label: "New site: " + newSite.Name})
	}
	key, err := r.prompt.Select("Site", "Where should the passes write?", choices, current.Slug)
	if err != nil {
		return current, err
	}
	if key == answerNew {
		return newSite, nil
	}
	for _, s := range r.sites {
		if s.Slug == key {
			return setup.Site{Slug: s.Slug, Name: firstNonEmpty(s.Name, s.Slug), Position: s.Position}, nil
		}
	}
	return current, nil
}

func (r *initRun) askConversion() error {
	p := r.a.ui
	conv := r.conv
	p.Println("%s %s is a v1 manifest; converting it to v2:", p.Mark(ui.MarkWarn), r.data.Manifest.Path)
	rows := make([][]string, 0, len(conv.Notes))
	for _, n := range conv.Notes {
		mark := p.Mark(ui.MarkOK)
		if n.Action == config.ActionDropped {
			mark = p.Mark(ui.MarkSkip)
		}
		rows = append(rows, []string{mark, n.Key, n.Detail})
	}
	p.Table("  ", rows)
	if conv.Adopts {
		p.Println("  %s verbatim pages become repo-locked once their import is accepted (v1 ownership: human pages were editable in Gravity)", p.Mark(ui.MarkWarn))
	}
	if r.auto() {
		return nil
	}
	r.data.Questions++
	choice, err := r.prompt.Select("Convert "+r.data.Manifest.Path+" to version 2?", "the original is kept as "+backupName(r.data.Manifest.Path), []ui.Choice{
		{Key: "convert", Label: "Convert and continue"},
		{Key: answerCancel, Label: "Cancel"},
	}, "convert")
	if err != nil {
		return err
	}
	if choice == answerCancel {
		return ui.ErrAborted
	}
	return nil
}

func backupName(manifestPath string) string {
	dir := filepath.Dir(manifestPath)
	base := strings.TrimSuffix(filepath.Base(manifestPath), filepath.Ext(manifestPath))
	name := base + ".v1" + filepath.Ext(manifestPath) + ".bak"
	if dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}

func (r *initRun) minimalManifest(code *config.Code, apiURL string, passes []config.Pass) (string, error) {
	m := &config.Manifest{Version: config.ManifestVersion, Product: r.product.Slug, APIURL: apiURL, Code: code, Passes: passes}
	out, err := config.Render(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (r *initRun) plan() error {
	ip := &initPlan{}
	r.inCode = r.o.passesAsCode || (r.data.Mode == modeConvert && !r.o.appPasses)
	var err error
	switch r.data.Mode {
	case modeConvert:
		err = r.planConversion(ip)
	case modeFresh:
		err = r.planFresh(ip)
	default:
		err = r.planConnected(ip)
	}
	if err != nil {
		return err
	}
	if ip.content != "" {
		parsed, perr := config.Parse([]byte(ip.content))
		if perr != nil {
			return Fail(CodeError, fmt.Errorf("the manifest init would write is invalid: %w", perr))
		}
		parsed.Path = r.path
		ip.parsed = parsed
	}
	r.data.Manifest.Content = ip.content
	r.collectPasses(ip)
	ip.scopes = r.scopes(ip)
	ip.canMint, ip.mintReason = r.mintable()
	provider := r.ciProvider()
	apiURL := ""
	if u := r.manifestAPIURL(); u != "" && !auth.SameAPIURL(u, config.DefaultAPIURL) {
		apiURL = u
	}
	ci, err := cisetup.Build(r.info.root, provider, cisetup.Options{
		DefaultBranch: firstNonEmpty(r.info.defaultBranch, r.info.branch, "main"),
		Schedule:      setup.HasSchedule(r.passes, r.conn.Effective.Passes),
		APIURL:        apiURL,
		WebURL:        r.info.webURL,
	})
	if err != nil {
		return Fail(CodeError, err)
	}
	ip.ci = ci
	r.data.CI = ci
	if ip.canMint && !r.o.noSecret && provider == r.info.provider && (provider == cisetup.GitHub || provider == cisetup.GitLab) {
		ip.installer = cisetup.FindInstaller(r.ctx, provider, r.info.remoteKey, r.a.secretRunner)
	}
	if ip.canMint && ip.installer == nil && !r.o.noSecret && !r.a.terminal {
		ip.canMint, ip.mintReason = false, "there is no terminal to show the token on and it must not land in logs; pass --no-secret to print it anyway, or mint one in the app"
	}
	r.ip = ip
	if ip.parsed != nil && ip.content != string(r.existingYAML()) {
		req := r.connectRequest(ip.parsed, true)
		req.CreateTargets = r.data.CreateTargets
		conn, err := r.client.Connect(r.ctx, req)
		if err != nil {
			return explainAPI(fmt.Errorf("connect: %w", err))
		}
		r.previewConn = conn
	}
	return nil
}

func (r *initRun) existingYAML() []byte {
	switch {
	case r.manifest != nil:
		return r.manifest.YAML
	case r.v1Data != nil:
		return r.v1Data
	}
	return nil
}

func (r *initRun) planConversion(ip *initPlan) error {
	conv := r.conv
	r.data.CreateTargets = setup.DeclaredTargets(conv, r.siteTree(setup.Site{Slug: conv.Site}))
	r.data.Manifest.Action = "convert"
	r.data.Manifest.Backup = backupName(r.data.Manifest.Path)
	if r.inCode {
		content := string(conv.YAML)
		if conv.Product == "" && r.product.Slug != "" {
			content = strings.Replace(content, "version: 2\n", "version: 2\nproduct: "+r.product.Slug+"\n", 1)
		}
		ip.content = content
		r.passes = conv.Manifest.Passes
		return nil
	}
	cm := conv.Manifest
	content, err := r.minimalManifest(cm.Code, cm.APIURL, nil)
	if err != nil {
		return Fail(CodeError, err)
	}
	ip.content = content
	ip.appPasses = cm.Passes
	r.passes = cm.Passes
	return nil
}

func (r *initRun) planFresh(ip *initPlan) error {
	passes := make([]config.Pass, 0, len(r.chosen))
	for _, s := range r.chosen {
		passes = append(passes, s.Pass)
	}
	r.passes = passes
	r.data.CreateTargets = setup.CreateTargets(r.chosen)
	if !r.inCode {
		ip.appPasses = passes
	}
	switch {
	case r.manifest != nil && r.inCode && len(passes) > 0:
		block, err := config.RenderPasses(passes)
		if err != nil {
			return Fail(CodeError, err)
		}
		existing := emptyPasses.ReplaceAllString(string(r.manifest.YAML), "")
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		ip.content = existing + string(block)
		r.data.Manifest.Action = "append"
	case r.manifest != nil:
		ip.content = string(r.manifest.YAML)
		r.data.Manifest.Action = "keep"
	default:
		var code *config.Code
		if paths := r.det.OpenAPIPaths(); len(paths) > 0 {
			code = &config.Code{OpenAPI: paths}
		}
		var inManifest []config.Pass
		if r.inCode {
			inManifest = passes
		}
		content, err := r.minimalManifest(code, "", inManifest)
		if err != nil {
			return Fail(CodeError, err)
		}
		ip.content = content
		r.data.Manifest.Action = "create"
	}
	return nil
}

var emptyPasses = regexp.MustCompile(`(?m)^passes:[ \t]*(\[[ \t]*\]|~|null)?[ \t]*(#.*)?(\n|$)`)

func (r *initRun) planConnected(ip *initPlan) error {
	if !r.isRepoPrincipal() {
		r.data.CreateTargets = setup.MissingTargets(r.conn.Effective.Passes)
	}
	if r.manifest != nil {
		ip.content = string(r.manifest.YAML)
		r.data.Manifest.Action = "keep"
		r.passes = r.manifest.Passes
		return nil
	}
	if names := r.storedManifestPasses(); len(names) > 0 {
		return &ExitError{Code: CodeError, ErrCode: "manifest_not_found", Err: fmt.Errorf("%s has passes managed in a .gravity.yaml that is not in this checkout (%s); a new manifest without them would archive them. Pull %s, or point --manifest at that file, and run gravity init again", r.info.name, strings.Join(names, ", "), r.authoritativeBranch())}
	}
	var code *config.Code
	if paths := r.det.OpenAPIPaths(); len(paths) > 0 {
		code = &config.Code{OpenAPI: paths}
	}
	content, err := r.minimalManifest(code, "", nil)
	if err != nil {
		return Fail(CodeError, err)
	}
	ip.content = content
	r.data.Manifest.Action = "create"
	return nil
}

func (r *initRun) storedManifestPasses() []string {
	var names []string
	for _, ep := range r.conn.Effective.Passes {
		if ep.Source == "manifest" {
			names = append(names, ep.Name)
		}
	}
	return names
}

func (r *initRun) authoritativeBranch() string {
	return firstNonEmpty(r.conn.Manifest.AuthoritativeBranch, r.info.defaultBranch)
}

func (r *initRun) offAuthoritative() bool {
	b := r.authoritativeBranch()
	return b != "" && r.info.branch != b
}

func (r *initRun) branchLabel() string {
	if r.info.branch == "" {
		return "a detached HEAD"
	}
	return r.info.branch
}

func (r *initRun) targetRefs(targets []api.CreateTarget) string {
	refs := make([]string, 0, len(targets))
	for _, t := range targets {
		refs = append(refs, t.Site+"/"+t.Space)
	}
	return strings.Join(refs, ", ")
}

func (r *initRun) checkBranch() error {
	if r.o.dryRun || len(r.data.CreateTargets) == 0 || !r.offAuthoritative() {
		return nil
	}
	b := r.authoritativeBranch()
	return &ExitError{Code: CodeError, ErrCode: "branch_not_authoritative", Err: fmt.Errorf("init would create %s, and Gravity creates spaces only from %s (this is %s). Run `git switch %s` and gravity init again (init never commits, so you can branch afterwards), or create the spaces in the app first", r.targetRefs(r.data.CreateTargets), b, r.branchLabel(), b)}
}

func (r *initRun) collectPasses(ip *initPlan) {
	labels := map[string]setup.Suggestion{}
	for _, s := range r.chosen {
		labels[s.Pass.Name] = s
	}
	seen := map[string]bool{}
	add := func(p config.Pass, source string) {
		if seen[p.Name] {
			return
		}
		seen[p.Name] = true
		pc := p
		ip := initPass{Name: p.Name, Kind: p.Kind, Target: p.Target, Source: source, spec: &pc}
		if s, ok := labels[p.Name]; ok {
			ip.Label = s.Target
		}
		r.data.Passes = append(r.data.Passes, ip)
	}
	if r.inCode || r.data.Mode == modeConnected {
		for _, p := range r.passes {
			add(p, "manifest")
		}
	}
	for _, p := range ip.appPasses {
		add(p, "app")
	}
	for _, ep := range r.conn.Effective.Passes {
		if seen[ep.Name] {
			continue
		}
		seen[ep.Name] = true
		r.data.Passes = append(r.data.Passes, initPass{Name: ep.Name, Kind: ep.Kind, Target: ep.Target.Ref, Source: firstNonEmpty(ep.Source, "app"), Registered: true, Status: ep.Target.Status, ApproveURL: ep.Target.ApproveURL})
	}
}

func (r *initRun) scopes(ip *initPlan) []string {
	all := append([]config.Pass{}, r.passes...)
	all = append(all, ip.appPasses...)
	declared := map[string]bool{}
	for _, p := range all {
		declared[p.Name] = true
	}
	for _, ep := range r.conn.Effective.Passes {
		if declared[ep.Name] {
			continue
		}
		enabled := ep.Enabled
		all = append(all, config.Pass{Name: ep.Name, Kind: ep.Kind, Options: ep.Options, Enabled: &enabled})
	}
	return setup.Scopes(all)
}

func (r *initRun) mintable() (bool, string) {
	switch {
	case r.isRepoPrincipal():
		return false, "you are using a repository token; CI can use it as GRAVITY_TOKEN"
	case !r.canMint():
		return false, "you can't mint repository tokens here; ask an admin to mint one in the app"
	case !r.who.Features["machine-tokens"]:
		return false, "this Gravity server cannot mint repository tokens yet; create one in the app"
	}
	return true, ""
}

func (r *initRun) printPreview() {
	p := r.a.ui
	ip := r.ip
	p.Println("%s", p.Bold("Preview"))
	m := r.data.Manifest
	switch m.Action {
	case "keep":
		p.Println("  = %s (kept as is)", m.Path)
	case "append":
		p.Println("  ~ %s (passes appended)", m.Path)
		printBlock(p, ip.content)
	case "convert":
		p.Println("  ~ %s (converted to version 2; the original is kept as %s)", m.Path, m.Backup)
		printBlock(p, ip.content)
	default:
		p.Println("  + %s (%d lines)", m.Path, strings.Count(ip.content, "\n"))
		printBlock(p, ip.content)
	}
	for _, f := range ip.ci.Files {
		switch f.Action {
		case cisetup.ActionCreate:
			p.Println("  + %s (%d lines)", f.Path, strings.Count(f.Content, "\n"))
			printBlock(p, f.Content)
		case cisetup.ActionAppend:
			p.Println("  ~ %s (append)", f.Path)
			printBlock(p, strings.TrimLeft(f.Content, "\n"))
		default:
			p.Println("  = %s (%s)", f.Path, f.Note)
		}
	}
	if ip.ci.Snippet != "" {
		p.Println("  > %s, to add by hand to %s", ip.ci.Label, ip.ci.SnippetTarget)
	}
	var app, code, existing []string
	for _, ps := range r.data.Passes {
		label := ps.Name
		if ps.Target != "" {
			label += " → " + ps.Target
		}
		switch {
		case ps.Registered:
			existing = append(existing, label)
		case ps.Source == "app":
			app = append(app, label)
		default:
			code = append(code, label)
		}
	}
	if len(app) > 0 {
		p.Println("  + app passes: %s   (registered in Gravity, editable there)", strings.Join(app, ", "))
	}
	if len(code) > 0 {
		p.Println("  + passes in %s: %s   (managed in the repository, locked in the app)", m.Path, strings.Join(code, ", "))
	}
	if len(existing) > 0 {
		p.Println("  = passes already in Gravity: %s", strings.Join(existing, ", "))
	}
	off := r.offAuthoritative()
	for _, t := range r.data.CreateTargets {
		if off {
			p.Println("  ! new space %s/%s (%s), created only from %s", t.Site, t.Space, firstNonEmpty(t.Name, t.Space), r.authoritativeBranch())
			continue
		}
		p.Println("  + new space %s/%s (%s)", t.Site, t.Space, firstNonEmpty(t.Name, t.Space))
	}
	if off && len(r.data.CreateTargets) > 0 {
		b := r.authoritativeBranch()
		p.Println("  %s this is %s, not %s: Gravity creates spaces only from %s. Run `git switch %s` and gravity init again (init never commits), or create the spaces in the app first", p.Mark(ui.MarkWarn), r.branchLabel(), b, b, b)
	}
	if ip.canMint {
		p.Println("  + repository token with %s", strings.Join(ip.scopes, ", "))
		if ip.installer != nil {
			p.Println("  + secret %s on %s (%s)", cisetup.SecretName, ip.installer.Repo, ip.installer.Describe(cisetup.SecretName))
		} else {
			p.Println("  + the token is printed once to paste: %s", ip.ci.PasteHint)
		}
	} else {
		p.Println("  - no token minted: %s", ip.mintReason)
	}
	if ip.ci.CommentToken != "" {
		p.Println("  ! by hand, for pull request comments: %s", ip.ci.CommentHint)
	}
	if r.previewConn != nil {
		for _, w := range r.previewConn.Manifest.Warnings {
			p.Println("  %s %s", p.Mark(ui.MarkWarn), w.Message)
		}
	}
}

func printBlock(p *ui.Printer, content string) {
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		p.Println("      %s", p.Dim(l))
	}
}

func (r *initRun) confirm() (string, error) {
	ip := r.ip
	var choices []ui.Choice
	if ip.installer != nil {
		choices = append(choices,
			ui.Choice{Key: answerSecret, Label: "Write + set the secret (" + ip.installer.Tool + ")"},
			ui.Choice{Key: answerFiles, Label: "Write files only (print the token once to paste)"})
	} else if ip.canMint {
		choices = append(choices, ui.Choice{Key: answerFiles, Label: "Write files (print the token once to paste)"})
	} else {
		choices = append(choices, ui.Choice{Key: answerFiles, Label: "Write files"})
	}
	choices = append(choices, ui.Choice{Key: answerCancel, Label: "Cancel"})
	if r.auto() {
		return choices[0].Key, nil
	}
	r.data.Questions++
	return r.prompt.Select("Write these and wire CI?", "nothing is committed or pushed", choices, choices[0].Key)
}

func (r *initRun) apply(answer string) error {
	a := r.a
	ip := r.ip
	prog := a.ui.StartProgress("", false)
	defer prog.Stop()
	step := prog.Add("Connect " + r.info.name + " to " + r.productLabel())
	prog.Begin(step)
	req := r.connectRequest(ip.parsed, false)
	req.CreateTargets = r.data.CreateTargets
	conn, err := r.client.Connect(r.ctx, req)
	if err != nil {
		prog.Fail(step, err.Error())
		prog.Stop()
		return explainAPI(fmt.Errorf("connect: %w", err))
	}
	r.data.Connect = conn
	prog.Done(step, conn.Repo.AppURL)
	r.updateFromConnect(conn)
	if !conn.Manifest.Persisted && len(r.data.CreateTargets) > 0 {
		r.data.PendingTargets = r.data.CreateTargets
	}
	failed := r.registerPasses(prog, conn)
	token := r.mint(prog, conn)
	werr := r.writeFiles(prog)
	r.installSecret(prog, answer, token)
	prog.Stop()
	for _, w := range conn.Manifest.Warnings {
		a.ui.Warn(w.Code, w.Message)
	}
	if len(r.data.PendingTargets) > 0 {
		a.ui.Warn("spaces_not_created", r.pendingMessage(conn))
	}
	if token != "" && r.data.Token.Secret == secretPrinted {
		r.printToken(token)
	}
	if werr != nil {
		return Fail(CodeError, fmt.Errorf("connected, but %w", werr))
	}
	if ip.ci.Snippet != "" {
		a.ui.Println("%s Add this to %s:", a.ui.Mark(ui.MarkInfo), ip.ci.SnippetTarget)
		a.ui.Print(ip.ci.Snippet)
	}
	r.printSummary(conn)
	if len(failed) > 0 {
		msg := fmt.Sprintf("connected, but %d passes could not be registered: %s", len(failed), strings.Join(failed, "; "))
		ee := &ExitError{Code: CodeError, ErrCode: "pass_registration_failed", Err: errors.New(msg)}
		if err := a.ui.Failure(ui.ErrorInfo{Code: ee.ErrCode, Message: msg, ExitCode: ee.Code}, r.data); err != nil {
			return err
		}
		return ee
	}
	return a.ui.Result(r.data)
}

func (r *initRun) pendingMessage(conn *api.ConnectResponse) string {
	refs := r.targetRefs(r.data.PendingTargets)
	reason := ""
	if conn.Manifest.Reason != nil {
		reason = *conn.Manifest.Reason
	}
	b := firstNonEmpty(conn.Manifest.AuthoritativeBranch, r.authoritativeBranch())
	switch reason {
	case "branch_not_authoritative", "pr":
		return fmt.Sprintf("Gravity did not create %s: spaces are created only from %s. Passes targeting them report target missing until they exist; run gravity init on %s, or create them in the app", refs, b, b)
	case "permission":
		return fmt.Sprintf("Gravity did not create %s: you cannot reconcile this repository. Passes targeting them report target missing until an admin creates them in the app", refs)
	}
	return fmt.Sprintf("Gravity did not create %s (%s). Passes targeting them report target missing until they exist; create them in the app", refs, firstNonEmpty(reason, "not persisted"))
}

func (r *initRun) updateFromConnect(conn *api.ConnectResponse) {
	byName := map[string]api.PlanPass{}
	for _, ep := range conn.Effective.Passes {
		byName[ep.Name] = ep
	}
	for i := range r.data.Passes {
		ps := &r.data.Passes[i]
		if ep, ok := byName[ps.Name]; ok {
			ps.Status = ep.Target.Status
			ps.ApproveURL = ep.Target.ApproveURL
			if ps.Source == "manifest" && conn.Manifest.Persisted {
				ps.Registered = true
			}
		}
	}
}

func (r *initRun) registerPasses(prog *ui.Progress, conn *api.ConnectResponse) []string {
	var failed []string
	for i := range r.data.Passes {
		ps := &r.data.Passes[i]
		if ps.Source != "app" || ps.Registered || ps.spec == nil {
			continue
		}
		step := prog.Add("Register pass " + ps.Name + targetSuffix(ps.Target))
		prog.Begin(step)
		res, err := r.client.UpsertPass(r.ctx, conn.Repo.ID, ps.Name, passSpec(*ps.spec))
		if err != nil {
			ps.Error = err.Error()
			failed = append(failed, ps.Name+": "+err.Error())
			prog.Fail(step, err.Error())
			continue
		}
		ps.Registered = true
		ps.Status = res.Target.Status
		ps.ApproveURL = res.Target.ApproveURL
		if res.Target.Status == api.TargetUnapproved {
			prog.Warn(step, "target awaits approval")
		} else {
			prog.Done(step, "")
		}
	}
	return failed
}

func targetSuffix(t string) string {
	if t == "" {
		return ""
	}
	return " → " + t
}

func passSpec(p config.Pass) api.PassSpec {
	spec := api.PassSpec{
		Kind: p.Kind, Title: p.Title, Template: p.Template, Target: p.Target, Triggers: p.Triggers, Branches: p.Branches,
		Audiences: p.Audiences, Instructions: p.Instructions, Publish: p.Publish, Enabled: p.Enabled, Options: p.Options,
	}
	if p.Scope != nil {
		spec.Scope = &api.PassScope{Paths: p.Scope.Paths, Exclude: p.Scope.Exclude, Units: p.Scope.Units}
	}
	return spec
}

func (r *initRun) mint(prog *ui.Progress, conn *api.ConnectResponse) string {
	ip := r.ip
	r.data.Token = &initToken{Scopes: ip.scopes, Secret: secretSkipped}
	if !ip.canMint {
		r.data.Token.Note = ip.mintReason
		return ""
	}
	step := prog.Add(fmt.Sprintf("Mint a repository token (%d scopes)", len(ip.scopes)))
	prog.Begin(step)
	minted, err := r.client.MintRepoToken(r.ctx, conn.Repo.ID, api.MintTokenRequest{Name: ip.ci.Label + " · " + r.info.name, Scopes: ip.scopes})
	if err != nil {
		r.data.Token.Note = "minting failed: " + err.Error()
		prog.Fail(step, err.Error())
		r.a.ui.Warn("token_mint_failed", "could not mint a repository token ("+err.Error()+"); mint one in the app and store it as "+cisetup.SecretName)
		return ""
	}
	r.data.Token.KeyHint = minted.Key.KeyHint
	r.data.Token.ExpiresAt = minted.Key.ExpiresAt
	if len(minted.Key.Scopes) > 0 {
		r.data.Token.Scopes = minted.Key.Scopes
	}
	r.data.Token.Secret = secretPrinted
	prog.Done(step, "…"+minted.Key.KeyHint)
	return minted.Token
}

func (r *initRun) writeFiles(prog *ui.Progress) error {
	m := &r.data.Manifest
	if m.Action != "keep" && r.ip.content != "" {
		step := prog.Add("Write " + m.Path)
		prog.Begin(step)
		if r.v1Data != nil {
			bak := filepath.Join(r.info.root, m.Backup)
			if err := os.WriteFile(bak, r.v1Data, 0o644); err != nil {
				prog.Fail(step, err.Error())
				return fmt.Errorf("writing %s failed: %w", m.Backup, err)
			}
			r.data.Written = append(r.data.Written, m.Backup)
		}
		if err := os.WriteFile(r.path, []byte(r.ip.content), 0o644); err != nil {
			prog.Fail(step, err.Error())
			return fmt.Errorf("writing %s failed: %w", m.Path, err)
		}
		m.Written = true
		r.data.Written = append(r.data.Written, m.Path)
		prog.Done(step, "")
	}
	for _, f := range r.ip.ci.Files {
		if f.Action == cisetup.ActionKeep {
			continue
		}
		step := prog.Add("Write " + f.Path)
		prog.Begin(step)
		written, err := cisetup.Write(r.info.root, &cisetup.Plan{Files: []cisetup.File{f}})
		r.data.Written = append(r.data.Written, written...)
		if err != nil {
			prog.Fail(step, err.Error())
			return fmt.Errorf("writing %s failed: %w", f.Path, err)
		}
		prog.Done(step, "")
	}
	return nil
}

func (r *initRun) installSecret(prog *ui.Progress, answer, token string) {
	if token == "" || answer != answerSecret || r.ip.installer == nil {
		return
	}
	inst := r.ip.installer
	step := prog.Add("Set " + cisetup.SecretName + " on " + inst.Repo + " (" + inst.Tool + ")")
	prog.Begin(step)
	if err := inst.Install(r.ctx, cisetup.SecretName, token); err != nil {
		prog.Fail(step, err.Error())
		r.a.ui.Warn("secret_install_failed", "could not set "+cisetup.SecretName+" with "+inst.Tool+": "+err.Error())
		return
	}
	r.data.Token.Secret = secretInstalled
	r.data.Token.Via = inst.Tool
	prog.Done(step, "")
}

func (r *initRun) printToken(token string) {
	w := r.a.stderr
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "-------- GRAVITY_TOKEN (shown once) --------")
	fmt.Fprintln(w, token)
	fmt.Fprintln(w, "--------------------------------------------")
	fmt.Fprintln(w, "Copy it now and "+r.ip.ci.PasteHint+".")
	fmt.Fprintln(w, "")
}

func (r *initRun) printSummary(conn *api.ConnectResponse) {
	a := r.a
	n := len(r.data.Passes)
	title := fmt.Sprintf("Connected %s with %d passes", firstNonEmpty(conn.Repo.Name, r.info.name), n)
	if n == 1 {
		title = fmt.Sprintf("Connected %s with 1 pass", firstNonEmpty(conn.Repo.Name, r.info.name))
	}
	lines := []string{"Product: " + firstNonEmpty(conn.Repo.Product.Name, conn.Repo.Product.Slug, r.productLabel())}
	names := make([]string, 0, n)
	for _, ps := range r.data.Passes {
		names = append(names, ps.Name)
	}
	if n > 0 {
		lines = append(lines, "Passes: "+strings.Join(names, ", "))
	}
	if !conn.Manifest.Persisted && r.inCode && conn.Manifest.Reason != nil && *conn.Manifest.Reason == "branch_not_authoritative" {
		lines = append(lines, "The passes in "+r.data.Manifest.Path+" land when it reaches "+firstNonEmpty(conn.Manifest.AuthoritativeBranch, r.info.defaultBranch))
	}
	if len(r.data.PendingTargets) > 0 {
		lines = append(lines, "Spaces not created: "+r.targetRefs(r.data.PendingTargets)+" (see the warning above)")
	}
	if t := r.data.Token; t != nil {
		switch t.Secret {
		case secretInstalled:
			lines = append(lines, "Token: "+cisetup.SecretName+" set with "+t.Via)
		case secretPrinted:
			lines = append(lines, "Token: printed above, paste it as the CI secret "+cisetup.SecretName)
		default:
			if t.Note != "" {
				lines = append(lines, "Token: "+t.Note)
			}
		}
	}
	if len(r.data.Written) > 0 {
		lines = append(lines, "Commit: "+strings.Join(r.data.Written, ", "))
	}
	if r.ip != nil && r.ip.ci != nil && r.ip.ci.CommentToken != "" {
		lines = append(lines, "Comments: "+r.ip.ci.CommentHint)
	}
	links := []ui.Link{{Label: "Repository", URL: conn.Repo.AppURL}}
	for _, ps := range r.data.Passes {
		if ps.Status == api.TargetUnapproved && ps.ApproveURL != "" {
			links = append(links, ui.Link{Label: "Approve " + ps.Name, URL: ps.ApproveURL})
		}
	}
	a.ui.Card(title, lines, links)
	a.ui.Println("%s Try it now:  gravity preview", a.ui.Mark(ui.MarkOK))
}
