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
	// A versionless legacy file with the old top-level `space:` still loads.
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

	// Repo-owned space: slug used verbatim, no collection.
	if sp, slug, col := p.PageTarget("billing-api", "api-reference", ""); sp != "billing-api" || slug != "api-reference" || col != "" {
		t.Errorf("repo-owned target = (%q,%q,%q)", sp, slug, col)
	}
	// Shared space: slug prefixed with the repo (page identity is (space,slug)
	// platform-side) AND grouped into the per-repo collection.
	if sp, slug, col := p.PageTarget("changelog", "v1.2.0", ""); sp != "changelog" || slug != "billing-api/v1.2.0" || col != "billing-api" {
		t.Errorf("shared target = (%q,%q,%q); want (changelog, billing-api/v1.2.0, billing-api)", sp, slug, col)
	}
	// An explicit mapping collection wins over the per-repo default.
	if _, _, col := p.PageTarget("changelog", "v1.2.0", "releases"); col != "releases" {
		t.Errorf("explicit collection = %q; want releases", col)
	}
	// Empty space falls back to spaces.default.
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
	// The home page is the space's single landing page: unprefixed, uncollected
	// (the platform rejects a collection-filed overview page).
	if sp, slug, col := p.PageTarget("connect", "overview", ""); sp != "connect" || slug != "overview" || col != "" {
		t.Errorf("home target = (%q,%q,%q); want (connect, overview, \"\")", sp, slug, col)
	}
	// Sibling pages in the same shared space keep prefix + collection.
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
	// A docs-generate-only repo (no mappings) may declare a home page — the
	// AI plan produces the pages, so there is nothing to cross-check offline.
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

func TestLoadProject_Missing(t *testing.T) {
	p, err := config.LoadProject(t.TempDir())
	if err != nil {
		t.Fatalf("missing file should not error; got %v", err)
	}
	if p != nil {
		t.Errorf("missing file should return nil project; got %+v", p)
	}
}
