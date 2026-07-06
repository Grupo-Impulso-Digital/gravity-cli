package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

// repoRootForTest resolves the CLI git toplevel the same way selfdoc does at
// runtime, so block sourceBindings hash against real repo files.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	repo, err := git.Open(context.Background(), ".")
	if err != nil {
		t.Fatalf("git.Open: %v", err)
	}
	return repo.Root
}

var validBlockTypes = map[string]bool{
	"heading": true,
	"prose":   true,
	"code":    true,
	"table":   true,
}

// TestBuildCommandReferenceBlocks exercises the deterministic machine-block
// builder against the real cobra tree.
func TestBuildCommandReferenceBlocks(t *testing.T) {
	root := NewRootCommand()
	repoRoot := repoRootForTest(t)

	blocks, err := buildCommandReferenceBlocks(root, repoRoot)
	if err != nil {
		t.Fatalf("buildCommandReferenceBlocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one reference block")
	}

	seenKeys := map[string]bool{}
	for i, b := range blocks {
		// Valid type.
		if !validBlockTypes[b.Type] {
			t.Errorf("block %d: invalid type %q", i, b.Type)
		}
		// Machine ownership.
		if b.Ownership != "machine" {
			t.Errorf("block %d: ownership = %q, want machine", i, b.Ownership)
		}
		// Stable, non-empty, unique key.
		if strings.TrimSpace(b.Key) == "" {
			t.Errorf("block %d: empty key", i)
		}
		if seenKeys[b.Key] {
			t.Errorf("block %d: duplicate key %q", i, b.Key)
		}
		seenKeys[b.Key] = true

		// Every machine block must carry a drift-verifiable cli binding.
		sb := b.SourceBinding
		if sb == nil {
			t.Fatalf("block %d (%s): missing sourceBinding", i, b.Key)
		}
		if sb.Kind != "cli" {
			t.Errorf("block %d: binding kind = %q, want cli", i, sb.Kind)
		}
		if strings.TrimSpace(sb.Ref) == "" {
			t.Errorf("block %d: empty binding ref", i)
		}
		if !strings.HasPrefix(sb.Generator, "gravity selfdoc") {
			t.Errorf("block %d: generator = %q, want prefix 'gravity selfdoc'", i, sb.Generator)
		}

		// Ref must be a REAL repo-relative file (exists, not a directory).
		abs := filepath.Join(repoRoot, sb.Ref)
		info, statErr := os.Stat(abs)
		if statErr != nil {
			t.Errorf("block %d: ref %q does not exist under repo root: %v", i, sb.Ref, statErr)
			continue
		}
		if info.IsDir() {
			t.Errorf("block %d: ref %q is a directory, not a file", i, sb.Ref)
			continue
		}

		// Hash must be sha256: + the actual hex digest of that file.
		want := "sha256:" + sha256Hex(t, abs)
		if sb.Hash != want {
			t.Errorf("block %d: hash = %q, want %q", i, sb.Hash, want)
		}
	}
}

// TestBuildOverviewBlocks asserts the overview block is hybrid prose with no
// sourceBinding (a human may freely edit it).
func TestBuildOverviewBlocks(t *testing.T) {
	blocks := buildOverviewBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected exactly 1 overview block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.Type != "prose" {
		t.Errorf("overview type = %q, want prose", b.Type)
	}
	if b.Ownership != "hybrid" {
		t.Errorf("overview ownership = %q, want hybrid", b.Ownership)
	}
	if b.SourceBinding != nil {
		t.Errorf("overview block must NOT have a sourceBinding, got %+v", b.SourceBinding)
	}
	if strings.TrimSpace(b.Key) == "" {
		t.Error("overview block has empty key")
	}
}

// TestBuildCommandReferenceBlocksIdempotent asserts generating twice from the
// same inputs yields identical blocks (same keys, same hashes) so re-runs match
// by key.
func TestBuildCommandReferenceBlocksIdempotent(t *testing.T) {
	repoRoot := repoRootForTest(t)

	first, err := buildCommandReferenceBlocks(NewRootCommand(), repoRoot)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	second, err := buildCommandReferenceBlocks(NewRootCommand(), repoRoot)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("block count drifted: %d vs %d", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.Key != b.Key {
			t.Errorf("block %d key drift: %q vs %q", i, a.Key, b.Key)
		}
		if a.Type != b.Type || a.Ownership != b.Ownership || a.Position != b.Position {
			t.Errorf("block %d shape drift: %+v vs %+v", i, a, b)
		}
		if (a.SourceBinding == nil) != (b.SourceBinding == nil) {
			t.Errorf("block %d binding presence drift", i)
			continue
		}
		if a.SourceBinding != nil {
			if a.SourceBinding.Ref != b.SourceBinding.Ref {
				t.Errorf("block %d ref drift: %q vs %q", i, a.SourceBinding.Ref, b.SourceBinding.Ref)
			}
			if a.SourceBinding.Hash != b.SourceBinding.Hash {
				t.Errorf("block %d hash drift: %q vs %q", i, a.SourceBinding.Hash, b.SourceBinding.Hash)
			}
		}
	}
}

// TestBuildCommandReferenceBlocksCoversExitCodes is a light smoke check that the
// exit-code contract block exists and binds to exit.go (a real file).
func TestBuildCommandReferenceBlocksExitGo(t *testing.T) {
	repoRoot := repoRootForTest(t)
	blocks, err := buildCommandReferenceBlocks(NewRootCommand(), repoRoot)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	foundExit := false
	for _, b := range blocks {
		if b.SourceBinding != nil && b.SourceBinding.Ref == "internal/cli/exit.go" {
			foundExit = true
		}
	}
	if !foundExit {
		t.Error("expected at least one block bound to internal/cli/exit.go")
	}
}

// TestHashRepoFile verifies hashRepoFile matches sha256 of the file's bytes and
// rejects "..", absolute paths, and directories — mirroring the drift checker.
func TestHashRepoFile(t *testing.T) {
	repoRoot := repoRootForTest(t)

	got, err := hashRepoFile(repoRoot, rootSourceRef)
	if err != nil {
		t.Fatalf("hashRepoFile(%q): %v", rootSourceRef, err)
	}
	want := sha256Hex(t, filepath.Join(repoRoot, rootSourceRef))
	if got != want {
		t.Errorf("hash = %q, want %q", got, want)
	}

	// Reject "..".
	if _, err := hashRepoFile(repoRoot, "../escape.go"); err == nil {
		t.Error("expected error for '..' ref")
	}

	// Reject absolute paths.
	if _, err := hashRepoFile(repoRoot, filepath.Join(repoRoot, rootSourceRef)); err == nil {
		t.Error("expected error for absolute ref")
	}

	// Reject directories.
	if _, err := hashRepoFile(repoRoot, "internal/cli"); err == nil {
		t.Error("expected error for directory ref")
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
