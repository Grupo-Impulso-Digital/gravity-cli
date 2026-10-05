package setup

import (
	"path"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

// DocsPassName is the name of the verbatim pass a structure draft proposes.
const DocsPassName = "docs"

var packageRoots = []string{"packages", "apps", "services", "libs", "modules", "plugins", "crates"}

// Layout is one repository a structure draft reads.
type Layout struct {
	Name    string
	Files   []string
	Read    func(rel string) ([]byte, bool, error)
	Detect  *detect.Result
	Primary bool
}

// DraftInput feeds DraftStructure.
type DraftInput struct {
	Site     api.StructureSite
	Declared *api.Structure
	Live     *api.Structure
	Layouts  []Layout
}

// Draft is a proposed structure and the verbatim pass that fills its pages.
type Draft struct {
	Structure api.Structure `json:"structure"`
	Pass      *config.Pass  `json:"pass,omitempty"`
	Notes     []string      `json:"notes,omitempty"`
}

type layoutSource struct{ l Layout }

func (s layoutSource) Files() ([]string, error) { return s.l.Files, nil }

func (s layoutSource) Read(p string) ([]byte, bool, error) { return s.l.Read(p) }

// DocsFiles proposes the options.files of a verbatim pass for a repository layout: the root README, package READMEs and docs folders.
func DocsFiles(files []string) []config.VerbatimFile {
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	var out []config.VerbatimFile
	if has["README.md"] {
		out = append(out, config.VerbatimFile{Include: "README.md", Slug: "overview"})
	}
	if docsFolder(files, "docs") {
		out = append(out, config.VerbatimFile{Include: "docs/**/*.md", StripPrefix: "docs"})
	}
	for _, pkg := range packages(files) {
		slug := docs.Slug(path.Base(pkg))
		if slug == "" {
			continue
		}
		if has[pkg+"/README.md"] {
			out = append(out, config.VerbatimFile{Include: pkg + "/README.md", Collection: slug, Slug: slug + "-overview"})
		}
		if docsFolder(files, pkg+"/docs") {
			out = append(out, config.VerbatimFile{Include: pkg + "/docs/**/*.md", Collection: slug, StripPrefix: pkg + "/docs"})
		}
	}
	return out
}

func docsFolder(files []string, dir string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, dir+"/") && (strings.HasSuffix(f, ".md") || strings.HasSuffix(f, ".mdx")) && !strings.Contains(f, "/node_modules/") {
			return true
		}
	}
	return false
}

func packages(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		parts := strings.Split(f, "/")
		if len(parts) < 3 {
			continue
		}
		for _, root := range packageRoots {
			if parts[0] != root {
				continue
			}
			pkg := parts[0] + "/" + parts[1]
			if !seen[pkg] && (parts[2] == "README.md" || parts[2] == "docs") {
				seen[pkg] = true
				out = append(out, pkg)
			}
		}
	}
	sort.Strings(out)
	return out
}

