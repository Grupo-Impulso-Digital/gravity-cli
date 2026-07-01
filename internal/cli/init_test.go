package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/config"
)

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderScaffoldRoundTripWithMappings(t *testing.T) {
	dir := t.TempDir()
	p := scaffoldParams{
		Site:        "acme",
		APIURL:      "https://example.com",
		Space:       "docs",
		Role:        "api",
		Repo:        "acme-api",
		ProductSlug: "acme",
		Sources: []config.SourceMap{
			{Source: "openapi/openapi.yaml", Kind: "openapi", Space: "api", Page: "api-reference", Title: "API Reference"},
		},
		Documents: []config.DocMap{
			{File: "README.md", Page: "overview", Ownership: "machine", As: "page"},
		},
	}
	writeFile(t, dir, config.ProjectFileName, renderScaffold(p))

	proj, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v\n---\n%s", err, renderScaffold(p))
	}
	if proj == nil {
		t.Fatal("expected a project")
	}
	if len(proj.Sources) != 1 || proj.Sources[0].Page != "api-reference" || proj.Sources[0].Source != "openapi/openapi.yaml" {
		t.Errorf("sources = %+v", proj.Sources)
	}
	if len(proj.Documents) != 1 || proj.Documents[0].File != "README.md" || proj.Documents[0].As != "page" {
		t.Errorf("documents = %+v", proj.Documents)
	}
}

func TestRenderScaffoldEmptyRoundTrips(t *testing.T) {
	dir := t.TempDir()
	content := renderScaffold(scaffoldParams{Site: "acme", APIURL: "https://example.com", Space: "docs", Repo: "acme"})
	writeFile(t, dir, config.ProjectFileName, content)

	proj, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v\n---\n%s", err, content)
	}
	if len(proj.Sources) != 0 || len(proj.Documents) != 0 {
		t.Errorf("expected no mappings, got sources=%v documents=%v", proj.Sources, proj.Documents)
	}
}

func TestDetectDocSources(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "openapi.yaml", "openapi: 3.0.0")
	writeFile(t, dir, "README.md", "# hi")
	writeFile(t, dir, "docs/guide.md", "# guide")
	writeFile(t, dir, "CHANGELOG.md", "# changes") // excluded (release notes)
	writeFile(t, dir, "package.json", "{}")        // not a spec
	writeFile(t, dir, "node_modules/foo/swagger.yaml", "x")
	writeFile(t, dir, ".hidden/openapi.yaml", "x")

	specs, mds := detectDocSources(dir)
	if len(specs) != 1 || specs[0] != "openapi.yaml" {
		t.Errorf("specs = %v, want [openapi.yaml]", specs)
	}
	if len(mds) != 2 {
		t.Errorf("mds = %v, want README.md + docs/guide.md", mds)
	}
}

func TestPickDefaultSite(t *testing.T) {
	sites := []api.SiteSummary{
		{Slug: "docs", Name: "Docs"},
		{Slug: "internal", Name: "Internal"},
	}
	if got := pickDefaultSite(sites, "internal"); got != "internal" {
		t.Errorf("seed in list: got %q, want internal", got)
	}
	if got := pickDefaultSite(sites, "nope"); got != "docs" {
		t.Errorf("seed not in list: got %q, want first (docs)", got)
	}
	if got := pickDefaultSite(nil, "fallback"); got != "fallback" {
		t.Errorf("empty list: got %q, want seed fallback", got)
	}
}

func TestSlugInSpaces(t *testing.T) {
	spaces := []api.Space{{Slug: "guides"}, {Slug: "api"}}
	if !slugInSpaces("api", spaces) {
		t.Error("expected api to be found")
	}
	if slugInSpaces("missing", spaces) {
		t.Error("missing should not be found")
	}
	if slugInSpaces("", spaces) {
		t.Error("empty slug is never a match")
	}
}

func TestSiteAndSpaceLabel(t *testing.T) {
	if got := siteLabel(api.SiteSummary{Slug: "docs", Name: "Docs"}); got != "docs — Docs" {
		t.Errorf("siteLabel with name = %q", got)
	}
	if got := siteLabel(api.SiteSummary{Slug: "docs", Name: "docs"}); got != "docs" {
		t.Errorf("siteLabel name==slug = %q, want bare slug", got)
	}
	if got := siteLabel(api.SiteSummary{Slug: "docs"}); got != "docs" {
		t.Errorf("siteLabel no name = %q, want bare slug", got)
	}
	if got := spaceLabel(api.Space{Slug: "api", Name: "API"}); got != "api — API" {
		t.Errorf("spaceLabel with name = %q", got)
	}
}

func TestSlugAndSpecPageSlug(t *testing.T) {
	slugs := map[string]string{
		"README.md":               "overview",
		"docs/Getting Started.md": "getting-started",
		"openapi/openapi.yaml":    "openapi",
	}
	for in, want := range slugs {
		if got := slugFromPath(in); got != want {
			t.Errorf("slugFromPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := specPageSlug("api/openapi.yaml"); got != "api-reference" {
		t.Errorf("specPageSlug = %q, want api-reference", got)
	}
}
