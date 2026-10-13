package docs

import (
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
)

type lineRange struct {
	start int
	end   int
}

func operationLines(spec []byte) map[string]lineRange {
	var root yaml.Node
	if err := yaml.Unmarshal(spec, &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	paths := mappingValue(root.Content[0], "paths")
	if paths == nil || paths.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]lineRange{}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		path, item := paths.Content[i], paths.Content[i+1]
		if item.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			key, op := item.Content[j], item.Content[j+1]
			out[strings.ToUpper(key.Value)+" "+path.Value] = lineRange{start: key.Line, end: max(lastLine(op), key.Line)}
		}
	}
	return out
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func lastLine(n *yaml.Node) int {
	last := n.Line
	if n.Kind == yaml.ScalarNode && (n.Style == yaml.LiteralStyle || n.Style == yaml.FoldedStyle) {
		last += strings.Count(strings.TrimRight(n.Value, "\n"), "\n") + 1
	}
	for _, c := range n.Content {
		last = max(last, lastLine(c))
	}
	return last
}

func locateOperations(spec []byte, ops []checks.OperationDetail) {
	lines := operationLines(spec)
	for i := range ops {
		if r, ok := lines[strings.ToUpper(ops[i].Method)+" "+ops[i].Path]; ok {
			ops[i].LineStart, ops[i].LineEnd = r.start, r.end
		}
	}
}
