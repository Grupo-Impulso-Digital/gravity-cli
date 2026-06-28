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
// Provenance binding: a "machine" block with exactly one source gets a real
// sha256 binding (so `check docs` verifies it); every other block gets a
// hash-less {kind:"ai"} binding (or none), which the drift checker reports as
// skipped rather than stale — AI prose is not a pure function of one file.
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
		if err := ValidAudiences(b.Audiences); err != nil {
			return nil, fmt.Errorf("block %q: %w", key, err)
		}
		content, err := normalizeContent(typ, b.Content)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w", key, err)
		}
		binding, err := provenanceBinding(repoRoot, generator, own, b.Sources)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w", key, err)
		}

		out = append(out, api.BlockInput{
			Key:           key,
			Type:          typ,
			Ownership:     own,
			Audiences:     b.Audiences,
			Content:       content,
			SourceBinding: binding,
			Position:      i,
		})
	}
	return out, nil
}

// provenanceBinding picks the right binding for an authored block. Human blocks
// and blocks with no source carry none. A single-source machine block gets a
// real verifiable hash; anything else gets a hash-less ai-provenance binding.
func provenanceBinding(repoRoot, generator, ownership string, sources []string) (*api.SourceBinding, error) {
	primary := ""
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" {
			primary = s
			break
		}
	}
	if ownership == "human" || primary == "" {
		return nil, nil
	}
	if ownership == "machine" && len(sources) == 1 {
		return BuildBinding(repoRoot, primary, "cli", generator)
	}
	return &api.SourceBinding{Kind: "ai", Ref: primary, Generator: generator}, nil
}

// normalizeContent decodes block content into a map and checks the minimal shape
// for the type. The map (not the raw bytes) is returned so the text-preview
// renderer can summarize it.
func normalizeContent(typ string, raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("content is required")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("content must be a JSON object: %w", err)
	}
	switch typ {
	case "heading", "prose", "code":
		if s, ok := m["text"].(string); !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("%s block requires a non-empty content.text", typ)
		}
	case "table":
		if _, ok := m["rows"]; !ok {
			return nil, fmt.Errorf("table block requires content.rows")
		}
	}
	return m, nil
}
