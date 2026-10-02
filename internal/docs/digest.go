package docs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

var skipContentKeys = map[string]bool{
	"id": true, "key": true, "level": true, "language": true, "variant": true,
	"src": true, "href": true, "url": true, "width": true, "height": true, "icon": true,
}

var preferredContentKeys = []string{"title", "method", "path", "summary", "text", "caption", "body"}

// BlockText extracts the human-readable text of a block's content.
func BlockText(blk api.ContentBlock) string {
	if len(blk.Content) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(blk.Content, &v); err != nil {
		return ""
	}
	var parts []string
	collectText(v, &parts)
	text := strings.Join(parts, " ")
	if blk.Type == "heading" && text != "" {
		return "## " + text
	}
	return text
}

func collectText(v any, parts *[]string) {
	switch t := v.(type) {
	case string:
		if s := strings.Join(strings.Fields(t), " "); s != "" {
			*parts = append(*parts, s)
		}
	case []any:
		for _, item := range t {
			collectText(item, parts)
		}
	case map[string]any:
		seen := map[string]bool{}
		for _, k := range preferredContentKeys {
			if val, ok := t[k]; ok {
				seen[k] = true
				collectText(val, parts)
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			if !seen[k] && !skipContentKeys[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectText(t[k], parts)
		}
	}
}

// PageDigest renders a page's block text, capped at limit bytes.
func PageDigest(blocks []api.ContentBlock, limit int) string {
	var b strings.Builder
	for _, blk := range blocks {
		text := BlockText(blk)
		if text == "" {
			continue
		}
		line := text + "\n"
		if b.Len()+len(line) > limit {
			remaining := limit - b.Len()
			if remaining > 0 {
				b.WriteString(clipText(line, remaining))
			}
			fmt.Fprintf(&b, "\n[... page text truncated at %d bytes]\n", limit)
			return b.String()
		}
		b.WriteString(line)
	}
	return b.String()
}

func clipText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// ClipText bounds s to limit bytes on a UTF-8 boundary, marking the cut.
func ClipText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return clipText(s, limit) + "…"
}
