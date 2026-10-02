package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const maxDiffLines = 4000

type block struct {
	key, typ, own string
	text          string
}

func contentText(typ string, raw any) string {
	var m map[string]any
	switch v := raw.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(v, &m)
	case map[string]any:
		m = v
	default:
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, &m)
	}
	switch typ {
	case "api":
		return fmt.Sprintf("%v %v — %v", m["method"], m["path"], m["summary"])
	case "table":
		var rows []string
		if list, ok := m["rows"].([]any); ok {
			for _, r := range list {
				if cells, ok := r.([]any); ok {
					parts := make([]string, 0, len(cells))
					for _, c := range cells {
						parts = append(parts, fmt.Sprint(c))
					}
					rows = append(rows, "| "+strings.Join(parts, " | ")+" |")
				}
			}
		}
		return strings.Join(rows, "\n")
	case "image":
		return fmt.Sprintf("![%v](%v)", m["alt"], m["url"])
	case "tabs":
		var parts []string
		if list, ok := m["tabs"].([]any); ok {
			for _, t := range list {
				if tm, ok := t.(map[string]any); ok {
					parts = append(parts, fmt.Sprintf("[%v] %v", tm["label"], tm["text"]))
				}
			}
		}
		return strings.Join(parts, "\n")
	case "divider":
		return "---"
	}
	text, _ := m["text"].(string)
	if title, ok := m["title"].(string); ok && title != "" {
		text = title + "\n" + text
	}
	if v, ok := m["variant"].(string); ok && typ == "callout" {
		text = "(" + v + ") " + text
	}
	return text
}

func render(blocks []block) []string {
	var lines []string
	for _, b := range blocks {
		lines = append(lines, fmt.Sprintf("[%s %s %s]", b.typ, b.key, b.own))
		for _, l := range strings.Split(b.text, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

func fromPage(pc *api.PageContent) []block {
	if pc == nil {
		return nil
	}
	out := make([]block, 0, len(pc.Blocks))
	for _, b := range pc.Blocks {
		key := b.Key
		if key == "" {
			key = fmt.Sprintf("#%d", b.Position)
		}
		out = append(out, block{key: key, typ: b.Type, own: b.Ownership, text: firstOf(b.Text, contentText(b.Type, b.Content))})
	}
	return out
}

func fromChange(b api.ChangeBlock) block {
	return block{key: b.Key, typ: b.Type, own: b.Ownership, text: contentText(b.Type, b.Content)}
}

func apply(current *api.PageContent, req api.ChangeRequest) []block {
	blocks := fromPage(current)
	if req.Op == api.OpDelete {
		return nil
	}
	removed := map[string]bool{}
	for _, k := range req.RemoveBlockKeys {
		removed[k] = true
	}
	idx := map[string]int{}
	for i, b := range blocks {
		idx[b.key] = i
	}
	for _, nb := range req.Blocks {
		if i, ok := idx[nb.Key]; ok {
			if blocks[i].own != api.OwnershipHuman {
				blocks[i] = fromChange(nb)
			}
			continue
		}
		pos := len(blocks)
		if nb.After != nil {
			if i, ok := idx[*nb.After]; ok {
				pos = i + 1
			}
		}
		blocks = append(blocks[:pos], append([]block{fromChange(nb)}, blocks[pos:]...)...)
		idx = map[string]int{}
		for i, b := range blocks {
			idx[b.key] = i
		}
	}
	out := blocks[:0]
	for _, b := range blocks {
		if !removed[b.key] || b.own == api.OwnershipHuman {
			out = append(out, b)
		}
	}
	return out
}

// ChangeDiff renders a unified line diff of a page before and after a change.
func ChangeDiff(current *api.PageContent, req api.ChangeRequest) string {
	return diffLines(render(fromPage(current)), render(apply(current, req)))
}

// VerbatimDiff renders a line diff of a page before and after a whole-page import.
func VerbatimDiff(current *api.PageContent, blocks []api.ChangeBlock) string {
	next := make([]block, 0, len(blocks))
	for _, b := range blocks {
		next = append(next, fromChange(b))
	}
	return diffLines(render(fromPage(current)), render(next))
}

func diffLines(a, b []string) string {
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return fmt.Sprintf("(page too large to diff: %d → %d lines)\n", len(a), len(b))
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			out.WriteString("  " + a[i] + "\n")
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("+ " + b[j] + "\n")
			j++
		default:
			out.WriteString("- " + a[i] + "\n")
			i++
		}
	}
	return out.String()
}
