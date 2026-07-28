package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestWhoAmI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/whoami" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_live_abc" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organizationId":   "org_1",
			"organizationName": "Acme",
			"defaultSiteSlug":  "docs",
			"keyHint":          "sk_live_…abc",
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "sk_live_abc")
	who, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if who.OrganizationName != "Acme" {
		t.Errorf("org name = %q", who.OrganizationName)
	}
	if who.DefaultSiteSlug == nil || *who.DefaultSiteSlug != "docs" {
		t.Errorf("default site = %v", who.DefaultSiteSlug)
	}
}

func TestWhoAmINullDefaultSite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organizationId":   "org_1",
			"organizationName": "Acme",
			"defaultSiteSlug":  nil,
			"keyHint":          "sk_live_…abc",
		})
	}))
	defer srv.Close()
	c := api.New(srv.URL, "t")
	who, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if who.DefaultSiteSlug != nil {
		t.Errorf("expected nil default site, got %v", who.DefaultSiteSlug)
	}
}

func TestSites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_live_abc" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sites": []map[string]any{
				{"id": "s1", "slug": "docs", "name": "Docs", "description": "Public docs", "visibility": "public"},
				{"id": "s2", "slug": "internal", "name": "Internal", "visibility": "private"},
			},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "sk_live_abc")
	sites, err := c.Sites(context.Background())
	if err != nil {
		t.Fatalf("sites: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(sites))
	}
	if sites[0].Slug != "docs" || sites[0].Name != "Docs" || sites[0].Description != "Public docs" {
		t.Errorf("site[0] = %+v", sites[0])
	}
	if sites[1].Slug != "internal" || sites[1].Visibility != "private" {
		t.Errorf("site[1] = %+v", sites[1])
	}
}

func TestDeletePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/sites/docs/spaces/cli/pages/agents-md-ed44" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageId": "p2", "pageSlug": "agents-md-ed44",
			"proposalId": "prop_9", "status": "proposed", "reviewUrl": "/app/proposals?id=prop_9",
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	resp, err := c.DeletePage(context.Background(), "docs", "cli", "agents-md-ed44")
	if err != nil {
		t.Fatalf("delete page: %v", err)
	}
	if resp.Status != "proposed" || resp.ProposalID != "prop_9" {
		t.Errorf("response = %+v", resp)
	}
}

func TestPagesQueryParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/docs/pages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("space"); got != "changelog" {
			t.Errorf("expected space=changelog, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pages": []map[string]any{
				{
					"id": "p1", "slug": "intro", "title": "Intro", "spaceSlug": "changelog",
					"version": 3, "releasedAt": "2024-01-01",
					"blocks": []map[string]any{
						{
							"id": "b1", "type": "api", "ownership": "machine", "content": map[string]any{}, "position": 0,
							"sourceBinding": map[string]any{"kind": "file", "ref": "openapi.yaml", "hash": "deadbeef", "generator": "x"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	pages, err := c.Pages(context.Background(), "docs", "changelog")
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if pages[0].Version == nil || *pages[0].Version != 3 {
		t.Errorf("version = %v", pages[0].Version)
	}
	if len(pages[0].Blocks) != 1 || pages[0].Blocks[0].SourceBinding == nil {
		t.Fatalf("expected one block with a binding")
	}
	if pages[0].Blocks[0].SourceBinding.Hash != "deadbeef" {
		t.Errorf("binding hash = %q", pages[0].Blocks[0].SourceBinding.Hash)
	}
}

func TestPagesNoSpace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pages": []any{}})
	}))
	defer srv.Close()
	c := api.New(srv.URL, "t")
	if _, err := c.Pages(context.Background(), "docs", ""); err != nil {
		t.Fatalf("pages: %v", err)
	}
}

func TestAPIBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/docs/api-blocks" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocks": []map[string]any{
				{
					"pageId": "p1", "pageSlug": "users", "spaceSlug": "api", "blockId": "b1",
					"ownership": "machine",
					"content":   map[string]any{"method": "GET", "path": "/users", "summary": "List users"},
				},
			},
		})
	}))
	defer srv.Close()
	c := api.New(srv.URL, "t")
	blocks, err := c.APIBlocks(context.Background(), "docs")
	if err != nil {
		t.Fatalf("api blocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Content.Method != "GET" || blocks[0].Content.Path != "/users" {
		t.Errorf("unexpected blocks: %+v", blocks)
	}
}

func TestCreateReleaseNotesRequestShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/sites/docs/release-notes" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected JSON content type, got %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageId": "p9", "pageSlug": "v1-1-0", "proposalId": "prop_1",
			"status": "proposed", "reviewUrl": "https://app/r/prop_1",
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	resp, err := c.CreateReleaseNotes(context.Background(), "docs", api.ReleaseNotesRequest{
		SpaceSlug: "changelog",
		Title:     "v1.1.0",
		Summary:   "A release",
		Sections: []api.ReleaseNoteSection{
			{Heading: "Added", Items: []string{"thing one", "thing two"}},
		},
	})
	if err != nil {
		t.Fatalf("create release notes: %v", err)
	}
	if resp.Status != "proposed" || resp.ReviewURL == "" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if captured["spaceSlug"] != "changelog" || captured["title"] != "v1.1.0" {
		t.Errorf("request body not shaped correctly: %+v", captured)
	}
	secs, ok := captured["sections"].([]any)
	if !ok || len(secs) != 1 {
		t.Fatalf("expected 1 section in body, got %v", captured["sections"])
	}
}

func TestEnsureSpaceRequestShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/sites/docs/spaces" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_live_xyz" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"space": map[string]any{"id": "sp_1", "slug": "cli", "name": "Gravity CLI"},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "sk_live_xyz")
	sp, err := c.EnsureSpace(context.Background(), "docs", api.SpaceUpsertRequest{
		Slug:        "cli",
		Name:        "Gravity CLI",
		Description: "Living reference for the CLI",
	})
	if err != nil {
		t.Fatalf("ensure space: %v", err)
	}
	if sp.Slug != "cli" || sp.Name != "Gravity CLI" {
		t.Errorf("decoded space wrong: %+v", sp)
	}
	if captured["slug"] != "cli" || captured["name"] != "Gravity CLI" {
		t.Errorf("request body not shaped correctly: %+v", captured)
	}
	if captured["description"] != "Living reference for the CLI" {
		t.Errorf("description not sent: %+v", captured["description"])
	}
}

func TestEnsureSpaceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "forbidden", "message": "not authorized for site"},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "bad")
	_, err := c.EnsureSpace(context.Background(), "docs", api.SpaceUpsertRequest{Slug: "cli"})
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "forbidden" || apiErr.Message != "not authorized for site" {
		t.Errorf("envelope not parsed: code=%q msg=%q", apiErr.Code, apiErr.Message)
	}
}

func TestUpsertPageRequestShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/sites/docs/pages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_live_pg" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected JSON content type, got %q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageId": "p9", "pageSlug": "command-reference", "proposalId": "prop_2",
			"status": "proposed", "reviewUrl": "https://app/r/prop_2",
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "sk_live_pg")
	resp, err := c.UpsertPage(context.Background(), "docs", api.PageUpsertRequest{
		SpaceSlug: "cli",
		Slug:      "command-reference",
		Title:     "Command reference",
		Blocks: []api.BlockInput{
			{
				Key:       "cmdref-0",
				Type:      "heading",
				Ownership: "machine",
				Content:   map[string]any{"text": "gravity", "level": 1},
				SourceBinding: &api.SourceBinding{
					Kind:      "cli",
					Ref:       "internal/cli/root.go",
					Hash:      "sha256:deadbeef",
					Generator: "gravity selfdoc v0.1.0",
				},
				Position: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert page: %v", err)
	}
	if resp.PageSlug != "command-reference" || resp.Status != "proposed" || resp.ProposalID != "prop_2" {
		t.Errorf("unexpected response: %+v", resp)
	}

	if captured["spaceSlug"] != "cli" || captured["slug"] != "command-reference" || captured["title"] != "Command reference" {
		t.Errorf("page envelope not shaped correctly: %+v", captured)
	}

	blocks, ok := captured["blocks"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("expected 1 block in body, got %v", captured["blocks"])
	}
	blk, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatalf("block not an object: %v", blocks[0])
	}
	if blk["key"] != "cmdref-0" {
		t.Errorf("block key = %v, want cmdref-0", blk["key"])
	}
	if blk["type"] != "heading" || blk["ownership"] != "machine" {
		t.Errorf("block type/ownership wrong: %+v", blk)
	}
	bind, ok := blk["sourceBinding"].(map[string]any)
	if !ok {
		t.Fatalf("block sourceBinding missing/not object: %v", blk["sourceBinding"])
	}
	if bind["kind"] != "cli" || bind["ref"] != "internal/cli/root.go" {
		t.Errorf("sourceBinding not shaped correctly: %+v", bind)
	}
	if bind["hash"] != "sha256:deadbeef" {
		t.Errorf("sourceBinding hash = %v", bind["hash"])
	}
}

func TestUpsertPageAudiences(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "x", "status": "proposed"})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	_, err := c.UpsertPage(context.Background(), "docs", api.PageUpsertRequest{
		SpaceSlug: "docs", Slug: "x", Title: "X",
		Blocks: []api.BlockInput{
			{Key: "a", Type: "prose", Ownership: "hybrid", Audiences: []string{"developers"}, Content: map[string]any{"text": "dev only"}},
			{Key: "b", Type: "prose", Ownership: "hybrid", Content: map[string]any{"text": "everyone"}},
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	blocks, ok := captured["blocks"].([]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v", captured["blocks"])
	}
	first, _ := blocks[0].(map[string]any)
	aud, ok := first["audiences"].([]any)
	if !ok || len(aud) != 1 || aud[0] != "developers" {
		t.Errorf("block a audiences = %v, want [developers]", first["audiences"])
	}
	second, _ := blocks[1].(map[string]any)
	if _, present := second["audiences"]; present {
		t.Errorf("block b should omit audiences when empty, got %v", second["audiences"])
	}
}

func TestUpsertPageError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "invalid_block", "message": "block 0 has invalid type"},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	_, err := c.UpsertPage(context.Background(), "docs", api.PageUpsertRequest{
		SpaceSlug: "cli", Slug: "x", Title: "X",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
	if apiErr.Code != "invalid_block" || apiErr.Message != "block 0 has invalid type" {
		t.Errorf("envelope not parsed: code=%q msg=%q", apiErr.Code, apiErr.Message)
	}
}

func TestSetupPingV2RoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/setup/ping" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"ok": true, "organizationName": "Acme", "keyHint": "a1b2", "defaultSiteSlug": "orbit",
			"repo": {"id": "cr_9f13", "firstSeenAt": "2026-05-02T11:04:19.220Z"},
			"siblings": [{
				"name": "orbit-web", "productSlug": "orbit", "remoteKey": "github.com/Acme/orbit-web",
				"spaces": ["platform", "guides"], "collections": ["orbit-web"],
				"lastPingAt": "2026-07-26T09:12:00.000Z", "lastWriteAt": "2026-07-26T09:14:31.881Z",
				"cliVersion": "0.6.0"
			}],
			"serverFeatures": {"repos": true, "inventory": true, "page-languages": false}
		}`))
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").SetupPing(context.Background(), api.SetupPingRequest{
		CLI:    api.PingCLI{Version: "0.6.0", OS: "darwin", Arch: "arm64"},
		Config: api.PingConfig{APIURL: "https://docs.acme.com", Site: "orbit", Space: "platform"},
		Repo: api.PingRepo{
			Name: "orbit-api", Remote: "github.com/Acme/orbit-api", Branch: "live",
			Commit: "9f2c1ab4e7d0", RemoteKeySource: api.RemoteKeySourceRemote,
		},
		ConfigFull: map[string]any{"version": 1, "site": "orbit"},
		ConfigYAML: "version: 1\nsite: orbit\n",
		DocSources: &api.PingDocSources{
			Sources: 1, Documents: 1,
			Spaces: []string{"platform", "changelog"}, Kinds: []string{"openapi"},
			Languages: []string{"fr", "es"}, Units: "service",
		},
	})
	if err != nil {
		t.Fatalf("SetupPing: %v", err)
	}

	repo, _ := captured["repo"].(map[string]any)
	if repo["commit"] != "9f2c1ab4e7d0" || repo["remoteKeySource"] != "remote" {
		t.Errorf("repo not shaped correctly: %+v", repo)
	}
	if captured["configYaml"] != "version: 1\nsite: orbit\n" {
		t.Errorf("configYaml = %v", captured["configYaml"])
	}
	if _, ok := captured["configFull"].(map[string]any); !ok {
		t.Errorf("configFull = %v", captured["configFull"])
	}
	docSources, _ := captured["docSources"].(map[string]any)
	if docSources["units"] != "service" || docSources["sources"] != float64(1) {
		t.Errorf("docSources = %+v", docSources)
	}

	if resp.Repo == nil || resp.Repo.ID != "cr_9f13" || resp.Repo.FirstSeenAt == "" {
		t.Fatalf("repo registration = %+v", resp.Repo)
	}
	if len(resp.Siblings) != 1 {
		t.Fatalf("siblings = %+v", resp.Siblings)
	}
	sib := resp.Siblings[0]
	if sib.Name != "orbit-web" || sib.RemoteKey != "github.com/Acme/orbit-web" || sib.CLIVersion != "0.6.0" {
		t.Errorf("sibling = %+v", sib)
	}
	if len(sib.Spaces) != 2 || len(sib.Collections) != 1 {
		t.Errorf("sibling spaces/collections = %+v", sib)
	}
	if !resp.ServerFeatures["repos"] || resp.ServerFeatures["page-languages"] {
		t.Errorf("serverFeatures = %+v", resp.ServerFeatures)
	}
}

func TestSetupPingV1PlatformOmitsV2Fields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"ok":true,"organizationName":"Acme","keyHint":"a1b2","defaultSiteSlug":"orbit"}`))
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").SetupPing(context.Background(), api.SetupPingRequest{
		CLI:    api.PingCLI{Version: "0.6.0"},
		Config: api.PingConfig{APIURL: "https://docs.acme.com"},
		Repo:   api.PingRepo{Name: "orbit-api"},
	})
	if err != nil {
		t.Fatalf("SetupPing: %v", err)
	}
	if resp.Repo != nil || resp.Siblings != nil || resp.ServerFeatures != nil {
		t.Errorf("v1 response should decode with empty v2 fields: %+v", resp)
	}
	for _, key := range []string{"configFull", "configYaml", "docSources"} {
		if _, present := captured[key]; present {
			t.Errorf("%s should be omitted when unset; got %v", key, captured[key])
		}
	}
	repo, _ := captured["repo"].(map[string]any)
	for _, key := range []string{"commit", "remoteKeySource"} {
		if _, present := repo[key]; present {
			t.Errorf("repo.%s should be omitted when unset; got %v", key, repo[key])
		}
	}
}

