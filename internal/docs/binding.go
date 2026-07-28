// Package docs builds the blocks that `gravity sync` authors onto the Gravity docs platform.
package docs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// BuildBinding builds a drift-verifiable SourceBinding for a machine block.
func BuildBinding(repoRoot, ref, kind, generator string) (*api.SourceBinding, error) {
	hash, err := checks.HashRepoFile(repoRoot, ref)
	if err != nil {
		return nil, fmt.Errorf("hash %q: %w", ref, err)
	}
	return &api.SourceBinding{
		Kind:      kind,
		Ref:       ref,
		Hash:      "sha256:" + hash,
		Generator: generator,
	}, nil
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
	return os.ReadFile(filepath.Join(repoRoot, clean))
}
