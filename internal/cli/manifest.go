package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

const manifestHeader = `# .gravity.yaml — committed, non-secret config for the Gravity docs platform.
# NEVER put a token here: use the GRAVITY_TOKEN env var (CI) or ` + "`gravity auth login`" + ` (local).
`

var manifestSectionComments = map[string]string{
	"product":      "Product / multi-repo identity: repo is this repo's unique name within the product.",
	"spaces":       "Where this repo's pages live.",
	"sources":      "OpenAPI specs -> machine-owned api blocks, authored by `gravity sync`.",
	"documents":    "Markdown files -> pages or releases, authored by `gravity sync`.\nownership: human (editable in Gravity, default) | machine (drift-locked mirror) | hybrid",
	"releaseNotes": "Release notes from git history (`gravity release-notes`).",
	"knowledge":    "Nucleus memory namespace shared across a product's repos.",
	"discovery":    "How `gravity docs generate` surveys the repo.",
	"i18n":         "Languages this repo's pages should exist in.",
	"coverage":     "Documentation-coverage bar (`gravity coverage`).",
}

const commentedSourcesExample = `# sources:                          # OpenAPI specs -> api blocks
#   - source: openapi/openapi.yaml   # repo-relative path to the spec
#     kind: openapi
#     page: api-reference
#     title: API Reference`

const commentedDocumentsExample = `# documents:                        # Markdown -> pages or releases
#   - file: docs/getting-started.md
#     page: getting-started
#     ownership: human               # human | machine | hybrid
#     as: page                       # page | release`

const commentedReleaseNotesExample = `# releaseNotes:                     # defaults shown
#   space: changelog
#   changelog: CHANGELOG.md`

func renderManifest(p *config.Project) string {
	var parts []string
	v := reflect.ValueOf(*p)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		val, ok := valueNode(v.Field(i))
		if !ok {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		if c := manifestSectionComments[key]; c != "" {
			keyNode.HeadComment = c
		}
		parts = append(parts, encodeMapping(keyNode, val))
	}
	if len(p.Sources) == 0 {
		parts = append(parts, commentedSourcesExample+"\n")
	}
	if len(p.Documents) == 0 {
		parts = append(parts, commentedDocumentsExample+"\n")
	}
	if p.ReleaseNotes == (config.ReleaseNotes{}) {
		parts = append(parts, commentedReleaseNotesExample+"\n")
	}
	return manifestHeader + strings.Join(parts, "\n")
}

func encodeMapping(key, val *yaml.Node) string {
	doc := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{key, val}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Sprintf("# could not render %s: %v\n", key.Value, err)
	}
	_ = enc.Close()
	return buf.String()
}

func valueNode(v reflect.Value) (*yaml.Node, bool) {
	switch v.Kind() {
	case reflect.Struct:
		node := &yaml.Node{Kind: yaml.MappingNode}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			key := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
			if key == "" || key == "-" {
				continue
			}
			child, ok := valueNode(v.Field(i))
			if !ok {
				continue
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		return node, len(node.Content) > 0
	case reflect.Slice:
		if v.Len() == 0 {
			return nil, false
		}
		node := &yaml.Node{Kind: yaml.SequenceNode}
		if v.Type().Elem().Kind() == reflect.String {
			node.Style = yaml.FlowStyle
		}
		for i := 0; i < v.Len(); i++ {
			child, ok := valueNode(v.Index(i))
			if !ok {
				child = &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
				if v.Index(i).Kind() == reflect.String {
					child = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
				}
			}
			node.Content = append(node.Content, child)
		}
		return node, true
	case reflect.String:
		if v.String() == "" {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v.String()}, true
	case reflect.Int, reflect.Int64, reflect.Int32:
		if v.Int() == 0 {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(v.Int(), 10)}, true
	case reflect.Float64, reflect.Float32:
		if v.Float() == 0 {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(v.Float(), 'f', -1, 64)}, true
	case reflect.Bool:
		if !v.Bool() {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}, true
	}
	return nil, false
}
