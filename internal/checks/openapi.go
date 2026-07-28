// Package checks holds the deterministic check logic: OpenAPI operation diff
// for `check api --openapi`, and source-binding hash verification for the
// no-spec api check and `check docs`.
package checks

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi"
)

// errNoOpenAPIModel is returned when a document parses but neither the v3 nor
// the v2 model can be built from it.
var errNoOpenAPIModel = errors.New("unable to build an OpenAPI model from the document")

// Operation is a normalised (method, path) pair plus its summary.
type Operation struct {
	Method  string
	Path    string
	Summary string
}

// Key uniquely identifies an operation by method+path.
func (o Operation) Key() string {
	return strings.ToUpper(o.Method) + " " + o.Path
}

// ParseOpenAPI parses an OpenAPI document (2.0 or 3.x) from raw bytes and
// returns its operations keyed by method+path.
func ParseOpenAPI(spec []byte) (map[string]Operation, error) {
	doc, err := libopenapi.NewDocument(spec)
	if err != nil {
		return nil, fmt.Errorf("parse openapi document: %w", err)
	}

	ops := map[string]Operation{}

	// Try v3 first, then fall back to v2.
	if v3, err := doc.BuildV3Model(); err == nil && v3 != nil {
		if v3.Model.Paths != nil {
			for pair := v3.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
				path := pair.Key()
				item := pair.Value()
				for method, op := range item.GetOperations().FromOldest() {
					summary := ""
					if op != nil {
						summary = op.Summary
					}
					o := Operation{Method: strings.ToUpper(method), Path: path, Summary: summary}
					ops[o.Key()] = o
				}
			}
		}
		return ops, nil
	}

	if v2, err := doc.BuildV2Model(); err == nil && v2 != nil {
		if v2.Model.Paths != nil {
			for pair := v2.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
				path := pair.Key()
				item := pair.Value()
				for method, op := range item.GetOperations().FromOldest() {
					summary := ""
					if op != nil {
						summary = op.Summary
					}
					o := Operation{Method: strings.ToUpper(method), Path: path, Summary: summary}
					ops[o.Key()] = o
				}
			}
		}
		return ops, nil
	}

	return nil, errNoOpenAPIModel
}

// ParamDetail is one operation parameter captured for an authored api block.
type ParamDetail struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// ResponseDetail is one documented response code captured for an api block.
type ResponseDetail struct {
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
}

// OperationDetail is the full operation payload authored into a machine api
// block. Summary is read from the same model field as ParseOpenAPI, so an
// authored block satisfies `check api --openapi` by construction.
type OperationDetail struct {
	Method    string           `json:"method"`
	Path      string           `json:"path"`
	Summary   string           `json:"summary"`
	Params    []ParamDetail    `json:"params"`
	Responses []ResponseDetail `json:"responses"`
}

