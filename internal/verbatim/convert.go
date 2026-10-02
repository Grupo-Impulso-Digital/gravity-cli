package verbatim

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

// LinkFunc rewrites a link destination found in the document.
type LinkFunc func(dest string) string

// ImageFunc resolves an image source to the URL to publish; ok is false when the image is missing.
type ImageFunc func(src string) (url string, ok bool)

// Options configure one conversion.
type Options struct {
	Path        string
	Hash        string
	Generator   string
	MDX         bool
	DropTitleH1 bool
	Audiences   []string
	Link        LinkFunc
	Image       ImageFunc
}

// Document is a converted file.
type Document struct {
	Blocks   []api.ChangeBlock `json:"blocks"`
	Warnings []string          `json:"warnings,omitempty"`
}

type converter struct {
	opts         Options
	md           goldmark.Markdown
	blocks       []api.ChangeBlock
	warnings     []string
	section      string
	intra        int
	used         map[string]bool
	titleDropped bool
	dropped      map[string]bool
}

var alertRE = regexp.MustCompile(`^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*`)

// Convert turns a Markdown or MDX body (front matter already removed) into native blocks.
func Convert(body []byte, opts Options) *Document {
	c := &converter{
		opts:    opts,
		md:      goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)),
		section: "intro",
		used:    map[string]bool{"intro": true, "footnotes": true},
		dropped: map[string]bool{},
	}
	src := strings.ReplaceAll(string(body), "\r\n", "\n")
	if opts.MDX {
		src = stripMDXImports(src)
	}
	src, fn := extractFootnotes(src)
	c.process(src)
	if len(fn.order) > 0 {
		c.section, c.intra = "footnotes", 0
		c.emit("divider", map[string]any{})
		for _, label := range fn.order {
			c.emit("list", map[string]any{"text": c.markdownText(fn.defs[label]), "variant": "numbered"})
		}
	}
	if len(c.dropped) > 0 {
		names := make([]string, 0, len(c.dropped))
		for n := range c.dropped {
			names = append(names, n)
		}
		sort.Strings(names)
		c.warn("dropped MDX components: %s", strings.Join(names, ", "))
	}
	if c.blocks == nil {
		c.blocks = []api.ChangeBlock{}
	}
	return &Document{Blocks: c.blocks, Warnings: c.warnings}
}

func (c *converter) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

func (c *converter) link(dest string) string {
	if unsafeURL(dest) {
		c.warn("link %s uses a scheme that is not allowed; replaced with #", clipURL(dest))
		return "#"
	}
	if c.opts.Link == nil {
		return dest
	}
	return c.opts.Link(dest)
}

func (c *converter) image(src string) (string, bool) {
	if unsafeURL(src) {
		c.warn("image %s uses a scheme that is not allowed; dropped", clipURL(src))
		return "", false
	}
	if c.opts.Image == nil {
		return src, src != ""
	}
	u, ok := c.opts.Image(src)
	if !ok {
		c.warn("image %s not found; dropped", src)
	}
	return u, ok
}

func clipURL(u string) string {
	if len(u) > 40 {
		return u[:40] + "…"
	}
	return u
}

func (c *converter) binding() *api.SourceBinding {
	return &api.SourceBinding{Kind: "file", Ref: c.opts.Path + "#" + c.section, Hash: c.opts.Hash, Generator: c.opts.Generator}
}

func (c *converter) emit(typ string, content map[string]any) {
	c.intra++
	c.push(fmt.Sprintf("doc:%s:%s:%d", c.opts.Path, c.section, c.intra), typ, content)
}

func (c *converter) push(key, typ string, content map[string]any) {
	c.blocks = append(c.blocks, api.ChangeBlock{
		Key:           key,
		Type:          typ,
		Ownership:     api.OwnershipMachine,
		Content:       content,
		SourceBinding: c.binding(),
		Audiences:     c.opts.Audiences,
	})
}

