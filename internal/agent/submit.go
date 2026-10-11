package agent

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func unwrapJSONString(raw json.RawMessage) (json.RawMessage, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
		return json.RawMessage(t), true
	}
	return nil, false
}

// Decode unmarshals a terminal tool input, accepting a JSON document the model wrapped in a string.
func Decode(raw json.RawMessage, v any) error {
	err := json.Unmarshal(raw, v)
	if err == nil {
		return nil
	}
	if inner, ok := unwrapJSONString(raw); ok {
		if err2 := json.Unmarshal(inner, v); err2 == nil {
			return nil
		}
	}
	return err
}

var stringItems = map[string]any{"type": "string"}

func object(required []string, props map[string]any) map[string]any {
	req := make([]any, 0, len(required))
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func enum(values ...string) map[string]any {
	list := make([]any, 0, len(values))
	for _, v := range values {
		list = append(list, v)
	}
	return map[string]any{"type": "string", "enum": list}
}

// Page plan actions.
const (
	ActionUpdate    = "update"
	ActionCreate    = "create"
	ActionDeprecate = "deprecate"
)

// PageAction is one planned page decision.
type PageAction struct {
	Action         string      `json:"action"`
	PageID         string      `json:"pageId,omitempty"`
	Slug           string      `json:"slug"`
	Title          string      `json:"title,omitempty"`
	CollectionPath []string    `json:"collectionPath,omitempty"`
	Units          []string    `json:"units"`
	MustCover      []MustCover `json:"mustCover"`
	Why            string      `json:"why"`
}

// SlugPattern is the shape of a page slug.
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Rationale explains a block edit.
type Rationale struct {
	Summary    string   `json:"summary"`
	Commits    []string `json:"commits,omitempty"`
	SourceRefs []string `json:"sourceRefs,omitempty"`
}

// HintDraft is a cross-repository hint the author raises with raise_hint instead of editing.
type HintDraft struct {
	Kind     string   `json:"kind"`
	Claim    string   `json:"claim"`
	UnitKey  string   `json:"unitKey,omitempty"`
	BlockKey string   `json:"blockKey,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	ForRepos []string `json:"forRepos,omitempty"`
}

var listVariants = map[string]string{
	"bulleted": "bulleted", "bullet": "bulleted", "bullets": "bulleted", "unordered": "bulleted", "ul": "bulleted", "disc": "bulleted", "dash": "bulleted",
	"numbered": "numbered", "number": "numbered", "numbers": "numbered", "ordered": "numbered", "ol": "numbered", "decimal": "numbered",
	"task": "bulleted", "tasks": "bulleted", "todo": "bulleted", "checklist": "bulleted", "checkbox": "bulleted", "check": "bulleted",
}

// NormalizeContent maps common model variations of authored block content onto the shapes the platform accepts.
func NormalizeContent(blockType string, raw json.RawMessage) any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return raw
	}
	switch blockType {
	case "list":
		v, _ := m["variant"].(string)
		variant, ok := listVariants[strings.ToLower(strings.TrimSpace(v))]
		if !ok {
			variant = "bulleted"
		}
		m["variant"] = variant
		delete(m, "checked")
	case "heading":
		level := 2
		switch l := m["level"].(type) {
		case float64:
			level = int(l)
		case string:
			if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(l)), "h")); err == nil {
				level = n
			}
		}
		m["level"] = min(max(level, 1), 3)
	case "callout", "code":
		for _, k := range []string{"variant", "language"} {
			if v, ok := m[k]; ok {
				if _, isString := v.(string); !isString {
					delete(m, k)
				}
			}
		}
	}
	return m
}

// ReleaseSections are the changelog sections in display order.
var ReleaseSections = []string{"Added", "Changed", "Fixed", "Deprecated", "Removed", "Security"}

// Atom is one distilled memory.
type Atom struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Kind       string   `json:"kind,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Commits    []string `json:"commits,omitempty"`
	Units      []string `json:"units,omitempty"`
}

// MemoryKinds are the Nucleus atom kinds.
var MemoryKinds = []string{"fact", "entity", "concept", "procedure", "decision", "glossary", "relationship", "preference", "other"}

// MemoryKind returns kind when Nucleus knows it, fact when it is empty and other otherwise.
func MemoryKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return "fact"
	}
	for _, known := range MemoryKinds {
		if k == known {
			return k
		}
	}
	return "other"
}

// ClaimEvidence supports a claim verdict.
type ClaimEvidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// ClaimFinding is one reviewed claim.
type ClaimFinding struct {
	Verdict  string          `json:"verdict"`
	Category string          `json:"category,omitempty"`
	Title    string          `json:"title"`
	Claim    string          `json:"claim,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	UnitKey  string          `json:"unitKey,omitempty"`
	BlockKey string          `json:"blockKey,omitempty"`
	PageID   string          `json:"pageId,omitempty"`
	PageSlug string          `json:"pageSlug,omitempty"`
	File     string          `json:"file,omitempty"`
	Line     int             `json:"line,omitempty"`
	Repo     string          `json:"repo,omitempty"`
	Evidence []ClaimEvidence `json:"evidence,omitempty"`
}

// Verdicts a claim review assigns.
var Verdicts = []string{api.VerdictVerifiedHere, api.VerdictTrueElsewhere, api.VerdictUnverifiable, api.VerdictContradicted}

// MappedUnit is one unit found by the map-units step.
type MappedUnit struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary,omitempty"`
	SourceRefs []string `json:"sourceRefs"`
	Aliases    []string `json:"aliases,omitempty"`
	Audiences  []string `json:"audiences,omitempty"`
}

// UnitKeyPattern is the grammar of a product unit key.
var UnitKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,127}$`)

var unitKinds = []string{api.UnitKindFeature, api.UnitKindService, api.UnitKindSystem, api.UnitKindAPI, api.UnitKindCapability}
