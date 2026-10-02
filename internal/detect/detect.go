// Package detect inspects a repository's files locally to suggest what gravity init should set up.
package detect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

const (
	maxSpecBytes   = 2 << 20
	maxSpecScanned = 200
	maxSourceRead  = 3000
	maxSourceBytes = 512 << 10
)

// Input is the repository content detection looks at.
type Input struct {
	Root     string
	Files    []string
	Tags     []string
	ReadFile func(rel string) ([]byte, error)
}

// OpenAPIDoc is one OpenAPI or Swagger document.
type OpenAPIDoc struct {
	Path       string `json:"path"`
	Operations int    `json:"operations"`
	Version    string `json:"version,omitempty"`
}

// Folder is a directory of documents.
type Folder struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}

// Language is a source language and how many files use it.
type Language struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Files int    `json:"files"`
}

// Result is everything detection found.
type Result struct {
	Languages       []Language   `json:"languages"`
	OpenAPI         []OpenAPIDoc `json:"openapi"`
	UIFramework     string       `json:"uiFramework,omitempty"`
	UIRoutes        int          `json:"uiRoutes"`
	UIPaths         []string     `json:"uiPaths,omitempty"`
	ServerFramework string       `json:"serverFramework,omitempty"`
	ServerRoutes    int          `json:"serverRoutes"`
	CLICommands     int          `json:"cliCommands"`
	ServerPaths     []string     `json:"serverPaths,omitempty"`
	MarkdownFiles   int          `json:"markdownFiles"`
	DocsFolders     []Folder     `json:"docsFolders"`
	Runbooks        []Folder     `json:"runbooks"`
	ReleaseTags     int          `json:"releaseTags"`
	Changelog       string       `json:"changelog,omitempty"`
	CI              []string     `json:"ci"`
}

var vendored = map[string]bool{
	"node_modules": true, "vendor": true, "third_party": true, "dist": true, "build": true, ".git": true,
	".next": true, ".nuxt": true, ".svelte-kit": true, "out": true, "target": true, "coverage": true,
	"bower_components": true, ".venv": true, "venv": true, "__pycache__": true, "testdata": true,
	"fixtures": true, "__fixtures__": true, "__tests__": true, ".gradle": true, "Pods": true,
}

var languages = map[string]Language{
	".go": {Key: "go", Name: "Go"}, ".ts": {Key: "typescript", Name: "TypeScript"}, ".tsx": {Key: "typescript", Name: "TypeScript"},
	".js": {Key: "javascript", Name: "JavaScript"}, ".jsx": {Key: "javascript", Name: "JavaScript"}, ".mjs": {Key: "javascript", Name: "JavaScript"},
	".cjs": {Key: "javascript", Name: "JavaScript"}, ".py": {Key: "python", Name: "Python"}, ".rb": {Key: "ruby", Name: "Ruby"},
	".java": {Key: "java", Name: "Java"}, ".kt": {Key: "kotlin", Name: "Kotlin"}, ".cs": {Key: "csharp", Name: "C#"},
	".php": {Key: "php", Name: "PHP"}, ".rs": {Key: "rust", Name: "Rust"}, ".swift": {Key: "swift", Name: "Swift"},
	".scala": {Key: "scala", Name: "Scala"}, ".ex": {Key: "elixir", Name: "Elixir"}, ".exs": {Key: "elixir", Name: "Elixir"},
	".vue": {Key: "vue", Name: "Vue"}, ".svelte": {Key: "svelte", Name: "Svelte"}, ".dart": {Key: "dart", Name: "Dart"},
	".c": {Key: "c", Name: "C"}, ".cpp": {Key: "cpp", Name: "C++"}, ".cc": {Key: "cpp", Name: "C++"},
}

var ciFiles = []struct {
	match    func(string) bool
	provider string
}{
	{func(p string) bool {
		return strings.HasPrefix(p, ".github/workflows/") && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml"))
	}, "github"},
	{func(p string) bool { return p == ".gitlab-ci.yml" }, "gitlab"},
	{func(p string) bool { return p == "bitbucket-pipelines.yml" }, "bitbucket"},
	{func(p string) bool {
		return strings.HasPrefix(p, "azure-pipelines") && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml"))
	}, "azure"},
	{func(p string) bool { return p == "Jenkinsfile" }, "jenkins"},
	{func(p string) bool { return p == ".circleci/config.yml" || p == ".circleci/config.yaml" }, "circleci"},
}

