// Package docs converts repository sources (OpenAPI documents, Markdown files) into Gravity blocks.
package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// FileHash returns "sha256:<hex>" of a repository file.
func FileHash(repoRoot, ref string) (string, error) {
	data, err := readRepoFile(repoRoot, ref)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// FileBinding builds a file source binding for a block generated from ref.
func FileBinding(repoRoot, ref, anchor, generator string) (*api.SourceBinding, error) {
	hash, err := FileHash(repoRoot, ref)
	if err != nil {
		return nil, fmt.Errorf("hash %q: %w", ref, err)
	}
	full := ref
	if anchor != "" {
		full += "#" + anchor
	}
	return &api.SourceBinding{Kind: "file", Ref: full, Hash: hash, Generator: generator}, nil
}

func readRepoFile(repoRoot, ref string) ([]byte, error) {
	clean, err := pathsafe.Rel(ref)
	switch {
	case errors.Is(err, pathsafe.ErrAbsolute):
		return nil, fmt.Errorf("%q is an absolute path; want a repo-relative file", ref)
	case errors.Is(err, pathsafe.ErrEscape):
		return nil, fmt.Errorf("%q escapes the repo root", ref)
	case err != nil:
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, clean))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", ref, err)
	}
	return data, nil
}
