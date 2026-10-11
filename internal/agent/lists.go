package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// List-phase tool names (gravity.tools/1).
const (
	ToolAddPages           = "add_pages"
	ToolFinishPlan         = "finish_plan"
	ToolAddReleaseItems    = "add_release_items"
	ToolFinishReleaseNotes = "finish_release_notes"
	ToolAddAtoms           = "add_atoms"
	ToolFinishAtoms        = "finish_atoms"
	ToolAddFindings        = "add_findings"
	ToolFinishReview       = "finish_review"
	ToolAddUnits           = "add_units"
	ToolFinishUnits        = "finish_units"
)

func listItems(raw json.RawMessage, field string) ([]json.RawMessage, error) {
	var in map[string]json.RawMessage
	if err := decodeInput(raw, &in); err != nil {
		return nil, err
	}
	v, ok := in[field]
	if !ok {
		return nil, fmt.Errorf("%s is required", field)
	}
	var items []json.RawMessage
	if err := Decode(v, &items); err != nil {
		return nil, fmt.Errorf("%s must be an array of objects", field)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s is empty", field)
	}
	return items, nil
}

func decodeItem(raw json.RawMessage, v any) error {
	if err := Decode(raw, v); err != nil {
		return fmt.Errorf("not a valid object: %w", err)
	}
	return nil
}

// addTool builds an accumulating tool: every item is validated on its own, valid ones are kept and the invalid ones are listed in a tool error.
func addTool(name, description, field string, item map[string]any, add func(raw json.RawMessage) error, total func() string) Tool {
	return Tool{
		Def: api.Tool{Name: name, Description: description, InputSchema: object([]string{field}, map[string]any{field: map[string]any{"type": "array", "items": item}})},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			items, err := listItems(raw, field)
			if err != nil {
				return "", err
			}
			var problems []string
			for i, it := range items {
				if err := add(it); err != nil {
					problems = append(problems, fmt.Sprintf("%s[%d]: %v", field, i, err))
				}
			}
			status := fmt.Sprintf("Kept %d of %d %s; %s.", len(items)-len(problems), len(items), field, total())
			if len(problems) > 0 {
				return "", fmt.Errorf("%s Rejected:\n- %s", status, strings.Join(problems, "\n- "))
			}
			return status, nil
		},
	}
}

type summaryInput struct {
	Summary string `json:"summary"`
}

func finishTool(name, description string, schema map[string]any, validate func(raw json.RawMessage) error, done func(raw json.RawMessage)) Tool {
	return Tool{
		Terminal: true,
		Def:      api.Tool{Name: name, Description: description, InputSchema: schema},
		Validate: validate,
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			done(raw)
			return "Finished.", nil
		},
	}
}

