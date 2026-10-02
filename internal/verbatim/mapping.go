package verbatim

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
)

// MaxCollectionDepth is the deepest collection level below a target.
const MaxCollectionDepth = 5

// DefaultIndexFiles become their folder's overview page.
var DefaultIndexFiles = []string{"index.md", "README.md", "_index.md"}

var markdownExts = map[string]bool{".md": true, ".mdx": true, ".markdown": true}

// FileSpec is one options.files entry of a verbatim pass.
type FileSpec struct {
	Include     string   `json:"include"`
	Exclude     []string `json:"exclude,omitempty"`
	Collection  string   `json:"collection,omitempty"`
	StripPrefix string   `json:"stripPrefix,omitempty"`
	Slug        string   `json:"slug,omitempty"`
	Title       string   `json:"title,omitempty"`
}

// Source is the repository at the head of the range under review.
type Source interface {
	Files() ([]string, error)
	Read(path string) ([]byte, bool, error)
}

// MapOptions configure file mapping.
type MapOptions struct {
	Files      []FileSpec
	IndexFiles []string
	Detached   []string
}

// Page is one mapped file and the page it becomes.
type Page struct {
	Path           string      `json:"path"`
	Slug           string      `json:"slug"`
	Title          string      `json:"title"`
	TitleFromH1    bool        `json:"-"`
	Description    string      `json:"description,omitempty"`
	Position       int         `json:"position"`
	CollectionPath []string    `json:"collectionPath"`
	Hash           string      `json:"hash"`
	Index          bool        `json:"index,omitempty"`
	MDX            bool        `json:"-"`
	FrontMatter    FrontMatter `json:"-"`
	Body           []byte      `json:"-"`
	explicitOrder  *int
	pinned         bool
	sourceDir      string
	collectionDirs []string
}

// Mapping is the full file-to-page mapping of a verbatim pass.
type Mapping struct {
	Pages            []Page            `json:"pages"`
	Hidden           []string          `json:"hidden,omitempty"`
	CollectionTitles map[string]string `json:"collectionTitles"`
	Warnings         []string          `json:"warnings,omitempty"`
	byPath           map[string]int
}

// Map selects the files of a verbatim pass and decides each page's slug, title, collection and position.
func Map(src Source, opts MapOptions) (*Mapping, error) {
	all, err := src.Files()
	if err != nil {
		return nil, err
	}
	sort.Strings(all)
	index := opts.IndexFiles
	if len(index) == 0 {
		index = DefaultIndexFiles
	}
	detached := map[string]bool{}
	for _, d := range opts.Detached {
		detached[strings.TrimPrefix(d, "./")] = true
	}
	m := &Mapping{CollectionTitles: map[string]string{}, byPath: map[string]int{}}
	claimed := map[string]bool{}
	for _, spec := range opts.Files {
		include := strings.TrimPrefix(spec.Include, "./")
		literal := !glob.HasMeta(include)
		root := strings.TrimSuffix(strings.TrimPrefix(spec.StripPrefix, "./"), "/")
		if root == "" {
			if literal {
				root = path.Dir(include)
			} else {
				root = strings.TrimSuffix(glob.Root(include), "/")
			}
		}
		if root == "." {
			root = ""
		}
		for _, f := range all {
			if claimed[f] || !markdownExts[strings.ToLower(path.Ext(f))] || detached[f] {
				continue
			}
			if literal && f != include || !literal && !glob.Match(include, f) {
				continue
			}
			if glob.MatchAny(spec.Exclude, f) {
				continue
			}
			claimed[f] = true
			if err := m.add(src, f, spec, root, literal, index); err != nil {
				return nil, err
			}
		}
	}
	m.dedupeSlugs()
	m.order()
	m.titles(src)
	return m, nil
}

