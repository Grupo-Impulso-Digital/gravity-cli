package docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
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

func TestAssembleBlocksTableCanonicalized(t *testing.T) {
	in := []docs.AuthoredBlock{
		{Key: "t", Type: "table", Content: raw(t, map[string]any{
			"header": []string{"Flag", "Default"},
			"rows":   []any{[]any{"--require", false}, []any{"--max", 42}},
		})},
	}
	blocks, err := docs.AssembleBlocks(t.TempDir(), "gen", in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	m, ok := blocks[0].Content.(map[string]any)
	if !ok {
		t.Fatalf("content type = %T", blocks[0].Content)
	}
	if hdr, ok := m["header"].(bool); !ok || !hdr {
		t.Errorf("header = %v, want true (folded)", m["header"])
	}
	rows, ok := m["rows"].([][]string)
	if !ok {
		t.Fatalf("rows type = %T", m["rows"])
	}
	want := [][]string{{"Flag", "Default"}, {"--require", "false"}, {"--max", "42"}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i := range want {
		for j := range want[i] {
			if rows[i][j] != want[i][j] {
				t.Errorf("rows[%d][%d] = %q, want %q", i, j, rows[i][j], want[i][j])
			}
		}
	}
}

func TestAssembleBlocksMachineNarrativeDowngraded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []docs.AuthoredBlock{
		{
			Key: "t", Type: "table", Ownership: "machine", Sources: []string{"root.go"},
			Content: raw(t, map[string]any{"rows": [][]string{{"Command", "Purpose"}}, "header": true}),
		},
		{
			Key: "p", Type: "prose", Ownership: "machine", Sources: []string{"root.go"},
			Content: raw(t, map[string]any{"text": "Narrative."}),
		},
		{
			Key: "c", Type: "code", Ownership: "machine", Sources: []string{"root.go"},
			Content: raw(t, map[string]any{"text": "package cli", "language": "go"}),
		},
	}
	blocks, err := docs.AssembleBlocks(dir, "gen", in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, b := range blocks[:2] {
		if b.Ownership != "hybrid" {
			t.Errorf("block %q ownership = %q, want hybrid (downgraded)", b.Key, b.Ownership)
		}
		if b.SourceBinding != nil {
			t.Errorf("block %q: downgraded narrative must be unbound, got %+v", b.Key, b.SourceBinding)
		}
	}
	if blocks[2].Ownership != "machine" || blocks[2].SourceBinding == nil {
		t.Errorf("code block should stay machine + bound, got ownership=%q binding=%+v",
			blocks[2].Ownership, blocks[2].SourceBinding)
	}
}

func TestSanitizeBlockReplay(t *testing.T) {
	b := api.BlockInput{
		Key: "t", Type: "table", Ownership: "machine",
		SourceBinding: &api.SourceBinding{Kind: "cli", Ref: "root.go"},
		Content: map[string]any{
			"header": []any{"A", "B"},
			"rows":   []any{[]any{"1", "2"}},
		},
	}
	if err := docs.SanitizeBlock(&b); err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if b.Ownership != "hybrid" || b.SourceBinding != nil {
		t.Errorf("ownership=%q binding=%+v, want hybrid + unbound", b.Ownership, b.SourceBinding)
	}
	m := b.Content.(map[string]any)
	rows := m["rows"].([][]string)
	if len(rows) != 2 || rows[0][0] != "A" || rows[1][1] != "2" {
		t.Errorf("rows = %v, want header folded first", rows)
	}
	if hdr, _ := m["header"].(bool); !hdr {
		t.Errorf("header = %v, want true", m["header"])
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

func TestAssembleBlocksNarrativeUnbound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, own := range []string{"hybrid", "human", ""} {
		in := []docs.AuthoredBlock{
			{Key: "p", Type: "prose", Ownership: own, Sources: []string{"main.go"}, Content: raw(t, map[string]any{"text": "AI prose"})},
		}
		blocks, err := docs.AssembleBlocks(dir, "gen", in)
		if err != nil {
			t.Fatalf("assemble (ownership=%q): %v", own, err)
		}
		if blocks[0].SourceBinding != nil {
			t.Errorf("ownership=%q: narrative must be unbound, got %+v", own, blocks[0].SourceBinding)
		}
	}
}

func TestAssembleBlocksUnresolvableMachineSourceDropsBinding(t *testing.T) {
	in := []docs.AuthoredBlock{
		{Key: "c", Type: "code", Ownership: "machine", Sources: []string{"does/not/exist.go"}, Content: raw(t, map[string]any{"text": "x", "language": "go"})},
	}
	blocks, err := docs.AssembleBlocks(t.TempDir(), "gen", in)
	if err != nil {
		t.Fatalf("assemble should not fail on an unresolvable source: %v", err)
	}
	if blocks[0].SourceBinding != nil {
		t.Errorf("expected no binding for an unresolvable machine source, got %+v", blocks[0].SourceBinding)
	}
}

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
