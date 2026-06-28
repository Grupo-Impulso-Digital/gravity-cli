package cli

import (
	"os"
	"path/filepath"
	"testing"

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
