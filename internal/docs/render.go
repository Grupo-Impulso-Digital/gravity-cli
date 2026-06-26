package docs

import (
	"fmt"
	"io"
	"strings"

	"github.com/impulso/gravity-cli/internal/api"
)

// WritePageText prints a human-readable preview of a page's blocks (used by
// `gravity sync --output stdout`).
func WritePageText(w io.Writer, p api.PageUpsertRequest) {
	fmt.Fprintf(w, "# %s  (space: %s, slug: %s)\n", p.Title, p.SpaceSlug, p.Slug)
	for _, b := range p.Blocks {
		bind := ""
		if b.SourceBinding != nil {
			bind = fmt.Sprintf("  [%s %s]", b.SourceBinding.Kind, b.SourceBinding.Ref)
		}
		fmt.Fprintf(w, "  [%d] %-7s %-7s %s%s\n", b.Position, b.Type, b.Ownership, oneLine(blockSummary(b)), bind)
	}
	fmt.Fprintln(w)
}

func blockSummary(b api.BlockInput) string {
	m, ok := b.Content.(map[string]any)
	if !ok {
		// Typed content (e.g. apiContent) — fall back to the key.
		return b.Key
	}
	switch b.Type {
	case "heading", "prose", "code":
		if t, ok := m["text"].(string); ok {
			return t
		}
	case "table":
		if rows, ok := m["rows"].([][]string); ok {
			return fmt.Sprintf("%d row(s)", len(rows))
		}
	}
	return b.Key
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