func TestListPagesFilters(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"pages":[{
			"id": "p1", "slug": "orbit-api/invoicing", "title": "Invoicing", "spaceSlug": "platform",
			"version": 2, "releasedAt": "2026-07-01", "blocks": [],
			"repoId": "cr_9f13", "repoRemoteKey": "github.com/Acme/orbit-api",
			"languages": [
				{"language": "fr", "status": "live", "outdated": false, "updatedAt": "2026-07-20T00:00:00.000Z"},
				{"language": "es", "status": "draft", "outdated": true, "updatedAt": "2026-06-02T00:00:00.000Z"}
			]
		}]}`))
	}))
	defer srv.Close()

	pages, err := api.New(srv.URL, "tok").ListPages(context.Background(), "orbit", api.PageListOptions{
		SpaceSlug: "platform",
		Repo:      "github.com/Acme/orbit-api",
		Languages: true,
	})
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if gotQuery.Get("space") != "platform" {
		t.Errorf("space = %q", gotQuery.Get("space"))
	}
	if gotQuery.Get("repo") != "github.com/Acme/orbit-api" {
		t.Errorf("repo = %q", gotQuery.Get("repo"))
	}
	if gotQuery.Get("languages") != "1" {
		t.Errorf("languages = %q", gotQuery.Get("languages"))
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %+v", pages)
	}
	p := pages[0]
	if p.RepoID == nil || *p.RepoID != "cr_9f13" {
		t.Errorf("repoId = %v", p.RepoID)
	}
	if p.RepoRemoteKey == nil || *p.RepoRemoteKey != "github.com/Acme/orbit-api" {
		t.Errorf("repoRemoteKey = %v", p.RepoRemoteKey)
	}
	if len(p.Languages) != 2 {
		t.Fatalf("languages = %+v", p.Languages)
	}
	if p.Languages[0].Language != "fr" || p.Languages[0].Status != "live" || p.Languages[0].Outdated {
		t.Errorf("languages[0] = %+v", p.Languages[0])
	}
	if !p.Languages[1].Outdated || p.Languages[1].Status != "draft" {
		t.Errorf("languages[1] = %+v", p.Languages[1])
	}
}

func TestPagesUnattributedStayNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"pages":[{"id":"p1","slug":"intro","title":"Intro","spaceSlug":"docs",
			"version":1,"releasedAt":"","blocks":[],"repoId":null,"repoRemoteKey":null}]}`))
	}))
	defer srv.Close()

	pages, err := api.New(srv.URL, "tok").Pages(context.Background(), "orbit", "")
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if pages[0].RepoID != nil || pages[0].RepoRemoteKey != nil {
		t.Errorf("expected nil attribution, got %v / %v", pages[0].RepoID, pages[0].RepoRemoteKey)
	}
	if pages[0].Languages != nil {
		t.Errorf("expected no languages projection, got %+v", pages[0].Languages)
	}
}

func TestAPIBlocksKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"blocks":[
			{"pageId":"p1","pageSlug":"users","spaceSlug":"api","blockId":"b1",
			 "key":"api:GET:/v1/users","ownership":"machine",
			 "content":{"method":"GET","path":"/v1/users","summary":"List users"}},
			{"pageId":"p1","pageSlug":"users","spaceSlug":"api","blockId":"b2",
			 "key":null,"ownership":"human",
			 "content":{"method":"POST","path":"/v1/users","summary":"Create"}}
		]}`))
	}))
	defer srv.Close()

	blocks, err := api.New(srv.URL, "tok").APIBlocks(context.Background(), "orbit")
	if err != nil {
		t.Fatalf("APIBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[0].Key != "api:GET:/v1/users" {
		t.Errorf("block key = %q", blocks[0].Key)
	}
	if blocks[1].Key != "" {
		t.Errorf("null key should decode to empty, got %q", blocks[1].Key)
	}
}

func TestUpsertPageRepoAndLanguages(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "invoicing", "status": "proposed"})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "tok")
	_, err := c.UpsertPage(context.Background(), "orbit", api.PageUpsertRequest{
		SpaceSlug: "platform", Slug: "invoicing", Title: "Invoicing",
		Blocks:    []api.BlockInput{{Key: "a", Type: "prose", Ownership: "machine", Content: map[string]any{"text": "x"}}},
		Repo:      &api.RepoRef{RemoteKey: "github.com/Acme/orbit-api", Name: "orbit-api"},
		Languages: []string{"fr", "es"},
	})
	if err != nil {
		t.Fatalf("upsert page: %v", err)
	}
	repo, ok := captured["repo"].(map[string]any)
	if !ok || repo["remoteKey"] != "github.com/Acme/orbit-api" || repo["name"] != "orbit-api" {
		t.Errorf("repo = %v", captured["repo"])
	}
	langs, ok := captured["languages"].([]any)
	if !ok || len(langs) != 2 || langs[0] != "fr" {
		t.Errorf("languages = %v", captured["languages"])
	}
}

func TestUpsertPageOmitsRepoAndLanguagesWhenUnset(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "x", "status": "proposed"})
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").UpsertPage(context.Background(), "orbit", api.PageUpsertRequest{
		SpaceSlug: "platform", Slug: "x", Title: "X",
	})
	if err != nil {
		t.Fatalf("upsert page: %v", err)
	}
	for _, key := range []string{"repo", "languages"} {
		if _, present := captured[key]; present {
			t.Errorf("%s should be omitted when unset; got %v", key, captured[key])
		}
	}
}

func TestCreateReleaseNotesRepoAndLanguages(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "v1-1-0", "status": "proposed"})
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").CreateReleaseNotes(context.Background(), "orbit", api.ReleaseNotesRequest{
		SpaceSlug: "changelog", Title: "v1.1.0",
		Repo:      &api.RepoRef{RemoteKey: "github.com/Acme/orbit-api"},
		Languages: []string{"fr"},
	})
	if err != nil {
		t.Fatalf("create release notes: %v", err)
	}
	repo, ok := captured["repo"].(map[string]any)
	if !ok || repo["remoteKey"] != "github.com/Acme/orbit-api" {
		t.Errorf("repo = %v", captured["repo"])
	}
	if _, present := repo["name"]; present {
		t.Errorf("repo.name should be omitted when unset; got %v", repo["name"])
	}
	langs, ok := captured["languages"].([]any)
	if !ok || len(langs) != 1 {
		t.Errorf("languages = %v", captured["languages"])
	}
}

func TestErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "unauthorized", "message": "bad token"},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "bad")
	_, err := c.WhoAmI(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
	if apiErr.Code != "unauthorized" || apiErr.Message != "bad token" {
		t.Errorf("envelope not parsed: code=%q msg=%q", apiErr.Code, apiErr.Message)
	}
	if !apiErr.IsAuth() {
		t.Error("expected IsAuth() to be true for 401")
	}
}
