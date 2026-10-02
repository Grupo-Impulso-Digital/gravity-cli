package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

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

// Terminal tool names.
const (
	ToolSubmitPagePlan     = "submit_page_plan"
	ToolSubmitPageChanges  = "submit_page_changes"
	ToolSubmitReleaseNotes = "submit_release_notes"
	ToolSubmitAtoms        = "submit_atoms"
	ToolReportFindings     = "report_findings"
	ToolSubmitUnits        = "submit_units"
)

var stringItems = map[string]any{"type": "string"}

func noop(context.Context, json.RawMessage) (string, error) { return "", nil }

func terminal(name, description string, schema map[string]any, validate func(json.RawMessage) error) Tool {
	return Tool{
		Terminal: true,
		Validate: validate,
		Run:      noop,
		Def:      api.Tool{Name: name, Description: description, InputSchema: schema},
	}
}

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
	ActionNone      = "none"
)

// PagePlan is the input of submit_page_plan.
type PagePlan struct {
	Actions []PageAction `json:"actions"`
}

// PageAction is one planned page decision.
type PageAction struct {
	Action         string   `json:"action"`
	PageID         string   `json:"pageId,omitempty"`
	Slug           string   `json:"slug"`
	Title          string   `json:"title,omitempty"`
	CollectionPath []string `json:"collectionPath,omitempty"`
	Reason         string   `json:"reason"`
	Units          []string `json:"units,omitempty"`
}

// SlugPattern is the shape of a page slug.
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// SubmitPagePlanTool ends the guides plan phase.
func SubmitPagePlanTool() Tool {
	action := object([]string{"action", "slug", "reason"}, map[string]any{
		"action":         enum(ActionUpdate, ActionCreate, ActionDeprecate, ActionNone),
		"pageId":         map[string]any{"type": "string", "description": "Existing page id (update, deprecate)."},
		"slug":           map[string]any{"type": "string", "description": "Page slug; for create a new kebab-case slug."},
		"title":          map[string]any{"type": "string", "description": "Title of a new page."},
		"collectionPath": map[string]any{"type": "array", "items": stringItems, "description": "Collection slugs of a new page under the target."},
		"reason":         map[string]any{"type": "string", "description": "Why, naming the commits or units."},
		"units":          map[string]any{"type": "array", "items": stringItems, "description": "Unit keys this action concerns."},
	})
	return terminal(ToolSubmitPagePlan, "Submit the page actions for this change set. Call exactly once; an empty list when nothing is needed.",
		object([]string{"actions"}, map[string]any{"actions": map[string]any{"type": "array", "items": action}}), ValidatePagePlan)
}

// ValidatePagePlan checks a submit_page_plan input.
func ValidatePagePlan(raw json.RawMessage) error {
	var in PagePlan
	if err := Decode(raw, &in); err != nil {
		return fmt.Errorf("actions must be an array of objects: %w", err)
	}
	for i, a := range in.Actions {
		switch a.Action {
		case ActionUpdate, ActionDeprecate:
			if a.PageID == "" && a.Slug == "" {
				return fmt.Errorf("actions[%d]: %s needs the pageId or slug of an existing page", i, a.Action)
			}
		case ActionCreate:
			if !SlugPattern.MatchString(a.Slug) || strings.TrimSpace(a.Title) == "" {
				return fmt.Errorf("actions[%d]: create needs a kebab-case slug and a title", i)
			}
		case ActionNone:
		default:
			return fmt.Errorf("actions[%d]: action %q must be update, create, deprecate or none", i, a.Action)
		}
	}
	return nil
}

// Rationale explains a block edit.
type Rationale struct {
	Summary    string   `json:"summary"`
	Commits    []string `json:"commits,omitempty"`
	SourceRefs []string `json:"sourceRefs,omitempty"`
}

// BlockEdit is one block the author adds or replaces.
type BlockEdit struct {
	Key       string          `json:"key"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	After     *string         `json:"after,omitempty"`
	Units     []string        `json:"units,omitempty"`
	Audiences []string        `json:"audiences,omitempty"`
	Rationale Rationale       `json:"rationale"`
}

// HintDraft is a cross-repository hint the author raises instead of editing.
type HintDraft struct {
	Kind     string   `json:"kind"`
	Claim    string   `json:"claim"`
	UnitKey  string   `json:"unitKey,omitempty"`
	BlockKey string   `json:"blockKey,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	ForRepos []string `json:"forRepos,omitempty"`
}

