package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
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
						{"id": "b1", "type": "api", "ownership": "machine", "content": map[string]any{}, "position": 0,
							"sourceBinding": map[string]any{"kind": "file", "ref": "openapi.yaml", "hash": "deadbeef", "generator": "x"}},
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
	apiErr, ok := err.(*api.APIError)
	if !ok {
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