func (c *converter) process(src string) {
	for _, seg := range segmentize(src) {
		switch seg.kind {
		case segMarkdown:
			c.markdown([]byte(seg.body))
		case segCallout:
			c.emit("callout", map[string]any{"text": titled(seg.title, c.markdownText(seg.body)), "variant": calloutVariant(seg.variant)})
		case segExpandable:
			title := seg.title
			if title == "" {
				title = humanize(seg.variant)
			}
			c.emit("expandable", map[string]any{"title": title, "text": c.markdownText(seg.body)})
		case segTabs:
			tabs := make([]any, 0, len(seg.tabs))
			for _, t := range seg.tabs {
				tabs = append(tabs, map[string]any{"label": t.label, "text": c.markdownText(t.body)})
			}
			c.emit("tabs", map[string]any{"tabs": tabs})
		case segComponent:
			c.dropped[seg.name] = true
			if strings.TrimSpace(seg.body) != "" {
				c.process(seg.body)
			}
		}
	}
}

func titled(title, body string) string {
	if title == "" {
		return body
	}
	if body == "" {
		return "**" + title + "**"
	}
	return "**" + title + "** " + body
}

func calloutVariant(kind string) string {
	switch strings.ToLower(kind) {
	case "tip", "success", "check", "hint", "done":
		return "success"
	case "important", "warning", "caution", "attention":
		return "warning"
	case "danger", "error", "bug", "failure", "fail", "missing":
		return "danger"
	}
	return "info"
}

func (c *converter) parse(src []byte) ast.Node {
	return c.md.Parser().Parse(text.NewReader(src))
}

func (c *converter) markdown(src []byte) {
	doc := c.parse(src)
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		c.block(n, src)
	}
}

func (c *converter) block(n ast.Node, src []byte) {
	switch node := n.(type) {
	case *ast.Heading:
		c.heading(node, src)
	case *ast.Paragraph, *ast.TextBlock:
		if img := soleImage(node); img != nil {
			url, ok := c.image(string(img.Destination))
			if ok {
				c.emit("image", map[string]any{"url": url, "alt": plainText(img, src), "caption": string(img.Title)})
			}
			return
		}
		if t := c.inline(node, src); t != "" {
			c.emit("prose", map[string]any{"text": t})
		}
	case *ast.List:
		c.list(node, src)
	case *ast.FencedCodeBlock:
		c.emit("code", map[string]any{"text": codeText(node, src), "language": fenceLanguage(node, src)})
	case *ast.CodeBlock:
		c.emit("code", map[string]any{"text": codeText(node, src), "language": ""})
	case *extast.Table:
		if rows := c.tableRows(node, src); len(rows) > 0 {
			c.emit("table", map[string]any{"rows": rows, "header": true})
		}
	case *ast.Blockquote:
		c.blockquote(node, src)
	case *ast.ThematicBreak:
		c.emit("divider", map[string]any{})
	case *ast.HTMLBlock:
		if t := c.htmlToMarkdown(linesText(node, src)); t != "" {
			c.emit("prose", map[string]any{"text": t})
		}
	case *extast.DefinitionList:
		c.definitions(node, src)
	default:
		if t := strings.TrimSpace(c.renderMD(n, src)); t != "" {
			c.emit("prose", map[string]any{"text": t})
		}
	}
}

func (c *converter) heading(h *ast.Heading, src []byte) {
	txt := c.inline(h, src)
	s := docs.Slug(plainText(h, src))
	if s == "" {
		s = fmt.Sprintf("section-%d", len(c.blocks)+1)
	}
	uniq := s
	for i := 2; c.used[uniq]; i++ {
		uniq = fmt.Sprintf("%s-%d", s, i)
	}
	c.used[uniq] = true
	c.section, c.intra = uniq, 0
	if c.opts.DropTitleH1 && !c.titleDropped && h.Level == 1 {
		c.titleDropped = true
		return
	}
	c.push(fmt.Sprintf("doc:%s:%s", c.opts.Path, uniq), "heading", map[string]any{"text": txt, "level": min(h.Level, 3)})
}

