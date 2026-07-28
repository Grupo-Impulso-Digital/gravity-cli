// Package pathsafe validates that caller-supplied paths stay within a repository root.
package pathsafe

import (
	"errors"
	"path/filepath"
	"strings"
)

// Sentinel errors returned by Rel and Resolve.
var (
	ErrAbsolute = errors.New("absolute path")
	ErrEscape   = errors.New("path escapes repository root")
)

// Rel cleans p and verifies it does not escape the repository root.
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

// Resolve validates p with Rel and joins it onto root.
func Resolve(root, p string) (string, error) {
	clean, err := Rel(p)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, clean), nil
}
