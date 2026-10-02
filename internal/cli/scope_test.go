package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
)

func TestResolveRepoScope(t *testing.T) {
	key := "github.com/acme/api"
	proj := &config.Project{
		Product: config.Product{Repo: "api"},
		Spaces: config.Spaces{
			Default: "guides",
			Shared:  []string{"platform"},
			Declare: []config.SpaceDecl{{Slug: "developers"}},
		},
		Sources:   []config.SourceMap{{Source: "openapi.yaml", Space: "platform", Page: "reference"}},
		Documents: []config.DocMap{{File: "CHANGELOG.md", As: "release", Space: "news"}},
	}

	attributed, err := resolveRepoScope(key, true, proj, "")
	if err != nil || attributed.bySpaces() || !attributed.includes("anything", "x", &key) {
		t.Fatalf("attributed scope = %+v, %v", attributed, err)
	}
	other := "github.com/acme/web"
	if attributed.includes("guides", "x", &other) || attributed.includes("guides", "x", nil) {
		t.Error("an attributed scope must exclude other repos' and unattributed pages")
	}

	bySpace, err := resolveRepoScope(key, false, proj, "")
	if err != nil || !bySpace.bySpaces() {
		t.Fatalf("space scope = %+v, %v", bySpace, err)
	}
	for _, tc := range []struct {
		space, slug string
		want        bool
	}{
		{"guides", "intro", true},
		{"developers", "sdk", true},
		{"platform", "api/reference", true},
		{"platform", "web/reference", false},
		{"news", "v1", false},
		{"marketing", "home", false},
	} {
		if got := bySpace.includes(tc.space, tc.slug, nil); got != tc.want {
			t.Errorf("includes(%s/%s) = %v, want %v", tc.space, tc.slug, got, tc.want)
		}
	}

	override, err := resolveRepoScope("", false, proj, "marketing")
	if err != nil || !override.includes("marketing", "home", nil) || override.includes("guides", "intro", nil) {
		t.Errorf("--space must replace the declared spaces: %+v %v", override, err)
	}

	if _, err := resolveRepoScope("", false, nil, ""); !errors.Is(err, errNoScope) {
		t.Errorf("no manifest and no attribution must refuse to fall back to the whole site, got %v", err)
	}
}

const scopedSpec = `openapi: 3.0.0
info: {title: T, version: "1"}
paths:
  /users:
    get: {summary: List users}
    post: {summary: Create a user}
`

func apiBlock(pageID, space, page, method, path, summary string) map[string]any {
	return map[string]any{
		"pageId": pageID, "pageSlug": page, "spaceSlug": space, "blockId": pageID + method,
		"content": map[string]any{"method": method, "path": path, "summary": summary},
	}
}

func TestCheckAPIReadsSourcesAndScopesToRepo(t *testing.T) {
	dir := newGitRepo(t, map[string]string{
		config.ProjectFileName: "site: acme\nspaces:\n  default: guides\nsources:\n  - source: openapi.yaml\n    space: dev\n    page: api-reference\n",
		"openapi.yaml":         scopedSpec,
	})
	chdirTemp(t, dir)
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/api-blocks": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"blocks": []any{
				apiBlock("p1", "dev", "api-reference", "GET", "/users", "List users"),
				apiBlock("p2", "other-team", "reference", "DELETE", "/billing", "Remove billing"),
			}})
		},
		"GET /api/v1/sites/acme": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site":  map[string]any{"slug": "acme"},
				"pages": []any{map[string]any{"id": "p1", "slug": "api-reference", "spaceSlug": "dev"}, map[string]any{"id": "p2", "slug": "reference", "spaceSlug": "other-team"}},
			})
		},
	}}
	srv := fp.serve(t)

	stdout, _, err := runRoot(t, "check", "api", "--json", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit = %d (%v), want findings\n%s", CodeFor(err), err, stdout)
	}
	var res output.Result
	if jerr := json.Unmarshal([]byte(stdout), &res); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, stdout)
	}
	if len(res.Findings) != 1 || res.Findings[0].Kind != "undocumented" || !strings.Contains(res.Findings[0].Title, "POST /users") {
		t.Errorf("findings = %+v, want only POST /users undocumented (the other team's block must not be orphaned)", res.Findings)
	}
	joined := strings.Join(res.Notes, "\n")
	if !strings.Contains(joined, "openapi.yaml: compared 2 spec operation(s) against 1 documented block(s)") {
		t.Errorf("notes = %s", joined)
	}
	if !strings.Contains(joined, "1 of 2 api block(s)") {
		t.Errorf("expected a scope note, got %s", joined)
	}
}

func TestCheckDocsRefusesWholeSiteFallback(t *testing.T) {
	dir := newGitRepo(t, nil)
	chdirTemp(t, dir)
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []api.Page{{Slug: "x", SpaceSlug: "marketing"}}})
		},
	}}
	srv := fp.serve(t)
	_, _, err := runRoot(t, "check", "docs", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "acme")
	if CodeFor(err) != CodeError || !errors.Is(err, errNoScope) {
		t.Errorf("err = %v (code %d), want the no-scope refusal", err, CodeFor(err))
	}
}

func TestCheckDocsScopesToDeclaredSpaces(t *testing.T) {
	dir := newGitRepo(t, map[string]string{config.ProjectFileName: "site: acme\nspaces:\n  default: guides\n"})
	chdirTemp(t, dir)
	stale := &api.SourceBinding{Kind: "cli", Ref: "README.md", Hash: "sha256:" + strings.Repeat("0", 64)}
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []api.Page{
				{Slug: "intro", SpaceSlug: "guides", Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
				{Slug: "home", SpaceSlug: "marketing", Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
			}})
		},
	}}
	srv := fp.serve(t)
	stdout, _, err := runRoot(t, "check", "docs", "--format", "json", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit = %d (%v)\n%s", CodeFor(err), err, stdout)
	}
	var res output.Result
	_ = json.Unmarshal([]byte(stdout), &res)
	if len(res.Findings) != 1 || res.Findings[0].SuggestedPage != "guides/intro" {
		t.Errorf("findings = %+v, want only the guides page", res.Findings)
	}
}