// PageChanges is the input of submit_page_changes.
type PageChanges struct {
	Title      string      `json:"title,omitempty"`
	Summary    string      `json:"summary"`
	Upserts    []BlockEdit `json:"upserts"`
	RemoveKeys []string    `json:"removeKeys,omitempty"`
	Hints      []HintDraft `json:"hints,omitempty"`
}

// AuthoredBlockTypes are the block types authors may write.
var AuthoredBlockTypes = []string{"heading", "prose", "code", "list", "callout", "table", "quote"}

// SubmitPageChangesTool ends an author phase.
func SubmitPageChangesTool() Tool {
	rationale := object([]string{"summary"}, map[string]any{
		"summary":    map[string]any{"type": "string"},
		"commits":    map[string]any{"type": "array", "items": stringItems, "description": "SHAs that justify the edit."},
		"sourceRefs": map[string]any{"type": "array", "items": stringItems, "description": "Files (path or path:line) the edit is based on."},
	})
	block := object([]string{"key", "type", "content", "rationale"}, map[string]any{
		"key":       map[string]any{"type": "string", "description": "Existing key to replace a block, or a new guide:<page-slug>:<section-slug>[:n] key."},
		"type":      enum(AuthoredBlockTypes...),
		"content":   map[string]any{"type": "object", "description": "heading {text, level}; prose {text}; code {text, language}; list {text, variant}; callout {text, variant}; table {rows, header}; quote {text}."},
		"after":     map[string]any{"type": "string", "description": "Key of the block a new block goes after; omit for the end of the page."},
		"units":     map[string]any{"type": "array", "items": stringItems},
		"audiences": map[string]any{"type": "array", "items": stringItems},
		"rationale": rationale,
	})
	hint := object([]string{"kind", "claim"}, map[string]any{
		"kind":     enum("contradiction", "impact"),
		"claim":    map[string]any{"type": "string"},
		"unitKey":  map[string]any{"type": "string"},
		"blockKey": map[string]any{"type": "string"},
		"detail":   map[string]any{"type": "string"},
		"forRepos": map[string]any{"type": "array", "items": stringItems, "description": "Remote keys of the repositories that should look at it; empty = the unit's other contributors."},
	})
	return terminal(ToolSubmitPageChanges, "Submit the block-level edits of this page. Call exactly once.",
		object([]string{"summary", "upserts"}, map[string]any{
			"title":      map[string]any{"type": "string", "description": "Page title (new pages only)."},
			"summary":    map[string]any{"type": "string", "description": "One sentence for reviewers: what changed on the page and why."},
			"upserts":    map[string]any{"type": "array", "items": block},
			"removeKeys": map[string]any{"type": "array", "items": stringItems},
			"hints":      map[string]any{"type": "array", "items": hint},
		}), ValidatePageChanges)
}

// ValidatePageChanges checks a submit_page_changes input.
func ValidatePageChanges(raw json.RawMessage) error {
	var in PageChanges
	if err := Decode(raw, &in); err != nil {
		return fmt.Errorf("upserts must be an array of block objects: %w", err)
	}
	allowed := map[string]bool{}
	for _, t := range AuthoredBlockTypes {
		allowed[t] = true
	}
	seen := map[string]bool{}
	for i, b := range in.Upserts {
		switch {
		case strings.TrimSpace(b.Key) == "":
			return fmt.Errorf("upserts[%d] needs a key", i)
		case seen[b.Key]:
			return fmt.Errorf("upserts[%d]: key %q appears twice", i, b.Key)
		case !allowed[b.Type]:
			return fmt.Errorf("upserts[%d] (%s): type %q must be one of %s", i, b.Key, b.Type, strings.Join(AuthoredBlockTypes, ", "))
		case !isObject(b.Content):
			return fmt.Errorf("upserts[%d] (%s): content must be an object", i, b.Key)
		case strings.TrimSpace(b.Rationale.Summary) == "":
			return fmt.Errorf("upserts[%d] (%s): rationale.summary is required; cite the commits behind the edit", i, b.Key)
		}
		seen[b.Key] = true
	}
	return nil
}

func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

// ReleaseSections are the changelog sections in display order.
var ReleaseSections = []string{"Added", "Changed", "Fixed", "Deprecated", "Removed", "Security"}

// ReleaseItem is one release-notes entry.
type ReleaseItem struct {
	Text    string   `json:"text"`
	Commits []string `json:"commits,omitempty"`
	Units   []string `json:"units,omitempty"`
}

// UnmarshalJSON accepts an entry object or a bare string.
func (r *ReleaseItem) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*r = ReleaseItem{Text: s}
		return nil
	}
	type plain ReleaseItem
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*r = ReleaseItem(p)
	return nil
}