func (c *converter) list(l *ast.List, src []byte) {
	variant := "bulleted"
	if l.IsOrdered() {
		variant = "numbered"
	}
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		content := map[string]any{"variant": variant}
		var parts []string
		for ch := item.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if ch == item.FirstChild() {
				if box := taskBox(ch); box != nil {
					content["variant"] = "task"
					content["checked"] = box.IsChecked
				}
				if _, isBlock := ch.(*ast.Paragraph); isBlock || isTextBlock(ch) {
					parts = append(parts, c.inline(ch, src))
					continue
				}
			}
			parts = append(parts, indent(c.renderMD(ch, src), "  "))
		}
		content["text"] = strings.TrimRight(strings.Join(parts, "\n"), "\n")
		c.emit("list", content)
	}
}

func isTextBlock(n ast.Node) bool {
	_, ok := n.(*ast.TextBlock)
	return ok
}

func taskBox(n ast.Node) *extast.TaskCheckBox {
	if n == nil || n.FirstChild() == nil {
		return nil
	}
	box, _ := n.FirstChild().(*extast.TaskCheckBox)
	return box
}

func (c *converter) blockquote(q *ast.Blockquote, src []byte) {
	first := q.FirstChild()
	if first != nil && first.Lines().Len() > 0 {
		seg := first.Lines().At(0)
		line := string(seg.Value(src))
		if m := alertRE.FindStringSubmatch(line); m != nil {
			body := alertRE.ReplaceAllString(c.renderChildren(q, src), "")
			c.emit("callout", map[string]any{"text": strings.TrimSpace(body), "variant": calloutVariant(m[1])})
			return
		}
	}
	c.emit("quote", map[string]any{"text": c.renderChildren(q, src)})
}

func (c *converter) definitions(dl *extast.DefinitionList, src []byte) {
	var term string
	var descs []string
	flush := func() {
		if term == "" && len(descs) == 0 {
			return
		}
		c.emit("prose", map[string]any{"text": strings.TrimSpace("**" + term + "**: " + strings.Join(descs, "; "))})
		term, descs = "", nil
	}
	for n := dl.FirstChild(); n != nil; n = n.NextSibling() {
		switch n.(type) {
		case *extast.DefinitionTerm:
			flush()
			term = c.inline(n, src)
		case *extast.DefinitionDescription:
			descs = append(descs, strings.TrimSpace(c.renderChildren(n, src)))
		}
	}
	flush()
}

func (c *converter) tableRows(tbl ast.Node, src []byte) [][]string {
	var rows [][]string
	for r := tbl.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, c.inline(cell, src))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

func (c *converter) markdownText(src string) string {
	data := []byte(src)
	return strings.TrimSpace(c.renderChildren(c.parse(data), data))
}

