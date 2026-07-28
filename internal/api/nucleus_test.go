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

func TestRecallSiteScoped(t *testing.T) {
	var gotPath string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"scope": {"type": "site", "id": "st_1"},
			"hits": [{
				"id": "mem_1", "scope": {"type": "site", "id": "st_1"}, "kind": "procedure",
				"status": "active", "title": "Invoice numbering", "body": "Invoice numbers are per-org.",
				"tags": ["billing", "ns:orbit"], "confidence": 0.8,
				"sources": [{"refType": "page", "refId": "pg_1"}],
				"score": 0.914, "matchedBy": ["semantic", "keyword"]
			}]
		}`))
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").Recall(context.Background(), "orbit", api.RecallRequest{
		Query:     "how does invoice numbering work",
		Limit:     8,
		SpaceID:   "spc_1",
		Kinds:     []string{"fact", "procedure"},
		Namespace: "orbit",
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if gotPath != "/api/v1/sites/orbit/nucleus/recall" {
		t.Errorf("path = %q", gotPath)
	}
	if captured["query"] != "how does invoice numbering work" || captured["spaceId"] != "spc_1" {
		t.Errorf("request body = %+v", captured)
	}
	if captured["namespace"] != "orbit" {
		t.Errorf("namespace = %v", captured["namespace"])
	}
	if _, present := captured["tags"]; present {
		t.Errorf("tags should be omitted when unset; got %v", captured["tags"])
	}
	if resp.Scope.Type != api.MemoryScopeSite || resp.Scope.ID != "st_1" {
		t.Errorf("scope = %+v", resp.Scope)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("hits = %+v", resp.Hits)
	}
	hit := resp.Hits[0]
	if hit.Title != "Invoice numbering" || hit.Body == "" || hit.Kind != "procedure" {
		t.Errorf("hit memory not decoded: %+v", hit)
	}
	if hit.Score != 0.914 || len(hit.MatchedBy) != 2 {
		t.Errorf("hit ranking not decoded: %+v", hit)
	}
	if len(hit.Sources) != 1 || hit.Sources[0].RefType != "page" || hit.Sources[0].RefID != "pg_1" {
		t.Errorf("hit sources = %+v", hit.Sources)
	}
}

func TestRecallOrgScopedWhenNoSite(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"scope":{"type":"org","id":"org_1"},"hits":[]}`))
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").Recall(context.Background(), "", api.RecallRequest{Query: "x"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if gotPath != "/api/v1/nucleus/recall" {
		t.Errorf("path = %q, want the org-level recall", gotPath)
	}
	if resp.Scope.Type != api.MemoryScopeOrg {
		t.Errorf("scope = %+v", resp.Scope)
	}
}

func TestUpsertMemory(t *testing.T) {
	var gotPath string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"memory": {"id": "mem_1", "scope": {"type": "site", "id": "st_1"}, "kind": "procedure",
			           "status": "active", "title": "Invoice numbering", "body": "…",
			           "tags": ["ns:orbit", "repo:orbit-api", "src:src/billing/invoice.ts"],
			           "confidence": 0.8},
			"outcome": "created"
		}`))
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").UpsertMemory(context.Background(), api.MemoryUpsertRequest{
		Title:      "Invoice numbering",
		Body:       "Invoice numbers are per-org and gapless.",
		Kind:       "procedure",
		Tags:       []string{"ns:orbit", "repo:orbit-api", "src:src/billing/invoice.ts"},
		Confidence: 0.8,
		Scope:      api.MemoryScopeSite,
		SiteSlug:   "orbit",
	})
	if err != nil {
		t.Fatalf("UpsertMemory: %v", err)
	}
	if gotPath != "/api/v1/nucleus/memories" {
		t.Errorf("path = %q", gotPath)
	}
	if captured["title"] != "Invoice numbering" || captured["scope"] != "site" || captured["siteSlug"] != "orbit" {
		t.Errorf("request body = %+v", captured)
	}
	if _, present := captured["sources"]; present {
		t.Errorf("sources should be omitted when unset; got %v", captured["sources"])
	}
	if resp.Outcome != "created" || resp.Memory.ID != "mem_1" {
		t.Errorf("response = %+v", resp)
	}
	if len(resp.Memory.Tags) != 3 {
		t.Errorf("memory tags = %v", resp.Memory.Tags)
	}
}

func TestRecallModuleDisabledIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"The Nucleus module is not enabled."}}`))
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").Recall(context.Background(), "orbit", api.RecallRequest{Query: "x"})
	var ae *api.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if !ae.IsAuth() || ae.Message != "The Nucleus module is not enabled." {
		t.Errorf("envelope not parsed: %+v", ae)
	}
}
