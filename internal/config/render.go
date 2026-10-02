package config

import (
	"bytes"
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// VerbatimFile is one options.files[] entry of a verbatim pass, in manifest key order.
type VerbatimFile struct {
	Include     string   `yaml:"include" json:"include"`
	Exclude     []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Collection  string   `yaml:"collection,omitempty" json:"collection,omitempty"`
	StripPrefix string   `yaml:"stripPrefix,omitempty" json:"stripPrefix,omitempty"`
	Slug        string   `yaml:"slug,omitempty" json:"slug,omitempty"`
	Title       string   `yaml:"title,omitempty" json:"title,omitempty"`
}

// ReferenceSource is one options.sources[] entry of a reference pass, in manifest key order.
type ReferenceSource struct {
	Path       string `yaml:"path" json:"path"`
	Page       string `yaml:"page,omitempty" json:"page,omitempty"`
	Title      string `yaml:"title,omitempty" json:"title,omitempty"`
	Collection string `yaml:"collection,omitempty" json:"collection,omitempty"`
}

// Render writes a manifest as YAML with two-space indentation.
func Render(m *Manifest) ([]byte, error) {
	return encode(m)
}

// RenderPasses writes a top-level passes: block for appending to an existing manifest.
func RenderPasses(passes []Pass) ([]byte, error) {
	if len(passes) == 0 {
		return nil, nil
	}
	return encode(struct {
		Passes []Pass `yaml:"passes"`
	}{passes})
}

func encode(v any) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	flowScalarLists(&node)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	return buf.Bytes(), nil
}

func flowScalarLists(n *yaml.Node) {
	if n.Kind == yaml.SequenceNode && len(n.Content) > 0 {
		width := 0
		flat := true
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode || strings.Contains(c.Value, "\n") {
				flat = false
				break
			}
			width += len(c.Value) + 2
		}
		if flat && width <= 72 {
			n.Style = yaml.FlowStyle
			return
		}
	}
	for _, c := range n.Content {
		flowScalarLists(c)
	}
}
