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

func TestPublishInventory(t *testing.T) {
	var gotPath, gotMethod string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"repoId": "cr_9f13",
			"units": map[string]any{
				"received": 24, "created": 6, "updated": 17, "unchanged": 1, "removed": 2,
			},
			"coverageUrl": "/api/v1/sites/orbit/coverage?repo=github.com%2FAcme%2Forbit-api",
		})
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").PublishInventory(context.Background(), "orbit", api.InventoryRequest{
		Repo:        api.RepoRef{RemoteKey: "github.com/Acme/orbit-api", Name: "orbit-api"},
		GeneratedAt: "2026-07-27T14:03:11.004Z",
		Replace:     true,
		Units: []api.InventoryUnit{{
			Key:        "svc.billing.invoicing",
			Kind:       api.UnitKindService,
			Title:      "Invoicing service",
			Summary:    "Generates, numbers, and dispatches invoices.",
			SourceRefs: []string{"src/billing/invoice.ts"},
			SourceHash: "sha256:6f1c",
			Audiences:  []string{api.AudienceDevelopers},
			PageSlugs:  []string{"orbit-api/invoicing"},
		}},
	})
	if err != nil {
		t.Fatalf("PublishInventory: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/sites/orbit/inventory" {
		t.Errorf("path = %q", gotPath)
	}
	repo, ok := captured["repo"].(map[string]any)
	if !ok || repo["remoteKey"] != "github.com/Acme/orbit-api" || repo["name"] != "orbit-api" {
		t.Errorf("repo = %v", captured["repo"])
	}
	if captured["replace"] != true || captured["generatedAt"] != "2026-07-27T14:03:11.004Z" {
		t.Errorf("request body = %+v", captured)
	}
	units, ok := captured["units"].([]any)
	if !ok || len(units) != 1 {
		t.Fatalf("expected 1 unit, got %v", captured["units"])
	}
	unit, _ := units[0].(map[string]any)
	if unit["key"] != "svc.billing.invoicing" || unit["kind"] != "service" {
		t.Errorf("unit = %+v", unit)
	}
	if resp.RepoID != "cr_9f13" || resp.Units.Created != 6 || resp.Units.Removed != 2 {
		t.Errorf("response = %+v", resp)
	}
	if resp.Units.Received != 24 || resp.Units.Updated != 17 || resp.Units.Unchanged != 1 {
		t.Errorf("counts = %+v", resp.Units)
	}
	if resp.CoverageURL == "" {
		t.Error("coverageUrl not decoded")
	}
}

func TestPublishInventoryReplaceFalseIsSent(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"repoId": "cr_1"})
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").PublishInventory(context.Background(), "orbit", api.InventoryRequest{
		Repo:  api.RepoRef{RemoteKey: "github.com/Acme/orbit-api"},
		Units: []api.InventoryUnit{{Key: "u1", Kind: api.UnitKindFeature, Title: "One"}},
	})
	if err != nil {
		t.Fatalf("PublishInventory: %v", err)
	}
	replace, present := captured["replace"]
	if !present || replace != false {
		t.Errorf("replace = %v (present=%v), want explicit false", replace, present)
	}
}

func TestPublishInventoryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"units[0].key is invalid"}}`))
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").PublishInventory(context.Background(), "orbit", api.InventoryRequest{
		Repo: api.RepoRef{RemoteKey: "x"},
	})
	var ae *api.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	if ae.Code != "bad_request" || ae.Message != "units[0].key is invalid" {
		t.Errorf("envelope not parsed: code=%q msg=%q", ae.Code, ae.Message)
	}
	if ae.IsUnavailable() {
		t.Error("a 400 must not read as unavailable")
	}
}

func TestCoverage(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte(`{
			"siteSlug": "orbit",
			"generatedAt": "2026-07-27T14:05:00.000Z",
			"repos": [{
				"repoId": "cr_9f13", "remoteKey": "github.com/Acme/orbit-api", "name": "orbit-api",
				"productSlug": "orbit", "repoRole": "api",
				"lastPingAt": "2026-07-27T14:03:09.000Z", "lastWriteAt": "2026-07-27T14:04:52.000Z",
				"totals": {"units": 24, "documented": 20, "stale": 3, "undocumented": 4, "ratio": 0.833},
				"byKind": [
					{"kind": "service", "units": 12, "documented": 11, "stale": 2, "undocumented": 1, "ratio": 0.917}
				],
				"units": [
					{"key": "svc.billing.invoicing", "kind": "service", "title": "Invoicing service",
					 "state": "stale", "pageSlugs": ["orbit-api/invoicing"],
					 "sourceRefs": ["src/billing/invoice.ts"],
					 "firstSeenAt": "2026-05-02T00:00:00.000Z", "lastSeenAt": "2026-07-27T00:00:00.000Z",
					 "documentedAt": "2026-07-11T00:00:00.000Z"}
				],
				"uncoveredPages": [
					{"pageId": "pg_44", "slug": "orbit-api/legacy-webhooks", "spaceSlug": "platform",
					 "title": "Legacy webhooks", "releasedAt": "2026-02-10T00:00:00.000Z"}
				]
			}]
		}`))
	}))
	defer srv.Close()

	cov, err := api.New(srv.URL, "tok").Coverage(context.Background(), "orbit", "github.com/Acme/orbit-api", "service")
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if gotPath != "/api/v1/sites/orbit/coverage" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("repo") != "github.com/Acme/orbit-api" {
		t.Errorf("repo filter = %q", gotQuery.Get("repo"))
	}
	if gotQuery.Get("kind") != "service" {
		t.Errorf("kind filter = %q", gotQuery.Get("kind"))
	}
	if cov.SiteSlug != "orbit" || len(cov.Repos) != 1 {
		t.Fatalf("coverage = %+v", cov)
	}
	repo := cov.Repos[0]
	if repo.RepoID != "cr_9f13" || repo.RemoteKey != "github.com/Acme/orbit-api" || repo.RepoRole != "api" {
		t.Errorf("repo = %+v", repo)
	}
	if repo.Totals.Units != 24 || repo.Totals.Documented != 20 || repo.Totals.Stale != 3 || repo.Totals.Ratio != 0.833 {
		t.Errorf("totals = %+v", repo.Totals)
	}
	if len(repo.ByKind) != 1 || repo.ByKind[0].Kind != api.UnitKindService || repo.ByKind[0].Documented != 11 {
		t.Errorf("byKind = %+v", repo.ByKind)
	}
	if len(repo.Units) != 1 || repo.Units[0].State != api.UnitStateStale || repo.Units[0].DocumentedAt == "" {
		t.Errorf("units = %+v", repo.Units)
	}
	if len(repo.UncoveredPages) != 1 || repo.UncoveredPages[0].Slug != "orbit-api/legacy-webhooks" {
		t.Errorf("uncoveredPages = %+v", repo.UncoveredPages)
	}
}

func TestCoverageUnfiltered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"siteSlug":"orbit","repos":[]}`))
	}))
	defer srv.Close()

	cov, err := api.New(srv.URL, "tok").Coverage(context.Background(), "orbit", "", "")
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if len(cov.Repos) != 0 {
		t.Errorf("repos = %+v", cov.Repos)
	}
}

func TestCoverageUnavailableDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"unknown_route","message":"not found"}}`))
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").Coverage(context.Background(), "orbit", "", "")
	var ae *api.APIError
	if !errors.As(err, &ae) || !ae.IsUnavailable() {
		t.Fatalf("expected an unavailable API error, got %v", err)
	}
}
