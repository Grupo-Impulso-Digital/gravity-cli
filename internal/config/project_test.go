package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func loadProject(t *testing.T, body string) *config.Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p == nil {
		t.Fatal("expected a project, got nil")
	}
	return p
}

func TestLoadProject_Defaults(t *testing.T) {
	p := loadProject(t, "version: 1\nsite: acme\nproduct:\n  repo: billing-api\n")
	if p.Product.Slug != "acme" {
		t.Errorf("product.slug should default to site; got %q", p.Product.Slug)
	}
	if p.Spaces.Default != "billing-api" {
		t.Errorf("spaces.default should default to repo; got %q", p.Spaces.Default)
	}
	if p.ReleaseNotes.Space != "changelog" {
		t.Errorf("releaseNotes.space default = %q", p.ReleaseNotes.Space)
	}
	if p.Knowledge.Namespace != "acme" {
		t.Errorf("knowledge.namespace should default to product.slug; got %q", p.Knowledge.Namespace)
	}
}

func TestLoadProject_LegacySpace(t *testing.T) {
	p := loadProject(t, "site: gravity\napiUrl: https://x.example\nspace: gravity-cli\n")
	if p.Spaces.Default != "gravity-cli" {
		t.Errorf("legacy space should map to spaces.default; got %q", p.Spaces.Default)
	}
	if p.ReleaseNotes.Space != "gravity-cli" {
		t.Errorf("legacy space should map to releaseNotes.space; got %q", p.ReleaseNotes.Space)
	}
}

func TestPageTarget_SharedNamespacing(t *testing.T) {
	p := loadProject(t, "site: acme\nproduct:\n  repo: billing-api\nspaces:\n  default: billing-api\n  shared: [changelog]\n")

	if sp, slug, col := p.PageTarget("billing-api", "api-reference", ""); sp != "billing-api" || slug != "api-reference" || col != "" {
		t.Errorf("repo-owned target = (%q,%q,%q)", sp, slug, col)
	}
	if sp, slug, col := p.PageTarget("changelog", "v1.2.0", ""); sp != "changelog" || slug != "billing-api/v1.2.0" || col != "billing-api" {
		t.Errorf("shared target = (%q,%q,%q); want (changelog, billing-api/v1.2.0, billing-api)", sp, slug, col)
	}
	if _, _, col := p.PageTarget("changelog", "v1.2.0", "releases"); col != "releases" {
		t.Errorf("explicit collection = %q; want releases", col)
	}
	if sp, _, _ := p.PageTarget("", "x", ""); sp != "billing-api" {
		t.Errorf("empty space should fall back to default; got %q", sp)
	}
}

func TestPageTarget_HomePageStaysFlat(t *testing.T) {
	p := loadProject(t, `site: acme
product:
  repo: device-api
spaces:
  default: connect
  parent: fundamentum
  home: overview
  shared: [connect]
documents:
  - file: README.md
    page: overview
`)
	if sp, slug, col := p.PageTarget("connect", "overview", ""); sp != "connect" || slug != "overview" || col != "" {
		t.Errorf("home target = (%q,%q,%q); want (connect, overview, \"\")", sp, slug, col)
	}
	if _, slug, col := p.PageTarget("connect", "guide", ""); slug != "device-api/guide" || col != "device-api" {
		t.Errorf("shared sibling = (%q,%q)", slug, col)
	}
}

func TestLoadProject_HierarchyValidation(t *testing.T) {
	dir := t.TempDir()
	body := `site: acme
spaces:
  default: connect
  parent: connect
  home: overview
documents:
  - file: docs/a.md
    page: not-overview
`
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "spaces.parent must name a different space") {
		t.Errorf("missing self-parent error: %v", msg)
	}
	if !strings.Contains(msg, "matches no source/document page") {
		t.Errorf("missing home-typo error: %v", msg)
	}
}

func TestLoadProject_HomeWithoutMappingsAllowed(t *testing.T) {
	p := loadProject(t, "site: acme\nspaces:\n  default: connect\n  home: overview\n")
	if p.Spaces.Home != "overview" {
		t.Errorf("home = %q", p.Spaces.Home)
	}
}

func TestLoadProject_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	body := "site: acme\nsources:\n  - kind: openapi\n    title: No source or page\n"
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil {
		t.Fatal("expected validation error for incomplete source mapping")
	}
	for _, want := range []string{"sources[0]: 'source' is required", "sources[0]: 'page' is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %s", want, err.Error())
		}
	}
}

func TestLoadProject_VersionTooNew(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte("version: 999\nsite: acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil || !strings.Contains(err.Error(), "upgrade the CLI") {
		t.Fatalf("expected an upgrade error for a too-new version; got %v", err)
	}
}