var (
	jsRoute      = regexp.MustCompile(`\b(?:app|router|server|fastify|api|route|routes)\.(?:get|post|put|patch|delete|all|route)\(\s*['"` + "`" + `]/`)
	goRoute      = regexp.MustCompile(`\.(?:HandleFunc|Handle)\(\s*"/|\.(?:Get|Post|Put|Patch|Delete|GET|POST|PUT|PATCH|DELETE)\(\s*"/`)
	cobraCommand = regexp.MustCompile(`&cobra\.Command\{`)
	pyRoute      = regexp.MustCompile(`@(?:app|router|api|bp|blueprint)\.(?:get|post|put|patch|delete|route)\(\s*['"]/`)
	angularPath  = regexp.MustCompile(`\bpath:\s*['"]`)
	releaseTag   = regexp.MustCompile(`^v\d`)
)

// Run detects languages, API specs, UI and server routes, documents, releases and CI files.
func Run(in Input) (*Result, error) {
	read := in.ReadFile
	if read == nil {
		read = func(rel string) ([]byte, error) {
			return os.ReadFile(filepath.Join(in.Root, filepath.FromSlash(rel)))
		}
	}
	r := &Result{OpenAPI: []OpenAPIDoc{}, DocsFolders: []Folder{}, Runbooks: []Folder{}, CI: []string{}, Languages: []Language{}}
	files := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		f = filepath.ToSlash(f)
		if f != "" && !isVendored(f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	r.detectCI(in.Files)
	r.detectLanguages(files)
	r.detectOpenAPI(files, read)
	r.detectUI(files, read)
	r.detectServer(files, read)
	r.detectDocs(files)
	r.detectReleases(files, in.Tags)
	return r, nil
}

func isVendored(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if vendored[seg] {
			return true
		}
	}
	return false
}

func (r *Result) detectCI(files []string) {
	seen := map[string]bool{}
	for _, f := range files {
		f = filepath.ToSlash(f)
		for _, c := range ciFiles {
			if c.match(f) && !seen[c.provider] {
				seen[c.provider] = true
				r.CI = append(r.CI, c.provider)
			}
		}
	}
	sort.Strings(r.CI)
}

func (r *Result) detectLanguages(files []string) {
	counts := map[string]*Language{}
	total := 0
	for _, f := range files {
		l, ok := languages[strings.ToLower(path.Ext(f))]
		if !ok {
			continue
		}
		total++
		if c, ok := counts[l.Key]; ok {
			c.Files++
			continue
		}
		l.Files = 1
		counts[l.Key] = &l
	}
	list := make([]Language, 0, len(counts))
	for _, l := range counts {
		list = append(list, *l)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Files != list[j].Files {
			return list[i].Files > list[j].Files
		}
		return list[i].Key < list[j].Key
	})
	for i, l := range list {
		if i >= 3 && l.Files*20 < total {
			break
		}
		r.Languages = append(r.Languages, l)
	}
}

func specCandidate(f string) bool {
	ext := strings.ToLower(path.Ext(f))
	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

func specRank(f string) int {
	base := strings.ToLower(path.Base(f))
	switch {
	case strings.Contains(base, "openapi") || strings.Contains(base, "swagger"):
		return 0
	case strings.Contains(strings.ToLower(f), "api"):
		return 1
	}
	return 2
}

func (r *Result) detectOpenAPI(files []string, read func(string) ([]byte, error)) {
	var cands []string
	for _, f := range files {
		if specCandidate(f) && !strings.HasPrefix(f, ".") && !strings.Contains(f, "/.") && path.Base(f) != "package.json" && path.Base(f) != "package-lock.json" {
			cands = append(cands, f)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return specRank(cands[i]) < specRank(cands[j]) })
	if len(cands) > maxSpecScanned {
		cands = cands[:maxSpecScanned]
	}
	for _, f := range cands {
		data, err := read(f)
		if err != nil || len(data) > maxSpecBytes {
			continue
		}
		if doc, ok := parseSpec(f, data); ok {
			r.OpenAPI = append(r.OpenAPI, doc)
		}
	}
	sort.Slice(r.OpenAPI, func(i, j int) bool { return r.OpenAPI[i].Path < r.OpenAPI[j].Path })
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}

func parseSpec(f string, data []byte) (OpenAPIDoc, bool) {
	if !hasSpecKey(data) {
		return OpenAPIDoc{}, false
	}
	var doc map[string]any
	if strings.HasSuffix(strings.ToLower(f), ".json") {
		if err := json.Unmarshal(data, &doc); err != nil {
			return OpenAPIDoc{}, false
		}
	} else if err := yaml.Unmarshal(data, &doc); err != nil {
		return OpenAPIDoc{}, false
	}
	version := ""
	for _, k := range []string{"openapi", "swagger"} {
		if v, ok := doc[k]; ok {
			version = scalar(v)
			if k == "swagger" {
				version = "swagger " + version
			}
		}
	}
	if version == "" {
		return OpenAPIDoc{}, false
	}
	ops := 0
	if paths, ok := doc["paths"].(map[string]any); ok {
		for _, item := range paths {
			if m, ok := item.(map[string]any); ok {
				for k := range m {
					if httpMethods[strings.ToLower(k)] {
						ops++
					}
				}
			}
		}
	}
	return OpenAPIDoc{Path: f, Operations: ops, Version: version}, true
}

func hasSpecKey(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	s := string(head)
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "openapi:") || strings.HasPrefix(line, "swagger:") {
			return true
		}
	}
	return strings.Contains(s, `"openapi"`) || strings.Contains(s, `"swagger"`)
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	}
	return ""
}

