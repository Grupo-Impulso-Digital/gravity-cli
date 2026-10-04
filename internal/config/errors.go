package config

import (
	"fmt"
	"strings"
)

// Issue is one manifest problem at a path such as passes[0].target.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Path == "" {
		return i.Message
	}
	return i.Path + ": " + i.Message
}

// ManifestError lists every problem found in a manifest.
type ManifestError struct {
	File   string
	Issues []Issue
}

func (e *ManifestError) Error() string {
	name := e.File
	if name == "" {
		name = ManifestFileName
	}
	if len(e.Issues) == 1 {
		return fmt.Sprintf("%s: %s", name, e.Issues[0])
	}
	lines := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		lines[i] = "  - " + is.String()
	}
	return fmt.Sprintf("%s has %d problems:\n%s", name, len(e.Issues), strings.Join(lines, "\n"))
}
