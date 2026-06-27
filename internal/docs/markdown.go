package docs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/impulso/gravity-cli/internal/api"
)

// MarkdownPage decomposes a Markdown file into Gravity's native blocks:
// headings, fenced/indented code, and tables map to their own block types;
// everything else (paragraphs, lists, quotes, HTML) is preserved verbatim in a
// prose block. (When the platform ships a `markdown` block type, the verbatim
// branch can target it instead.) It returns the blocks and a sniffed title
// (first H1, else the filename).
//
// ownership is machine | hybrid | human (default machine). machine/hybrid blocks
// bind to the source file (whole-file sha256) so `check docs` verifies them;
// human blocks are seeded once and carry no binding.
func MarkdownPage(repoRoot, fileRef, ownership, generator string) (blocks []api.BlockInput, title string, err error) {
	data, err := readRepoFile(repoRoot, fileRef)
	if err != nil {
		return nil, "", fmt.Errorf("read %q: %w", fileRef, err)
	}
	if ownership == "" {
		ownership = "machine"
	}
	var binding *api.SourceBinding
	if ownership != "human" {
		binding, err = BuildBinding(repoRoot, fileRef, "doc", generator)
		if err != nil {
			return nil, "", err
		}
	}

	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(data))

	var out []api.BlockInput
	usedSections := map[string]bool{}
	section := "intro"
	intra := 0
	pos := 0

	emit := func(typ string, content any, key string) {
		out = append(out, api.BlockInput{
			Key:           key,
			Type:          typ,
			Ownership:     ownership,
			Content:       content,
			SourceBinding: binding,
			Position:      pos,
		})
		pos++
	}
	sectionKey := func() string {
		intra++
		return fmt.Sprintf("doc:%s:%s:%d", fileRef, section, intra)
	}

	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch node := n.(type) {
		case *ast.Heading:
			txt := inlineText(node, data)
			s := slug(txt)
			if s == "" {
				s = fmt.Sprintf("section-%d", pos)
			}
			uniq := s
			for i := 2; usedSections[uniq]; i++ {
				uniq = fmt.Sprintf("%s-%d", s, i)
			}
			usedSections[uniq] = true
			section = uniq
			intra = 0
			if title == "" && node.Level == 1 {
				title = txt
			}
			emit("heading", map[string]any{"text": txt, "level": node.Level}, fmt.Sprintf("doc:%s:%s", fileRef, uniq))
		case *ast.FencedCodeBlock:
			emit("code", map[string]any{"text": codeText(node, data), "language": string(node.Language(data))}, sectionKey())
		case *ast.CodeBlock:
			emit("code", map[string]any{"text": codeText(node, data), "language": ""}, sectionKey())
		case *extast.Table:
			if rows := tableRows(node, data); len(rows) > 0 {
				emit("table", map[string]any{"rows": rows, "header": true}, sectionKey())
			}
		default:
			if txt := rawSpan(n, data); strings.TrimSpace(txt) != "" {
				emit("prose", map[string]any{"text": txt}, sectionKey())
			}
		}
	}

	if title == "" {
		base := filepath.Base(fileRef)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return out, title, nil
}

// ReadVerbatim reads a Markdown file unchanged (for the release path, which
// sends the whole document as bodyMarkdown) and sniffs a title from the first
// H1, falling back to the filename.
func ReadVerbatim(repoRoot, fileRef string) (content, title string, err error) {
	data, err := readRepoFile(repoRoot, fileRef)
	if err != nil {
		return "", "", fmt.Errorf("read %q: %w", fileRef, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			title = strings.TrimSpace(t[2:])
			break
		}
	}
	if title == "" {
		base := filepath.Base(fileRef)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return string(data), title, nil
}

// inlineText concatenates the literal text of all inline Text descendants.
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := node.(*ast.Text); ok {
				b.Write(t.Segment.Value(src))
				if t.SoftLineBreak() || t.HardLineBreak() {
					b.WriteByte(' ')
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// codeText returns the literal content of a code block (fence lines excluded).
func codeText(n ast.Node, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return strings.TrimRight(b.String(), "\n")
}

// tableRows extracts a GFM table's rows (header row first) as plain strings.
func tableRows(tbl ast.Node, src []byte) [][]string {
	var rows [][]string
	for r := tbl.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, inlineText(c, src))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

// rawSpan returns the verbatim source bytes spanning a top-level node,
// expanded back to the start of the first line so list/quote markers survive.
func rawSpan(n ast.Node, src []byte) string {
	start, stop := -1, -1
	consider := func(s, e int) {
		if s < 0 || e < 0 {
			return
		}
		if start == -1 || s < start {
			start = s
		}
		if e > stop {
			stop = e
		}
	}
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		// Lines() panics on inline nodes, so only query block nodes for spans.
		if node.Type() == ast.TypeBlock {
			if ln := node.Lines(); ln != nil && ln.Len() > 0 {
				consider(ln.At(0).Start, ln.At(ln.Len()-1).Stop)
			}
		}
		if t, ok := node.(*ast.Text); ok {
			consider(t.Segment.Start, t.Segment.Stop)
		}
		return ast.WalkContinue, nil
	})
	if start < 0 || stop <= start {
		return ""
	}
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	return strings.TrimRight(string(src[start:stop]), "\n")
}

// slug lowercases text and collapses non-alphanumerics into single dashes.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