type uiRule struct {
	framework string
	root      string
	match     func(rel string) bool
}

var uiRules = []uiRule{
	{"SvelteKit", "src/routes", func(rel string) bool { return path.Base(rel) == "+page.svelte" }},
	{"Next.js", "app", func(rel string) bool { return isPageFile(rel, "page") }},
	{"Next.js", "src/app", func(rel string) bool { return isPageFile(rel, "page") }},
	{"React Router", "app/routes", func(rel string) bool { return hasExt(rel, ".tsx", ".jsx", ".ts", ".js") }},
	{"TanStack Router", "src/routes", func(rel string) bool {
		return hasExt(rel, ".tsx", ".jsx") && !strings.HasPrefix(path.Base(rel), "-")
	}},
	{"Nuxt", "pages", func(rel string) bool { return hasExt(rel, ".vue") }},
	{"Vue", "src/pages", func(rel string) bool { return hasExt(rel, ".vue") }},
	{"Next.js", "pages", func(rel string) bool {
		return hasExt(rel, ".tsx", ".jsx", ".ts", ".js") && !strings.HasPrefix(rel, "api/") && !strings.HasPrefix(path.Base(rel), "_")
	}},
	{"Next.js", "src/pages", func(rel string) bool {
		return hasExt(rel, ".tsx", ".jsx", ".ts", ".js") && !strings.HasPrefix(rel, "api/") && !strings.HasPrefix(path.Base(rel), "_")
	}},
}

func isPageFile(rel, name string) bool {
	base := path.Base(rel)
	return strings.TrimSuffix(base, path.Ext(base)) == name && hasExt(rel, ".tsx", ".jsx", ".ts", ".js", ".mdx")
}

func hasExt(p string, exts ...string) bool {
	e := strings.ToLower(path.Ext(p))
	for _, x := range exts {
		if e == x {
			return true
		}
	}
	return false
}

func (r *Result) detectUI(files []string, read func(string) ([]byte, error)) {
	for _, rule := range uiRules {
		n := 0
		for _, f := range files {
			if rel, ok := strings.CutPrefix(f, rule.root+"/"); ok && rule.match(rel) {
				n++
			}
		}
		if n > r.UIRoutes {
			r.UIRoutes, r.UIFramework, r.UIPaths = n, rule.framework, []string{rule.root + "/**"}
		}
	}
	if r.UIRoutes > 0 {
		return
	}
	n := 0
	var dirs []string
	for _, f := range files {
		base := path.Base(f)
		if !strings.HasSuffix(base, "-routing.module.ts") && !strings.HasSuffix(base, ".routes.ts") {
			continue
		}
		data, err := read(f)
		if err != nil {
			continue
		}
		if c := len(angularPath.FindAll(data, -1)); c > 0 {
			n += c
			dirs = appendUnique(dirs, topDir(f, 2)+"/**")
		}
	}
	if n > 0 {
		r.UIRoutes, r.UIFramework, r.UIPaths = n, "Angular", dirs
	}
}