// ParseOpenAPIDetailed parses an OpenAPI document (2.0 or 3.x) and returns the
// full operation detail for each (method, path), sorted by path then method for
// deterministic, byte-stable authoring.
func ParseOpenAPIDetailed(spec []byte) ([]OperationDetail, error) {
	doc, err := libopenapi.NewDocument(spec)
	if err != nil {
		return nil, fmt.Errorf("parse openapi document: %w", err)
	}

	var out []OperationDetail

	if v3, err := doc.BuildV3Model(); err == nil && v3 != nil {
		if v3.Model.Paths != nil {
			for pair := v3.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
				path := pair.Key()
				item := pair.Value()
				for method, op := range item.GetOperations().FromOldest() {
					d := OperationDetail{Method: strings.ToUpper(method), Path: path}
					if op != nil {
						d.Summary = op.Summary
						for _, p := range op.Parameters {
							if p == nil {
								continue
							}
							req := false
							if p.Required != nil {
								req = *p.Required
							}
							d.Params = append(d.Params, ParamDetail{Name: p.Name, In: p.In, Required: req, Description: p.Description})
						}
						if op.Responses != nil && op.Responses.Codes != nil {
							for c := op.Responses.Codes.First(); c != nil; c = c.Next() {
								desc := ""
								if c.Value() != nil {
									desc = c.Value().Description
								}
								d.Responses = append(d.Responses, ResponseDetail{Code: c.Key(), Description: desc})
							}
						}
					}
					out = append(out, d)
				}
			}
		}
		sortOperationDetails(out)
		return out, nil
	}

	if v2, err := doc.BuildV2Model(); err == nil && v2 != nil {
		if v2.Model.Paths != nil {
			for pair := v2.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
				path := pair.Key()
				item := pair.Value()
				for method, op := range item.GetOperations().FromOldest() {
					d := OperationDetail{Method: strings.ToUpper(method), Path: path}
					if op != nil {
						d.Summary = op.Summary
						for _, p := range op.Parameters {
							if p == nil {
								continue
							}
							req := false
							if p.Required != nil {
								req = *p.Required
							}
							d.Params = append(d.Params, ParamDetail{Name: p.Name, In: p.In, Required: req, Description: p.Description})
						}
						if op.Responses != nil && op.Responses.Codes != nil {
							for c := op.Responses.Codes.First(); c != nil; c = c.Next() {
								desc := ""
								if c.Value() != nil {
									desc = c.Value().Description
								}
								d.Responses = append(d.Responses, ResponseDetail{Code: c.Key(), Description: desc})
							}
						}
					}
					out = append(out, d)
				}
			}
		}
		sortOperationDetails(out)
		return out, nil
	}

	return nil, errNoOpenAPIModel
}

func sortOperationDetails(ops []OperationDetail) {
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
}

// DocumentedOp is one operation as captured in a Gravity api block.
type DocumentedOp struct {
	Method   string
	Path     string
	Summary  string
	PageSlug string
	BlockID  string
}

// Key uniquely identifies a documented operation by method+path.
func (d DocumentedOp) Key() string {
	return strings.ToUpper(d.Method) + " " + d.Path
}

// APIDiffFinding is a single drift finding from the operation diff.
type APIDiffFinding struct {
	// Kind is one of: undocumented, orphaned, changed.
	Kind     string
	Method   string
	Path     string
	Detail   string
	PageSlug string
}

// DiffOperations compares the spec operations against the documented blocks.
//   - undocumented: present in spec, absent from docs.
//   - orphaned: present in docs, absent from spec.
//   - changed: present in both but the summary differs.
//
// Results are returned sorted for deterministic output.
func DiffOperations(spec map[string]Operation, docs []DocumentedOp) []APIDiffFinding {
	docByKey := map[string]DocumentedOp{}
	for _, d := range docs {
		docByKey[d.Key()] = d
	}

	var findings []APIDiffFinding

	// Undocumented + changed.
	for key, op := range spec {
		doc, ok := docByKey[key]
		if !ok {
			findings = append(findings, APIDiffFinding{
				Kind:   "undocumented",
				Method: op.Method,
				Path:   op.Path,
				Detail: fmt.Sprintf("%s %s is in the OpenAPI spec but has no documented api block", op.Method, op.Path),
			})
			continue
		}
		if normalizeSummary(op.Summary) != normalizeSummary(doc.Summary) {
			findings = append(findings, APIDiffFinding{
				Kind:     "changed",
				Method:   op.Method,
				Path:     op.Path,
				PageSlug: doc.PageSlug,
				Detail: fmt.Sprintf("summary differs — spec: %q, docs: %q",
					strings.TrimSpace(op.Summary), strings.TrimSpace(doc.Summary)),
			})
		}
	}

	// Orphaned.
	for key, doc := range docByKey {
		if _, ok := spec[key]; !ok {
			findings = append(findings, APIDiffFinding{
				Kind:     "orphaned",
				Method:   doc.Method,
				Path:     doc.Path,
				PageSlug: doc.PageSlug,
				Detail:   fmt.Sprintf("%s %s is documented but not present in the OpenAPI spec", doc.Method, doc.Path),
			})
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Method < findings[j].Method
	})
	return findings
}

func normalizeSummary(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
