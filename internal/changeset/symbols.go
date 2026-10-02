package changeset

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Symbol is a code symbol found in a diff.
type Symbol struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Language string `json:"language"`
}

// RenamedSymbol pairs a removed symbol with the one that replaced it in the same file.
type RenamedSymbol struct {
	From     string `json:"from"`
	To       string `json:"to"`
	File     string `json:"file"`
	Language string `json:"language"`
}

// Symbols lists best-effort removed and renamed symbols, hints for AI passes only.
type Symbols struct {
	Removed []Symbol        `json:"removed"`
	Renamed []RenamedSymbol `json:"renamed"`
}

type symbolRule struct {
	language string
	exts     []string
	patterns []*regexp.Regexp
}

var symbolRules = []symbolRule{
	{"go", []string{".go"}, []*regexp.Regexp{
		regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`),
		regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)`),
	}},
	{"typescript", []string{".ts", ".tsx", ".mts", ".cts"}, []*regexp.Regexp{
		regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?(?:function\*?|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`),
	}},
	{"javascript", []string{".js", ".jsx", ".mjs", ".cjs"}, []*regexp.Regexp{
		regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?(?:function\*?|class|const|let|var)\s+([A-Za-z_$][\w$]*)`),
	}},
	{"python", []string{".py"}, []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:async\s+)?(?:def|class)\s+([A-Za-z_]\w*)`),
	}},
	{"java", []string{".java"}, []*regexp.Regexp{publicRE}},
	{"kotlin", []string{".kt", ".kts"}, []*regexp.Regexp{publicRE}},
	{"csharp", []string{".cs"}, []*regexp.Regexp{publicRE}},
}

var publicRE = regexp.MustCompile(`^\s*public\s+(?:(?:static|final|abstract|sealed|override|async|virtual|partial|readonly|data|open|suspend)\s+)*(?:class|interface|enum|record|struct|object|fun|[\w<>\[\],.?]+)\s+([A-Za-z_]\w*)`)

func ruleFor(file string) *symbolRule {
	ext := strings.ToLower(path.Ext(file))
	for i := range symbolRules {
		for _, e := range symbolRules[i].exts {
			if e == ext {
				return &symbolRules[i]
			}
		}
	}
	return nil
}

func symbolName(rule *symbolRule, line string) string {
	for _, re := range rule.patterns {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// ParseSymbols extracts removed and renamed symbols from a zero-context unified diff.
func ParseSymbols(diff string, keep func(file string) bool) Symbols {
	type fileSyms struct {
		rule           *symbolRule
		removed, added []string
	}
	files := map[string]*fileSyms{}
	var order []string
	var cur *fileSyms
	oldPath := ""
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur, oldPath = nil, ""
			continue
		case strings.HasPrefix(line, "--- "):
			oldPath = strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/")
			continue
		case strings.HasPrefix(line, "+++ "):
			name := strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if name == "/dev/null" {
				name = oldPath
			}
			cur = nil
			if keep != nil && !keep(name) {
				continue
			}
			if rule := ruleFor(name); rule != nil {
				fs, ok := files[name]
				if !ok {
					fs = &fileSyms{rule: rule}
					files[name] = fs
					order = append(order, name)
				}
				cur = fs
			}
			continue
		}
		if cur == nil || line == "" {
			continue
		}
		switch line[0] {
		case '-':
			if n := symbolName(cur.rule, line[1:]); n != "" {
				cur.removed = append(cur.removed, n)
			}
		case '+':
			if n := symbolName(cur.rule, line[1:]); n != "" {
				cur.added = append(cur.added, n)
			}
		}
	}
	out := Symbols{Removed: []Symbol{}, Renamed: []RenamedSymbol{}}
	sort.Strings(order)
	addedAnywhere := map[string]bool{}
	for _, name := range order {
		fs := files[name]
		for _, a := range fs.added {
			addedAnywhere[fs.rule.language+":"+a] = true
		}
	}
	for _, name := range order {
		fs := files[name]
		gone := difference(fs.removed, fs.added)
		fresh := difference(fs.added, fs.removed)
		if len(gone) == 1 && len(fresh) == 1 {
			out.Renamed = append(out.Renamed, RenamedSymbol{From: gone[0], To: fresh[0], File: name, Language: fs.rule.language})
			continue
		}
		for _, g := range gone {
			if addedAnywhere[fs.rule.language+":"+g] {
				continue
			}
			out.Removed = append(out.Removed, Symbol{Name: g, File: name, Language: fs.rule.language})
		}
	}
	return out
}

func difference(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range a {
		if !in[x] && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