// DraftStructure builds a deterministic structure from repository layouts, keeping what is declared and reusing live spaces.
func DraftStructure(in DraftInput) Draft {
	d := Draft{}
	st := api.Structure{Site: in.Site}
	if in.Declared != nil {
		st = cloneStructure(*in.Declared)
		if st.Site.Name == "" {
			st.Site.Name = in.Site.Name
		}
	}
	docsSpace := pickSpace(in.Live, "docs", "Documentation", "product-docs", []string{"product-docs", "knowledge-base", "developer-docs"})
	for _, l := range in.Layouts {
		if !l.Primary {
			for _, pkg := range packages(l.Files) {
				slug := docs.Slug(path.Base(pkg))
				addCollection(&st, docsSpace, api.StructureCollection{Slug: slug, Title: humanTitle(path.Base(pkg))})
			}
			d.Notes = append(d.Notes, l.Name+": collections only; its pages come from its own verbatim import")
			continue
		}
		files := DocsFiles(l.Files)
		if len(files) > 0 {
			pass := config.Pass{Name: DocsPassName, Kind: config.KindVerbatim, Target: st.Site.Slug + "/" + docsSpace.Slug, Triggers: []string{config.TriggerPush}, Options: map[string]any{"files": files}}
			d.Pass = &pass
			specs := make([]verbatim.FileSpec, 0, len(files))
			for _, f := range files {
				specs = append(specs, verbatim.FileSpec{Include: f.Include, Exclude: f.Exclude, Collection: f.Collection, StripPrefix: f.StripPrefix, Slug: f.Slug, Title: f.Title})
			}
			if m, err := verbatim.Map(layoutSource{l}, verbatim.MapOptions{Files: specs}); err == nil {
				empty := map[string]bool{}
				for _, pg := range m.Pages {
					empty[pg.Path] = empty[pg.Path] || pg.Empty
				}
				kept := files[:0]
				for _, f := range files {
					if !empty[f.Include] {
						kept = append(kept, f)
					}
				}
				pass.Options["files"] = kept
				for _, pg := range m.Pages {
					if pg.Empty {
						continue
					}
					var chain []api.StructureCollection
					for depth := range pg.CollectionPath {
						key := strings.Join(pg.CollectionPath[:depth+1], "/")
						chain = append(chain, api.StructureCollection{Slug: pg.CollectionPath[depth], Title: m.CollectionTitles[key]})
					}
					addPage(&st, docsSpace, chain, api.StructurePage{Slug: pg.Slug, Title: pg.Title, Source: pg.Path})
				}
			}
		}
		if l.Detect != nil && len(l.Detect.OpenAPI) > 0 {
			ensureSpace(&st, pickSpace(in.Live, "api", "API reference", "api-reference", []string{"api-reference"}))
		}
		if l.Detect != nil && (l.Detect.ReleaseTags > 0 || l.Detect.Changelog != "") {
			ensureSpace(&st, pickSpace(in.Live, "changelog", "Changelog", "release-notes", []string{"release-notes"}))
		}
		if l.Detect != nil && len(l.Detect.Runbooks) > 0 {
			sp := pickSpace(in.Live, "runbooks", "Runbooks", "handbook", []string{"handbook"})
			if sp.Visibility == "" {
				sp.Visibility = "private"
			}
			ensureSpace(&st, sp)
		}
	}
	d.Structure = st
	return d
}

func humanTitle(s string) string {
	if t, ok := verbatim.HumanizeTitle(s); ok {
		return t
	}
	return titleCase(s)
}

func pickSpace(live *api.Structure, slug, name, typ string, types []string) api.StructureSpace {
	if live != nil {
		for _, sp := range live.Spaces {
			if sp.Slug == slug {
				return api.StructureSpace{Slug: sp.Slug, Name: firstNonEmpty(sp.Name, name), Type: firstNonEmpty(sp.Type, typ), Visibility: sp.Visibility}
			}
		}
		for _, sp := range live.Spaces {
			for _, t := range types {
				if sp.Type == t {
					return api.StructureSpace{Slug: sp.Slug, Name: firstNonEmpty(sp.Name, name), Type: sp.Type, Visibility: sp.Visibility}
				}
			}
		}
	}
	return api.StructureSpace{Slug: slug, Name: name, Type: typ, Visibility: "public"}
}

func ensureSpace(st *api.Structure, sp api.StructureSpace) *api.StructureSpace {
	for i := range st.Spaces {
		if st.Spaces[i].Slug == sp.Slug {
			return &st.Spaces[i]
		}
	}
	st.Spaces = append(st.Spaces, sp)
	return &st.Spaces[len(st.Spaces)-1]
}

func addCollection(st *api.Structure, sp api.StructureSpace, c api.StructureCollection) {
	space := ensureSpace(st, sp)
	for _, have := range space.Collections {
		if have.Slug == c.Slug {
			return
		}
	}
	space.Collections = append(space.Collections, c)
}

