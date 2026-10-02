package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pinnedSchemaSHA256 = "95b65d903a0b9a4bcf54f78aee50556ad2dccdaf669a75b77f81756e44b1009f"

func TestEmbeddedSchemaIsPinned(t *testing.T) {
	sum := sha256.Sum256(SchemaJSON())
	if got := hex.EncodeToString(sum[:]); got != pinnedSchemaSHA256 {
		t.Fatalf("embedded schema sha256 = %s, want %s (copy docs/pipelines/gravity.schema.json byte for byte)", got, pinnedSchemaSHA256)
	}
	if _, err := loadSchema(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/manifests/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]struct {
		Valid  bool   `json:"valid"`
		Path   string `json:"path"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("testdata/manifests/*/*.yaml")
	if len(files) != len(expected) {
		t.Fatalf("%d fixture files, %d expectations", len(files), len(expected))
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			m, err := Load(filepath.Join("testdata/manifests", name))
			if want.Valid {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				if m == nil || m.Version != 2 || !strings.HasPrefix(m.Hash, "sha256:") {
					t.Fatalf("bad manifest %+v", m)
				}
				return
			}
			if err == nil {
				t.Fatalf("want invalid (%s), got valid", want.Reason)
			}
			if want.Path == "" {
				if !errors.Is(err, ErrV1Manifest) {
					t.Fatalf("want v1 error, got %v", err)
				}
				return
			}
			var me *ManifestError
			if !errors.As(err, &me) {
				t.Fatalf("want ManifestError, got %T %v", err, err)
			}
			found := false
			for _, is := range me.Issues {
				if strings.HasPrefix(is.Path, want.Path) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no issue at %s in %v", want.Path, me.Issues)
			}
		})
	}
}

func TestExactMessages(t *testing.T) {
	cases := map[string]string{
		"invalid/typo-key.yaml":               `passes[0].trigers: unknown key (did you mean "triggers"?)`,
		"invalid/on-key.yaml":                 `passes[0].on: unknown key (did you mean "triggers"?)`,
		"invalid/target-site-only.yaml":       `passes[0].target: guides targets need <site>/<space>[/<collection>...]`,
		"invalid/verbatim-without-files.yaml": `passes[0]: verbatim passes require options.files`,
		"invalid/parent-path.yaml":            `code.openapi[0]: paths must stay inside the repository (no .. segments)`,
		"invalid/token-in-manifest.yaml":      `token: tokens are never read from .gravity.yaml; use GRAVITY_TOKEN in CI or ` + "`gravity login`" + ` locally`,
		"invalid/bad-pass-name.yaml":          `passes[0].name: pass names are lowercase slugs (a-z, 0-9, dashes; at most 63 characters)`,
		"invalid/template-without-kind.yaml":  `passes[0]: kind is required even when template is given (template api-reference is a reference pass)`,
		"invalid/override-on-glob.yaml":       `passes[0].options.files[0].include: slug/title overrides need a literal include (one file), not a glob`,
	}
	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata/manifests", file))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Parse(data)
			var me *ManifestError
			if !errors.As(err, &me) {
				t.Fatalf("want ManifestError, got %v", err)
			}
			if len(me.Issues) != 1 || me.Issues[0].String() != want {
				t.Fatalf("issues = %v\nwant [%s]", me.Issues, want)
			}
		})
	}
}

func TestMoreInvalidManifests(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"unknown top key", "version: 2\nprodcut: acme\n", `prodcut: unknown key (did you mean "product"?)`},
		{"unknown options key", "version: 2\npasses:\n  - name: api\n    kind: reference\n    target: dev/api\n    options:\n      pagestrategy: single\n", `passes[0].options.pagestrategy: unknown key (did you mean "pageStrategy"?)`},
		{"template mismatch", "version: 2\npasses:\n  - name: api\n    kind: guides\n    template: api-reference\n    target: dev/api\n", "passes[0].kind: template api-reference is a reference pass, not guides"},
		{"duplicate names", "version: 2\npasses:\n  - name: a\n    kind: nucleus\n  - name: a\n    kind: nucleus\n", `passes[1].name: duplicate pass name "a" (also passes[0])`},
		{"bad trigger enum", "version: 2\npasses:\n  - name: a\n    kind: nucleus\n    triggers: [nightly]\n", "passes[0].triggers[0]: value must be one of"},
		{"version 3", "version: 3\n", "version: value must be 2"},
		{"marketing api url", "version: 2\napiUrl: https://gravitydocs.io\n", "apiUrl: invalid API URL"},
		{"namespace outside product", "version: 2\nproduct: acme\npasses:\n  - name: m\n    kind: nucleus\n    options:\n      namespace: other\n", "passes[0].options.namespace: must be product:acme or start with product:acme/"},
		{"absolute scope", "version: 2\npasses:\n  - name: m\n    kind: nucleus\n    scope:\n      paths: [/etc]\n", "passes[0].scope.paths[0]: paths must stay inside the repository (repository-relative, not absolute)"},
		{"nested token", "version: 2\ncode:\n  token: x\n", "code.token: tokens are never read"},
		{"not a mapping", "- a\n- b\n", "the manifest must be a YAML mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant containing %q", err, tc.want)
			}
		})
	}
}

func TestV1Detection(t *testing.T) {
	for _, src := range []string{"", "site: docs\n", "version: 1\n", "version: 2\nsources: []\n", "spaces:\n  default: x\n"} {
		if !DetectV1([]byte(src)) {
			t.Errorf("DetectV1(%q) = false", src)
		}
		if _, err := Parse([]byte(src)); !errors.Is(err, ErrV1Manifest) {
			t.Errorf("Parse(%q) = %v, want v1", src, err)
		}
	}
	if DetectV1([]byte("version: 2\n")) {
		t.Error("version 2 detected as v1")
	}
}

func TestParseTypedFields(t *testing.T) {
	m, err := Load("testdata/manifests/valid/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Product != "acme-platform" || len(m.Passes) != 6 || m.Code.Units.Role[0] != "implements" {
		t.Fatalf("decode: %+v", m)
	}
	p, ok := m.PassByName("developer-api")
	if !ok || p.Kind != KindReference || p.Options["pageStrategy"] != "per-tag" || !p.IsEnabled() {
		t.Fatalf("pass: %+v", p)
	}
	if got := m.CodeExclude(); len(got) != 2 {
		t.Fatalf("CodeExclude = %v", got)
	}
	g, err := Load("testdata/manifests/valid/gateway-declares.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if g.AppPasses != "ignore" || len(g.Code.Units.Role) != 1 {
		t.Fatalf("gateway: %+v", g)
	}
	c, err := Load("testdata/manifests/valid/converted-v1-documents.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Code.Units.Role) != 2 {
		t.Fatalf("roles: %v", c.Code.Units.Role)
	}
}

func TestHashIgnoresFormatting(t *testing.T) {
	a, err := Parse([]byte("version: 2\nproduct: acme\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse([]byte("# comment\nproduct:   acme\nversion: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash || string(a.Doc) != `{"product":"acme","version":2}` {
		t.Fatalf("hash %s vs %s, doc %s", a.Hash, b.Hash, a.Doc)
	}
}

func TestLoadMissingAndPath(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), ".gravity.yaml"))
	if err != nil || m != nil {
		t.Fatalf("missing manifest = %v, %v", m, err)
	}
	if got := ManifestPath("/repo", ""); got != filepath.Join("/repo", ".gravity.yaml") {
		t.Fatal(got)
	}
	if got := ManifestPath("/repo", "ci/g.yaml"); got != filepath.Join("/repo", "ci/g.yaml") {
		t.Fatal(got)
	}
	if got := ManifestPath("/repo", "/abs/g.yaml"); got != "/abs/g.yaml" {
		t.Fatal(got)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".gravity.yaml")
	if err := os.WriteFile(path, []byte("site: docs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if !errors.Is(err, ErrV1Manifest) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "gravity init") {
		t.Fatalf("v1 load = %v", err)
	}
}

func TestCheckAPIURL(t *testing.T) {
	for _, ok := range []string{"https://api.gravitydocs.io", "http://localhost:8787"} {
		if err := CheckAPIURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"api.gravitydocs.io", "ftp://x", "https://www.gravitydocs.io"} {
		if CheckAPIURL(bad) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
