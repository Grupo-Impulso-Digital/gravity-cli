package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config/legacy"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// ErrNotV1 is returned when converting a manifest that is not a v1 file.
var ErrNotV1 = errors.New("not a v1 manifest")

// Conversion actions recorded in the mapping report.
const (
	ActionMapped  = "mapped"
	ActionDropped = "dropped"
)

// ConvertOptions carries what the conversion needs from outside the file.
type ConvertOptions struct {
	RepoName string
}

// ConvertNote is one line of the mapping report.
type ConvertNote struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

// DeclaredSpace is a v1 spaces.declare[] entry that init creates when it is missing.
type DeclaredSpace struct {
	Site       string `json:"site"`
	Slug       string `json:"slug"`
	Name       string `json:"name,omitempty"`
	Parent     string `json:"parent,omitempty"`
	Type       string `json:"type,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// Conversion is the v2 result of converting a v1 manifest.
type Conversion struct {
	Manifest *Manifest       `json:"-"`
	YAML     []byte          `json:"-"`
	Site     string          `json:"site"`
	Product  string          `json:"product,omitempty"`
	RepoName string          `json:"repoName,omitempty"`
	Spaces   []DeclaredSpace `json:"spaces"`
	Notes    []ConvertNote   `json:"notes"`
	Adopts   bool            `json:"adopts"`
}

type converter struct {
	p        legacy.Project
	raw      map[string]any
	opts     ConvertOptions
	notes    []ConvertNote
	noted    map[string]bool
	site     string
	product  string
	audience map[string][]string
	passes   []Pass
	openapi  []string
}

// ConvertV1 converts a v1 .gravity.yaml to a validated v2 manifest and reports every key it mapped or dropped.
func ConvertV1(data []byte, opts ConvertOptions) (*Conversion, error) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("read v1 manifest: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("read v1 manifest: the manifest must be a YAML mapping")
	}
	if !IsV1(root) {
		return nil, ErrNotV1
	}
	if issues := tokenIssues(root, nil); len(issues) > 0 {
		return nil, &ManifestError{Issues: issues}
	}
	c := &converter{raw: root, opts: opts, noted: map[string]bool{}, audience: map[string][]string{}}
	if err := yaml.Unmarshal(data, &c.p); err != nil {
		return nil, fmt.Errorf("read v1 manifest: %w", err)
	}
	return c.convert()
}

func (c *converter) note(key, action, format string, args ...any) {
	c.notes = append(c.notes, ConvertNote{Key: key, Action: action, Detail: fmt.Sprintf(format, args...)})
	c.noted[key] = true
}

func (c *converter) target(space string, rest ...string) string {
	parts := append([]string{c.site, space}, rest...)
	return strings.Join(parts, "/")
}

func (c *converter) convert() (*Conversion, error) {
	p := c.p
	c.note("version", ActionMapped, "version: 2")
	c.site = strings.TrimSpace(p.Site)
	if c.site != "" {
		c.note("site", ActionMapped, "prefix of every pass target (%s/...)", c.site)
	}
	m := &Manifest{Version: ManifestVersion}
	if p.APIURL != "" {
		m.APIURL = p.APIURL
		c.note("apiUrl", ActionMapped, "apiUrl")
	}
	c.convertProduct(m)
	c.convertCode(m)
	for _, d := range p.Spaces.Declare {
		if len(d.Audiences) > 0 {
			c.audience[d.Slug] = d.Audiences
		}
	}
	c.convertGuides()
	c.convertSources()
	c.convertDocuments()
	c.convertKnowledge()
	c.convertCoverage()
	spaces := c.convertDeclare()
	if len(c.openapi) > 0 {
		if m.Code == nil {
			m.Code = &Code{}
		}
		m.Code.OpenAPI = append(m.Code.OpenAPI, c.openapi...)
	}
	m.Passes = c.passes
	c.reportUnmapped()
	out, err := Render(m)
	if err != nil {
		return nil, err
	}
	parsed, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("the converted manifest is invalid: %w", err)
	}
	conv := &Conversion{Manifest: parsed, YAML: out, Site: c.site, Product: c.product, RepoName: p.Product.Repo, Spaces: spaces, Notes: c.notes}
	for _, ps := range parsed.Passes {
		if ps.Kind == KindVerbatim {
			conv.Adopts = true
		}
	}
	if conv.Spaces == nil {
		conv.Spaces = []DeclaredSpace{}
	}
	return conv, nil
}

func (c *converter) convertProduct(m *Manifest) {
	pr := c.p.Product
	if pr.Slug != "" {
		slug := normalize.ProductSlug(pr.Slug)
		c.product = slug
		m.Product = slug
		if slug == pr.Slug {
			c.note("product.slug", ActionMapped, "product: %s", slug)
		} else {
			c.note("product.slug", ActionMapped, "product: %s (normalized from %q)", slug, pr.Slug)
		}
	}
	if pr.Repo != "" {
		c.note("product.repo", ActionDropped, "repository identity comes from the git remote (%s is used only when there is no remote)", pr.Repo)
	}
}

func (c *converter) convertCode(m *Manifest) {
	d := c.p.Discovery
	code := &Code{Include: d.Include, Exclude: d.Exclude, Entrypoints: d.Entrypoints}
	for _, k := range []struct {
		key  string
		list []string
		to   string
	}{{"discovery.include", d.Include, "code.include"}, {"discovery.exclude", d.Exclude, "code.exclude"}, {"discovery.entrypoints", d.Entrypoints, "code.entrypoints"}} {
		if len(k.list) > 0 {
			c.note(k.key, ActionMapped, "%s", k.to)
		}
	}
	units := &Units{}
	switch role := c.p.Product.Role; role {
	case "api", "service":
		units.Kind = "service"
		c.note("product.role", ActionMapped, "code.units.kind: service")
	case "frontend":
		units.Kind = "feature"
		c.note("product.role", ActionMapped, "code.units.kind: feature")
	case "docs":
		units.Role = Roles{"documents"}
		c.note("product.role", ActionMapped, "code.units.role: documents")
	case "":
	default:
		c.note("product.role", ActionDropped, "role %q has no v2 equivalent", role)
	}
	if d.Units != "" {
		switch d.Units {
		case "auto", "feature", "service", "system", "api", "capability":
			units.Kind = d.Units
			c.note("discovery.units", ActionMapped, "code.units.kind: %s", d.Units)
		default:
			c.note("discovery.units", ActionDropped, "unit kind %q is not a v2 unit kind", d.Units)
		}
	}
	if units.Kind != "" || len(units.Role) > 0 {
		code.Units = units
	}
	if len(code.Include)+len(code.Exclude)+len(code.Entrypoints) > 0 || code.Units != nil {
		m.Code = code
	}
}

func (c *converter) languages() []string {
	return c.p.I18n.Languages
}

func (c *converter) withLanguages(opts map[string]any) map[string]any {
	if langs := c.languages(); len(langs) > 0 {
		if opts == nil {
			opts = map[string]any{}
		}
		opts["languages"] = langs
	}
	return opts
}

func (c *converter) passAudiences(space string, fallback []string) []string {
	if a := c.audience[space]; len(a) > 0 {
		return a
	}
	return fallback
}

func (c *converter) needSite(key string) bool {
	if c.site != "" {
		return true
	}
	c.note(key, ActionDropped, "no site: a pass target needs <site>/<space>")
	return false
}

func (c *converter) defaultSpace() string {
	if c.p.Spaces.Default != "" {
		return c.p.Spaces.Default
	}
	return c.p.LegacySpace
}

func (c *converter) convertGuides() {
	sp := c.p.Spaces
	template := TemplateUserGuide
	if r := c.p.Product.Role; r == "api" || r == "service" {
		template = TemplateDeveloperGuide
	}
	audiences := c.p.Discovery.Audiences.Default
	if len(audiences) > 0 {
		c.note("discovery.audiences.default", ActionMapped, "audiences of the generated guides passes")
	}
	def := c.defaultSpace()
	key := "spaces.default"
	if sp.Default == "" && c.p.LegacySpace != "" {
		key = "space"
	}
	if def != "" && c.needSite(key) {
		c.passes = append(c.passes, Pass{
			Name: "docs", Kind: KindGuides, Template: template, Target: c.target(def),
			Triggers: []string{TriggerPush, TriggerPR}, Audiences: c.passAudiences(def, audiences),
			Options: c.withLanguages(nil),
		})
		c.note(key, ActionMapped, "guides pass docs (%s) -> %s", template, c.target(def))
	}
	if len(sp.Shared) > 0 && c.needSite("spaces.shared") {
		repo := normalize.ProductSlug(firstNonBlank(c.opts.RepoName, c.p.Product.Repo))
		var targets []string
		for _, s := range sp.Shared {
			t := c.target(s, repo)
			targets = append(targets, t)
			c.passes = append(c.passes, Pass{
				Name: "guides-" + s, Kind: KindGuides, Template: template, Target: t,
				Triggers: []string{TriggerPush, TriggerPR}, Audiences: c.passAudiences(s, audiences),
				Options: c.withLanguages(nil),
			})
		}
		c.note("spaces.shared", ActionMapped, "guides passes into this repository's collection of each shared space: %s", strings.Join(targets, ", "))
	}
	if sp.Parent != "" {
		c.note("spaces.parent", ActionDropped, "space structure is app state (was %s)", sp.Parent)
	}
	if sp.Home != "" {
		c.note("spaces.home", ActionDropped, "pinned pages are app state (was %s)", sp.Home)
	}
	if len(c.p.I18n.Languages) > 0 {
		c.note("i18n.languages", ActionMapped, "options.languages on every guides, reference and verbatim pass")
	}
}

func (c *converter) convertSources() {
	type group struct {
		space   string
		sources []ReferenceSource
	}
	var order []string
	groups := map[string]*group{}
	sawGenerator, sawCode := false, false
	for _, s := range c.p.Sources {
		if s.Generator != "" {
			sawGenerator = true
		}
		kind := s.Kind
		if kind == "" {
			kind = "openapi"
		}
		if kind != "openapi" {
			sawCode = true
			continue
		}
		space := firstNonBlank(s.Space, c.defaultSpace())
		if space == "" || s.Source == "" {
			continue
		}
		g, ok := groups[space]
		if !ok {
			g = &group{space: space}
			groups[space] = g
			order = append(order, space)
		}
		g.sources = append(g.sources, ReferenceSource{Path: s.Source, Page: s.Page, Title: s.Title, Collection: s.Collection})
		if !contains(c.openapi, s.Source) {
			c.openapi = append(c.openapi, s.Source)
		}
	}
	if len(order) > 0 && c.needSite("sources") {
		var names []string
		for _, space := range order {
			g := groups[space]
			name := "api-" + space
			names = append(names, name)
			c.passes = append(c.passes, Pass{
				Name: name, Kind: KindReference, Template: TemplateAPIReference, Target: c.target(space),
				Triggers: []string{TriggerPush, TriggerPR}, Audiences: c.passAudiences(space, nil),
				Options: c.withLanguages(map[string]any{"sources": g.sources}),
			})
		}
		c.note("sources", ActionMapped, "reference passes %s; each source path added to code.openapi", strings.Join(names, ", "))
	}
	if sawCode {
		c.note("sources[].kind", ActionDropped, "kind code sources were already rejected by v1")
	}
	if sawGenerator {
		c.note("sources[].generator", ActionDropped, "dead key")
	}
	if len(c.p.Sources) > 0 {
		for _, k := range []string{"sources[].source", "sources[].space", "sources[].page", "sources[].title", "sources[].collection"} {
			c.noted[k] = true
		}
		if !sawCode {
			c.noted["sources[].kind"] = true
		}
	}
}

func (c *converter) convertDocuments() {
	type group struct {
		space string
		files []VerbatimFile
	}
	var order []string
	groups := map[string]*group{}
	var release *legacy.DocMap
	sawOwnership, sawVersion := false, false
	for i := range c.p.Documents {
		d := c.p.Documents[i]
		if d.Ownership != "" {
			sawOwnership = true
		}
		if d.Version != "" {
			sawVersion = true
		}
		if d.As == "release" {
			if release == nil {
				release = &d
			} else {
				c.note("documents[].as", ActionDropped, "only the first as: release document (%s) becomes the changelog file; %s is dropped", release.File, d.File)
			}
			continue
		}
		space := firstNonBlank(d.Space, c.defaultSpace())
		if space == "" || d.File == "" {
			continue
		}
		g, ok := groups[space]
		if !ok {
			g = &group{space: space}
			groups[space] = g
			order = append(order, space)
		}
		g.files = append(g.files, VerbatimFile{Include: d.File, Collection: d.Collection, Slug: d.Page, Title: d.Title})
	}
	if len(order) > 0 && c.needSite("documents") {
		var names []string
		for _, space := range order {
			name := "docs-" + space
			names = append(names, name)
			c.passes = append(c.passes, Pass{
				Name: name, Kind: KindVerbatim, Target: c.target(space), Triggers: []string{TriggerPush},
				Audiences: c.passAudiences(space, nil),
				Options:   c.withLanguages(map[string]any{"files": groups[space].files, "adopt": true}),
			})
		}
		c.note("documents", ActionMapped, "verbatim passes %s with adopt: true and per-file slug/title overrides; the pages become repo-locked once their import is accepted", strings.Join(names, ", "))
	}
	if sawOwnership {
		c.note("documents[].ownership", ActionDropped, "verbatim pages are machine-owned and locked (v1 ownership: human pages were editable in Gravity)")
	}
	if sawVersion {
		c.note("documents[].version", ActionDropped, "versions are git history")
	}
	if len(c.p.Documents) > 0 && len(order) == 0 {
		c.note("documents", ActionMapped, "only as: release documents, which feed the changelog pass (see documents[].as)")
	}
	if len(c.p.Documents) > 0 {
		for _, k := range []string{"documents[].file", "documents[].space", "documents[].page", "documents[].title", "documents[].collection", "documents[].as"} {
			c.noted[k] = true
		}
	}
	c.convertChangelog(release)
}

func (c *converter) convertChangelog(release *legacy.DocMap) {
	rn := c.p.ReleaseNotes
	if rn.Changelog != "" {
		c.note("releaseNotes.changelog", ActionDropped, "dead key")
	}
	switch {
	case rn.Space != "" && c.needSite("releaseNotes.space"):
		p := Pass{Name: "changelog", Kind: KindChangelog, Template: TemplateCustomerChangelog, Target: c.target(rn.Space), Triggers: []string{TriggerRelease}}
		detail := "changelog pass changelog (customer-changelog) -> " + p.Target
		if release != nil {
			p.Options = map[string]any{"source": "changelog-file", "changelogFile": release.File}
			detail += ", reading " + release.File
			c.note("documents[].as", ActionMapped, "the as: release document %s is the changelog pass's changelogFile", release.File)
		}
		c.passes = append(c.passes, p)
		c.note("releaseNotes.space", ActionMapped, "%s", detail)
	case release != nil:
		space := firstNonBlank(release.Space, c.defaultSpace())
		if space == "" || !c.needSite("documents[].as") {
			return
		}
		p := Pass{
			Name: "release-notes", Kind: KindChangelog, Target: c.target(space), Triggers: []string{TriggerRelease},
			Options: map[string]any{"source": "changelog-file", "changelogFile": release.File},
		}
		c.passes = append(c.passes, p)
		c.note("documents[].as", ActionMapped, "changelog pass release-notes -> %s reading %s", p.Target, release.File)
	}
}

func (c *converter) convertKnowledge() {
	k := c.p.Knowledge
	if k.Scope != "" {
		c.note("knowledge.scope", ActionDropped, "dead key")
	}
	if k.Namespace == "" {
		return
	}
	p := Pass{Name: "memory", Kind: KindNucleus, Template: TemplateNucleusFacts, Triggers: []string{TriggerPush}}
	ns := "product:" + c.product
	switch {
	case c.product != "" && (k.Namespace == ns || strings.HasPrefix(k.Namespace, ns+"/")):
		p.Options = map[string]any{"namespace": k.Namespace}
		c.note("knowledge.namespace", ActionMapped, "nucleus pass memory with options.namespace: %s", k.Namespace)
	case c.product != "":
		c.note("knowledge.namespace", ActionDropped, "nucleus pass memory writes to the product namespace %s; %q is not inside it", ns, k.Namespace)
	default:
		c.note("knowledge.namespace", ActionDropped, "nucleus pass memory writes to the product namespace product:<slug>; %q is not inside it", k.Namespace)
	}
	c.passes = append(c.passes, p)
}

func (c *converter) convertCoverage() {
	cov := c.p.Coverage
	if cov.Min == 0 && len(cov.Require) == 0 {
		return
	}
	opts := map[string]any{"failOn": []string{"drift", "claims", "verbatim", "coverage"}}
	if cov.Min != 0 {
		ratio := cov.Min
		if ratio > 1 && ratio <= 100 {
			ratio /= 100
			c.note("coverage.min", ActionMapped, "check pass pr-check options.coverageMin: %g (from %g%%)", ratio, cov.Min)
		} else {
			c.note("coverage.min", ActionMapped, "check pass pr-check options.coverageMin: %g", ratio)
		}
		opts["coverageMin"] = ratio
	}
	if len(cov.Require) > 0 {
		opts["require"] = cov.Require
		c.note("coverage.require", ActionMapped, "check pass pr-check options.require")
	}
	c.passes = append(c.passes, Pass{Name: "pr-check", Kind: KindCheck, Triggers: []string{TriggerPR}, Options: opts})
}

func (c *converter) convertDeclare() []DeclaredSpace {
	var out []DeclaredSpace
	for _, d := range c.p.Spaces.Declare {
		if d.Slug == "" {
			continue
		}
		out = append(out, DeclaredSpace{Site: c.site, Slug: d.Slug, Name: d.Name, Parent: d.Parent, Type: d.Type, Visibility: d.Visibility})
	}
	if len(out) > 0 {
		slugs := make([]string, len(out))
		for i, d := range out {
			slugs[i] = d.Slug
		}
		c.note("spaces.declare", ActionMapped, "spaces %s are created in Gravity by init when missing; their audiences apply to the passes targeting them", strings.Join(slugs, ", "))
		for _, k := range []string{"spaces.declare[].slug", "spaces.declare[].name", "spaces.declare[].parent", "spaces.declare[].type", "spaces.declare[].visibility", "spaces.declare[].audiences"} {
			c.noted[k] = true
		}
	}
	return out
}

func (c *converter) reportUnmapped() {
	for _, key := range LiveKeys(c.raw) {
		if c.noted[key] || c.noted[parentKey(key)] {
			continue
		}
		c.note(key, ActionDropped, "not part of the v1 schema")
	}
}

func parentKey(key string) string {
	if i := strings.LastIndex(key, "."); i > 0 {
		return key[:i]
	}
	return ""
}

// LiveKeys lists the dotted keys present in a decoded v1 manifest (list items as key[].field).
func LiveKeys(root map[string]any) []string {
	var keys []string
	var walk func(prefix string, v any, depth int)
	walk = func(prefix string, v any, depth int) {
		switch t := v.(type) {
		case map[string]any:
			if depth >= 3 && prefix != "" {
				keys = append(keys, prefix)
				return
			}
			if len(t) == 0 && prefix != "" {
				keys = append(keys, prefix)
			}
			for k, e := range t {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, e, depth+1)
			}
		case []any:
			fields := false
			for _, e := range t {
				if m, ok := e.(map[string]any); ok {
					fields = true
					for k := range m {
						keys = append(keys, prefix+"[]."+k)
					}
				}
			}
			if !fields {
				keys = append(keys, prefix)
			}
		default:
			if prefix != "" {
				keys = append(keys, prefix)
			}
		}
	}
	walk("", root, 0)
	sort.Strings(keys)
	out := keys[:0]
	for i, k := range keys {
		if i == 0 || k != keys[i-1] {
			out = append(out, k)
		}
	}
	return out
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
