// Package docs builds the blocks that `gravity sync` authors onto the Gravity
// docs platform: machine-owned, code-derived api blocks from an OpenAPI spec,
// and Markdown documents decomposed into native blocks (human-owned by default,
// so they stay editable in Gravity, unless a mapping opts into machine). Every
// machine block is bound to a repo file via a SourceBinding whose hash is
// computed by the same hasher the drift checker (`check api`/`check docs`) uses,
// so authored machine blocks are verifiable by construction.
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

// BuildBinding builds a drift-verifiable SourceBinding for a machine block,
// hashing the repo-relative file at ref with the shared checks hasher.
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

// readRepoFile reads a repo-relative file, rejecting absolute paths and ".."
// escapes.
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