// ReleaseSection groups entries under a heading.
type ReleaseSection struct {
	Heading string        `json:"heading"`
	Items   []ReleaseItem `json:"items"`
}

// ReleaseNotesInput is the input of submit_release_notes.
type ReleaseNotesInput struct {
	Title    string           `json:"title"`
	Summary  string           `json:"summary"`
	Sections []ReleaseSection `json:"sections"`
}

// SubmitReleaseNotesTool ends the changelog author phase.
func SubmitReleaseNotesTool() Tool {
	item := object([]string{"text"}, map[string]any{
		"text":    map[string]any{"type": "string"},
		"commits": map[string]any{"type": "array", "items": stringItems},
		"units":   map[string]any{"type": "array", "items": stringItems},
	})
	section := object([]string{"heading", "items"}, map[string]any{
		"heading": enum(ReleaseSections...),
		"items":   map[string]any{"type": "array", "items": item},
	})
	return terminal(ToolSubmitReleaseNotes, "Submit the finished release notes. Call exactly once.",
		object([]string{"summary", "sections"}, map[string]any{
			"title":    map[string]any{"type": "string"},
			"summary":  map[string]any{"type": "string", "description": "One or two sentences about the release."},
			"sections": map[string]any{"type": "array", "items": section},
		}), ValidateReleaseNotes)
}

// ValidateReleaseNotes checks a submit_release_notes input.
func ValidateReleaseNotes(raw json.RawMessage) error {
	var in ReleaseNotesInput
	if err := Decode(raw, &in); err != nil {
		return fmt.Errorf("sections must be an array of objects: %w", err)
	}
	known := map[string]bool{}
	for _, s := range ReleaseSections {
		known[s] = true
	}
	for i, s := range in.Sections {
		if !known[s.Heading] {
			return fmt.Errorf("sections[%d]: heading %q must be one of %s", i, s.Heading, strings.Join(ReleaseSections, ", "))
		}
	}
	return nil
}

// ParseReleaseNotes decodes a submit_release_notes input.
func ParseReleaseNotes(raw json.RawMessage) (ReleaseNotesInput, error) {
	var in ReleaseNotesInput
	err := Decode(raw, &in)
	return in, err
}

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

// AtomsInput is the input of submit_atoms.
type AtomsInput struct {
	Atoms []Atom `json:"atoms"`
}

// MemoryKinds are the Nucleus atom kinds.
var MemoryKinds = []string{"fact", "entity", "concept", "procedure", "decision", "glossary", "relationship", "preference", "other"}

