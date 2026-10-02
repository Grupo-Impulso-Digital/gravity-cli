package verbatim

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type segKind int

const (
	segMarkdown segKind = iota
	segCallout
	segExpandable
	segTabs
	segComponent
)

type tab struct {
	label string
	body  string
}

type segment struct {
	kind    segKind
	body    string
	title   string
	variant string
	name    string
	tabs    []tab
}

var (
	fenceRE      = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	colonOpenRE  = regexp.MustCompile(`^ {0,3}(:{3,})\s*([A-Za-z][\w-]*)\s*(.*)$`)
	mkdocsOpenRE = regexp.MustCompile(`^(!!!|\?{3}\+?)\s+([A-Za-z][\w-]*)(?:\s+"([^"]*)")?\s*$`)
	jsxOpenRE    = regexp.MustCompile(`^\s*<([A-Z][A-Za-z0-9.]*)(\s[^>]*)?(/?)>`)
	importRE     = regexp.MustCompile(`^(import|export)\s`)
	summaryRE    = regexp.MustCompile(`(?is)<summary[^>]*>(.*?)</summary>`)
	tabItemRE    = regexp.MustCompile(`(?is)<TabItem\b([^>]*)>(.*?)</TabItem>`)
	attrRE       = regexp.MustCompile(`(\w+)\s*=\s*(?:"([^"]*)"|'([^']*)'|\{["']([^"']*)["']\})`)
	footDefRE    = regexp.MustCompile(`^\[\^([^\]\s]+)\]:\s?(.*)$`)
	footRefRE    = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
)

type fenceState struct {
	marker string
}

func (f *fenceState) toggle(line string) bool {
	m := fenceRE.FindStringSubmatch(line)
	if f.marker == "" {
		if m != nil {
			f.marker = m[1]
			return true
		}
		return false
	}
	if len(m) < 2 || !strings.HasPrefix(m[1], f.marker[:1]) || len(m[1]) < len(f.marker) {
		return true
	}
	if strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), f.marker[:1])) == "" {
		f.marker = ""
	}
	return true
}

func stripMDXImports(body string) string {
	var out []string
	var fs fenceState
	for _, line := range strings.Split(body, "\n") {
		if fs.toggle(line) {
			out = append(out, line)
			continue
		}
		if importRE.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

type footnotes struct {
	numbers map[string]int
	defs    map[string]string
	order   []string
}

func extractFootnotes(body string) (string, *footnotes) {
	fn := &footnotes{numbers: map[string]int{}, defs: map[string]string{}}
	lines := strings.Split(body, "\n")
	var kept []string
	var fs fenceState
	var defOrder []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fs.toggle(line) {
			kept = append(kept, line)
			continue
		}
		m := footDefRE.FindStringSubmatch(line)
		if m == nil {
			kept = append(kept, line)
			continue
		}
		text := []string{m[2]}
		for i+1 < len(lines) {
			next := lines[i+1]
			if strings.HasPrefix(next, "    ") || strings.HasPrefix(next, "\t") {
				text = append(text, strings.TrimSpace(next))
				i++
				continue
			}
			if strings.TrimSpace(next) == "" && i+2 < len(lines) && (strings.HasPrefix(lines[i+2], "    ") || strings.HasPrefix(lines[i+2], "\t")) {
				text = append(text, "")
				i++
				continue
			}
			break
		}
		if _, dup := fn.defs[m[1]]; !dup {
			defOrder = append(defOrder, m[1])
		}
		fn.defs[m[1]] = strings.TrimSpace(strings.Join(text, "\n"))
	}
	if len(fn.defs) == 0 {
		return body, fn
	}
	out := strings.Join(kept, "\n")
	var fs2 fenceState
	var b strings.Builder
	for i, line := range strings.Split(out, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if fs2.toggle(line) {
			b.WriteString(line)
			continue
		}
		b.WriteString(footRefRE.ReplaceAllStringFunc(line, func(ref string) string {
			label := footRefRE.FindStringSubmatch(ref)[1]
			if _, ok := fn.defs[label]; !ok {
				return ref
			}
			n, ok := fn.numbers[label]
			if !ok {
				n = len(fn.order) + 1
				fn.numbers[label] = n
				fn.order = append(fn.order, label)
			}
			return "[" + strconv.Itoa(n) + "]"
		}))
	}
	for _, label := range defOrder {
		if _, ok := fn.numbers[label]; !ok {
			fn.numbers[label] = len(fn.order) + 1
			fn.order = append(fn.order, label)
		}
	}
	sort.SliceStable(fn.order, func(i, j int) bool { return fn.numbers[fn.order[i]] < fn.numbers[fn.order[j]] })
	return b.String(), fn
}

func segmentize(body string) []segment {
	lines := strings.Split(body, "\n")
	var segs []segment
	var md []string
	flush := func() {
		if len(md) > 0 {
			segs = append(segs, segment{kind: segMarkdown, body: strings.Join(md, "\n")})
			md = nil
		}
	}
	var fs fenceState
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fs.toggle(line) {
			md = append(md, line)
			continue
		}
		if m := colonOpenRE.FindStringSubmatch(line); m != nil {
			if end := findColonClose(lines, i+1, m[1]); end > 0 {
				flush()
				title := strings.TrimSpace(m[3])
				title = strings.TrimSuffix(strings.TrimPrefix(title, "["), "]")
				segs = append(segs, segment{kind: segCallout, variant: strings.ToLower(m[2]), title: title, body: strings.Join(lines[i+1:end], "\n")})
				i = end
				continue
			}
		}
		if m := mkdocsOpenRE.FindStringSubmatch(line); m != nil {
			end, inner := indentedBlock(lines, i+1)
			flush()
			kind := segCallout
			if strings.HasPrefix(m[1], "???") {
				kind = segExpandable
			}
			segs = append(segs, segment{kind: kind, variant: strings.ToLower(m[2]), title: m[3], body: inner})
			i = end - 1
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "<details") {
			if end := findTagClose(lines, i, "details"); end >= 0 {
				flush()
				raw := strings.Join(lines[i:end+1], "\n")
				segs = append(segs, detailsSegment(raw))
				i = end
				continue
			}
		}
		if m := jsxOpenRE.FindStringSubmatch(line); m != nil {
			name := m[1]
			if m[3] == "/" {
				flush()
				segs = append(segs, segment{kind: segComponent, name: name})
				continue
			}
			if end := findTagClose(lines, i, name); end >= 0 {
				flush()
				raw := strings.Join(lines[i:end+1], "\n")
				if name == "Tabs" {
					segs = append(segs, tabsSegment(raw))
				} else {
					segs = append(segs, segment{kind: segComponent, name: name, body: innerOf(raw, name)})
				}
				i = end
				continue
			}
		}
		md = append(md, line)
	}
	flush()
	return segs
}

