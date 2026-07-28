package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type attributionServer struct {
	features map[string]bool
	pages    []api.Page
	upserts  []map[string]any
	deletes  []string
}

func (a *attributionServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/whoami"):
			_ = json.NewEncoder(w).Encode(map[string]any{"organizationId": "org", "features": a.features})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": a.pages})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/spaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"space": map[string]any{"id": "sp", "slug": "docs", "name": "Docs"}})
		case r.Method == http.MethodDelete:
			a.deletes = append(a.deletes, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "x", "status": "proposed", "proposalId": "pr_del"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages"):
			var m map[string]any
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &m)
			a.upserts = append(a.upserts, m)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pageSlug": m["slug"], "status": "draft", "proposalId": "pr_1",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func attributedTarget(space, slug, title, remoteKey string, languages []string) syncTarget {
	tgt := pageTarget(space, slug, title)
	tgt.page.Repo = &api.RepoRef{RemoteKey: remoteKey, Name: "api"}
	tgt.page.Languages = languages
	return tgt
}

func TestRunSyncSendsRepoAndLanguages(t *testing.T) {
	a := &attributionServer{features: map[string]bool{featureRepos: true, featurePageLanguages: true}}
	srv := httptest.NewServer(a.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{attributedTarget("docs", "guide", "Guide", "github.com/acme/api", []string{"fr", "es"})}
	if err := runSync(context.Background(), client, "site", targets, true, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if len(a.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(a.upserts))
	}
	repo, ok := a.upserts[0]["repo"].(map[string]any)
	if !ok || repo["remoteKey"] != "github.com/acme/api" {
		t.Errorf("upsert not attributed: %v", a.upserts[0]["repo"])
	}
	langs, ok := a.upserts[0]["languages"].([]any)
	if !ok || len(langs) != 2 {
		t.Errorf("languages not sent: %v", a.upserts[0]["languages"])
	}
}

func TestRunSyncStripsRepoAndLanguagesOnOldPlatform(t *testing.T) {
	a := &attributionServer{features: map[string]bool{}}
	srv := httptest.NewServer(a.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{attributedTarget("docs", "guide", "Guide", "github.com/acme/api", []string{"fr"})}
	var out bytes.Buffer
	if err := runSync(context.Background(), client, "site", targets, true, nil, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if _, has := a.upserts[0]["repo"]; has {
		t.Errorf("repo must not be sent to an old platform: %v", a.upserts[0])
	}
	if _, has := a.upserts[0]["languages"]; has {
		t.Errorf("languages must not be sent to an old platform: %v", a.upserts[0])
	}
	s := out.String()
	if !strings.Contains(s, "does not attribute pages to repos yet") {
		t.Errorf("expected an attribution degradation note; got:\n%s", s)
	}
	if !strings.Contains(s, "translation requests yet") {
		t.Errorf("expected a translation degradation note; got:\n%s", s)
	}
}

func TestRunSyncNeverPrunesSiblingPages(t *testing.T) {
	const mine, sibling = "github.com/acme/api", "github.com/acme/web"
	a := &attributionServer{
		features: map[string]bool{featureRepos: true},
		pages: []api.Page{
			repoPage("p1", "docs", "guide", "Guide", mine),
			repoPage("p2", "docs", "guide-ed44", "Guide", sibling),
		},
	}
	srv := httptest.NewServer(a.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{attributedTarget("docs", "guide", "Guide", mine, nil)}
	var out bytes.Buffer
	if err := runSync(context.Background(), client, "site", targets, true, nil, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if len(a.deletes) != 0 {
		t.Errorf("a sibling's page must never be proposed for deletion; got %v", a.deletes)
	}
	if !strings.Contains(out.String(), "plan: UPDATE docs/guide") {
		t.Errorf("expected this repo's own page to be updated; got:\n%s", out.String())
	}
}

func TestRunSyncReportsUnattributedDuplicate(t *testing.T) {
	const mine = "github.com/acme/api"
	a := &attributionServer{
		features: map[string]bool{featureRepos: true},
		pages: []api.Page{
			repoPage("p1", "docs", "guide", "Guide", mine),
			repoPage("p2", "docs", "guide-ed44", "Guide", ""),
		},
	}
	srv := httptest.NewServer(a.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{attributedTarget("docs", "guide", "Guide", mine, nil)}
	var out bytes.Buffer
	if err := runSync(context.Background(), client, "site", targets, true, nil, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if len(a.deletes) != 0 {
		t.Errorf("an unattributed duplicate must not be deleted; got %v", a.deletes)
	}
	if !strings.Contains(out.String(), "predates repo attribution") {
		t.Errorf("expected the predates-attribution note; got:\n%s", out.String())
	}
}

func TestRunSyncPrunesDuplicateWithoutAttribution(t *testing.T) {
	a := &attributionServer{
		features: map[string]bool{},
		pages: []api.Page{
			repoPage("p1", "docs", "guide", "Guide", ""),
			repoPage("p2", "docs", "guide-ed44", "Guide", ""),
		},
	}
	srv := httptest.NewServer(a.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{pageTarget("docs", "guide", "Guide")}
	if err := runSync(context.Background(), client, "site", targets, true, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if len(a.deletes) != 1 || !strings.HasSuffix(a.deletes[0], "/pages/guide-ed44") {
		t.Errorf("expected the suffixed duplicate to be proposed for deletion; got %v", a.deletes)
	}
}