// SubmitAtomsTool ends the nucleus distill phase.
func SubmitAtomsTool() Tool {
	atom := object([]string{"title", "body"}, map[string]any{
		"title":      map[string]any{"type": "string", "description": "Short, stable noun phrase; reusing it revises that memory."},
		"body":       map[string]any{"type": "string", "description": "One or two self-contained sentences."},
		"kind":       enum(MemoryKinds...),
		"tags":       map[string]any{"type": "array", "items": stringItems},
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"commits":    map[string]any{"type": "array", "items": stringItems},
		"units":      map[string]any{"type": "array", "items": stringItems},
	})
	return terminal(ToolSubmitAtoms, "Submit the distilled memories. Call exactly once, with an empty array when nothing is worth remembering.",
		object([]string{"atoms"}, map[string]any{"atoms": map[string]any{"type": "array", "items": atom}}), func(raw json.RawMessage) error {
			var in AtomsInput
			if err := Decode(raw, &in); err != nil {
				return fmt.Errorf("atoms must be an array of objects: %w", err)
			}
			for i, a := range in.Atoms {
				switch {
				case strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "":
					return fmt.Errorf("atoms[%d] needs a title and a body", i)
				case utf8.RuneCountInString(a.Title) > 200:
					return fmt.Errorf("atoms[%d].title is longer than 200 characters", i)
				case utf8.RuneCountInString(a.Body) > 8000:
					return fmt.Errorf("atoms[%d].body is longer than 8000 characters", i)
				case len(a.Tags) > 32:
					return fmt.Errorf("atoms[%d] has more than 32 tags", i)
				}
			}
			return nil
		})
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

// FindingsInput is the input of report_findings.
type FindingsInput struct {
	Findings []ClaimFinding `json:"findings"`
}

// Verdicts a claim review assigns.
var Verdicts = []string{api.VerdictVerifiedHere, api.VerdictTrueElsewhere, api.VerdictUnverifiable, api.VerdictContradicted}

// ReportFindingsTool ends a check phase.
func ReportFindingsTool() Tool {
	finding := object([]string{"verdict", "title"}, map[string]any{
		"verdict":  enum(Verdicts...),
		"category": enum("claims", "verbatim"),
		"title":    map[string]any{"type": "string"},
		"claim":    map[string]any{"type": "string", "description": "The statement as the page makes it."},
		"detail":   map[string]any{"type": "string"},
		"unitKey":  map[string]any{"type": "string"},
		"blockKey": map[string]any{"type": "string"},
		"pageId":   map[string]any{"type": "string"},
		"pageSlug": map[string]any{"type": "string"},
		"file":     map[string]any{"type": "string"},
		"line":     map[string]any{"type": "integer"},
		"repo":     map[string]any{"type": "string", "description": "For true-elsewhere: the repository that owns the claim."},
		"evidence": map[string]any{"type": "array", "items": object([]string{"kind", "ref"}, map[string]any{"kind": enum("commit", "file", "spec", "atom", "repo"), "ref": stringItems})},
	})
	return terminal(ToolReportFindings, "Report the reviewed claims. Call exactly once, with an empty array when the pages hold no claims about the changed units.",
		object([]string{"findings"}, map[string]any{"findings": map[string]any{"type": "array", "items": finding}}), func(raw json.RawMessage) error {
			var in FindingsInput
			if err := Decode(raw, &in); err != nil {
				return fmt.Errorf("findings must be an array of objects: %w", err)
			}
			valid := map[string]bool{}
			for _, v := range Verdicts {
				valid[v] = true
			}
			for i, f := range in.Findings {
				if !valid[f.Verdict] {
					return fmt.Errorf("findings[%d]: verdict %q must be one of %s", i, f.Verdict, strings.Join(Verdicts, ", "))
				}
			}
			return nil
		})
}

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

// UnitsInput is the input of submit_units.
type UnitsInput struct {
	Units []MappedUnit `json:"units"`
}

// UnitKeyPattern is the grammar of a product unit key.
var UnitKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,127}$`)

var unitKinds = []string{api.UnitKindFeature, api.UnitKindService, api.UnitKindSystem, api.UnitKindAPI, api.UnitKindCapability}

// SubmitUnitsTool ends the map-units step.
func SubmitUnitsTool() Tool {
	unit := object([]string{"key", "kind", "title", "sourceRefs"}, map[string]any{
		"key":        map[string]any{"type": "string", "description": "Existing product key when it is the same unit; else a new stable key."},
		"kind":       enum(unitKinds...),
		"title":      map[string]any{"type": "string"},
		"summary":    map[string]any{"type": "string"},
		"sourceRefs": map[string]any{"type": "array", "items": stringItems},
		"aliases":    map[string]any{"type": "array", "items": stringItems},
		"audiences":  map[string]any{"type": "array", "items": stringItems},
	})
	return terminal(ToolSubmitUnits, "Submit the units this repository contributes to. Call exactly once.",
		object([]string{"units"}, map[string]any{"units": map[string]any{"type": "array", "items": unit}}), ValidateUnits)
}

// ValidateUnits checks a submit_units input.
func ValidateUnits(raw json.RawMessage) error {
	var in UnitsInput
	if err := Decode(raw, &in); err != nil {
		return fmt.Errorf("units must be an array of objects: %w", err)
	}
	kinds := map[string]bool{}
	for _, k := range unitKinds {
		kinds[k] = true
	}
	seen := map[string]bool{}
	for i, u := range in.Units {
		switch {
		case !UnitKeyPattern.MatchString(u.Key):
			return fmt.Errorf("units[%d].key %q must match %s", i, u.Key, UnitKeyPattern)
		case seen[u.Key]:
			return fmt.Errorf("units[%d].key %q is duplicated", i, u.Key)
		case !kinds[u.Kind]:
			return fmt.Errorf("units[%d] (%s): kind %q must be one of %s", i, u.Key, u.Kind, strings.Join(unitKinds, ", "))
		case len(u.SourceRefs) == 0:
			return fmt.Errorf("units[%d] (%s) needs at least one sourceRef", i, u.Key)
		case strings.TrimSpace(u.Title) == "":
			return fmt.Errorf("units[%d] (%s) needs a title", i, u.Key)
		case utf8.RuneCountInString(u.Title) > 300:
			return fmt.Errorf("units[%d] (%s): title is longer than 300 characters", i, u.Key)
		case len(u.Audiences) > 8:
			return fmt.Errorf("units[%d] (%s) has more than 8 audiences", i, u.Key)
		}
		seen[u.Key] = true
	}
	return nil
}

// ErrNoSubmit means the model never called the terminal tool.
var ErrNoSubmit = errors.New("the model did not submit a result")
