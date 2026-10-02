package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

var removedKeys = map[string]string{
	"sources[].generator": "was removed in v0.3 (it was never read); delete it",
	"knowledge.scope":     "was removed in v0.3 (the platform never used it); delete it",
}

// ParseProject strictly decodes a manifest: unknown keys fail with a suggestion, removed keys with a clear message.
func ParseProject(data []byte, path string) (*Project, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if problems := checkKeys(&doc, false); len(problems) > 0 {
		return nil, fmt.Errorf("%s:\n  - %s", path, strings.Join(problems, "\n  - "))
	}
	var p Project
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &p, nil
}

// ParseProjectForMigration decodes a manifest without applying defaults, dropping removed keys and reporting each one.
func ParseProjectForMigration(data []byte, path string) (*Project, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var dropped []string
	stripRemoved(&doc, projectType, "", &dropped)
	if problems := checkKeys(&doc, true); len(problems) > 0 {
		return nil, nil, fmt.Errorf("%s:\n  - %s", path, strings.Join(problems, "\n  - "))
	}
	var p Project
	if len(doc.Content) > 0 {
		if err := doc.Decode(&p); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return &p, dropped, nil
}

var projectType = reflect.TypeOf(Project{})

func checkKeys(doc *yaml.Node, skipRemoved bool) []string {
	if len(doc.Content) == 0 {
		return nil
	}
	var problems []string
	walkKeys(doc.Content[0], projectType, "", "", skipRemoved, &problems)
	return problems
}

func walkKeys(node *yaml.Node, t reflect.Type, path, pattern string, skipRemoved bool, problems *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return
		}
		fields := yamlFields(t)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			name := key.Value
			childPath := joinPath(path, name)
			childPattern := joinPath(pattern, name)
			ft, ok := fields[name]
			if !ok {
				if msg, removed := removedKeys[childPattern]; removed {
					if !skipRemoved {
						*problems = append(*problems, fmt.Sprintf("line %d: %s %s", key.Line, childPath, msg))
					}
					continue
				}
				*problems = append(*problems, fmt.Sprintf("line %d: unknown key %q in %s%s", key.Line, childPath, describeLevel(path), suggest(name, fields)))
				continue
			}
			walkKeys(val, ft, childPath, childPattern, skipRemoved, problems)
		}
	case reflect.Slice:
		if node.Kind != yaml.SequenceNode {
			return
		}
		for i, item := range node.Content {
			walkKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i), pattern+"[]", skipRemoved, problems)
		}
	}
}

func stripRemoved(node *yaml.Node, t reflect.Type, pattern string, dropped *[]string) {
	if node.Kind == yaml.DocumentNode {
		for _, c := range node.Content {
			stripRemoved(c, t, pattern, dropped)
		}
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return
		}
		fields := yamlFields(t)
		kept := make([]*yaml.Node, 0, len(node.Content))
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			childPattern := joinPath(pattern, key.Value)
			if _, removed := removedKeys[childPattern]; removed {
				if _, known := fields[key.Value]; !known {
					*dropped = append(*dropped, fmt.Sprintf("dropped %s (line %d): it %s", childPattern, key.Line, strings.TrimSuffix(removedKeys[childPattern], "; delete it")))
					continue
				}
			}
			if ft, ok := fields[key.Value]; ok {
				stripRemoved(val, ft, childPattern, dropped)
			}
			kept = append(kept, key, val)
		}
		node.Content = kept
	case reflect.Slice:
		if node.Kind != yaml.SequenceNode {
			return
		}
		for _, item := range node.Content {
			stripRemoved(item, t.Elem(), pattern+"[]", dropped)
		}
	}
}

func yamlFields(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		out[tag] = f.Type
	}
	return out
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func describeLevel(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}

func suggest(name string, fields map[string]reflect.Type) string {
	best, bestDist := "", 0
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lower := strings.ToLower(name)
	for _, k := range keys {
		d := levenshtein(lower, strings.ToLower(k))
		if best == "" || d < bestDist {
			best, bestDist = k, d
		}
	}
	limit := 2
	if len(name) > 8 {
		limit = 3
	}
	if best != "" && bestDist <= limit {
		return fmt.Sprintf(" — did you mean %q?", best)
	}
	return fmt.Sprintf(" (valid keys: %s)", strings.Join(keys, ", "))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
