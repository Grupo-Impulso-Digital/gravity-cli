package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
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

func fullManifest() *config.Project {
	return &config.Project{
		Version: 1,
		Site:    "acme",
		APIURL:  "https://docs.example.com",
		Product: config.Product{Slug: "acme", Repo: "acme-api", Role: "api"},
		Spaces: config.Spaces{
			Default: "guides",
			Shared:  []string{"platform"},
			Parent:  "platform",
			Home:    "overview",
			Declare: []config.SpaceDecl{
				{Slug: "platform", Name: "Platform", Type: "product-docs", Visibility: "public", Audiences: []string{"public", "users"}},
				{Slug: "developers", Parent: "platform", Audiences: []string{"developers"}},
			},
		},
		Sources: []config.SourceMap{
			{Source: "openapi/openapi.yaml", Kind: "openapi", Space: "developers", Page: "api-reference", Title: "API Reference", Collection: "rest"},
		},
		Documents: []config.DocMap{
			{File: "README.md", Page: "overview", Ownership: "machine", As: "page", Title: "Overview", Collection: "intro"},
			{File: "CHANGELOG.md", As: "release", Version: "1.2.0", Space: "news"},
		},
		ReleaseNotes: config.ReleaseNotes{Space: "news", Changelog: "docs/HISTORY.md"},
		Knowledge:    config.Knowledge{Namespace: "acme-platform"},
		Discovery: config.Discovery{
			Units:       "service",
			Include:     []string{"src/**"},
			Exclude:     []string{"src/legacy/**"},
			Entrypoints: []string{"src/main.go"},
			Audiences:   config.DiscoveryAudiences{Default: []string{"developers"}},
		},
		I18n:     config.I18n{Languages: []string{"en", "fr-CA"}},
		Coverage: config.Coverage{Min: 0.75, Require: []string{"overview", "quickstart"}},
	}
}

func TestRenderManifestRoundTripsEverySection(t *testing.T) {
	want := fullManifest()
	content := renderManifest(want)
	got, err := config.ParseProject([]byte(content), config.ProjectFileName)
	if err != nil {
		t.Fatalf("ParseProject: %v\n---\n%s", err, content)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip lost data\n got: %+v\nwant: %+v\n---\n%s", got, want, content)
	}
	if strings.Contains(content, "kind: openapi | code") || strings.Contains(content, "code file") {
		t.Errorf("the scaffold must not advertise kind: code:\n%s", content)
	}
}

func TestRenderManifestMinimal(t *testing.T) {
	dir := t.TempDir()
	content := renderManifest(newManifest(initInputs{site: "acme", repo: "acme-api"}, ""))
	writeFile(t, dir, config.ProjectFileName, content)
	proj, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v\n---\n%s", err, content)
	}
	if proj.Spaces.Default != "acme-api" || proj.ReleaseNotes.Space != "changelog" || proj.APIURL != "" {
		t.Errorf("minimal manifest = %+v", proj)
	}
	for _, absent := range []string{"apiUrl", "\nreleaseNotes:", "\nknowledge:", "scope:"} {
		if strings.Contains(content, absent) {
			t.Errorf("minimal manifest should not write %q:\n%s", absent, content)
		}
	}
	if !strings.Contains(content, "# sources:") || !strings.Contains(content, "# documents:") {
		t.Errorf("empty mappings should leave commented examples:\n%s", content)
	}
}

func TestDetectDocSources(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"openapi.yaml", "README.md", "docs/guide.md", "docs/api/guide.md", "pkg/notes.md",
		"CHANGELOG.md", "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md",
		"LICENSE.md", "SECURITY.md", ".github/pull_request_template.md", "package.json",
		"node_modules/foo/README.md", "node_modules/foo/swagger.yaml", "vendor/x/README.md",
		"dist/README.md", ".hidden/openapi.yaml",
	} {
		writeFile(t, dir, f, "# x\n")
	}

	det := detectDocSources(dir)
	if len(det.specs) != 1 || det.specs[0] != "openapi.yaml" {
		t.Errorf("specs = %v, want [openapi.yaml]", det.specs)
	}
	wantDocs := []string{"README.md", "docs/api/guide.md", "docs/guide.md", "pkg/notes.md"}
	if !reflect.DeepEqual(det.docs, wantDocs) {
		t.Errorf("docs = %v, want %v", det.docs, wantDocs)
	}
	wantSelected := []string{"README.md", "docs/api/guide.md", "docs/guide.md"}
	if !reflect.DeepEqual(det.selected, wantSelected) {
		t.Errorf("selected = %v, want %v (pkg/notes.md offered but not preselected)", det.selected, wantSelected)
	}
}

func TestDocMappingsHaveUniqueSlugs(t *testing.T) {
	maps := docMappings([]string{"README.md", "docs/api/guide.md", "docs/guide.md"})
	seen := map[string]bool{}
	for _, m := range maps {
		if seen[m.Page] {
			t.Errorf("duplicate page slug %q in %+v", m.Page, maps)
		}
		seen[m.Page] = true
	}
	if maps[0].Page != "overview" {
		t.Errorf("README maps to overview, got %q", maps[0].Page)
	}
}

func initRepoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Acme\n")
	writeFile(t, dir, "docs/guide.md", "# Guide\n")
	writeFile(t, dir, "AGENTS.md", "# Agents\n")
	writeFile(t, dir, "CONTRIBUTING.md", "# Contributing\n")
	writeFile(t, dir, "api/openapi.yaml", "openapi: 3.0.0\n")
	return dir
}

func TestInitYesDetectsAndMaps(t *testing.T) {
	dir := initRepoDir(t)
	fp := &fakePlatform{}
	srv := fp.serve(t)
	t.Setenv(config.EnvAPIURL, srv.URL)

	stdout, _, err := runRoot(t, "init", "--yes", "--site", "acme", "--space", "product-docs", "--dir", dir, "--token", "sk_live_x")
	if err != nil {
		t.Fatalf("init --yes: %v", err)
	}
	if len(fp.requests) != 0 {
		t.Errorf("init must not touch the platform (sync creates spaces); got %v", fp.requests)
	}
	proj, err := config.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Sources) != 1 || proj.Sources[0].Source != "api/openapi.yaml" || proj.Sources[0].Page != "api-reference" {
		t.Errorf("sources = %+v", proj.Sources)
	}
	var files []string
	for _, d := range proj.Documents {
		files = append(files, d.File)
	}
	if !reflect.DeepEqual(files, []string{"README.md", "docs/guide.md"}) {
		t.Errorf("documents = %v, want README.md + docs/guide.md only", files)
	}
	if proj.ReleaseNotes.Space != "changelog" {
		t.Errorf("releaseNotes.space = %q, want changelog (never the docs space)", proj.ReleaseNotes.Space)
	}
	if proj.Spaces.Default != "product-docs" || proj.APIURL != "" {
		t.Errorf("spaces.default = %q apiUrl = %q", proj.Spaces.Default, proj.APIURL)
	}
	if !strings.Contains(stdout, "mapped 1 OpenAPI spec(s)") || !strings.Contains(stdout, "mapped 2 Markdown doc(s)") {
		t.Errorf("stdout should summarize what was mapped:\n%s", stdout)
	}
}

func TestInitCIModeIsNonInteractive(t *testing.T) {
	dir := initRepoDir(t)
	if _, _, err := runRoot(t, "init", "--ci", "--site", "acme", "--dir", dir); err != nil {
		t.Fatalf("init --ci: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, config.ProjectFileName)); err != nil {
		t.Errorf("--ci must write without prompting: %v", err)
	}
}

func TestInitRecordsAPIURLOnlyWhenPassed(t *testing.T) {
	dir := initRepoDir(t)
	stdout, _, err := runRoot(t, "init", "--yes", "--site", "acme", "--dir", dir, "--api-url", "https://gravity.internal.example", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "apiUrl: https://gravity.internal.example") {
		t.Errorf("--api-url must be recorded:\n%s", stdout)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := initRepoDir(t)
	stdout, _, err := runRoot(t, "init", "--yes", "--site", "acme", "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "site: acme") {
		t.Errorf("dry run should print the manifest:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, config.ProjectFileName)); !os.IsNotExist(err) {
		t.Errorf("dry run must not write the file (stat err %v)", err)
	}
}

func TestInitYesRequiresSite(t *testing.T) {
	_, _, err := runRoot(t, "init", "--yes", "--dir", t.TempDir())
	if CodeFor(err) != CodeError || !strings.Contains(err.Error(), "site is required") {
		t.Errorf("err = %v", err)
	}
}

func TestInitMigrateIsLossless(t *testing.T) {
	dir := t.TempDir()
	original := renderManifest(fullManifest())
	original = strings.Replace(original, "version: 1\n", "", 1)
	writeFile(t, dir, config.ProjectFileName, original)
	before, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("load original: %v", err)
	}

	if _, _, err := runRoot(t, "init", "--migrate", "--dir", dir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	after, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("load migrated: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("migration changed the effective config\nbefore: %+v\n after: %+v", before, after)
	}
}

func TestInitMigrateUpgradesLegacyAndRemovedKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, config.ProjectFileName, `site: acme
space: handbook
sources:
  - source: openapi.yaml
    page: api-reference
    generator: legacy-gen
knowledge:
  namespace: acme
  scope: acme-api
discovery:
  include: [src/**]
`)
	if _, err := config.LoadProject(dir); err == nil {
		t.Fatal("the removed keys must fail a normal load")
	}
	stdout, _, err := runRoot(t, "init", "--migrate", "--dir", dir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, want := range []string{"dropped sources[].generator", "dropped knowledge.scope", "moved the legacy top-level `space: handbook`"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("migrate output missing %q:\n%s", want, stdout)
		}
	}
	proj, err := config.LoadProject(dir)
	if err != nil {
		t.Fatalf("migrated file must load: %v", err)
	}
	if proj.Spaces.Default != "handbook" || proj.ReleaseNotes.Space != "handbook" || proj.Knowledge.Namespace != "acme" ||
		len(proj.Discovery.Include) != 1 || proj.Sources[0].Page != "api-reference" {
		t.Errorf("migrated = %+v", proj)
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
