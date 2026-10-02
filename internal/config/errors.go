package config

import (
	"errors"
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

// ErrV1Manifest is matched by errors.Is for a v1 manifest.
var ErrV1Manifest = errors.New("v1 manifest")

// V1Error reports a v1 manifest, which CLI 1.x refuses outside of init.
type V1Error struct {
	File string
}

func (e *V1Error) Error() string {
	name := e.File
	if name == "" {
		name = ManifestFileName
	}
	return fmt.Sprintf("%s is a v1 manifest. Run `gravity init` to convert it (or keep using gravity v0.3)", name)
}

// Is matches ErrV1Manifest.
func (e *V1Error) Is(target error) bool { return target == ErrV1Manifest }

var v1Keys = []string{"site", "space", "spaces", "sources", "documents", "releaseNotes", "knowledge", "discovery", "i18n", "coverage"}

// IsV1 reports whether a decoded manifest is a v1 file.
func IsV1(root map[string]any) bool {
	v, ok := root["version"]
	if !ok || v == nil {
		return true
	}
	switch n := v.(type) {
	case int:
		if n == 1 {
			return true
		}
	case float64:
		if n == 1 {
			return true
		}
	}
	for _, k := range v1Keys {
		if _, ok := root[k]; ok {
			return true
		}
	}
	return false
}
