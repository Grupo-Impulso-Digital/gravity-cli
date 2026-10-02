// Package pathsafe validates that caller-supplied paths stay within a repository root.
package pathsafe

import (
	"errors"
	"io/fs"
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

// ResolveInRoot resolves p like Resolve and refuses it when symlinks lead outside root; a missing path resolves to the joined path.
func ResolveInRoot(root, p string) (string, error) {
	full, err := Resolve(root, p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if errors.Is(err, fs.ErrNotExist) {
		return full, nil
	}
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrEscape
	}
	return resolved, nil
}
