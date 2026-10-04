// Package setup turns local detection and the organization's products and sites into gravity setup's suggestions.
package setup

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// ProductOption is one answer to the product question.
type ProductOption struct {
	Slug  string   `json:"slug"`
	Name  string   `json:"name"`
	New   bool     `json:"new"`
	Score int      `json:"score"`
	Repos []string `json:"repos"`
}

// Label renders the option as shown in the prompt.
func (o ProductOption) Label() string {
	if o.New {
		return "New product: " + o.Slug
	}
	label := firstNonEmpty(o.Name, o.Slug)
	if len(o.Repos) > 0 {
		label += " (" + strings.Join(o.Repos, ", ") + " connected)"
	}
	return label
}

func ownerOf(remoteKey string) string {
	if i := strings.LastIndex(remoteKey, "/"); i > 0 {
		return remoteKey[:i]
	}
	return ""
}

func namePrefix(name string) string {
	for _, sep := range []string{"-", "_", "."} {
		if i := strings.Index(name, sep); i > 0 {
			return name[:i]
		}
	}
	return name
}

// RankProducts orders the product answers by sibling evidence, an org's only product first when nothing points elsewhere; the first entry is the --yes answer.
func RankProducts(products []api.ProductSummary, remoteKey, repoName string) []ProductOption {
	owner := ownerOf(remoteKey)
	prefix := namePrefix(repoName)
	var scored, rest []ProductOption
	newSlug := normalize.ProductSlug(repoName)
	for _, p := range products {
		o := ProductOption{Slug: p.Slug, Name: p.Name, Repos: []string{}}
		for _, r := range p.Repos {
			o.Repos = append(o.Repos, r.Label())
			if owner != "" && ownerOf(r.RemoteKey) == owner {
				o.Score += 2
			}
			if prefix != "" && namePrefix(firstNonEmpty(r.Name, path.Base(r.RemoteKey))) == prefix {
				o.Score++
			}
		}
		if p.Slug == newSlug {
			o.Score += 3
		}
		if o.Score > 0 {
			scored = append(scored, o)
		} else {
			rest = append(rest, o)
		}
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Slug < rest[j].Slug })
	out := append([]ProductOption{}, scored...)
	taken := false
	for _, p := range products {
		if p.Slug == newSlug {
			taken = true
		}
	}
	if len(products) == 1 && len(scored) == 0 && !taken {
		return append(rest, ProductOption{Slug: newSlug, New: true, Repos: []string{}})
	}
	if !taken {
		out = append(out, ProductOption{Slug: newSlug, New: true, Repos: []string{}})
	}
	return append(out, rest...)
}

// Site is the site the suggested passes target.
type Site struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	New      bool   `json:"new"`
	Position int    `json:"position"`
}

// DefaultSite picks the product's most targeted site, else the first site by position, else a new site named after the product.
func DefaultSite(product ProductOption, products []api.ProductSummary, sites []api.Site) Site {
	byPos := append([]api.Site(nil), sites...)
	sort.SliceStable(byPos, func(i, j int) bool { return byPos[i].Position < byPos[j].Position })
	counts := map[string]int{}
	for _, p := range products {
		if p.Slug != product.Slug {
			continue
		}
		for _, t := range p.Targets {
			counts[t.SiteSlug] += max(t.Passes, 1)
		}
	}
	best, bestCount := "", 0
	for _, s := range byPos {
		if c := counts[s.Slug]; c > bestCount {
			best, bestCount = s.Slug, c
		}
	}
	for _, s := range byPos {
		if s.Slug == best {
			return Site{Slug: s.Slug, Name: firstNonEmpty(s.Name, s.Slug), Position: s.Position}
		}
	}
	if len(byPos) > 0 {
		s := byPos[0]
		return Site{Slug: s.Slug, Name: firstNonEmpty(s.Name, s.Slug), Position: s.Position}
	}
	name := firstNonEmpty(product.Name, product.Slug)
	return Site{Slug: product.Slug, Name: name, New: true}
}

// Suggestion is one pass init offers in the passes question.
type Suggestion struct {
	Pass     config.Pass       `json:"pass"`
	Target   string            `json:"targetLabel"`
	Source   string            `json:"source"`
	Selected bool              `json:"selected"`
	Create   *api.CreateTarget `json:"createTarget,omitempty"`
}

// Label renders the suggestion as a prompt option.
func (s Suggestion) Label() string {
	label := fmt.Sprintf("%-32s %-10s", s.Target, s.Pass.Kind)
	if s.Source != "" {
		label += " ← " + s.Source
	}
	if s.Create != nil {
		label += "  [new space]"
	}
	return label
}