func summaryFinish(name, description string, set func(string)) Tool {
	validate := func(raw json.RawMessage) error {
		var in summaryInput
		if err := decodeInput(raw, &in); err != nil {
			return fmt.Errorf("%s takes {summary}: %w", name, err)
		}
		return nil
	}
	return finishTool(name, description, object([]string{"summary"}, map[string]any{"summary": map[string]any{"type": "string"}}), validate, func(raw json.RawMessage) {
		var in summaryInput
		_ = decodeInput(raw, &in)
		set(strings.TrimSpace(in.Summary))
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// PlanList accumulates the page actions of the guides.plan phase (add_pages, finish_plan).
type PlanList struct {
	maxPages int
	pages    []PageAction
	summary  string
}

// NewPlanList returns an empty plan that holds at most maxPages actions.
func NewPlanList(maxPages int) *PlanList { return &PlanList{maxPages: maxPages} }

// Pages returns the accepted page actions in the order they were added.
func (p *PlanList) Pages() []PageAction { return append([]PageAction(nil), p.pages...) }

// Summary returns the finish_plan summary.
func (p *PlanList) Summary() string { return p.summary }

func (p *PlanList) add(raw json.RawMessage) error {
	var a PageAction
	if err := decodeItem(raw, &a); err != nil {
		return err
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	a.Slug = strings.TrimSpace(a.Slug)
	a.Title = strings.TrimSpace(a.Title)
	a.Why = strings.TrimSpace(a.Why)
	switch a.Action {
	case ActionUpdate, ActionDeprecate:
		if a.PageID == "" && a.Slug == "" {
			return fmt.Errorf("%s needs the pageId or slug of an existing page", a.Action)
		}
	case ActionCreate:
		if !SlugPattern.MatchString(a.Slug) {
			return fmt.Errorf("create needs a kebab-case slug, got %q", a.Slug)
		}
		if a.Title == "" {
			return errors.New("create needs a title")
		}
	default:
		return fmt.Errorf("action %q must be create, update or deprecate", a.Action)
	}
	if a.Why == "" {
		return errors.New("why is required")
	}
	for i, m := range a.MustCover {
		a.MustCover[i].Token = strings.TrimSpace(m.Token)
		a.MustCover[i].Text = strings.TrimSpace(m.Text)
		if a.MustCover[i].Text == "" {
			return fmt.Errorf("mustCover[%d] needs a text", i)
		}
	}
	for _, q := range p.pages {
		if a.Slug != "" && q.Slug == a.Slug || a.PageID != "" && q.PageID == a.PageID {
			return fmt.Errorf("page %s is already in the plan", pageName(a))
		}
	}
	if p.maxPages > 0 && len(p.pages) >= p.maxPages {
		return fmt.Errorf("page budget reached (%d)", p.maxPages)
	}
	p.pages = append(p.pages, a)
	return nil
}

func pageName(a PageAction) string {
	if a.Slug != "" {
		return a.Slug
	}
	return a.PageID
}

// AddTool is add_pages.
func (p *PlanList) AddTool() Tool {
	page := object([]string{"action", "slug", "title", "units", "mustCover", "why"}, map[string]any{
		"action":         enum(ActionCreate, ActionUpdate, ActionDeprecate),
		"pageId":         map[string]any{"type": "string"},
		"slug":           map[string]any{"type": "string"},
		"title":          map[string]any{"type": "string"},
		"collectionPath": map[string]any{"type": "array", "items": stringItems},
		"units":          map[string]any{"type": "array", "items": stringItems},
		"mustCover": map[string]any{"type": "array", "items": object([]string{"text"}, map[string]any{
			"token": map[string]any{"type": "string"},
			"text":  map[string]any{"type": "string"},
		})},
		"why": map[string]any{"type": "string"},
	})
	return addTool(ToolAddPages, "Add page actions to the plan.", "pages", page, p.add, func() string {
		if p.maxPages > 0 {
			return fmt.Sprintf("the plan has %s (budget %d)", plural(len(p.pages), "page", "pages"), p.maxPages)
		}
		return "the plan has " + plural(len(p.pages), "page", "pages")
	})
}

// FinishTool is finish_plan.
func (p *PlanList) FinishTool() Tool {
	return summaryFinish(ToolFinishPlan, "Finish the plan.", func(s string) { p.summary = s })
}

// ReleaseItem is one release-notes entry under a section.
type ReleaseItem struct {
	Section string   `json:"section"`
	Text    string   `json:"text"`
	Commits []string `json:"commits,omitempty"`
	Units   []string `json:"units,omitempty"`
}

// ReleaseSection groups entries under a heading.
type ReleaseSection struct {
	Heading string
	Items   []ReleaseItem
}

// ReleaseNotes accumulates the entries of the changelog.write phase (add_release_items, finish_release_notes).
type ReleaseNotes struct {
	items   []ReleaseItem
	summary string
}

// NewReleaseNotes returns empty release notes.
func NewReleaseNotes() *ReleaseNotes { return &ReleaseNotes{} }

// Summary returns the finish_release_notes summary.
func (r *ReleaseNotes) Summary() string { return r.summary }

// Sections groups the entries by section in ReleaseSections order, keeping the order they were added in.
func (r *ReleaseNotes) Sections() []ReleaseSection {
	var out []ReleaseSection
	for _, heading := range ReleaseSections {
		var items []ReleaseItem
		for _, it := range r.items {
			if it.Section == heading {
				items = append(items, it)
			}
		}
		if len(items) > 0 {
			out = append(out, ReleaseSection{Heading: heading, Items: items})
		}
	}
	return out
}

func releaseSection(s string) (string, bool) {
	for _, known := range ReleaseSections {
		if strings.EqualFold(strings.TrimSpace(s), known) {
			return known, true
		}
	}
	return "", false
}

func (r *ReleaseNotes) add(raw json.RawMessage) error {
	var it ReleaseItem
	if err := decodeItem(raw, &it); err != nil {
		return err
	}
	section, ok := releaseSection(it.Section)
	if !ok {
		return fmt.Errorf("section %q must be one of %s", it.Section, strings.Join(ReleaseSections, ", "))
	}
	it.Section, it.Text = section, strings.TrimSpace(it.Text)
	if it.Text == "" {
		return errors.New("text is required")
	}
	r.items = append(r.items, it)
	return nil
}

// AddTool is add_release_items.
func (r *ReleaseNotes) AddTool() Tool {
	item := object([]string{"section", "text"}, map[string]any{
		"section": enum(ReleaseSections...),
		"text":    map[string]any{"type": "string"},
		"commits": map[string]any{"type": "array", "items": stringItems},
		"units":   map[string]any{"type": "array", "items": stringItems},
	})
	return addTool(ToolAddReleaseItems, "Add release-notes entries.", "items", item, r.add, func() string {
		return "the notes have " + plural(len(r.items), "entry", "entries")
	})
}

// FinishTool is finish_release_notes.
func (r *ReleaseNotes) FinishTool() Tool {
	return summaryFinish(ToolFinishReleaseNotes, "Finish the release notes.", func(s string) { r.summary = s })
}

// AtomList accumulates the memories of the nucleus.distill phase (add_atoms, finish_atoms).
type AtomList struct {
	maxAtoms int
	kinds    []string
	atoms    []Atom
}

// NewAtomList returns an empty list that holds at most maxAtoms atoms of the given kinds (any kind when empty).
func NewAtomList(maxAtoms int, kinds []string) *AtomList {
	return &AtomList{maxAtoms: maxAtoms, kinds: kinds}
}

// Atoms returns the accepted atoms.
func (l *AtomList) Atoms() []Atom { return append([]Atom(nil), l.atoms...) }

func (l *AtomList) add(raw json.RawMessage) error {
	var a Atom
	if err := decodeItem(raw, &a); err != nil {
		return err
	}
	a.Title, a.Body = strings.TrimSpace(a.Title), strings.TrimSpace(a.Body)
	switch {
	case a.Title == "" || a.Body == "":
		return errors.New("needs a title and a body")
	case utf8.RuneCountInString(a.Title) > 200:
		return errors.New("title is longer than 200 characters")
	case utf8.RuneCountInString(a.Body) > 8000:
		return errors.New("body is longer than 8000 characters")
	case len(a.Tags) > 32:
		return errors.New("has more than 32 tags")
	case a.Confidence < 0 || a.Confidence > 1:
		return errors.New("confidence must be between 0 and 1")
	}
	a.Kind = MemoryKind(a.Kind)
	if len(l.kinds) > 0 && !oneOf(a.Kind, l.kinds) {
		return fmt.Errorf("kind %q is not allowed in this pass (allowed: %s)", a.Kind, strings.Join(l.kinds, ", "))
	}
	if l.maxAtoms > 0 && len(l.atoms) >= l.maxAtoms {
		return fmt.Errorf("atom budget reached (%d)", l.maxAtoms)
	}
	l.atoms = append(l.atoms, a)
	return nil
}

// AddTool is add_atoms.
func (l *AtomList) AddTool() Tool {
	kind := enum(MemoryKinds...)
	if len(l.kinds) > 0 {
		kind = enum(l.kinds...)
	}
	atom := object([]string{"title", "body"}, map[string]any{
		"title":      map[string]any{"type": "string"},
		"body":       map[string]any{"type": "string"},
		"kind":       kind,
		"tags":       map[string]any{"type": "array", "items": stringItems},
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"commits":    map[string]any{"type": "array", "items": stringItems},
		"units":      map[string]any{"type": "array", "items": stringItems},
	})
	return addTool(ToolAddAtoms, "Add memories.", "atoms", atom, l.add, func() string {
		if l.maxAtoms > 0 {
			return fmt.Sprintf("the list has %s (budget %d)", plural(len(l.atoms), "atom", "atoms"), l.maxAtoms)
		}
		return "the list has " + plural(len(l.atoms), "atom", "atoms")
	})
}

// FinishTool is finish_atoms.
func (l *AtomList) FinishTool() Tool {
	return finishTool(ToolFinishAtoms, "Finish the memories.", object(nil, map[string]any{}), nil, func(json.RawMessage) {})
}

// Review accumulates the claim findings of the check.review phase (add_findings, finish_review).
type Review struct {
	findings []ClaimFinding
	summary  string
}

// NewReview returns an empty review.
func NewReview() *Review { return &Review{} }

// Findings returns the accepted findings.
func (r *Review) Findings() []ClaimFinding { return append([]ClaimFinding(nil), r.findings...) }

// Summary returns the finish_review summary.
func (r *Review) Summary() string { return r.summary }

func (r *Review) add(raw json.RawMessage) error {
	var f ClaimFinding
	if err := decodeItem(raw, &f); err != nil {
		return err
	}
	if !oneOf(f.Verdict, Verdicts) {
		return fmt.Errorf("verdict %q must be one of %s", f.Verdict, strings.Join(Verdicts, ", "))
	}
	if strings.TrimSpace(f.Title) == "" && strings.TrimSpace(f.Claim) == "" {
		return errors.New("needs a title or a claim")
	}
	r.findings = append(r.findings, f)
	return nil
}

// AddTool is add_findings.
func (r *Review) AddTool() Tool {
	finding := object([]string{"verdict", "title"}, map[string]any{
		"verdict":  enum(Verdicts...),
		"category": enum("claims", "verbatim"),
		"title":    map[string]any{"type": "string"},
		"claim":    map[string]any{"type": "string"},
		"detail":   map[string]any{"type": "string"},
		"unitKey":  map[string]any{"type": "string"},
		"blockKey": map[string]any{"type": "string"},
		"pageId":   map[string]any{"type": "string"},
		"pageSlug": map[string]any{"type": "string"},
		"file":     map[string]any{"type": "string"},
		"line":     map[string]any{"type": "integer"},
		"repo":     map[string]any{"type": "string"},
		"evidence": map[string]any{"type": "array", "items": object([]string{"kind", "ref"}, map[string]any{"kind": enum("commit", "file", "spec", "atom", "repo"), "ref": stringItems})},
	})
	return addTool(ToolAddFindings, "Add reviewed claims.", "findings", finding, r.add, func() string {
		return "the review has " + plural(len(r.findings), "finding", "findings")
	})
}

// FinishTool is finish_review.
func (r *Review) FinishTool() Tool {
	return summaryFinish(ToolFinishReview, "Finish the review.", func(s string) { r.summary = s })
}

// UnitList accumulates the units of the units.map phase (add_units, finish_units); a later unit with the same key replaces the earlier one.
type UnitList struct {
	units []MappedUnit
}

// NewUnitList returns an empty unit list.
func NewUnitList() *UnitList { return &UnitList{} }

// Units returns the accepted units.
func (l *UnitList) Units() []MappedUnit { return append([]MappedUnit(nil), l.units...) }

func (l *UnitList) add(raw json.RawMessage) error {
	var u MappedUnit
	if err := decodeItem(raw, &u); err != nil {
		return err
	}
	u.Key, u.Title = strings.TrimSpace(u.Key), strings.TrimSpace(u.Title)
	switch {
	case !UnitKeyPattern.MatchString(u.Key):
		return fmt.Errorf("key %q must match %s", u.Key, UnitKeyPattern)
	case !oneOf(u.Kind, unitKinds):
		return fmt.Errorf("%s: kind %q must be one of %s", u.Key, u.Kind, strings.Join(unitKinds, ", "))
	case len(u.SourceRefs) == 0:
		return fmt.Errorf("%s needs at least one sourceRef", u.Key)
	case u.Title == "":
		return fmt.Errorf("%s needs a title", u.Key)
	case utf8.RuneCountInString(u.Title) > 300:
		return fmt.Errorf("%s: title is longer than 300 characters", u.Key)
	case len(u.Audiences) > 8:
		return fmt.Errorf("%s has more than 8 audiences", u.Key)
	}
	for i := range l.units {
		if l.units[i].Key == u.Key {
			l.units[i] = u
			return nil
		}
	}
	l.units = append(l.units, u)
	return nil
}

// AddTool is add_units.
func (l *UnitList) AddTool() Tool {
	unit := object([]string{"key", "kind", "title", "sourceRefs"}, map[string]any{
		"key":        map[string]any{"type": "string"},
		"kind":       enum(unitKinds...),
		"title":      map[string]any{"type": "string"},
		"summary":    map[string]any{"type": "string"},
		"sourceRefs": map[string]any{"type": "array", "items": stringItems},
		"aliases":    map[string]any{"type": "array", "items": stringItems},
		"audiences":  map[string]any{"type": "array", "items": stringItems},
	})
	return addTool(ToolAddUnits, "Add units; a unit with a key already added replaces it.", "units", unit, l.add, func() string {
		return "the list has " + plural(len(l.units), "unit", "units")
	})
}

type finishUnitsInput struct {
	Complete *bool `json:"complete"`
}

// FinishTool is finish_units.
func (l *UnitList) FinishTool() Tool {
	validate := func(raw json.RawMessage) error {
		var in finishUnitsInput
		if err := decodeInput(raw, &in); err != nil {
			return fmt.Errorf("finish_units takes {complete}: %w", err)
		}
		if in.Complete == nil {
			return errors.New("complete is required: true when the list covers every unit of the repository, else false")
		}
		return nil
	}
	return finishTool(ToolFinishUnits, "Finish the unit list.", object([]string{"complete"}, map[string]any{"complete": map[string]any{"type": "boolean"}}), validate, func(json.RawMessage) {})
}

// Complete reports whether the task ended with an unforced, successful finish_units{complete:true}; the loop discards tool calls of truncated turns, so that turn was not truncated.
func (l *UnitList) Complete(res *Result) bool {
	if res == nil || res.TerminalTool != ToolFinishUnits || res.Forced || res.Partial {
		return false
	}
	var in finishUnitsInput
	if err := Decode(res.TerminalInput, &in); err != nil || in.Complete == nil {
		return false
	}
	return *in.Complete
}