func (m *Mapping) add(src Source, file string, spec FileSpec, root string, literal bool, index []string) error {
	data, ok, err := src.Read(file)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	fm, body, err := SplitFrontMatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if fm.Hidden {
		m.Hidden = append(m.Hidden, file)
		return nil
	}
	if fm.Lang != "" {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%s: skipped (lang %s); language versions of verbatim files are not imported by this CLI yet", file, fm.Lang))
		return nil
	}
	dir := path.Dir(file)
	rel := strings.TrimPrefix(strings.TrimPrefix(dir, root), "/")
	if dir == root || dir == "." {
		rel = ""
	}
	var segs, dirs []string
	for _, s := range strings.Split(spec.Collection, "/") {
		if s != "" {
			segs = append(segs, docs.Slug(s))
			dirs = append(dirs, "")
		}
	}
	acc := root
	for _, s := range strings.Split(rel, "/") {
		if s == "" {
			continue
		}
		acc = path.Join(acc, s)
		if sl := docs.Slug(s); sl != "" {
			segs = append(segs, sl)
			dirs = append(dirs, acc)
		}
	}
	if len(segs) > MaxCollectionDepth {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%s: nested deeper than %d collections; flattened into %s", file, MaxCollectionDepth, strings.Join(segs[:MaxCollectionDepth], "/")))
		segs, dirs = segs[:MaxCollectionDepth], dirs[:MaxCollectionDepth]
	}
	base := path.Base(file)
	p := Page{
		Path: file, Hash: docs.HashBytes(data), FrontMatter: fm, Body: body, MDX: strings.EqualFold(path.Ext(file), ".mdx"),
		CollectionPath: append([]string{}, segs...), Description: fm.Description, explicitOrder: fm.Position,
		sourceDir: dir, collectionDirs: dirs,
	}
	if p.CollectionPath == nil {
		p.CollectionPath = []string{}
	}
	isIndex := false
	for _, name := range index {
		if base == name {
			isIndex = true
		}
	}
	p.Index = isIndex
	switch {
	case literal && spec.Slug != "":
		p.Slug = spec.Slug
		p.pinned = true
	case fm.Slug != "":
		p.Slug = docs.Slug(fm.Slug)
	case isIndex:
		p.Slug = "overview"
	default:
		p.Slug = docs.Slug(strings.TrimSuffix(base, path.Ext(base)))
	}
	if p.Slug == "" {
		p.Slug = "page"
	}
	switch {
	case literal && spec.Title != "":
		p.Title = spec.Title
	case fm.Title != "":
		p.Title = fm.Title
	default:
		if h1 := firstH1(body); h1 != "" {
			p.Title, p.TitleFromH1 = h1, true
		} else {
			p.Title = humanize(strings.TrimSuffix(base, path.Ext(base)))
		}
	}
	m.byPath[file] = len(m.Pages)
	m.Pages = append(m.Pages, p)
	return nil
}

func firstH1(body []byte) string {
	var fs fenceState
	for _, line := range strings.Split(string(body), "\n") {
		if fs.toggle(line) {
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(t[2:]), "#"))
		}
	}
	return ""
}

func (m *Mapping) dedupeSlugs() {
	seen := map[string]bool{}
	for _, p := range m.Pages {
		if p.pinned {
			seen[p.Slug] = true
		}
	}
	for i := range m.Pages {
		if m.Pages[i].pinned {
			continue
		}
		s := m.Pages[i].Slug
		uniq := s
		for n := 2; seen[uniq]; n++ {
			uniq = fmt.Sprintf("%s-%d", s, n)
		}
		seen[uniq] = true
		m.Pages[i].Slug = uniq
	}
}

func (m *Mapping) order() {
	groups := map[string][]int{}
	for i, p := range m.Pages {
		k := strings.Join(p.CollectionPath, "/")
		groups[k] = append(groups[k], i)
	}
	for _, idx := range groups {
		sort.SliceStable(idx, func(a, b int) bool {
			pa, pb := m.Pages[idx[a]], m.Pages[idx[b]]
			oa, ob := orderKey(pa), orderKey(pb)
			if oa != ob {
				return oa < ob
			}
			return pa.Path < pb.Path
		})
		for pos, i := range idx {
			m.Pages[i].Position = pos
		}
	}
}

func orderKey(p Page) float64 {
	switch {
	case p.explicitOrder != nil:
		return float64(*p.explicitOrder)
	case p.Index:
		return -1e9
	}
	return 1e9
}