func findColonClose(lines []string, from int, colons string) int {
	var fs fenceState
	for j := from; j < len(lines); j++ {
		if fs.toggle(lines[j]) {
			continue
		}
		if strings.TrimSpace(lines[j]) == colons {
			return j
		}
	}
	return -1
}

func indentedBlock(lines []string, from int) (int, string) {
	var inner []string
	j := from
	for ; j < len(lines); j++ {
		l := lines[j]
		switch {
		case strings.TrimSpace(l) == "":
			inner = append(inner, "")
		case strings.HasPrefix(l, "    "):
			inner = append(inner, l[4:])
		case strings.HasPrefix(l, "\t"):
			inner = append(inner, l[1:])
		default:
			return trimTrailingBlank(j, inner)
		}
	}
	return trimTrailingBlank(j, inner)
}

func trimTrailingBlank(end int, inner []string) (int, string) {
	for len(inner) > 0 && inner[len(inner)-1] == "" {
		inner = inner[:len(inner)-1]
		end--
	}
	return end, strings.Join(inner, "\n")
}

func findTagClose(lines []string, from int, name string) int {
	open := regexp.MustCompile(`(?i)<` + regexp.QuoteMeta(name) + `(\s[^>]*)?>`)
	selfClose := regexp.MustCompile(`(?i)<` + regexp.QuoteMeta(name) + `(\s[^>]*)?/>`)
	closeTag := regexp.MustCompile(`(?i)</` + regexp.QuoteMeta(name) + `\s*>`)
	depth := 0
	var fs fenceState
	for j := from; j < len(lines); j++ {
		if j > from && fs.toggle(lines[j]) {
			continue
		}
		depth += len(open.FindAllString(lines[j], -1)) - len(selfClose.FindAllString(lines[j], -1))
		depth -= len(closeTag.FindAllString(lines[j], -1))
		if depth <= 0 {
			return j
		}
	}
	return -1
}

func innerOf(raw, name string) string {
	start := regexp.MustCompile(`(?is)^\s*<` + regexp.QuoteMeta(name) + `(\s[^>]*)?>`)
	end := regexp.MustCompile(`(?is)</` + regexp.QuoteMeta(name) + `\s*>\s*$`)
	raw = start.ReplaceAllString(raw, "")
	raw = end.ReplaceAllString(raw, "")
	return dedent(strings.Trim(raw, "\n"))
}

func detailsSegment(raw string) segment {
	inner := innerOf(raw, "details")
	title := ""
	if m := summaryRE.FindStringSubmatch(inner); m != nil {
		title = strings.TrimSpace(stripTags(m[1]))
		inner = strings.Replace(inner, m[0], "", 1)
	}
	return segment{kind: segExpandable, title: title, body: dedent(strings.Trim(inner, "\n"))}
}

func tabsSegment(raw string) segment {
	s := segment{kind: segTabs, name: "Tabs"}
	for _, m := range tabItemRE.FindAllStringSubmatch(raw, -1) {
		attrs := attributes(m[1])
		label := attrs["label"]
		if label == "" {
			label = attrs["value"]
		}
		s.tabs = append(s.tabs, tab{label: label, body: dedent(strings.Trim(m[2], "\n"))})
	}
	return s
}

func attributes(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRE.FindAllStringSubmatch(s, -1) {
		out[m[1]] = m[2] + m[3] + m[4]
	}
	return out
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent <= 0 {
		return s
	}
	for i, l := range lines {
		if len(l) >= minIndent {
			lines[i] = l[minIndent:]
		} else {
			lines[i] = strings.TrimLeft(l, " \t")
		}
	}
	return strings.Join(lines, "\n")
}