// Inputs feed Suggest.
type Inputs struct {
	Detect       *detect.Result
	Site         Site
	Tree         *api.SiteTree
	MemoryModule bool
}

type candidate struct {
	template string
	name     string
	slug     string
	title    string
	source   string
	scope    []string
	options  map[string]any
	selected bool
}

// Suggest maps detection to pass templates and targets inside the chosen site.
func Suggest(in Inputs) []Suggestion {
	d := in.Detect
	if d == nil {
		d = &detect.Result{}
	}
	var cands []candidate
	if len(d.OpenAPI) > 0 {
		doc := d.OpenAPI[0]
		src := "OpenAPI " + doc.Path
		if doc.Operations > 0 {
			src += fmt.Sprintf(" (%d operations)", doc.Operations)
		}
		if len(d.OpenAPI) > 1 {
			src = fmt.Sprintf("OpenAPI %d documents", len(d.OpenAPI))
		}
		cands = append(cands, candidate{template: config.TemplateAPIReference, name: "developer-api", slug: "api", title: "API", source: src, selected: true})
	}
	if d.HasUI() {
		src := fmt.Sprintf("%s routes in %s (%d)", d.UIFramework, strings.TrimSuffix(strings.Join(d.UIPaths, ", "), "/**"), d.UIRoutes)
		cands = append(cands, candidate{template: config.TemplateUserGuide, name: "product-guides", slug: "guides", title: "Guides", source: src, scope: d.UIPaths, selected: true})
	}
	if d.HasServer() {
		src := ""
		switch {
		case d.ServerRoutes > 0 && d.CLICommands > 0:
			src = fmt.Sprintf("%d server routes, %d CLI commands", d.ServerRoutes, d.CLICommands)
		case d.ServerRoutes > 0:
			src = fmt.Sprintf("%d server routes", d.ServerRoutes)
		default:
			src = fmt.Sprintf("%d CLI commands", d.CLICommands)
		}
		cands = append(cands, candidate{template: config.TemplateDeveloperGuide, name: "developer-guides", slug: "developers", title: "Developer guides", source: src, scope: d.ServerPaths, selected: true})
	}
	if len(cands) == 0 && len(d.Languages) > 0 {
		cands = append(cands, candidate{template: config.TemplateDeveloperGuide, name: "developer-guides", slug: "developers", title: "Developer guides", source: d.Languages[0].Name + " code", selected: true})
	}
	if d.ReleaseTags > 0 || d.Changelog != "" {
		tpl := config.TemplateCustomerChangelog
		if !d.HasUI() && len(d.OpenAPI) == 0 {
			tpl = config.TemplateInternalChangelog
		}
		src := d.Changelog
		if d.ReleaseTags > 0 {
			src = fmt.Sprintf("tags v* (%d releases)", d.ReleaseTags)
			if d.ReleaseTags == 1 {
				src = "tags v* (1 release)"
			}
		}
		cands = append(cands, candidate{template: tpl, name: "changelog", slug: "changelog", title: "Changelog", source: src, selected: true})
	}
	if len(d.Runbooks) > 0 {
		rb := d.Runbooks[0]
		cands = append(cands, candidate{template: config.TemplateRunbook, name: "runbooks", slug: "runbooks", title: "Runbooks", source: fmt.Sprintf("%s (%d files)", rb.Path, rb.Files), scope: []string{rb.Path + "/**"}, selected: true})
	}
	for i, f := range d.DocsFolders {
		if i >= 2 {
			break
		}
		slug := normalize.ProductSlug(path.Base(f.Path))
		cands = append(cands, candidate{
			template: config.TemplateVerbatimDocs, name: "docs-" + slug, slug: slug, title: titleCase(path.Base(f.Path)),
			source:  fmt.Sprintf("%s (%d files)", f.Path, f.Files),
			options: map[string]any{"files": []config.VerbatimFile{{Include: f.Path + "/**/*.md", StripPrefix: f.Path}}},
		})
	}
	used := map[string]bool{}
	var out []Suggestion
	for _, c := range cands {
		out = append(out, place(in, c, used))
	}
	out = append(out, Suggestion{
		Pass:     config.Pass{Name: "memory", Kind: config.KindNucleus, Template: config.TemplateNucleusFacts},
		Target:   "Nucleus memory",
		Selected: in.MemoryModule,
	})
	return out
}

