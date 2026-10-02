package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

func renderConversion(c *Conversion) string {
	var b strings.Builder
	b.Write(c.YAML)
	b.WriteString("---\n")
	for _, n := range c.Notes {
		b.WriteString(n.Action + " " + n.Key + ": " + n.Detail + "\n")
	}
	for _, s := range c.Spaces {
		b.WriteString("declare " + s.Site + "/" + s.Slug + " " + s.Name + " " + s.Type + " " + s.Visibility + " parent=" + s.Parent + "\n")
	}
	return b.String()
}

func covered(key string, notes []ConvertNote) bool {
	for _, n := range notes {
		if n.Key == key || strings.HasPrefix(key, n.Key+".") || strings.HasPrefix(key, n.Key+"[]") {
			return true
		}
	}
	return false
}

func TestConvertV1Golden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "v1", "*.yaml"))
	if err != nil || len(files) < 3 {
		t.Fatalf("fixtures: %v %v", files, err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			conv, err := ConvertV1(data, ConvertOptions{RepoName: "billing-api"})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if _, err := Parse(conv.YAML); err != nil {
				t.Fatalf("converted manifest does not validate: %v", err)
			}
			var raw map[string]any
			if err := yaml.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			for _, key := range LiveKeys(raw) {
				if !covered(key, conv.Notes) {
					t.Errorf("live key %s is missing from the mapping report", key)
				}
			}
			for _, p := range conv.Manifest.Passes {
				if p.Kind != KindVerbatim {
					continue
				}
				if p.Options["adopt"] != true {
					t.Errorf("verbatim pass %s must adopt the v0.x pages", p.Name)
				}
			}
			if _, err := ConvertV1(conv.YAML, ConvertOptions{RepoName: "billing-api"}); !errors.Is(err, ErrNotV1) {
				t.Errorf("converting the output again must be a no-op, got %v", err)
			}
			golden := strings.TrimSuffix(f, ".yaml") + ".golden"
			got := renderConversion(conv)
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run go test -update): %v", err)
			}
			if string(want) != got {
				t.Fatalf("golden mismatch\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

func TestConvertV1DocumentsKeepTheirPageIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "v1", "full.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	conv, err := ConvertV1(data, ConvertOptions{RepoName: "billing-api"})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := conv.Manifest.PassByName("docs-product")
	if !ok {
		t.Fatal("documents of space product become pass docs-product")
	}
	files := p.Options["files"].([]any)
	first := files[0].(map[string]any)
	second := files[1].(map[string]any)
	if first["include"] != "docs/guide.md" || first["slug"] != "guide" || first["title"] != "Guide" {
		t.Fatalf("first file = %v", first)
	}
	if second["include"] != "docs/faq.md" || second["collection"] != "help" || second["slug"] != nil {
		t.Fatalf("second file = %v", second)
	}
	if conv.Manifest.Product != "acme-platform" {
		t.Fatalf("product = %q", conv.Manifest.Product)
	}
	cl, _ := conv.Manifest.PassByName("changelog")
	if cl.Options["changelogFile"] != "CHANGELOG.md" || cl.Template != TemplateCustomerChangelog || cl.Target != "acme/changelog" {
		t.Fatalf("changelog = %+v", cl)
	}
	mem, _ := conv.Manifest.PassByName("memory")
	if mem.Options["namespace"] != "product:acme-platform/billing" {
		t.Fatalf("memory = %+v", mem)
	}
	check, _ := conv.Manifest.PassByName("pr-check")
	if check.Options["coverageMin"] != 0.8 {
		t.Fatalf("pr-check = %+v", check)
	}
	ref, _ := conv.Manifest.PassByName("api-api")
	if len(ref.Options["sources"].([]any)) != 2 || strings.Join(conv.Manifest.Code.OpenAPI, ",") != "api/openapi.yaml,api/admin.yaml" {
		t.Fatalf("reference = %+v code = %+v", ref, conv.Manifest.Code)
	}
}

func TestConvertV1RefusesV2AndTokens(t *testing.T) {
	if _, err := ConvertV1([]byte("version: 2\n"), ConvertOptions{}); !errors.Is(err, ErrNotV1) {
		t.Fatalf("v2 = %v", err)
	}
	var me *ManifestError
	if _, err := ConvertV1([]byte("site: docs\ntoken: sk_live_x\n"), ConvertOptions{}); !errors.As(err, &me) {
		t.Fatalf("token = %v", err)
	}
}

func TestUnionScopes(t *testing.T) {
	cases := []struct {
		passes []ScopedPass
		want   string
	}{
		{nil, "repo:connect,runs:write,content:read,inventory:write"},
		{[]ScopedPass{{Kind: KindReference, Enabled: true}}, "repo:connect,runs:write,content:read,content:propose,inventory:write"},
		{[]ScopedPass{{Kind: KindReference, Enabled: true, Options: map[string]any{"prose": true}}}, "repo:connect,runs:write,content:read,content:propose,inventory:write,llm"},
		{[]ScopedPass{{Kind: KindVerbatim, Enabled: true}, {Kind: KindNucleus, Enabled: false}}, "repo:connect,runs:write,content:read,content:verbatim,inventory:write"},
		{[]ScopedPass{{Kind: KindCheck, Enabled: true, Options: map[string]any{"claims": false}}, {Kind: KindCapture, Enabled: true}}, "repo:connect,runs:write,content:read,content:propose,inventory:write"},
		{[]ScopedPass{{Kind: KindCheck, Enabled: true}, {Kind: KindNucleus, Enabled: true}}, "repo:connect,runs:write,content:read,content:propose,inventory:write,nucleus:read,nucleus:write,llm"},
	}
	for i, c := range cases {
		if got := strings.Join(UnionScopes(c.passes), ","); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestRenderPassesAppendsAValidBlock(t *testing.T) {
	block, err := RenderPasses([]Pass{{Name: "docs", Kind: KindGuides, Target: "docs/guides", Triggers: []string{TriggerPush}}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(append([]byte("version: 2\n"), block...))
	if err != nil || len(m.Passes) != 1 || m.Passes[0].Target != "docs/guides" {
		t.Fatalf("parsed = %+v %v\n%s", m, err, block)
	}
}