func (m *Mapping) titles(src Source) {
	byIndex := map[string]string{}
	for _, p := range m.Pages {
		if p.Index && len(p.CollectionPath) > 0 {
			byIndex[strings.Join(p.CollectionPath, "/")] = p.Title
		}
	}
	for _, p := range m.Pages {
		for depth := range p.CollectionPath {
			key := strings.Join(p.CollectionPath[:depth+1], "/")
			if _, done := m.CollectionTitles[key]; done {
				continue
			}
			title := byIndex[key]
			if title == "" && p.collectionDirs[depth] != "" {
				title = folderTitle(src, p.collectionDirs[depth])
			}
			if title == "" {
				name := p.CollectionPath[depth]
				if dir := p.collectionDirs[depth]; dir != "" {
					name = path.Base(dir)
				}
				title = humanize(name)
			}
			m.CollectionTitles[key] = title
		}
	}
}

func folderTitle(src Source, dir string) string {
	if data, ok, err := src.Read(path.Join(dir, "_category_.json")); err == nil && ok {
		var cat struct {
			Label string `json:"label"`
		}
		if json.Unmarshal(data, &cat) == nil && cat.Label != "" {
			return cat.Label
		}
	}
	if data, ok, err := src.Read(path.Join(dir, ".pages")); err == nil && ok {
		var pages struct {
			Title string `yaml:"title"`
		}
		if yaml.Unmarshal(data, &pages) == nil && pages.Title != "" {
			return pages.Title
		}
	}
	return ""
}

// Lookup returns the mapped page of a repository path.
func (m *Mapping) Lookup(p string) (*Page, bool) {
	i, ok := m.byPath[p]
	if !ok {
		return nil, false
	}
	return &m.Pages[i], true
}

// TitlesFor returns the collectionTitles of a page's chain, keyed by collection slug.
func (m *Mapping) TitlesFor(p Page) map[string]string {
	out := map[string]string{}
	for depth, seg := range p.CollectionPath {
		out[seg] = m.CollectionTitles[strings.Join(p.CollectionPath[:depth+1], "/")]
	}
	return out
}

// Repo describes where non-mapped repository links point.
type Repo struct {
	WebURL   string
	Provider string
	Branch   string
	Exists   func(path string) bool
}

// BlobURL is the web URL of a repository file on a branch.
func (r Repo) BlobURL(p string) string {
	base := strings.TrimRight(r.WebURL, "/")
	switch r.Provider {
	case "gitlab":
		return base + "/-/blob/" + r.Branch + "/" + p
	case "bitbucket":
		return base + "/src/" + r.Branch + "/" + p
	case "azure":
		return base + "?path=/" + p + "&version=GB" + r.Branch
	}
	return base + "/blob/" + r.Branch + "/" + p
}

// Linker returns the link rewriter of one mapped file.
func (m *Mapping) Linker(from, spaceSlug string, repo Repo) LinkFunc {
	return func(dest string) string {
		d := strings.TrimSpace(dest)
		if d == "" || strings.Contains(d, "://") || strings.HasPrefix(d, "//") || strings.HasPrefix(d, "mailto:") || strings.HasPrefix(d, "tel:") || strings.HasPrefix(d, "data:") {
			return dest
		}
		target, anchor, hasAnchor := strings.Cut(d, "#")
		if target == "" {
			return "#" + docs.Slug(anchor)
		}
		target, _, _ = strings.Cut(target, "?")
		var resolved string
		if strings.HasPrefix(target, "/") {
			resolved = path.Clean(strings.TrimPrefix(target, "/"))
		} else {
			resolved = path.Clean(path.Join(path.Dir(from), target))
		}
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			return dest
		}
		stem := strings.TrimSuffix(resolved, path.Ext(resolved))
		for _, cand := range []string{resolved, stem + ".md", stem + ".mdx", stem + ".markdown", resolved + ".md", resolved + ".mdx", path.Join(resolved, "index.md"), path.Join(resolved, "README.md")} {
			if p, ok := m.Lookup(cand); ok {
				out := "../" + spaceSlug + "/" + p.Slug
				if hasAnchor && anchor != "" {
					out += "#" + docs.Slug(anchor)
				}
				return out
			}
		}
		if repo.WebURL != "" && repo.Exists != nil && repo.Exists(resolved) {
			out := repo.BlobURL(resolved)
			if hasAnchor && anchor != "" {
				out += "#" + anchor
			}
			return out
		}
		return dest
	}
}
