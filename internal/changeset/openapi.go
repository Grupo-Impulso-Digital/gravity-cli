package changeset

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v2 "github.com/pb33f/libopenapi/datamodel/high/v2"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// OperationChange describes one changed operation.
type OperationChange struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	OperationID string   `json:"operationId,omitempty"`
	Changes     []string `json:"changes,omitempty"`
	Breaking    bool     `json:"breaking,omitempty"`
}

// SpecDiff is the deterministic diff of one OpenAPI document between base and head.
type SpecDiff struct {
	Path     string            `json:"path"`
	Added    []OperationChange `json:"added"`
	Removed  []OperationChange `json:"removed"`
	Changed  []OperationChange `json:"changed"`
	Breaking bool              `json:"breaking"`
	Error    string            `json:"error,omitempty"`
}

type field struct {
	required bool
	enum     []string
}

type operation struct {
	method, path, operationID, summary string
	deprecated                         bool
	params                             map[string]field
	body                               map[string]field
	bodyRequired                       bool
	hasBody                            bool
	responses                          map[string]map[string]field
}

func (o operation) key() string { return o.method + " " + o.path }

var errNoModel = errors.New("not an OpenAPI 2 or 3 document")

func parseOperations(data []byte) (map[string]operation, error) {
	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		return nil, fmt.Errorf("parse openapi document: %w", err)
	}
	ops := map[string]operation{}
	if v3doc, err := doc.BuildV3Model(); err == nil && v3doc != nil {
		if v3doc.Model.Paths == nil {
			return ops, nil
		}
		for pair := v3doc.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
			item := pair.Value()
			for method, op := range item.GetOperations().FromOldest() {
				o := v3Operation(strings.ToUpper(method), pair.Key(), item.Parameters, op)
				ops[o.key()] = o
			}
		}
		return ops, nil
	}
	if v2doc, err := doc.BuildV2Model(); err == nil && v2doc != nil {
		if v2doc.Model.Paths == nil {
			return ops, nil
		}
		for pair := v2doc.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
			item := pair.Value()
			for method, op := range item.GetOperations().FromOldest() {
				o := operation{method: strings.ToUpper(method), path: pair.Key(), params: map[string]field{}, body: map[string]field{}, responses: map[string]map[string]field{}}
				if op != nil {
					o.operationID, o.summary, o.deprecated = op.OperationId, op.Summary, op.Deprecated
					params := append(append([]*v2.Parameter{}, item.Parameters...), op.Parameters...)
					for _, p := range params {
						if p == nil {
							continue
						}
						if p.In == "body" {
							o.hasBody = true
							o.bodyRequired = p.Required != nil && *p.Required
							o.body = schemaFields(p.Schema)
							continue
						}
						f := field{required: p.Required != nil && *p.Required}
						for _, n := range p.Enum {
							f.enum = append(f.enum, n.Value)
						}
						o.params[p.In+":"+p.Name] = f
					}
					if op.Responses != nil && op.Responses.Codes != nil {
						for c := op.Responses.Codes.First(); c != nil; c = c.Next() {
							var fields map[string]field
							if c.Value() != nil {
								fields = schemaFields(c.Value().Schema)
							}
							o.responses[c.Key()] = fields
						}
					}
				}
				ops[o.key()] = o
			}
		}
		return ops, nil
	}
	return nil, errNoModel
}

func v3Operation(method, path string, shared []*v3.Parameter, op *v3.Operation) operation {
	o := operation{method: method, path: path, params: map[string]field{}, body: map[string]field{}, responses: map[string]map[string]field{}}
	if op == nil {
		return o
	}
	o.operationID, o.summary = op.OperationId, op.Summary
	o.deprecated = op.Deprecated != nil && *op.Deprecated
	for _, p := range append(append([]*v3.Parameter{}, shared...), op.Parameters...) {
		if p == nil {
			continue
		}
		f := field{required: p.Required != nil && *p.Required}
		if p.Schema != nil {
			if s := p.Schema.Schema(); s != nil {
				for _, n := range s.Enum {
					f.enum = append(f.enum, n.Value)
				}
			}
		}
		o.params[p.In+":"+p.Name] = f
	}
	if rb := op.RequestBody; rb != nil {
		o.hasBody = true
		o.bodyRequired = rb.Required != nil && *rb.Required
		o.body = contentFields(rb.Content)
	}
	if op.Responses != nil && op.Responses.Codes != nil {
		for c := op.Responses.Codes.First(); c != nil; c = c.Next() {
			var fields map[string]field
			if c.Value() != nil {
				fields = contentFields(c.Value().Content)
			}
			o.responses[c.Key()] = fields
		}
	}
	return o
}

