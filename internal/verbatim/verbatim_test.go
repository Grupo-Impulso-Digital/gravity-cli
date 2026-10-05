package verbatim_test

import (
	"encoding/json"
	"flag"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

var update = flag.Bool("update", false, "rewrite golden files")

type memSource map[string]string

func (m memSource) Files() ([]string, error) {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m memSource) Read(p string) ([]byte, bool, error) {
	v, ok := m[p]
	return []byte(v), ok, nil
}

const handbookIndex = `# Handbook

Welcome. Start with [on-call](oncall/rotation.md#Who-is-on-call) or read the [API spec](../../api/openapi.yaml).
`

const rotation = "---\ntitle: On-call rotation\ndescription: Who is on call and how to swap.\nsidebar_position: 2\naudiences: developers\n---\n" + `
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Intro paragraph with **bold**, _em_, ` + "`code`" + `, ~~gone~~ and a footnote[^swap].

## Who is on call

| Week | Person |
|------|--------|
| 1 | [Ana](../index.md) |
| 2 | Bo |

- [x] Page acknowledged
- [ ] Incident closed
  - nested detail

1. First
2. Second

:::warning[Escalation]
Call the **lead** after 15 minutes.
:::

> [!TIP]
> Swap shifts in the calendar.

> A plain quote.

!!! note "Paging"
    Pages go to the primary first.

??? danger "Outage"
    Follow the [runbook](../runbooks/outage.md).

<details>
<summary>Old process</summary>

We used a spreadsheet.
</details>

<Tabs>
  <TabItem value="mac" label="macOS">
    brew install pager
  </TabItem>
  <TabItem value="linux" label="Linux">
    apt install pager
  </TabItem>
</Tabs>

<Callout kind="x">
Text inside a custom component.
</Callout>

<Spacer />

` + "```mermaid\ngraph TD; A-->B\n```\n\n```go\nfmt.Println(\"hi\")\n```\n" + `
![Rotation chart](../../img/rotation.png "Weekly")

![Missing](./nope.png)

<div align="center"><strong>HTML</strong> block with <a href="rotation.md#who-is-on-call">a link</a><script>alert(1)</script></div>

---

Term
: Definition of the term.

### Deep heading
#### Deeper heading

[^swap]: Swaps need the lead's approval.
`

const outage = "# Outage runbook\n\nRestart the service.\n"

const hidden = "---\ndraft: true\n---\n# Draft\n"

const french = "---\nlang: fr\n---\n# Rotation\n"

func fixture() memSource {
	return memSource{
		"docs/handbook/index.md":               handbookIndex,
		"docs/handbook/oncall/rotation.mdx":    rotation,
		"docs/handbook/oncall/_category_.json": `{"label":"On-call"}`,
		"docs/handbook/runbooks/outage.md":     outage,
		"docs/handbook/runbooks/.pages":        "title: Runbooks & playbooks\n",
		"docs/handbook/drafts/wip.md":          hidden,
		"docs/handbook/oncall/rotation.fr.md":  french,
		"docs/handbook/a/b/c/d/e/f/deep.md":    "# Deep\n",
		"docs/handbook/runbooks/README.md":     "# Runbooks\n",
		"docs/img/rotation.png":                "PNG",
		"api/openapi.yaml":                     "openapi: 3.0.0",
		"docs/other/rotation.md":               "# Another rotation\n",
		"docs/handbook/oncall/skip-me.md":      "# Skipped\n",
	}
}

func TestMapAndConvertGolden(t *testing.T) {
	src := fixture()
	m, err := verbatim.Map(src, verbatim.MapOptions{
		Files: []verbatim.FileSpec{
			{Include: "docs/handbook/**", Exclude: []string{"**/skip-me.md"}},
			{Include: "docs/other/rotation.md", Slug: "rotation", Title: "Other rotation", Collection: "misc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := verbatim.Repo{WebURL: "https://github.com/acme/billing-api", Provider: "github", Branch: "main", Exists: func(p string) bool { _, ok := src[p]; return ok }}
	uploads := map[string]bool{}
	type converted struct {
		Page             verbatim.Page      `json:"page"`
		CollectionTitles map[string]string  `json:"collectionTitles"`
		Doc              *verbatim.Document `json:"doc"`
	}
	out := struct {
		Hidden   []string    `json:"hidden"`
		Warnings []string    `json:"warnings"`
		Pages    []converted `json:"pages"`
	}{Hidden: m.Hidden, Warnings: m.Warnings}
	for _, p := range m.Pages {
		doc := verbatim.Convert(p.Body, verbatim.Options{
			Path: p.Path, Hash: p.Hash, Generator: "gravity-cli/test", MDX: p.MDX, DropTitleH1: p.TitleFromH1, Title: p.Title, Audiences: p.FrontMatter.Audiences,
			Link: m.Linker(p.Path, "handbook", repo),
			Image: func(s string) (string, bool) {
				full := path.Clean(path.Join(path.Dir(p.Path), s))
				if _, ok := src[full]; !ok {
					return "", false
				}
				uploads[full] = true
				return "https://media.test/" + full, true
			},
		})
		out.Pages = append(out.Pages, converted{Page: p, CollectionTitles: m.TitlesFor(p), Doc: doc})
	}
	if !uploads["docs/img/rotation.png"] {
		t.Error("the relative image was not uploaded")
	}
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "handbook.golden.json")
	if *update {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run go test -update): %v", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(got)) {
		t.Fatalf("golden mismatch; run go test ./internal/verbatim -update and review\n%s", got)
	}
}

func TestConvertIsDeterministic(t *testing.T) {
	a := verbatim.Convert([]byte(rotation), verbatim.Options{Path: "x.mdx", MDX: true})
	b := verbatim.Convert([]byte(rotation), verbatim.Options{Path: "x.mdx", MDX: true})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("conversion is not deterministic")
	}
	keys := map[string]bool{}
	for _, blk := range a.Blocks {
		if keys[blk.Key] {
			t.Fatalf("duplicate key %s", blk.Key)
		}
		keys[blk.Key] = true
	}
}

func TestFrontMatterFlavours(t *testing.T) {
	fm, body, err := verbatim.SplitFrontMatter([]byte("+++\ntitle = \"Hello\"\nweight = 3\ndraft = false\n+++\nBody\n"))
	if err != nil || fm.Title != "Hello" || fm.Position == nil || *fm.Position != 3 || fm.Hidden || string(body) != "Body\n" {
		t.Fatalf("toml = %+v %q %v", fm, body, err)
	}
	fm, body, err = verbatim.SplitFrontMatter([]byte("---\nslug: /guides/setup\nhidden: true\naudiences: [developers, users]\n---\n# T\n"))
	if err != nil || fm.Slug != "setup" || !fm.Hidden || len(fm.Audiences) != 2 || string(body) != "# T\n" {
		t.Fatalf("yaml = %+v %q %v", fm, body, err)
	}
	fm, body, _ = verbatim.SplitFrontMatter([]byte("No front matter\n---\n"))
	if fm.Title != "" || string(body) != "No front matter\n---\n" {
		t.Fatalf("plain = %+v %q", fm, body)
	}
}

func TestBlobURLShapes(t *testing.T) {
	for provider, want := range map[string]string{
		"github":    "https://h/r/blob/main/a/b.go",
		"gitlab":    "https://h/r/-/blob/main/a/b.go",
		"bitbucket": "https://h/r/src/main/a/b.go",
		"azure":     "https://h/r?path=/a/b.go&version=GBmain",
	} {
		if got := (verbatim.Repo{WebURL: "https://h/r", Provider: provider, Branch: "main"}).BlobURL("a/b.go"); got != want {
			t.Errorf("%s: %s", provider, got)
		}
	}
}

func TestRawHTMLAndUnsafeLinksAreNeutralized(t *testing.T) {
	src := "<div>\n<p>&lt;script&gt;alert(1)&lt;/script&gt; and <a href=\"JavaScript:alert(1)\">x</a> and <code>&lt;div&gt;</code></p>\n</div>\n\nSee [y](javascript:alert(2)) and [ok](https://example.com) and ![i](data:image/svg+xml,boom).\n"
	doc := verbatim.Convert([]byte(src), verbatim.Options{Path: "x.md"})
	var all strings.Builder
	for _, blk := range doc.Blocks {
		if c, ok := blk.Content.(map[string]any); ok && c["text"] != nil {
			text, _ := c["text"].(string)
			all.WriteString(text + "\n")
		}
	}
	out := all.String()
	for _, bad := range []string{"<script", "javascript:", "JavaScript:", "data:image"} {
		if strings.Contains(out, bad) {
			t.Errorf("output keeps %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"&lt;script>alert(1)&lt;/script>", "[x](#)", "`<div>`", "[y](#)", "[ok](https://example.com)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(doc.Warnings) != 3 {
		t.Errorf("warnings = %v", doc.Warnings)
	}
}
