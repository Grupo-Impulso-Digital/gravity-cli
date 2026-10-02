package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestAPIBlocksCarryEndpointBindingsAndUnits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "openapi.yaml", specV3)

	blocks, err := docs.APIBlocks(dir, "openapi.yaml", "gravity-cli/test")
	if err != nil {
		t.Fatalf("APIBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 api blocks, got %d", len(blocks))
	}
	want := map[string]string{
		"api:GET:/users":      "api:get:/users",
		"api:GET:/users/{id}": "api:get:/users/:id",
	}
	for _, b := range blocks {
		if b.Type != "api" || b.Ownership != "machine" {
			t.Errorf("block %s: type/ownership = %s/%s", b.Key, b.Type, b.Ownership)
		}
		unit, ok := want[b.Key]
		if !ok || len(b.Units) != 1 || b.Units[0] != unit {
			t.Errorf("block %s units = %v", b.Key, b.Units)
		}
		if b.SourceBinding == nil || b.SourceBinding.Kind != "endpoint" || !strings.HasPrefix(b.SourceBinding.Hash, "sha256:") {
			t.Fatalf("block %s binding = %+v", b.Key, b.SourceBinding)
		}
	}
	again, err := docs.APIBlocks(dir, "openapi.yaml", "gravity-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].SourceBinding.Hash != blocks[0].SourceBinding.Hash {
		t.Error("endpoint hashes are not deterministic")
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
	if types["heading"] != 1 {
		t.Errorf("want exactly 1 heading block (title H1 not duplicated), got %d", types["heading"])
	}
	if types["table"] != 1 {
		t.Errorf("want 1 table block, got %d", types["table"])
	}
	if types["code"] != 1 {
		t.Errorf("want 1 code block, got %d", types["code"])
	}
	foundList := false
	for _, p := range proseTexts {
		if strings.Contains(p, "- a list item") {
			foundList = true
		}
	}
	if !foundList {
		t.Errorf("list markers not preserved verbatim in prose; got prose blocks: %v", proseTexts)
	}

	hash, err := docs.FileHash(dir, "guide.md")
	if err != nil || blocks[0].SourceBinding.Hash != hash || blocks[0].SourceBinding.Kind != "file" {
		t.Errorf("doc binding = %+v, file hash %s (%v)", blocks[0].SourceBinding, hash, err)
	}
}

func TestMarkdownPage_DefaultsToHumanAndDropsTitleBlock(t *testing.T) {
	dir := t.TempDir()
	md := "# Overview\n\nWelcome.\n\n## Details\n\nMore text.\n"
	writeFile(t, dir, "guide.md", md)

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
		if a[i].Key != b[i].Key || a[i].Type != b[i].Type || a[i].SourceBinding.Hash != b[i].SourceBinding.Hash {
			t.Errorf("block %d differs across runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestMarkdownPage_HeadingLevelClamp(t *testing.T) {
	dir := t.TempDir()
	md := "# Title\n\n## Two\n\nbody\n\n#### Four\n\nbody\n\n###### Six\n\nbody\n"
	writeFile(t, dir, "deep.md", md)

	blocks, _, err := docs.MarkdownPage(dir, "deep.md", "human", "test")
	if err != nil {
		t.Fatalf("MarkdownPage: %v", err)
	}
	var levels []int
	for _, b := range blocks {
		if b.Type != "heading" {
			continue
		}
		m, ok := b.Content.(map[string]any)
		if !ok {
			t.Fatalf("heading content type %T", b.Content)
		}
		levels = append(levels, m["level"].(int))
	}
	want := []int{2, 3, 3}
	if len(levels) != len(want) {
		t.Fatalf("heading levels = %v, want %v", levels, want)
	}
	for i, lv := range levels {
		if lv != want[i] {
			t.Errorf("heading %d level = %d, want %d (platform accepts only 1-3)", i, lv, want[i])
		}
	}
}
