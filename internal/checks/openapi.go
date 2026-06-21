// Package checks holds the deterministic check logic: OpenAPI operation diff
// for `check api --openapi`, and source-binding hash verification for the
// no-spec api check and `check docs`.
package checks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi"
)

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

	return nil, fmt.Errorf("unable to build an OpenAPI model from the document")
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
