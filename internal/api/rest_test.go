package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		// The server wraps the space in an envelope: { space: {...} }.
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
	// Response decodes into *ReleaseNotesResponse.
	if resp.PageSlug != "command-reference" || resp.Status != "proposed" || resp.ProposalID != "prop_2" {
		t.Errorf("unexpected response: %+v", resp)
	}

	// Top-level page fields.
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