func contentFields(content *orderedmap.Map[string, *v3.MediaType]) map[string]field {
	if content == nil {
		return map[string]field{}
	}
	var chosen *v3.MediaType
	for pair := content.First(); pair != nil; pair = pair.Next() {
		if chosen == nil || strings.Contains(pair.Key(), "json") {
			chosen = pair.Value()
			if strings.Contains(pair.Key(), "json") {
				break
			}
		}
	}
	if chosen == nil {
		return map[string]field{}
	}
	return schemaFields(chosen.Schema)
}

func schemaFields(proxy *base.SchemaProxy) map[string]field {
	out := map[string]field{}
	if proxy == nil {
		return out
	}
	s := proxy.Schema()
	if s == nil {
		return out
	}
	collect := func(sch *base.Schema) {
		required := map[string]bool{}
		for _, r := range sch.Required {
			required[r] = true
		}
		if sch.Properties == nil {
			return
		}
		for pair := sch.Properties.First(); pair != nil; pair = pair.Next() {
			f := field{required: required[pair.Key()]}
			if ps := pair.Value(); ps != nil {
				if sub := ps.Schema(); sub != nil {
					for _, n := range sub.Enum {
						f.enum = append(f.enum, n.Value)
					}
				}
			}
			out[pair.Key()] = f
		}
	}
	collect(s)
	for _, part := range s.AllOf {
		if part != nil {
			if sub := part.Schema(); sub != nil {
				collect(sub)
			}
		}
	}
	return out
}

func diffSpecs(path string, before, after map[string]operation) SpecDiff {
	d := SpecDiff{Path: path, Added: []OperationChange{}, Removed: []OperationChange{}, Changed: []OperationChange{}}
	for k, o := range after {
		if _, ok := before[k]; !ok {
			d.Added = append(d.Added, OperationChange{Method: o.method, Path: o.path, OperationID: o.operationID})
		}
	}
	for k, o := range before {
		if _, ok := after[k]; !ok {
			d.Removed = append(d.Removed, OperationChange{Method: o.method, Path: o.path, OperationID: o.operationID, Breaking: true})
			d.Breaking = true
		}
	}
	for k, b := range before {
		a, ok := after[k]
		if !ok {
			continue
		}
		changes, breaking := compareOperation(b, a)
		if len(changes) > 0 {
			d.Changed = append(d.Changed, OperationChange{Method: a.method, Path: a.path, OperationID: a.operationID, Changes: changes, Breaking: breaking})
			if breaking {
				d.Breaking = true
			}
		}
	}
	for _, list := range [][]OperationChange{d.Added, d.Removed, d.Changed} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].Method < list[j].Method
		})
	}
	return d
}