func place(in Inputs, c candidate, used map[string]bool) Suggestion {
	kind := config.TemplateKinds[c.template]
	p := config.Pass{Name: c.name, Kind: kind, Template: c.template, Options: c.options}
	if len(c.scope) > 0 {
		p.Scope = &config.Scope{Paths: c.scope}
	}
	s := Suggestion{Pass: p, Source: c.source, Selected: c.selected}
	space, ok := matchSpace(in.Tree, c, used)
	if ok {
		used[space.Slug] = true
		s.Pass.Target = in.Site.Slug + "/" + space.Slug
		s.Target = in.Site.Name + " › " + firstNonEmpty(space.Name, space.Slug)
		return s
	}
	slug := uniqueSlug(in.Tree, c.slug, used)
	used[slug] = true
	s.Pass.Target = in.Site.Slug + "/" + slug
	s.Target = in.Site.Name + " › " + c.title
	s.Create = newTarget(in.Site.Slug, slug, c.title, c.template)
	return s
}

func newTarget(site, space, name, template string) *api.CreateTarget {
	vis := "inherit"
	if template == config.TemplateRunbook || template == config.TemplateInternalChangelog {
		vis = "private"
	}
	typ := ""
	if types := config.TemplateSpaceTypes[template]; len(types) > 0 {
		typ = types[0]
	}
	return &api.CreateTarget{Site: site, Space: space, Name: name, Type: typ, Visibility: vis}
}

// MissingTargets lists createTargets for the spaces that manifest passes target and Gravity reports missing, once each.
func MissingTargets(passes []api.PlanPass) []api.CreateTarget {
	seen := map[string]bool{}
	out := []api.CreateTarget{}
	for _, p := range passes {
		if p.Source != "manifest" || p.Target.Status != api.TargetMissing {
			continue
		}
		parts := strings.Split(p.Target.Ref, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || seen[p.Target.Ref] {
			continue
		}
		seen[p.Target.Ref] = true
		out = append(out, *newTarget(parts[0], parts[1], titleCase(parts[1]), p.Template))
	}
	return out
}

func matchSpace(tree *api.SiteTree, c candidate, used map[string]bool) (api.SiteSpace, bool) {
	if tree == nil {
		return api.SiteSpace{}, false
	}
	for _, typ := range config.TemplateSpaceTypes[c.template] {
		for _, sp := range tree.Spaces {
			if sp.Type == typ && !used[sp.Slug] {
				return sp, true
			}
		}
	}
	for _, sp := range tree.Spaces {
		if sp.Slug == c.slug && !used[sp.Slug] {
			return sp, true
		}
	}
	return api.SiteSpace{}, false
}

func uniqueSlug(tree *api.SiteTree, slug string, used map[string]bool) string {
	exists := func(s string) bool {
		if used[s] {
			return true
		}
		if tree != nil {
			for _, sp := range tree.Spaces {
				if sp.Slug == s {
					return true
				}
			}
		}
		return false
	}
	if !exists(slug) {
		return slug
	}
	for i := 2; ; i++ {
		if c := fmt.Sprintf("%s-%d", slug, i); !exists(c) {
			return c
		}
	}
}

func titleCase(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Scopes returns the token scopes the passes need: the base set plus each enabled pass's requirements.
func Scopes(passes []config.Pass) []string {
	sp := make([]config.ScopedPass, 0, len(passes))
	for _, p := range passes {
		sp = append(sp, config.ScopedPass{Kind: p.Kind, Options: p.Options, Enabled: p.IsEnabled()})
	}
	return config.UnionScopes(sp)
}

// PlanScopes returns the token scopes of the effective passes connect reported.
func PlanScopes(passes []api.PlanPass) []string {
	sp := make([]config.ScopedPass, 0, len(passes))
	for _, p := range passes {
		sp = append(sp, config.ScopedPass{Kind: p.Kind, Options: p.Options, Enabled: p.Enabled})
	}
	return config.UnionScopes(sp)
}

// HasSchedule reports whether any pass runs on the schedule trigger.
func HasSchedule(passes []config.Pass, effective []api.PlanPass) bool {
	for _, p := range passes {
		for _, t := range config.PassTriggers(p) {
			if t == config.TriggerSchedule {
				return true
			}
		}
	}
	for _, p := range effective {
		for _, t := range p.Triggers {
			if t == config.TriggerSchedule {
				return true
			}
		}
	}
	return false
}

// CreateTargets lists the missing spaces the chosen suggestions need, once each.
func CreateTargets(chosen []Suggestion) []api.CreateTarget {
	seen := map[string]bool{}
	out := []api.CreateTarget{}
	for _, s := range chosen {
		if s.Create == nil {
			continue
		}
		key := s.Create.Site + "/" + s.Create.Space
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *s.Create)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