func addPage(st *api.Structure, sp api.StructureSpace, chain []api.StructureCollection, page api.StructurePage) {
	space := ensureSpace(st, sp)
	if hasPage(space.Pages, space.Collections, page) {
		return
	}
	pages, cols := &space.Pages, &space.Collections
	for _, link := range chain {
		var next *api.StructureCollection
		for i := range *cols {
			if (*cols)[i].Slug == link.Slug {
				next = &(*cols)[i]
			}
		}
		if next == nil {
			*cols = append(*cols, api.StructureCollection{Slug: link.Slug, Title: link.Title})
			next = &(*cols)[len(*cols)-1]
		}
		pages, cols = &next.Pages, &next.Collections
	}
	*pages = append(*pages, page)
}

func hasPage(pages []api.StructurePage, cols []api.StructureCollection, page api.StructurePage) bool {
	for _, p := range pages {
		if p.Slug == page.Slug || (page.Source != "" && p.Source == page.Source) {
			return true
		}
	}
	for _, c := range cols {
		if hasPage(c.Pages, c.Collections, page) {
			return true
		}
	}
	return false
}

func cloneStructure(s api.Structure) api.Structure {
	out := api.Structure{Site: s.Site}
	for _, sp := range s.Spaces {
		c := sp
		c.Pages = append([]api.StructurePage(nil), sp.Pages...)
		c.Collections = cloneCollections(sp.Collections)
		out.Spaces = append(out.Spaces, c)
	}
	return out
}

func cloneCollections(cols []api.StructureCollection) []api.StructureCollection {
	if cols == nil {
		return nil
	}
	out := make([]api.StructureCollection, len(cols))
	for i, c := range cols {
		out[i] = c
		out[i].Pages = append([]api.StructurePage(nil), c.Pages...)
		out[i].Collections = cloneCollections(c.Collections)
	}
	return out
}

// DiffItem is one node of a structure compared with the live site.
type DiffItem struct {
	Op    string `json:"op"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
}

// Diff operations.
const (
	DiffCreate = "create"
	DiffExists = "exists"
	DiffExtra  = "extra"
	DiffImport = "import"
)

// DiffStructure compares a declared structure with the live tree; live may be nil.
func DiffStructure(declared api.Structure, live *api.Structure) []DiffItem {
	have := map[string]bool{}
	if live != nil {
		for _, it := range flatten(*live) {
			have[it.Kind+" "+it.Path] = true
		}
	}
	want := map[string]bool{}
	out := []DiffItem{}
	for _, it := range flatten(declared) {
		want[it.Kind+" "+it.Path] = true
		switch {
		case have[it.Kind+" "+it.Path]:
			it.Op = DiffExists
		case it.Op == DiffImport:
		default:
			it.Op = DiffCreate
		}
		out = append(out, it)
	}
	if live != nil {
		for _, it := range flatten(*live) {
			if !want[it.Kind+" "+it.Path] {
				it.Op = DiffExtra
				out = append(out, it)
			}
		}
	}
	return out
}

func flatten(s api.Structure) []DiffItem {
	var out []DiffItem
	var walk func(prefix string, cols []api.StructureCollection)
	page := func(prefix string, p api.StructurePage) {
		it := DiffItem{Kind: "page", Path: prefix + "#" + p.Slug, Title: p.Title}
		if p.Source != "" {
			it.Op = DiffImport
		}
		out = append(out, it)
	}
	walk = func(prefix string, cols []api.StructureCollection) {
		for _, c := range cols {
			p := prefix + "/" + c.Slug
			out = append(out, DiffItem{Kind: "collection", Path: p, Title: c.Title})
			for _, pg := range c.Pages {
				page(p, pg)
			}
			walk(p, c.Collections)
		}
	}
	for _, sp := range s.Spaces {
		out = append(out, DiffItem{Kind: "space", Path: sp.Slug, Title: sp.Name})
		for _, pg := range sp.Pages {
			page(sp.Slug, pg)
		}
		walk(sp.Slug, sp.Collections)
	}
	return out
}
