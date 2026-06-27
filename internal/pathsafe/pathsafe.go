// Package pathsafe validates that caller-supplied paths stay within a
// repository root. It centralizes the "reject absolute paths and `..` escapes"
// rule shared by the agent tools, the drift checker, the config validator, and
// the docs authorer, so this security-sensitive check lives in exactly one
// place and cannot drift between call sites.
package pathsafe

import (
	"errors"
	"path/filepath"
	"strings"
)

// Sentinel errors returned by Rel and Resolve. Callers match these with
// errors.Is to map them onto their own context-specific messages.
var (
	ErrAbsolute = errors.New("absolute path")
	ErrEscape   = errors.New("path escapes repository root")
)

// Rel cleans p and verifies it does not escape the repository root, returning
// the cleaned, repo-relative path. An empty p cleans to "." and is allowed;
// callers that require a non-empty path enforce that themselves. It returns
// ErrAbsolute for absolute paths and ErrEscape for paths that resolve outside
// the root.
func Rel(p string) (string, error) {
	if filepath.IsAbs(p) {
		return "", ErrAbsolute
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrEscape
	}
	return clean, nil
}

// Resolve validates p with Rel and joins it onto root, returning the cleaned
// absolute path inside root.
func Resolve(root, p string) (string, error) {
	clean, err := Rel(p)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, clean), nil
}
