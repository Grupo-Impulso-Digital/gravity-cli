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

func TestResolveRepoScopeSpaceOverrideWinsOnAttributedSite(t *testing.T) {
	key := "github.com/acme/api"
	other := "github.com/acme/web"
	proj := &config.Project{Spaces: config.Spaces{Default: "guides"}}
	scope, err := resolveRepoScope(key, true, proj, "developers")
	if err != nil || !scope.bySpaces() {
		t.Fatalf("--space on an attributed site must scope by that space: %+v %v", scope, err)
	}
	if !scope.includes("developers", "sdk", &key) || !scope.includes("developers", "intro", nil) {
		t.Error("--space must include this repo's and unattributed pages in that space")
	}
	if scope.includes("guides", "intro", &key) {
		t.Error("--space must exclude the repo's pages in other spaces")
	}
	if scope.includes("developers", "web", &other) {
		t.Error("--space must still exclude pages another repo writes")
	}
	if !strings.Contains(scope.describe(), "--space developers") {
		t.Errorf("describe = %q", scope.describe())
	}
}

func TestUnsyncedRepoOnMultiRepoSiteFallsBackToDeclaredSpaces(t *testing.T) {
	key := "github.com/acme/api"
	other := "github.com/acme/web"
	pages := []api.Page{
		{Slug: "home", SpaceSlug: "guides", RepoRemoteKey: &other},
		{Slug: "intro", SpaceSlug: "guides"},
	}
	attributed := attributedTo(pageRemoteKeys(pages), key)
	if attributed {
		t.Fatal("pages another repo writes must not count as this repo's attribution")
	}
	scope, err := resolveRepoScope(key, attributed, &config.Project{Spaces: config.Spaces{Default: "guides"}}, "")
	if err != nil || !scope.bySpaces() {
		t.Fatalf("scope = %+v %v, want the declared spaces", scope, err)
	}
	got := scope.pages(pages)
	if len(got) != 1 || got[0].Slug != "intro" {
		t.Errorf("scoped pages = %+v, want only the unattributed guides/intro", got)
	}
	if !attributedTo(pageRemoteKeys(append(pages, api.Page{RepoRemoteKey: &key})), key) {
		t.Error("a page this repo writes must count as attributed")
	}
}

func TestCheckDocsSpaceFlagOverridesAttribution(t *testing.T) {
	dir := newGitRepo(t, map[string]string{config.ProjectFileName: "site: acme\nproduct:\n  slug: acme\n  repo: api\nspaces:\n  default: guides\n"})
	chdirTemp(t, dir)
	mine := "acme/api"
	stale := &api.SourceBinding{Kind: "cli", Ref: "README.md", Hash: "sha256:" + strings.Repeat("0", 64)}
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []api.Page{
				{Slug: "intro", SpaceSlug: "guides", RepoRemoteKey: &mine, Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
				{Slug: "sdk", SpaceSlug: "developers", Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
			}})
		},
	}}
	srv := fp.serve(t)
	stdout, _, err := runRoot(t, "check", "docs", "--json", "--space", "developers", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit = %d (%v)\n%s", CodeFor(err), err, stdout)
	}
	var res output.Result
	_ = json.Unmarshal([]byte(stdout), &res)
	if len(res.Findings) != 1 || res.Findings[0].SuggestedPage != "developers/sdk" {
		t.Errorf("findings = %+v, want only developers/sdk", res.Findings)
	}
}

func TestCheckDocsUnsyncedRepoIsNotVacuousOnMultiRepoSite(t *testing.T) {
	dir := newGitRepo(t, map[string]string{config.ProjectFileName: "site: acme\nproduct:\n  slug: acme\n  repo: api\nspaces:\n  default: guides\n"})
	chdirTemp(t, dir)
	other := "acme/web"
	stale := &api.SourceBinding{Kind: "cli", Ref: "README.md", Hash: "sha256:" + strings.Repeat("0", 64)}
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []api.Page{
				{Slug: "web", SpaceSlug: "guides", RepoRemoteKey: &other, Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
				{Slug: "intro", SpaceSlug: "guides", Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
				{Slug: "home", SpaceSlug: "marketing", Blocks: []api.ContentBlock{{Type: "prose", Ownership: "machine", SourceBinding: stale}}},
			}})
		},
	}}
	srv := fp.serve(t)
	stdout, _, err := runRoot(t, "check", "docs", "--json", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit = %d (%v), want findings on the declared space\n%s", CodeFor(err), err, stdout)
	}
	var res output.Result
	_ = json.Unmarshal([]byte(stdout), &res)
	if len(res.Findings) != 1 || res.Findings[0].SuggestedPage != "guides/intro" {
		t.Errorf("findings = %+v, want only guides/intro (not the other repo's page, not marketing)", res.Findings)
	}
}

func TestCheckAPISpaceFlagOverridesAttribution(t *testing.T) {
	dir := newGitRepo(t, map[string]string{
		config.ProjectFileName: "site: acme\nproduct:\n  slug: acme\n  repo: api\nspaces:\n  default: guides\n",
		"openapi.yaml":         scopedSpec,
	})
	chdirTemp(t, dir)
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme/api-blocks": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"blocks": []any{
				apiBlock("p1", "guides", "api", "GET", "/users", "List users"),
				apiBlock("p2", "dev", "reference", "GET", "/users", "List users"),
			}})
		},
		"GET /api/v1/sites/acme": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site": map[string]any{"slug": "acme"},
				"pages": []any{
					map[string]any{"id": "p1", "slug": "api", "spaceSlug": "guides", "repoRemoteKey": "acme/api"},
					map[string]any{"id": "p2", "slug": "reference", "spaceSlug": "dev"},
				},
			})
		},
	}}
	srv := fp.serve(t)
	stdout, _, err := runRoot(t, "check", "api", "--json", "--space", "dev", "--openapi", "openapi.yaml", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit = %d (%v)\n%s", CodeFor(err), err, stdout)
	}
	var res output.Result
	_ = json.Unmarshal([]byte(stdout), &res)
	joined := strings.Join(res.Notes, "\n")
	if !strings.Contains(joined, "--space dev") || !strings.Contains(joined, "1 of 2 api block(s)") {
		t.Errorf("notes = %s, want the --space dev scope", joined)
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
