package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

const (
	severityError   = "error"
	severityWarning = "warning"

	sourceLocal  = "local"
	sourceServer = "server"

	serverChecked     = "checked"
	serverUnavailable = "unavailable"
	serverSkipped     = "skipped"

	flagEmpty          = "empty"
	flagDuplicateSlug  = "duplicate_slug"
	flagPackageTitle   = "package_title"
	flagExistingPage   = "existing_page"
	flagNotInStructure = "not_in_structure"
)

type issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Pass     string `json:"pass,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
	Source   string `json:"source"`
}

type mappedTranslation struct {
	Source   string `json:"source"`
	Language string `json:"language"`
}

type mappedPage struct {
	Source         string              `json:"source"`
	CollectionPath []string            `json:"collectionPath"`
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Language       string              `json:"language,omitempty"`
	PackageName    string              `json:"packageName,omitempty"`
	Translations   []mappedTranslation `json:"translations,omitempty"`
	Flags          []string            `json:"flags"`
	empty          bool
}

type verbatimMap struct {
	Pass       string       `json:"pass"`
	Target     string       `json:"target"`
	AllowEmpty bool         `json:"allowEmpty,omitempty"`
	Pages      []mappedPage `json:"pages"`
	Warnings   []string     `json:"warnings,omitempty"`
	site       string
	space      string
}

type modelOptions struct {
	server bool
	plan   bool
}

type model struct {
	info        *repoInfo
	manifest    *config.Manifest
	manifestErr *config.ManifestError
	verbatim    []verbatimMap
	issues      []issue
	signedIn    bool
	server      string
	creds       auth.Credentials
	client      *api.Client
	who         *api.WhoAmI
	conn        *api.ConnectResponse
	plan        *api.Plan
	validation  *api.ValidateResponse
	live        *api.Structure
}

func (m *model) repoParam() string {
	return repoParam(m.who, m.info)
}

func (m *model) errors() int {
	n := 0
	for _, is := range m.issues {
		if is.Severity == severityError {
			n++
		}
	}
	return n
}

func (m *model) warnings() int {
	return len(m.issues) - m.errors()
}

func (m *model) add(is issue) {
	if is.Source == "" {
		is.Source = sourceLocal
	}
	for _, have := range m.issues {
		if have.Code == is.Code && have.Pass == is.Pass && have.Path == is.Path && have.Message == is.Message {
			return
		}
	}
	m.issues = append(m.issues, is)
}

func (a *app) collect(ctx context.Context, o modelOptions) (*model, error) {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return nil, err
	}
	m := &model{info: info, server: serverSkipped}
	path := config.ManifestPath(info.root, a.manifestOverride())
	man, err := config.Load(path)
	var me *config.ManifestError
	switch {
	case errors.As(err, &me):
		m.manifestErr = me
		for _, is := range me.Issues {
			m.add(issue{Severity: severityError, Code: api.CodeManifestInvalid, Path: is.Path, Message: is.Message, Hint: "fix " + relPath(info.root, path) + " (see `gravity show` for the expected shape)"})
		}
		return m, nil
	case err != nil:
		return nil, Fail(CodeError, err)
	case man == nil:
		return nil, &ExitError{Code: CodeError, ErrCode: "manifest_not_found", Err: fmt.Errorf("%s not found; run `gravity setup` to create it", relPath(info.root, path))}
	}
	m.manifest = man
	src, err := newWorktreeSource(ctx, info)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	m.mapVerbatim(src)
	m.localRules(src)
	if !o.server {
		return m, nil
	}
	if err := a.collectServer(ctx, m, o); err != nil {
		return nil, err
	}
	m.flagExisting()
	sortIssues(m.issues)
	return m, nil
}

func (a *app) collectServer(ctx context.Context, m *model, o modelOptions) error {
	creds, err := a.credentials(m.manifest.APIURL)
	if err != nil {
		return err
	}
	if creds.Token == "" {
		m.add(issue{Severity: severityWarning, Code: "not_signed_in", Message: "server checks skipped: not signed in", Hint: "run `gravity login`, then validate again"})
		return nil
	}
	m.creds = creds
	m.client = a.client(creds)
	who, err := m.client.WhoAmI(ctx)
	if err != nil {
		m.server = serverUnavailable
		a.ui.Debugf("whoami: %v", err)
		if api.IsLicenseError(err) || errors.Is(err, api.ErrUnauthorized) {
			return explainAPI(err)
		}
		m.add(issue{Severity: severityWarning, Code: "server_unreachable", Message: "server checks skipped: " + err.Error(), Hint: "check the network and the token (gravity whoami)"})
		return nil
	}
	m.signedIn = true
	m.who = who
	a.serverChecks(ctx, m, o)
	return nil
}

func (a *app) serverChecks(ctx context.Context, m *model, o modelOptions) {
	c, _ := a.detectCI(ctx, m.info.repo)
	conn, err := m.client.Connect(ctx, a.connectRequest(m.info, m.manifest, api.ContextStatus, origin(c), true))
	if err != nil {
		a.ui.Debugf("connect: %v", err)
		m.server = serverUnavailable
		m.add(issue{Severity: severityWarning, Code: "server_unavailable", Message: "server checks skipped: " + err.Error(), Hint: "run `gravity status` for details"})
		return
	}
	m.conn = conn
	for _, w := range conn.Manifest.Warnings {
		sev := severityWarning
		if w.Code == api.SkipTargetMissing {
			sev = severityError
		}
		m.add(issue{Severity: sev, Code: w.Code, Path: w.Path, Message: w.Message, Hint: connectHint(w.Code), Source: sourceServer})
	}
	if m.manifest.Structure != nil {
		if tree, err := m.client.StructureTree(ctx, m.manifest.Structure.Site.Slug); err == nil {
			m.live = tree
		} else if !api.IsUnsupported(err) && !api.HasCode(err, api.CodeNotFound) {
			a.ui.Debugf("structure: %v", err)
		}
	}
	if o.plan {
		q := api.PlanQuery{Repo: m.repoParam(), Trigger: config.TriggerManual, Branch: firstNonEmpty(m.info.branch, m.info.defaultBranch), Mode: api.ModeDry, ManifestHash: m.manifest.Hash}
		if p, err := m.client.Plan(ctx, q); err == nil {
			m.plan = p
		} else {
			a.ui.Debugf("plan: %v", err)
		}
	}
	req := api.ValidateRequest{ManifestHash: m.manifest.Hash, Branch: firstNonEmpty(m.info.branch, m.info.defaultBranch), Verbatim: m.validateVerbatim()}
	if m.manifest.Structure != nil {
		req.Structure = m.manifest.Structure
	}
	res, err := m.client.Validate(ctx, m.repoParam(), req)
	switch {
	case api.IsUnsupported(err):
		m.server = serverUnavailable
		m.add(issue{Severity: severityWarning, Code: "server_validate_unsupported", Message: "this Gravity server cannot validate manifests yet; only local checks ran (slug conflicts with existing pages show up at run time)", Hint: "upgrade the platform, or read the dry run report carefully"})
		return
	case err != nil:
		m.server = serverUnavailable
		a.ui.Debugf("validate: %v", err)
		m.add(issue{Severity: severityWarning, Code: "server_unavailable", Message: "server validation failed: " + err.Error(), Hint: "only local checks ran"})
		return
	}
	m.server = serverChecked
	m.validation = res
	for _, is := range res.Issues {
		m.add(issue{Severity: firstNonEmpty(is.Severity, severityWarning), Code: is.Code, Pass: is.Pass, Path: is.Path, Message: is.Message, Hint: is.Hint, Source: sourceServer})
	}
}

func connectHint(code string) string {
	switch code {
	case api.SkipTargetUnapproved:
		return "run `gravity approve`"
	case api.SkipTargetMissing:
		return "declare the space in structure: and run `gravity structure apply`, or fix the target"
	}
	return ""
}

func (m *model) validateVerbatim() []api.ValidateVerbatim {
	var out []api.ValidateVerbatim
	for _, vm := range m.verbatim {
		for _, pg := range vm.Pages {
			out = append(out, api.ValidateVerbatim{Pass: vm.Pass, Slug: pg.Slug, Title: pg.Title, CollectionPath: pg.CollectionPath, SourcePath: pg.Source, Empty: pg.empty})
			for _, t := range pg.Translations {
				out = append(out, api.ValidateVerbatim{Pass: vm.Pass, Slug: pg.Slug, Title: pg.Title, CollectionPath: pg.CollectionPath, Language: t.Language, SourcePath: t.Source})
			}
		}
	}
	return out
}

type worktreeSource struct {
	root  string
	files []string
}

func newWorktreeSource(ctx context.Context, info *repoInfo) (*worktreeSource, error) {
	files, err := info.repo.ListFiles(ctx, "")
	if err != nil {
		return nil, err
	}
	if untracked, err := info.repo.UntrackedFiles(ctx); err == nil {
		files = append(files, untracked...)
	}
	sort.Strings(files)
	return &worktreeSource{root: info.root, files: files}, nil
}

func (s *worktreeSource) Files() ([]string, error) { return s.files, nil }

func (s *worktreeSource) Read(p string) ([]byte, bool, error) {
	full, err := pathsafe.ResolveInRoot(s.root, p)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", p, err)
	}
	return data, true, nil
}

func (s *worktreeSource) exists(p string) bool {
	i := sort.SearchStrings(s.files, p)
	return i < len(s.files) && s.files[i] == p
}

func splitTarget(ref string) (site, space string, collections []string) {
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		site = parts[0]
	}
	if len(parts) > 1 {
		space = parts[1]
	}
	if len(parts) > 2 {
		collections = parts[2:]
	}
	return site, space, collections
}

func (m *model) mapVerbatim(src *worktreeSource) {
	for _, p := range m.manifest.Passes {
		if p.Kind != config.KindVerbatim || !p.IsEnabled() {
			continue
		}
		site, space, prefix := splitTarget(p.Target)
		pp := api.PlanPass{Name: p.Name, Options: p.Options}
		allow, _ := p.Options["allowEmpty"].(bool)
		vm := verbatimMap{Pass: p.Name, Target: p.Target, AllowEmpty: allow, Pages: []mappedPage{}, site: site, space: space}
		mp, err := verbatim.Map(src, verbatim.MapOptions{Files: passes.FileSpecs(pp), IndexFiles: stringsOption(p.Options, "indexFiles")})
		if err != nil {
			m.add(issue{Severity: severityError, Code: "mapping_failed", Pass: p.Name, Message: err.Error()})
			m.verbatim = append(m.verbatim, vm)
			continue
		}
		vm.Warnings = mp.Warnings
		for _, pg := range mp.Pages {
			out := mappedPage{Source: pg.Path, CollectionPath: append(append([]string{}, prefix...), pg.CollectionPath...), Slug: pg.Slug, Title: pg.Title, PackageName: pg.PackageName, Flags: []string{}, empty: pg.Empty}
			for _, t := range pg.Translations {
				out.Translations = append(out.Translations, mappedTranslation{Source: t.Path, Language: t.Lang})
			}
			if pg.Empty {
				out.Flags = append(out.Flags, flagEmpty)
			}
			if pg.PackageName != "" {
				out.Flags = append(out.Flags, flagPackageTitle)
			}
			vm.Pages = append(vm.Pages, out)
		}
		m.verbatim = append(m.verbatim, vm)
	}
}

func stringsOption(opts map[string]any, key string) []string {
	raw, _ := opts[key].([]any)
	var out []string
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type declaredPage struct {
	space string
	path  []string
	page  api.StructurePage
}

func walkStructure(s *api.Structure, fn func(space api.StructureSpace, path []string, coll *api.StructureCollection, page *api.StructurePage)) {
	if s == nil {
		return
	}
	var walk func(space api.StructureSpace, path []string, cols []api.StructureCollection)
	walk = func(space api.StructureSpace, path []string, cols []api.StructureCollection) {
		for i := range cols {
			c := &cols[i]
			p := append(append([]string{}, path...), c.Slug)
			fn(space, p, c, nil)
			for j := range c.Pages {
				fn(space, p, nil, &c.Pages[j])
			}
			walk(space, p, c.Collections)
		}
	}
	for _, sp := range s.Spaces {
		fn(sp, nil, nil, nil)
		for j := range sp.Pages {
			fn(sp, nil, nil, &sp.Pages[j])
		}
		walk(sp, nil, sp.Collections)
	}
}

func declaredPages(s *api.Structure) []declaredPage {
	var out []declaredPage
	walkStructure(s, func(space api.StructureSpace, path []string, _ *api.StructureCollection, page *api.StructurePage) {
		if page != nil {
			out = append(out, declaredPage{space: space.Slug, path: path, page: *page})
		}
	})
	return out
}

func (m *model) localRules(src *worktreeSource) {
	man := m.manifest
	slugs := map[string][]string{}
	for _, vm := range m.verbatim {
		for i := range vm.Pages {
			pg := &vm.Pages[i]
			key := vm.site + "/" + vm.space + "/" + pg.Slug
			slugs[key] = append(slugs[key], vm.Pass+":"+pg.Source)
			if pg.empty && !vm.AllowEmpty {
				m.add(issue{Severity: severityWarning, Code: "empty_source", Pass: vm.Pass, Path: pg.Source, Message: pg.Source + " has no content beyond a title; the run skips it", Hint: "write the page, exclude the file, or set options.allowEmpty: true to import it anyway"})
			}
			if pg.PackageName != "" {
				m.add(issue{Severity: severityWarning, Code: "package_title", Pass: vm.Pass, Path: pg.Source, Message: fmt.Sprintf("title %q comes from the package name %s", pg.Title, pg.PackageName), Hint: "set `title:` in the file's front matter (or options.files[].title) if " + pg.Title + " is not what readers should see"})
			}
		}
	}
	for _, vm := range m.verbatim {
		for i := range vm.Pages {
			pg := &vm.Pages[i]
			owners := slugs[vm.site+"/"+vm.space+"/"+pg.Slug]
			if len(owners) > 1 {
				pg.Flags = appendFlag(pg.Flags, flagDuplicateSlug)
				m.add(issue{Severity: severityError, Code: "duplicate_slug", Pass: vm.Pass, Path: pg.Source, Message: fmt.Sprintf("slug %s is mapped %d times in %s/%s (%s)", pg.Slug, len(owners), vm.site, vm.space, strings.Join(owners, ", ")), Hint: "give one file a `slug:` in its front matter or options.files[].slug"})
			}
		}
	}
	if man.Structure == nil {
		return
	}
	st := man.Structure
	bySource := map[string]mappedPage{}
	for _, vm := range m.verbatim {
		for _, pg := range vm.Pages {
			bySource[pg.Source] = pg
		}
	}
	seen := map[string]bool{}
	declared := map[string]bool{}
	walkStructure(st, func(space api.StructureSpace, path []string, coll *api.StructureCollection, page *api.StructurePage) {
		where := space.Slug + "/" + strings.Join(path, "/")
		switch {
		case page != nil:
			declared[space.Slug+"/"+page.Slug] = true
			key := space.Slug + "/page/" + page.Slug
			if seen[key] {
				m.add(issue{Severity: severityError, Code: "duplicate_slug", Path: "structure." + where, Message: fmt.Sprintf("page slug %s is declared twice in space %s", page.Slug, space.Slug), Hint: "page slugs are unique within a space"})
			}
			seen[key] = true
			if page.Source == "" {
				return
			}
			if !src.exists(page.Source) {
				m.add(issue{Severity: severityError, Code: "structure_source_missing", Path: "structure." + where, Message: fmt.Sprintf("page %s declares source %s, which is not in the repository", page.Slug, page.Source), Hint: "fix the path or drop `source:`"})
				return
			}
			mp, ok := bySource[page.Source]
			if !ok {
				m.add(issue{Severity: severityError, Code: "structure_source_unmapped", Path: "structure." + where, Message: fmt.Sprintf("page %s declares source %s, but no verbatim pass maps that file", page.Slug, page.Source), Hint: "add it to a verbatim pass's options.files, or drop `source:` to let structure apply create the page"})
				return
			}
			if mp.Slug != page.Slug {
				m.add(issue{Severity: severityWarning, Code: "structure_slug_mismatch", Path: page.Source, Message: fmt.Sprintf("structure calls this page %s but the verbatim mapping gives it slug %s", page.Slug, mp.Slug), Hint: "set `slug: " + page.Slug + "` in the file's front matter (or options.files[].slug), or rename it in structure:"})
			}
		case coll != nil:
			if len(path) > verbatim.MaxCollectionDepth {
				m.add(issue{Severity: severityError, Code: "structure_depth", Path: "structure." + where, Message: fmt.Sprintf("collection %s is nested %d levels deep; the limit is %d", coll.Slug, len(path), verbatim.MaxCollectionDepth), Hint: "flatten the tree"})
			}
			key := space.Slug + "/coll/" + strings.Join(path, "/")
			if seen[key] {
				m.add(issue{Severity: severityError, Code: "duplicate_slug", Path: "structure." + where, Message: "collection " + strings.Join(path, "/") + " is declared twice", Hint: "merge the two entries"})
			}
			seen[key] = true
		default:
			key := "space/" + space.Slug
			if seen[key] {
				m.add(issue{Severity: severityError, Code: "duplicate_slug", Path: "structure." + space.Slug, Message: "space " + space.Slug + " is declared twice", Hint: "merge the two entries"})
			}
			seen[key] = true
		}
	})
	spaces := map[string]bool{}
	for _, sp := range st.Spaces {
		spaces[sp.Slug] = true
	}
	for _, p := range man.Passes {
		site, space, _ := splitTarget(p.Target)
		if site != st.Site.Slug || space == "" || spaces[space] {
			continue
		}
		m.add(issue{Severity: severityWarning, Code: "target_not_in_structure", Pass: p.Name, Path: p.Target, Message: fmt.Sprintf("pass %s targets space %s, which structure: does not declare", p.Name, space), Hint: "add the space to structure: (or point the pass at a declared space)"})
	}
	for vi, vm := range m.verbatim {
		if vm.site != st.Site.Slug {
			continue
		}
		for pi, pg := range vm.Pages {
			if !declared[vm.space+"/"+pg.Slug] {
				m.verbatim[vi].Pages[pi].Flags = appendFlag(pg.Flags, flagNotInStructure)
			}
		}
	}
}

func (m *model) flagExisting() {
	if m.live == nil {
		return
	}
	existing := map[string]api.StructurePage{}
	walkStructure(m.live, func(space api.StructureSpace, _ []string, _ *api.StructureCollection, page *api.StructurePage) {
		if page != nil {
			existing[space.Slug+"/"+page.Slug] = *page
		}
	})
	me := m.info.remoteKey
	for vi, vm := range m.verbatim {
		if vm.site != m.live.Site.Slug {
			continue
		}
		for pi, pg := range vm.Pages {
			cur, ok := existing[vm.space+"/"+pg.Slug]
			if !ok {
				continue
			}
			if cur.LockedByRepo != "" && cur.LockedByRepo == me || cur.SourceRepo != "" && cur.SourceRepo == me {
				continue
			}
			m.verbatim[vi].Pages[pi].Flags = appendFlag(pg.Flags, flagExistingPage)
		}
	}
}

func appendFlag(flags []string, f string) []string {
	for _, have := range flags {
		if have == f {
			return flags
		}
	}
	return append(flags, f)
}

func sortIssues(list []issue) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Severity != list[j].Severity {
			return list[i].Severity == severityError
		}
		if list[i].Pass != list[j].Pass {
			return list[i].Pass < list[j].Pass
		}
		return list[i].Path < list[j].Path
	})
}

func manifestRel(info *repoInfo, m *config.Manifest) string {
	if m == nil || m.Path == "" {
		return config.ManifestFileName
	}
	if rel, err := filepath.Rel(info.root, m.Path); err == nil {
		return filepath.ToSlash(rel)
	}
	return m.Path
}
