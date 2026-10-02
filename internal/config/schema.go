package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

//go:embed schema/gravity.schema.json
var schemaJSON []byte

const schemaResource = "gravity.schema.json"

// SchemaJSON returns the embedded manifest JSON Schema.
func SchemaJSON() []byte {
	return bytes.Clone(schemaJSON)
}

type schemaSet struct {
	compiled *jsonschema.Schema
	raw      map[string]any
	defs     map[string]any
}

var loadSchema = sync.OnceValues(func() (*schemaSet, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaResource, doc); err != nil {
		return nil, fmt.Errorf("load embedded schema: %w", err)
	}
	sch, err := c.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile embedded schema: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(schemaJSON, &raw); err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	defs, _ := raw["$defs"].(map[string]any)
	return &schemaSet{compiled: sch, raw: raw, defs: defs}, nil
})

func (s *schemaSet) deref(node map[string]any) (map[string]any, string) {
	name := ""
	for range 8 {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node, name
		}
		name = strings.TrimPrefix(ref, "#/$defs/")
		next, ok := s.defs[name].(map[string]any)
		if !ok {
			return node, name
		}
		node = next
	}
	return node, name
}

func (s *schemaSet) optionsDef(passKind string) map[string]any {
	def, _ := s.defs[passKind+"Options"].(map[string]any)
	return def
}

func (s *schemaSet) validateSchema(jsonDoc []byte) []Issue {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonDoc))
	if err != nil {
		return []Issue{{Message: fmt.Sprintf("manifest is not valid JSON: %v", err)}}
	}
	err = s.compiled.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Issue{{Message: err.Error()}}
	}
	var issues []Issue
	collectLeaves(ve, &issues)
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Path < issues[j].Path })
	return issues
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]Issue) {
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			collectLeaves(c, out)
		}
		return
	}
	switch ve.ErrorKind.(type) {
	case *kind.AdditionalProperties, *kind.Not, *kind.Group, *kind.AllOf, *kind.AnyOf, *kind.Schema, *kind.Reference:
		return
	}
	leaf := &jsonschema.ValidationError{SchemaURL: ve.SchemaURL, InstanceLocation: ve.InstanceLocation, ErrorKind: ve.ErrorKind}
	msg := leaf.Error()
	if i := strings.Index(msg, "': "); i >= 0 {
		msg = msg[i+3:]
	}
	*out = append(*out, Issue{Path: formatPath(ve.InstanceLocation), Message: msg})
}

func formatPath(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		if _, err := strconv.Atoi(t); err == nil {
			b.WriteString("[" + t + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(t)
	}
	return b.String()
}
