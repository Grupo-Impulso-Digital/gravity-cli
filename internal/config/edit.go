package config

import (
	"bytes"
	"fmt"

	yaml "go.yaml.in/yaml/v3"
)

func documentOf(data []byte) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, nil, fmt.Errorf("parse manifest: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("the manifest must be a YAML mapping")
	}
	return &doc, doc.Content[0], nil
}

func valueNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	flowScalarLists(&n)
	return &n, nil
}

func encodeDoc(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// SetKey sets a top-level key of manifest YAML, keeping the other keys and their comments.
func SetKey(data []byte, key string, value any) ([]byte, error) {
	doc, root, err := documentOf(data)
	if err != nil {
		return nil, err
	}
	n, err := valueNode(value)
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			n.HeadComment = root.Content[i+1].HeadComment
			root.Content[i+1] = n
			return encodeDoc(doc)
		}
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, n)
	return encodeDoc(doc)
}

// UpsertPass adds a pass to manifest YAML, or replaces the pass with the same name.
func UpsertPass(data []byte, p Pass) ([]byte, error) {
	doc, root, err := documentOf(data)
	if err != nil {
		return nil, err
	}
	n, err := valueNode(p)
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "passes" {
			continue
		}
		list := root.Content[i+1]
		if list.Kind != yaml.SequenceNode {
			list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			root.Content[i+1] = list
		}
		list.Style = 0
		for j, item := range list.Content {
			if passName(item) == p.Name {
				list.Content[j] = n
				return encodeDoc(doc)
			}
		}
		list.Content = append(list.Content, n)
		return encodeDoc(doc)
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "passes"}, &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{n}})
	return encodeDoc(doc)
}

func passName(n *yaml.Node) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "name" {
			return n.Content[i+1].Value
		}
	}
	return ""
}
