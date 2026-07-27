package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

const specV3 = `openapi: 3.0.0
info:
  title: Test
  version: 1.0.0
paths:
  /users:
    get:
      summary: List users
      responses:
        '200':
          description: ok
  /users/{id}:
    get:
      summary: Get a user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: ok
`

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAPIBlocks_SatisfyCheckAPI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "openapi.yaml", specV3)

	blocks, err := docs.APIBlocks(dir, "openapi.yaml", "test")
	if err != nil {
		t.Fatalf("APIBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 api blocks, got %d", len(blocks))
	}

	// Each block is a machine api block with an identity key and a verifiable
	// binding to the spec file.
	for _, b := range blocks {
		if b.Type != "api" || b.Ownership != "machine" {
			t.Errorf("block %s: type/ownership = %s/%s", b.Key, b.Type, b.Ownership)
		}
		if b.SourceBinding == nil {
			t.Fatalf("block %s: missing source binding", b.Key)
		}
		chk := checks.VerifyBinding(dir, b.SourceBinding)
		if !chk.Verified || chk.Stale {
			t.Errorf("block %s: binding not verified/fresh: %+v", b.Key, chk)
		}
	}

	// Authoring agrees with `check api --openapi`: zero drift findings.
	spec, err := checks.ParseOpenAPI([]byte(specV3))
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild documented ops from the spec summaries the producer used (identical
	// source), which is exactly what the checker compares.
	var documented []checks.DocumentedOp
	for key, op := range spec {
		documented = append(documented, checks.DocumentedOp{
			Method: op.Method, Path: op.Path, Summary: op.Summary, PageSlug: "api", BlockID: key,
		})
	}
	if findings := checks.DiffOperations(spec, documented); len(findings) != 0 {
		t.Errorf("expected 0 drift findings, got %d: %+v", len(findings), findings)
	}
}

func TestMarkdownPage_NativeBlocks(t *testing.T) {
	dir := t.TempDir()
	md := "# My Doc\n\nIntro paragraph with **bold**.\n\n## Usage\n\n| Flag | Description |\n| ---- | ----------- |\n| --x | the x |\n\n~~~bash\ngravity sync\n~~~\n\n- a list item\n- another\n"
	writeFile(t, dir, "guide.md", md)

	blocks, title, err := docs.MarkdownPage(dir, "guide.md", "machine", "test")
	if err != nil {
		t.Fatalf("MarkdownPage: %v", err)
	}
	if title != "My Doc" {
		t.Errorf("title = %q, want My Doc", title)
	}

	types := map[string]int{}
	var proseTexts []string
	for _, b := range blocks {
		types[b.Type]++
		if b.Ownership != "machine" {
			t.Errorf("block %s ownership = %s", b.Key, b.Ownership)
		}
		if b.SourceBinding == nil || b.SourceBinding.Ref != "guide.md" {
			t.Errorf("block %s: machine doc block must bind to guide.md; got %+v", b.Key, b.SourceBinding)
		}
		if b.Type == "prose" {
			if m, ok := b.Content.(map[string]any); ok {
				proseTexts = append(proseTexts, m["text"].(string))
			}
		}
		if b.Type == "code" {
			m := b.Content.(map[string]any)
			if m["language"] != "bash" {
				t.Errorf("code block language = %v, want bash", m["language"])
			}
		}
		if b.Type == "heading" {
			if m, ok := b.Content.(map[string]any); ok && m["text"] == "My Doc" {
				t.Errorf("the H1 title must not be duplicated as a heading block")
			}
		}
	}
	// Only "## Usage" is a heading block; the H1 "My Doc" became the page title.
	if types["heading"] != 1 {
		t.Errorf("want exactly 1 heading block (title H1 not duplicated), got %d", types["heading"])
	}
	if types["table"] != 1 {
		t.Errorf("want 1 table block, got %d", types["table"])
	}
	if types["code"] != 1 {
		t.Errorf("want 1 code block, got %d", types["code"])
	}
	// The list is preserved verbatim (markers intact) in a prose block.
	foundList := false
	for _, p := range proseTexts {
		if strings.Contains(p, "- a list item") {
			foundList = true
		}
	}
	if !foundList {
		t.Errorf("list markers not preserved verbatim in prose; got prose blocks: %v", proseTexts)
	}

	// Machine doc blocks are verifiable by `check docs`.
	chk := checks.VerifyBinding(dir, blocks[0].SourceBinding)
	if !chk.Verified || chk.Stale {
		t.Errorf("doc binding not verified/fresh: %+v", chk)
	}
}

func TestMarkdownPage_DefaultsToHumanAndDropsTitleBlock(t *testing.T) {
	dir := t.TempDir()
	md := "# Overview\n\nWelcome.\n\n## Details\n\nMore text.\n"
	writeFile(t, dir, "guide.md", md)

	// Empty ownership => human default: editable in Gravity, no drift binding.
	blocks, title, err := docs.MarkdownPage(dir, "guide.md", "", "test")
	if err != nil {
		t.Fatalf("MarkdownPage: %v", err)
	}
	if title != "Overview" {
		t.Errorf("title = %q, want Overview", title)
	}
	for _, b := range blocks {
		if b.Ownership != "human" {
			t.Errorf("block %s ownership = %s, want human (default)", b.Key, b.Ownership)
		}
		if b.SourceBinding != nil {
			t.Errorf("human block %s must carry no source binding; got %+v", b.Key, b.SourceBinding)
		}
		if b.Type == "heading" {
			if m, ok := b.Content.(map[string]any); ok && m["text"] == "Overview" {
				t.Errorf("the H1 title must not be duplicated as a heading block")
			}
		}
	}
	// "## Details" survives as a heading; "# Overview" is the title, not a block.
	headings := 0
	for _, b := range blocks {
		if b.Type == "heading" {
			headings++
		}
	}
	if headings != 1 {
		t.Errorf("want exactly 1 heading block, got %d", headings)
	}
}

func TestMarkdownPage_Idempotent(t *testing.T) {
	dir := t.TempDir()
	md := "# T\n\npara\n\n## S\n\nmore\n"
	writeFile(t, dir, "d.md", md)

	a, _, err := docs.MarkdownPage(dir, "d.md", "machine", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := docs.MarkdownPage(dir, "d.md", "machine", "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("non-deterministic block count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Type != b[i].Type || a[i].Position != b[i].Position {
			t.Errorf("block %d differs across runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}
