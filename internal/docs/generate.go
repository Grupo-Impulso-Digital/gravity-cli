package docs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// AuthoredBlock is a block produced by the docs-generator agent.
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

// AssembleBlocks validates AI-authored blocks and converts them into api.BlockInput values ready for a page upsert.
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

func effectiveOwnership(typ, own string) string {
	if own == "machine" && typ != "code" {
		return "hybrid"
	}
	return own
}

// SanitizeBlock re-canonicalizes a block loaded from a saved artifact for replay.
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

func normalizeContent(typ string, raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("content is required")
	}
	m, err := decodeContentObject(typ, raw)
	if err != nil {
		return nil, err
	}
	if err := CanonicalizeContent(typ, m); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeContentObject(typ string, raw json.RawMessage) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		return m, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("content must be a JSON object")
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") {
		if err := json.Unmarshal([]byte(t), &m); err == nil {
			return m, nil
		}
	}
	switch typ {
	case "heading", "prose", "code":
		return map[string]any{"text": s}, nil
	}
	return nil, fmt.Errorf("content must be a JSON object")
}

// CanonicalizeContent validates decoded block content in place.
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
