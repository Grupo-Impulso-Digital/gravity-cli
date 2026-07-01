package docs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/impulso/gravity-cli/internal/api"
)

// AuthoredBlock is a block produced by the docs-generator agent, before it is
// validated and converted into an api.BlockInput.
type AuthoredBlock struct {
	Key       string
	Type      string
	Ownership string
	Audiences []string
	Content   json.RawMessage
	Sources   []string
}

var (
	validBlockType = map[string]bool{"heading": true, "prose": true, "code": true, "table": true}
	validOwnership = map[string]bool{"machine": true, "hybrid": true, "human": true}
	validAudience  = map[string]bool{
		api.AudiencePublic: true, api.AudienceUsers: true, api.AudienceDevelopers: true,
	}
)

// ValidAudiences returns an error if any value is outside the known audience set.
func ValidAudiences(a []string) error {
	for _, x := range a {
		if !validAudience[x] {
			return fmt.Errorf("unknown audience %q (want public|users|developers)", x)
		}
	}
	return nil
}

// AssembleBlocks validates AI-authored blocks and converts them into
// api.BlockInput values ready for a page upsert. It defaults ownership to
// "hybrid", validates type/ownership/audiences, normalizes content per type,
// rejects duplicate keys, and assigns sequential positions.
//
// Ownership policy: "machine" (locked, drift-bound) is honored only for code
// blocks — verbatim copies of a source file. Narrative types the model marks
// machine (headings, prose, tables) are downgraded to hybrid so the docs team
// always keeps editing rights over the text.
//
// Provenance binding: the CLI binds CODE only. A "machine" block with a single
// resolvable source (an API block or verbatim/code mirror) is pinned to that file
// by sha256 under kind "cli" — the only binding kind the server accepts — so
// `check docs` verifies it. The narrative blocks around it (hybrid/human) carry
// no binding, so the team can edit the text freely; a machine block whose source
// doesn't resolve is authored unbound too, best-effort, so a guessed path never
// sinks the page.
func AssembleBlocks(repoRoot, generator string, in []AuthoredBlock) ([]api.BlockInput, error) {
	out := make([]api.BlockInput, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, b := range in {
		key := strings.TrimSpace(b.Key)
		if key == "" {
			return nil, fmt.Errorf("block %d: key is required", i)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate block key %q", key)
		}
		seen[key] = true

		typ := strings.TrimSpace(b.Type)
		if !validBlockType[typ] {
			return nil, fmt.Errorf("block %q: type %q must be heading|prose|code|table", key, typ)
		}
		own := strings.TrimSpace(b.Ownership)
		if own == "" {
			own = "hybrid"
		}
		if !validOwnership[own] {
			return nil, fmt.Errorf("block %q: ownership %q must be machine|hybrid|human", key, own)
		}
		own = effectiveOwnership(typ, own)
		if err := ValidAudiences(b.Audiences); err != nil {
			return nil, fmt.Errorf("block %q: %w", key, err)
		}
		content, err := normalizeContent(typ, b.Content)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w", key, err)
		}

		out = append(out, api.BlockInput{
			Key:           key,
			Type:          typ,
			Ownership:     own,
			Audiences:     b.Audiences,
			Content:       content,
			SourceBinding: provenanceBinding(repoRoot, generator, own, b.Sources),
			Position:      i,
		})
	}
	return out, nil
}

// provenanceBinding picks the binding for an authored block. The CLI binds CODE
// and nothing else: only a "machine" block — an API block or a verbatim/code
// mirror the model marked as strongly code-derived — is pinned to its source
// file. The narrative prose around it (hybrid/human) is deliberately left unbound
// so the team can freely edit the text without it going stale or being
// re-authored over. A machine block binds to its single source by sha256 under
// kind "cli" (the only kind the server accepts; any other, e.g. the old "ai", is
// a 400). With no single resolvable source it authors unbound rather than failing
// the page.
func provenanceBinding(repoRoot, generator, ownership string, sources []string) *api.SourceBinding {
	if ownership != "machine" || len(sources) != 1 {
		return nil
	}
	ref := strings.TrimSpace(sources[0])
	if ref == "" {
		return nil
	}
	binding, err := BuildBinding(repoRoot, ref, "cli", generator)
	if err != nil {
		return nil
	}
	return binding
}

// effectiveOwnership enforces the ownership policy for AI-authored blocks:
// "machine" (locked against hand-editing) is reserved for code blocks — verbatim
// copies of a source file. Every text-bearing type (heading, prose, table) the
// model marks machine is downgraded to hybrid so the docs team can always edit
// the words; the server further reconciles an unbound hybrid to plain "human".
func effectiveOwnership(typ, own string) string {
	if own == "machine" && typ != "code" {
		return "hybrid"
	}
	return own
}

// SanitizeBlock re-canonicalizes a block loaded from a saved artifact for
// replay: earlier runs may have persisted the legacy array-header table shape
// (which the server 400s) or machine-owned narrative blocks that predate the
// code-only machine policy. Ownership downgrades drop the binding — the CLI
// binds code only.
func SanitizeBlock(b *api.BlockInput) error {
	if own := effectiveOwnership(b.Type, b.Ownership); own != b.Ownership {
		b.Ownership = own
		b.SourceBinding = nil
	}
	m, ok := b.Content.(map[string]any)
	if !ok {
		return fmt.Errorf("block %q: content must be a JSON object", b.Key)
	}
	return CanonicalizeContent(b.Type, m)
}

// normalizeContent decodes block content into a map and canonicalizes it for
// the type. The map (not the raw bytes) is returned so the text-preview
// renderer can summarize it.
func normalizeContent(typ string, raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("content is required")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("content must be a JSON object: %w", err)
	}
	if err := CanonicalizeContent(typ, m); err != nil {
		return nil, err
	}
	return m, nil
}

// CanonicalizeContent validates decoded block content in place and coerces the
// shapes models commonly emit into the server contract, so an authoring run is
// never sunk by a shape the server would 400.
func CanonicalizeContent(typ string, m map[string]any) error {
	if m == nil {
		return fmt.Errorf("content is required")
	}
	switch typ {
	case "heading", "prose", "code":
		if s, ok := m["text"].(string); !ok || strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s block requires a non-empty content.text", typ)
		}
	case "table":
		return canonicalizeTable(m)
	}
	return nil
}

// canonicalizeTable normalizes table content to the server contract:
// {rows: string[][] (header row first), header: bool}. Models routinely emit
// the natural authoring shape {header: [...columns], rows: [...]} instead —
// fold that header row into rows[0]. Cells are coerced to strings (numbers,
// booleans, and nested values become their JSON text) so a stray typed cell
// never fails the page.
func canonicalizeTable(m map[string]any) error {
	rows, _ := m["rows"].([]any)
	if cols, ok := m["header"].([]any); ok {
		rows = append([]any{cols}, rows...)
		m["header"] = true
	}
	if len(rows) == 0 {
		return fmt.Errorf("table block requires content.rows")
	}
	norm := make([][]string, 0, len(rows))
	for i, r := range rows {
		cells, ok := r.([]any)
		if !ok {
			return fmt.Errorf("table row %d must be an array of cells", i)
		}
		row := make([]string, 0, len(cells))
		for _, c := range cells {
			row = append(row, cellString(c))
		}
		norm = append(norm, row)
	}
	m["rows"] = norm
	if _, ok := m["header"].(bool); !ok {
		m["header"] = true
	}
	return nil
}

// cellString renders one table cell as the string the server schema requires.
func cellString(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