func (c *converter) renderChildren(n ast.Node, src []byte) string {
	var parts []string
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if s := c.renderMD(ch, src); strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (c *converter) renderMD(n ast.Node, src []byte) string {
	switch node := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return c.inline(node, src)
	case *ast.Heading:
		return strings.Repeat("#", node.Level) + " " + c.inline(node, src)
	case *ast.List:
		var items []string
		num := node.Start
		if num == 0 {
			num = 1
		}
		for item := node.FirstChild(); item != nil; item = item.NextSibling() {
			marker := "- "
			if node.IsOrdered() {
				marker = fmt.Sprintf("%d. ", num)
				num++
			}
			var parts []string
			for ch := item.FirstChild(); ch != nil; ch = ch.NextSibling() {
				s := c.renderMD(ch, src)
				if ch == item.FirstChild() {
					if box := taskBox(ch); box != nil {
						mark := "[ ] "
						if box.IsChecked {
							mark = "[x] "
						}
						s = mark + s
					}
					parts = append(parts, s)
					continue
				}
				parts = append(parts, indent(s, strings.Repeat(" ", len(marker))))
			}
			items = append(items, marker+strings.Join(parts, "\n"))
		}
		return strings.Join(items, "\n")
	case *ast.FencedCodeBlock:
		return "```" + fenceLanguage(node, src) + "\n" + codeText(node, src) + "\n```"
	case *ast.CodeBlock:
		return indent(codeText(node, src), "    ")
	case *ast.Blockquote:
		return indent(c.renderChildren(node, src), "> ")
	case *ast.ThematicBreak:
		return "---"
	case *ast.HTMLBlock:
		return c.htmlToMarkdown(linesText(node, src))
	case *extast.Table:
		rows := c.tableRows(node, src)
		var b strings.Builder
		for i, r := range rows {
			b.WriteString("| " + strings.Join(r, " | ") + " |\n")
			if i == 0 {
				b.WriteString("|" + strings.Repeat(" --- |", len(r)) + "\n")
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return c.renderChildren(n, src)
}

func (c *converter) inline(n ast.Node, src []byte) string {
	var b strings.Builder
	c.inlineChildren(&b, n, src)
	return strings.TrimSpace(b.String())
}

func (c *converter) inlineChildren(b *strings.Builder, n ast.Node, src []byte) {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		c.inlineNode(b, ch, src)
	}
}

func (c *converter) inlineNode(b *strings.Builder, n ast.Node, src []byte) {
	switch t := n.(type) {
	case *ast.Text:
		b.Write(t.Segment.Value(src))
		if t.HardLineBreak() || t.SoftLineBreak() {
			b.WriteByte('\n')
		}
	case *ast.String:
		b.Write(t.Value)
	case *ast.CodeSpan:
		var code strings.Builder
		for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if tx, ok := ch.(*ast.Text); ok {
				code.Write(tx.Segment.Value(src))
			}
		}
		fence := "`"
		if strings.Contains(code.String(), "`") {
			b.WriteString("`` " + code.String() + " ``")
			return
		}
		b.WriteString(fence + code.String() + fence)
	case *ast.Emphasis:
		mark := strings.Repeat("*", t.Level)
		b.WriteString(mark)
		c.inlineChildren(b, t, src)
		b.WriteString(mark)
	case *extast.Strikethrough:
		b.WriteString("~~")
		c.inlineChildren(b, t, src)
		b.WriteString("~~")
	case *ast.Link:
		b.WriteByte('[')
		c.inlineChildren(b, t, src)
		b.WriteString("](" + c.link(string(t.Destination)))
		if len(t.Title) > 0 {
			b.WriteString(` "` + string(t.Title) + `"`)
		}
		b.WriteByte(')')
	case *ast.AutoLink:
		b.Write(t.URL(src))
	case *ast.Image:
		url, ok := c.image(string(t.Destination))
		if !ok {
			return
		}
		b.WriteString("![" + plainText(t, src) + "](" + url)
		if len(t.Title) > 0 {
			b.WriteString(` "` + string(t.Title) + `"`)
		}
		b.WriteByte(')')
	case *ast.RawHTML:
		var raw strings.Builder
		for i := 0; i < t.Segments.Len(); i++ {
			seg := t.Segments.At(i)
			raw.Write(seg.Value(src))
		}
		if brRE.MatchString(raw.String()) {
			b.WriteByte('\n')
		}
	case *extast.TaskCheckBox:
	default:
		c.inlineChildren(b, n, src)
	}
}

func soleImage(n ast.Node) *ast.Image {
	first := n.FirstChild()
	if first == nil || first.NextSibling() != nil {
		return nil
	}
	switch t := first.(type) {
	case *ast.Image:
		return t
	case *ast.Link:
		if t.FirstChild() != nil && t.FirstChild().NextSibling() == nil {
			if img, ok := t.FirstChild().(*ast.Image); ok {
				return img
			}
		}
	}
	return nil
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func codeText(n ast.Node, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return strings.TrimRight(b.String(), "\n")
}

func linesText(n ast.Node, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	if hb, ok := n.(*ast.HTMLBlock); ok && hb.HasClosure() {
		closure := hb.ClosureLine
		b.Write(closure.Value(src))
	}
	return b.String()
}

func fenceLanguage(n *ast.FencedCodeBlock, src []byte) string {
	lang := string(n.Language(src))
	return strings.TrimSpace(lang)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" || strings.HasPrefix(prefix, ">") {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func humanize(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