func topDir(f string, depth int) string {
	parts := strings.Split(path.Dir(f), "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}

func appendUnique(list []string, s string) []string {
	for _, e := range list {
		if e == s {
			return list
		}
	}
	return append(list, s)
}

func (r *Result) detectServer(files []string, read func(string) ([]byte, error)) {
	read2 := 0
	frameworks := map[string]int{}
	for _, f := range files {
		if read2 >= maxSourceRead {
			break
		}
		var patterns []*regexp.Regexp
		var names []string
		switch strings.ToLower(path.Ext(f)) {
		case ".js", ".ts", ".mjs", ".cjs":
			if strings.HasSuffix(f, ".d.ts") || strings.Contains(f, ".test.") || strings.Contains(f, ".spec.") {
				continue
			}
			patterns, names = []*regexp.Regexp{jsRoute}, []string{"Node.js"}
		case ".go":
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			patterns, names = []*regexp.Regexp{goRoute, cobraCommand}, []string{"Go", "cobra"}
		case ".py":
			patterns, names = []*regexp.Regexp{pyRoute}, []string{"Python"}
		default:
			continue
		}
		data, err := read(f)
		read2++
		if err != nil || len(data) > maxSourceBytes {
			continue
		}
		for i, re := range patterns {
			c := len(re.FindAll(data, -1))
			if c == 0 {
				continue
			}
			if names[i] == "cobra" {
				r.CLICommands += c
			} else {
				r.ServerRoutes += c
				frameworks[names[i]] += c
			}
			r.ServerPaths = appendUnique(r.ServerPaths, topDir(f, 2)+"/**")
		}
	}
	best := 0
	for name, c := range frameworks {
		if c > best || c == best && name < r.ServerFramework {
			best, r.ServerFramework = c, name
		}
	}
	sort.Strings(r.ServerPaths)
	if len(r.ServerPaths) > 8 {
		r.ServerPaths = r.ServerPaths[:8]
	}
}

var docExcluded = []string{"changelog", "license", "code_of_conduct", "security", "contributing"}

func isDoc(f string) bool {
	if !hasExt(f, ".md", ".mdx") || strings.HasPrefix(f, ".github/") || strings.HasPrefix(f, ".") {
		return false
	}
	base := path.Base(f)
	if base == "AGENTS.md" || base == "CLAUDE.md" {
		return false
	}
	lower := strings.ToLower(base)
	for _, p := range docExcluded {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	return true
}

func isRunbook(f string) bool {
	return strings.HasPrefix(f, "runbooks/") || strings.HasPrefix(f, "ops/") || strings.HasPrefix(f, "docs/runbooks/")
}

func (r *Result) detectDocs(files []string) {
	docs := map[string]int{}
	books := map[string]int{}
	for _, f := range files {
		if !isDoc(f) {
			continue
		}
		r.MarkdownFiles++
		switch {
		case isRunbook(f):
			root := strings.Split(f, "/")[0]
			if strings.HasPrefix(f, "docs/runbooks/") {
				root = "docs/runbooks"
			}
			books[root]++
		case strings.HasPrefix(f, "docs/"):
			parts := strings.Split(f, "/")
			if len(parts) > 2 {
				docs["docs/"+parts[1]]++
			} else {
				docs["docs"]++
			}
		}
	}
	r.DocsFolders = folders(docs)
	r.Runbooks = folders(books)
}

func folders(m map[string]int) []Folder {
	out := make([]Folder, 0, len(m))
	for p, n := range m {
		out = append(out, Folder{Path: p, Files: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func (r *Result) detectReleases(files, tags []string) {
	for _, t := range tags {
		if releaseTag.MatchString(t) {
			r.ReleaseTags++
		}
	}
	for _, f := range files {
		if !strings.Contains(f, "/") && strings.HasPrefix(strings.ToUpper(f), "CHANGELOG") && hasExt(f, ".md") {
			r.Changelog = f
			return
		}
	}
}

// HasUI reports whether UI routes were found.
func (r *Result) HasUI() bool { return r.UIRoutes > 0 }

// HasServer reports whether server routes or CLI commands were found.
func (r *Result) HasServer() bool { return r.ServerRoutes > 0 || r.CLICommands > 0 }

// LanguageKeys returns the detected language keys, most used first.
func (r *Result) LanguageKeys() []string {
	out := make([]string, 0, len(r.Languages))
	for _, l := range r.Languages {
		out = append(out, l.Key)
	}
	return out
}

// OpenAPIPaths returns the detected spec paths.
func (r *Result) OpenAPIPaths() []string {
	out := make([]string, 0, len(r.OpenAPI))
	for _, d := range r.OpenAPI {
		out = append(out, d.Path)
	}
	return out
}

// ListFiles walks root and returns repository-relative paths, skipping vendored directories.
func ListFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (vendored[d.Name()] || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
