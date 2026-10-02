package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

var (
	slugRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	targetRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*){1,6}$`)
)

var keyAliases = map[string]string{
	"on":      "triggers",
	"trigger": "triggers",
	"when":    "triggers",
	"prompt":  "instructions",
	"space":   "target",
}

func validate(jsonDoc []byte) []Issue {
	set, err := loadSchema()
	if err != nil {
		return []Issue{{Message: err.Error()}}
	}
	var root map[string]any
	if err := json.Unmarshal(jsonDoc, &root); err != nil {
		return []Issue{{Message: fmt.Sprintf("manifest is not valid JSON: %v", err)}}
	}
	v := &validator{set: set}
	v.unknownKeys(set.raw, "", root, nil)
	v.semantic(root)
	for _, is := range set.validateSchema(jsonDoc) {
		if !v.covered(is.Path) {
			v.add(is.Path, is.Message)
		}
	}
	return v.issues
}

type validator struct {
	set    *schemaSet
	issues []Issue
}

func (v *validator) add(path, msg string) {
	for _, is := range v.issues {
		if is.Path == path && is.Message == msg {
			return
		}
	}
	v.issues = append(v.issues, Issue{Path: path, Message: msg})
}

func (v *validator) covered(path string) bool {
	for _, is := range v.issues {
		if is.Path == path || strings.HasPrefix(path, is.Path+".") || strings.HasPrefix(path, is.Path+"[") {
			return true
		}
		if strings.HasPrefix(is.Path, path+".") || strings.HasPrefix(is.Path, path+"[") {
			return true
		}
	}
	return false
}

func tokenIssues(node any, path []string) []Issue {
	var out []Issue
	switch t := node.(type) {
	case map[string]any:
		keys := sortedKeys(t)
		for _, k := range keys {
			child := append(append([]string{}, path...), k)
			if k == "token" {
				out = append(out, Issue{
					Path:    formatPath(child),
					Message: fmt.Sprintf("tokens are never read from %s; use %s in CI or `gravity login` locally", ManifestFileName, EnvToken),
				})
				continue
			}
			out = append(out, tokenIssues(t[k], child)...)
		}
	case []any:
		for i, e := range t {
			out = append(out, tokenIssues(e, append(append([]string{}, path...), strconv.Itoa(i)))...)
		}
	}
	return out
}

func (v *validator) unknownKeys(sch map[string]any, defName string, inst any, path []string) {
	sch, name := v.set.deref(sch)
	if name != "" {
		defName = name
	}
	switch t := inst.(type) {
	case map[string]any:
		props, _ := sch["properties"].(map[string]any)
		closed := sch["additionalProperties"] == false
		for _, k := range sortedKeys(t) {
			child := append(append([]string{}, path...), k)
			sub, known := props[k].(map[string]any)
			if !known {
				if closed {
					v.add(formatPath(child), "unknown key"+suggestKey(k, props))
				}
				continue
			}
			if defName == "pass" && k == "options" {
				kindName, _ := t["kind"].(string)
				if od := v.set.optionsDef(kindName); od != nil {
					v.unknownKeys(od, kindName+"Options", t[k], child)
				}
				continue
			}
			v.unknownKeys(sub, "", t[k], child)
		}
	case []any:
		items, ok := sch["items"].(map[string]any)
		if !ok {
			return
		}
		for i, e := range t {
			v.unknownKeys(items, "", e, append(append([]string{}, path...), strconv.Itoa(i)))
		}
	}
}

func suggestKey(name string, props map[string]any) string {
	keys := sortedKeys(props)
	if alias, ok := keyAliases[name]; ok {
		if _, exists := props[alias]; exists {
			return fmt.Sprintf(" (did you mean %q?)", alias)
		}
	}
	best, bestDist := "", 0
	lower := strings.ToLower(name)
	for _, k := range keys {
		d := levenshtein(lower, strings.ToLower(k))
		if best == "" || d < bestDist {
			best, bestDist = k, d
		}
	}
	if best != "" && bestDist <= 2 {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	if len(keys) == 0 {
		return ""
	}
	return fmt.Sprintf(" (valid keys: %s)", strings.Join(keys, ", "))
}

func (v *validator) semantic(root map[string]any) {
	if p, ok := root["product"].(string); ok && !slugRE.MatchString(p) {
		v.add("product", "must be a lowercase slug (a-z, 0-9, dashes; at most 63 characters)")
	}
	if u, ok := root["apiUrl"].(string); ok {
		if err := CheckAPIURL(u); err != nil {
			v.add("apiUrl", err.Error())
		}
	}
	if code, ok := root["code"].(map[string]any); ok {
		for _, k := range []string{"openapi", "entrypoints", "include", "exclude"} {
			v.globList(code[k], []string{"code", k})
		}
	}
	if docs, ok := root["docs"].(map[string]any); ok {
		for _, k := range []string{"include", "exclude"} {
			v.globList(docs[k], []string{"docs", k})
		}
	}
	passes, _ := root["passes"].([]any)
	seen := map[string]int{}
	product, _ := root["product"].(string)
	for i, raw := range passes {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		base := []string{"passes", strconv.Itoa(i)}
		v.pass(p, base, product)
		if name, ok := p["name"].(string); ok && name != "" {
			if prev, dup := seen[name]; dup {
				v.add(formatPath(append(base, "name")), fmt.Sprintf("duplicate pass name %q (also passes[%d])", name, prev))
			} else {
				seen[name] = i
			}
		}
	}
}

func (v *validator) pass(p map[string]any, base []string, product string) {
	at := func(k ...string) string { return formatPath(append(append([]string{}, base...), k...)) }
	if name, ok := p["name"].(string); ok {
		if !slugRE.MatchString(name) {
			v.add(at("name"), "pass names are lowercase slugs (a-z, 0-9, dashes; at most 63 characters)")
		}
	} else {
		v.add(formatPath(base), "name is required")
	}
	kindName, hasKind := p["kind"].(string)
	template, hasTemplate := p["template"].(string)
	switch {
	case !hasKind && hasTemplate:
		v.add(formatPath(base), fmt.Sprintf("kind is required even when template is given (template %s is a %s pass)", template, TemplateKinds[template]))
	case !hasKind:
		v.add(formatPath(base), "kind is required (guides, reference, verbatim, changelog, nucleus, check or capture)")
	case hasTemplate && TemplateKinds[template] != "" && TemplateKinds[template] != kindName:
		v.add(at("kind"), fmt.Sprintf("template %s is a %s pass, not %s", template, TemplateKinds[template], kindName))
	}
	target, hasTarget := p["target"].(string)
	if hasKind && NeedsSpaceTarget(kindName) {
		switch {
		case !hasTarget:
			v.add(formatPath(base), fmt.Sprintf("%s passes need a target <site>/<space>[/<collection>...]", kindName))
		case !targetRE.MatchString(target):
			v.add(at("target"), fmt.Sprintf("%s targets need <site>/<space>[/<collection>...]", kindName))
		}
	}
	if scope, ok := p["scope"].(map[string]any); ok {
		v.globList(scope["paths"], append(append([]string{}, base...), "scope", "paths"))
		v.globList(scope["exclude"], append(append([]string{}, base...), "scope", "exclude"))
	}
	opts, _ := p["options"].(map[string]any)
	optBase := append(append([]string{}, base...), "options")
	switch kindName {
	case KindVerbatim:
		files, ok := opts["files"].([]any)
		if !ok || len(files) == 0 {
			v.add(formatPath(base), "verbatim passes require options.files")
			return
		}
		for j, raw := range files {
			f, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			fb := append(append([]string{}, optBase...), "files", strconv.Itoa(j))
			include, _ := f["include"].(string)
			v.glob(include, append(append([]string{}, fb...), "include"))
			v.globList(f["exclude"], append(append([]string{}, fb...), "exclude"))
			if sp, ok := f["stripPrefix"].(string); ok {
				v.glob(sp, append(append([]string{}, fb...), "stripPrefix"))
			}
			_, hasSlug := f["slug"]
			_, hasTitle := f["title"]
			if (hasSlug || hasTitle) && glob.HasMeta(include) {
				v.add(formatPath(append(append([]string{}, fb...), "include")), "slug/title overrides need a literal include (one file), not a glob")
			}
		}
	case KindReference:
		if sources, ok := opts["sources"].([]any); ok {
			for j, raw := range sources {
				if s, ok := raw.(map[string]any); ok {
					if path, ok := s["path"].(string); ok {
						v.glob(path, append(append([]string{}, optBase...), "sources", strconv.Itoa(j), "path"))
					}
				}
			}
		}
	case KindChangelog:
		if f, ok := opts["changelogFile"].(string); ok {
			v.glob(f, append(append([]string{}, optBase...), "changelogFile"))
		}
	case KindNucleus:
		if ns, ok := opts["namespace"].(string); ok && product != "" {
			want := "product:" + product
			if ns != want && !strings.HasPrefix(ns, want+"/") {
				v.add(formatPath(append(append([]string{}, optBase...), "namespace")), fmt.Sprintf("must be %s or start with %s/", want, want))
			}
		}
	}
}

func (v *validator) globList(raw any, path []string) {
	list, ok := raw.([]any)
	if !ok {
		return
	}
	for i, e := range list {
		if s, ok := e.(string); ok {
			v.glob(s, append(append([]string{}, path...), strconv.Itoa(i)))
		}
	}
}

func (v *validator) glob(p string, path []string) {
	if p == "" {
		return
	}
	if strings.HasPrefix(p, "/") {
		v.add(formatPath(path), "paths must stay inside the repository (repository-relative, not absolute)")
		return
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			v.add(formatPath(path), "paths must stay inside the repository (no .. segments)")
			return
		}
	}
	if _, err := pathsafe.Rel(p); err != nil {
		v.add(formatPath(path), "paths must stay inside the repository")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