func TestLoadProject_V2Sections(t *testing.T) {
	p := loadProject(t, `version: 1
site: orbit
product:
  slug: orbit
  repo: orbit-api
  role: api
discovery:
  units: auto
  include: ["src/**", "cmd/**"]
  exclude: ["**/*_test.go", "vendor/**"]
  entrypoints: ["cmd/server/main.go", "openapi.yaml"]
  audiences:
    default: [developers]
i18n:
  languages: [fr, pt-BR]
coverage:
  min: 0.8
  require: [overview, quickstart]
`)
	if p.Discovery.Units != config.UnitsAuto {
		t.Errorf("discovery.units = %q", p.Discovery.Units)
	}
	if len(p.Discovery.Include) != 2 || p.Discovery.Include[0] != "src/**" {
		t.Errorf("discovery.include = %v", p.Discovery.Include)
	}
	if len(p.Discovery.Exclude) != 2 || p.Discovery.Exclude[0] != "**/*_test.go" {
		t.Errorf("discovery.exclude = %v", p.Discovery.Exclude)
	}
	if len(p.Discovery.Entrypoints) != 2 || p.Discovery.Entrypoints[1] != "openapi.yaml" {
		t.Errorf("discovery.entrypoints = %v", p.Discovery.Entrypoints)
	}
	if len(p.Discovery.Audiences.Default) != 1 || p.Discovery.Audiences.Default[0] != "developers" {
		t.Errorf("discovery.audiences.default = %v", p.Discovery.Audiences.Default)
	}
	if len(p.I18n.Languages) != 2 || p.I18n.Languages[1] != "pt-BR" {
		t.Errorf("i18n.languages = %v", p.I18n.Languages)
	}
	if p.Coverage.Min != 0.8 {
		t.Errorf("coverage.min = %v", p.Coverage.Min)
	}
	if len(p.Coverage.Require) != 2 || p.Coverage.Require[0] != "overview" {
		t.Errorf("coverage.require = %v", p.Coverage.Require)
	}
	if got := p.ResolveUnitKind(); got != config.UnitService {
		t.Errorf("ResolveUnitKind() = %q, want service (role api + units auto)", got)
	}
}

func TestLoadProject_V2Defaults(t *testing.T) {
	p := loadProject(t, "site: acme\nproduct:\n  repo: billing-api\n")
	if p.Discovery.Units != config.UnitsAuto {
		t.Errorf("discovery.units default = %q, want auto", p.Discovery.Units)
	}
	if p.Coverage.Min != 0 {
		t.Errorf("coverage.min default = %v, want 0 (no gate)", p.Coverage.Min)
	}
	if p.I18n.Languages != nil {
		t.Errorf("i18n.languages default = %v, want nil", p.I18n.Languages)
	}
	if p.Discovery.Include != nil || p.Discovery.Exclude != nil || p.Discovery.Entrypoints != nil {
		t.Errorf("discovery globs should stay nil; got %+v", p.Discovery)
	}
}

func TestResolveUnitKind(t *testing.T) {
	cases := []struct {
		units string
		role  string
		want  string
	}{
		{"auto", "frontend", config.UnitFeature},
		{"auto", "api", config.UnitService},
		{"auto", "service", config.UnitService},
		{"auto", "docs", config.UnitFeature},
		{"auto", "", config.UnitFeature},
		{"auto", "wat", config.UnitFeature},
		{"", "api", config.UnitService},
		{"system", "api", config.UnitSystem},
		{"feature", "service", config.UnitFeature},
		{"api", "frontend", config.UnitAPI},
		{"capability", "frontend", config.UnitCapability},
	}
	for _, c := range cases {
		p := &config.Project{
			Discovery: config.Discovery{Units: c.units},
			Product:   config.Product{Role: c.role},
		}
		if got := p.ResolveUnitKind(); got != c.want {
			t.Errorf("units=%q role=%q: ResolveUnitKind() = %q, want %q", c.units, c.role, got, c.want)
		}
	}
}

func TestLoadProject_SourceKindCodeRejected(t *testing.T) {
	dir := t.TempDir()
	body := "site: acme\nsources:\n  - source: internal/cli/root.go\n    kind: code\n    page: cli\n"
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil {
		t.Fatal("expected kind: code to be a validation error")
	}
	msg := err.Error()
	for _, want := range []string{
		`sources[0]: kind "code" is no longer supported`,
		"discovery.include/discovery.entrypoints",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

func TestLoadProject_SourceKindUnknownRejected(t *testing.T) {
	dir := t.TempDir()
	body := "site: acme\nsources:\n  - source: spec.yaml\n    kind: graphql\n    page: api\n"
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil || !strings.Contains(err.Error(), `sources[0]: kind "graphql" must be openapi`) {
		t.Fatalf("expected an unknown-kind error; got %v", err)
	}
}

func TestLoadProject_V2ValidationCollectsEveryError(t *testing.T) {
	dir := t.TempDir()
	body := `site: acme
discovery:
  units: modules
  include: ["/abs/path"]
  exclude: ["../outside"]
  entrypoints: [""]
  audiences:
    default: [internal]
i18n:
  languages: [french, fr]
coverage:
  min: 1.5
  require: [Overview]
`
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	for _, want := range []string{
		`discovery.units "modules" must be auto|feature|service|system|api|capability`,
		"discovery.include[0]: must be a repo-relative path, not absolute",
		"discovery.exclude[0]: must not escape the repo root",
		"discovery.entrypoints[0]: must not be empty",
		`discovery.audiences.default[0]: "internal" must be public|users|developers`,
		`i18n.languages[0]: "french" is not a valid language code`,
		"coverage.min 1.50 must be between 0 and 1",
		"coverage.require[0]",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

func TestLoadProject_TooManyLanguages(t *testing.T) {
	dir := t.TempDir()
	const letters = "abcdefghijklmnopqrstuvwxyz"
	langs := make([]string, 0, config.MaxLanguages+1)
	for i := 0; i <= config.MaxLanguages; i++ {
		langs = append(langs, "z"+string(letters[i]))
	}
	body := "site: acme\ni18n:\n  languages: [" + strings.Join(langs, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProject(dir)
	if err == nil || !strings.Contains(err.Error(), "i18n.languages: at most 24 languages") {
		t.Fatalf("expected a language-count error; got %v", err)
	}
}

func TestLoadProject_Missing(t *testing.T) {
	p, err := config.LoadProject(t.TempDir())
	if err != nil {
		t.Fatalf("missing file should not error; got %v", err)
	}
	if p != nil {
		t.Errorf("missing file should return nil project; got %+v", p)
	}
}
