package docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/impulso/gravity-cli/internal/checks"
	"github.com/impulso/gravity-cli/internal/docs"
)

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAssembleBlocksValid(t *testing.T) {
	in := []docs.AuthoredBlock{
		{Key: "h", Type: "heading", Audiences: []string{"public"}, Content: raw(t, map[string]any{"text": "Overview", "level": 2})},
		{Key: "p", Type: "prose", Content: raw(t, map[string]any{"text": "Body"})},
		{Key: "t", Type: "table", Content: raw(t, map[string]any{"header": []string{"A"}, "rows": [][]string{{"1"}}})},
	}
	blocks, err := docs.AssembleBlocks(t.TempDir(), "gen", in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	// Ownership defaults to hybrid; positions are sequential.
	for i, b := range blocks {
		if b.Position != i {
			t.Errorf("block %d position = %d", i, b.Position)
		}
		if b.Ownership != "hybrid" {
			t.Errorf("block %q ownership = %q, want hybrid (default)", b.Key, b.Ownership)
		}
	}
	if len(blocks[0].Audiences) != 1 || blocks[0].Audiences[0] != "public" {
		t.Errorf("heading audiences = %v", blocks[0].Audiences)
	}
}

func TestAssembleBlocksRejects(t *testing.T) {
	cases := map[string][]docs.AuthoredBlock{
		"missing key":      {{Type: "prose", Content: raw(t, map[string]any{"text": "x"})}},
		"bad type":         {{Key: "a", Type: "diagram", Content: raw(t, map[string]any{"text": "x"})}},
		"bad ownership":    {{Key: "a", Type: "prose", Ownership: "robot", Content: raw(t, map[string]any{"text": "x"})}},
		"bad audience":     {{Key: "a", Type: "prose", Audiences: []string{"aliens"}, Content: raw(t, map[string]any{"text": "x"})}},
		"empty prose text": {{Key: "a", Type: "prose", Content: raw(t, map[string]any{"text": "  "})}},
		"duplicate key": {
			{Key: "a", Type: "prose", Content: raw(t, map[string]any{"text": "x"})},
			{Key: "a", Type: "prose", Content: raw(t, map[string]any{"text": "y"})},
		},
	}
	for name, in := range cases {
		if _, err := docs.AssembleBlocks(t.TempDir(), "gen", in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// AI-authored hybrid blocks carry a hash-less provenance binding, which the
// drift checker must report as skipped (not stale).
func TestAssembleBlocksAIBindingSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []docs.AuthoredBlock{
		{Key: "p", Type: "prose", Ownership: "hybrid", Sources: []string{"main.go"}, Content: raw(t, map[string]any{"text": "AI prose"})},
	}
	blocks, err := docs.AssembleBlocks(dir, "gen", in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	b := blocks[0]
	if b.SourceBinding == nil || b.SourceBinding.Kind != "ai" {
		t.Fatalf("expected ai provenance binding, got %+v", b.SourceBinding)
	}
	if b.SourceBinding.Hash != "" {
		t.Errorf("ai binding should be hash-less, got %q", b.SourceBinding.Hash)
	}
	check := checks.VerifyBinding(dir, b.SourceBinding)
	if !check.Skipped || check.Stale {
		t.Errorf("expected skipped (not stale) for ai binding, got %+v", check)
	}
}

// A single-source machine block gets a real, verifiable sha256 binding.
func TestAssembleBlocksMachineBindingVerifies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []docs.AuthoredBlock{
		{Key: "c", Type: "code", Ownership: "machine", Sources: []string{"data.txt"}, Content: raw(t, map[string]any{"text": "hello", "language": "text"})},
	}
	blocks, err := docs.AssembleBlocks(dir, "gen", in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	check := checks.VerifyBinding(dir, blocks[0].SourceBinding)
	if !check.Verified || check.Stale {
		t.Errorf("expected verified, non-stale machine binding, got %+v", check)
	}
}