func compareOperation(b, a operation) ([]string, bool) {
	var changes []string
	breaking := false
	add := func(brk bool, format string, args ...any) {
		changes = append(changes, fmt.Sprintf(format, args...))
		if brk {
			breaking = true
		}
	}
	if strings.TrimSpace(b.summary) != strings.TrimSpace(a.summary) {
		add(false, "summary changed")
	}
	if !b.deprecated && a.deprecated {
		add(false, "deprecated")
	}
	for _, k := range sortedFieldKeys(a.params) {
		in, name, _ := strings.Cut(k, ":")
		bp, existed := b.params[k]
		ap := a.params[k]
		switch {
		case !existed && ap.required:
			add(true, "parameter %s (%s) added (required)", name, in)
		case !existed:
			add(false, "parameter %s (%s) added (optional)", name, in)
		default:
			if !bp.required && ap.required {
				add(true, "parameter %s (%s) is now required", name, in)
			}
			if bp.required && !ap.required {
				add(false, "parameter %s (%s) is now optional", name, in)
			}
			compareEnum(add, "parameter "+name, bp.enum, ap.enum)
		}
	}
	for _, k := range sortedFieldKeys(b.params) {
		if _, ok := a.params[k]; !ok {
			in, name, _ := strings.Cut(k, ":")
			add(true, "parameter %s (%s) removed", name, in)
		}
	}
	switch {
	case !b.hasBody && a.hasBody && a.bodyRequired:
		add(true, "requestBody added (required)")
	case !b.hasBody && a.hasBody:
		add(false, "requestBody added (optional)")
	case b.hasBody && !a.hasBody:
		add(true, "requestBody removed")
	}
	if b.hasBody && a.hasBody {
		for _, k := range sortedFieldKeys(a.body) {
			bf, existed := b.body[k]
			af := a.body[k]
			switch {
			case !existed && af.required:
				add(true, "requestBody.%s added (required)", k)
			case !existed:
				add(false, "requestBody.%s added (optional)", k)
			default:
				if !bf.required && af.required {
					add(true, "requestBody.%s is now required", k)
				}
				compareEnum(add, "requestBody."+k, bf.enum, af.enum)
			}
		}
		for _, k := range sortedFieldKeys(b.body) {
			if _, ok := a.body[k]; !ok {
				add(true, "requestBody.%s removed", k)
			}
		}
	}
	for _, code := range sortedRespKeys(a.responses) {
		if _, ok := b.responses[code]; !ok {
			add(false, "response %s added", code)
		}
	}
	for _, code := range sortedRespKeys(b.responses) {
		bf := b.responses[code]
		af, ok := a.responses[code]
		if !ok {
			add(true, "response %s removed", code)
			continue
		}
		for _, k := range sortedFieldKeys(af) {
			if _, existed := bf[k]; !existed {
				add(false, "response %s field %s added", code, k)
			}
		}
		for _, k := range sortedFieldKeys(bf) {
			if _, kept := af[k]; !kept {
				add(true, "response %s field %s removed", code, k)
				continue
			}
			compareEnum(add, "response "+code+" field "+k, bf[k].enum, af[k].enum)
		}
	}
	return changes, breaking
}

func compareEnum(add func(bool, string, ...any), label string, before, after []string) {
	if len(before) == 0 && len(after) == 0 {
		return
	}
	if len(before) == 0 {
		add(true, "%s enum added (%s)", label, strings.Join(after, ", "))
		return
	}
	if len(after) == 0 {
		add(false, "%s enum removed", label)
		return
	}
	have := map[string]bool{}
	for _, v := range after {
		have[v] = true
	}
	var dropped []string
	for _, v := range before {
		if !have[v] {
			dropped = append(dropped, v)
		}
	}
	had := map[string]bool{}
	for _, v := range before {
		had[v] = true
	}
	var added []string
	for _, v := range after {
		if !had[v] {
			added = append(added, v)
		}
	}
	if len(dropped) > 0 {
		add(true, "%s enum narrowed (removed %s)", label, strings.Join(dropped, ", "))
	}
	if len(added) > 0 {
		add(false, "%s enum widened (added %s)", label, strings.Join(added, ", "))
	}
}

func sortedFieldKeys(m map[string]field) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedRespKeys(m map[string]map[string]field) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DiffOpenAPI compares two versions of an OpenAPI document; nil content means the document does not exist on that side.
func DiffOpenAPI(path string, before, after []byte) SpecDiff {
	parse := func(data []byte) (map[string]operation, error) {
		if data == nil {
			return map[string]operation{}, nil
		}
		return parseOperations(data)
	}
	b, err := parse(before)
	if err != nil {
		return SpecDiff{Path: path, Added: []OperationChange{}, Removed: []OperationChange{}, Changed: []OperationChange{}, Error: "base: " + err.Error()}
	}
	a, err := parse(after)
	if err != nil {
		return SpecDiff{Path: path, Added: []OperationChange{}, Removed: []OperationChange{}, Changed: []OperationChange{}, Error: "head: " + err.Error()}
	}
	return diffSpecs(path, b, a)
}

// OperationKeys lists the "METHOD path" pairs of an OpenAPI document.
func OperationKeys(data []byte) ([][2]string, error) {
	ops, err := parseOperations(data)
	if err != nil {
		return nil, err
	}
	out := make([][2]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, [2]string{o.method, o.path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][1] != out[j][1] {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out, nil
}
