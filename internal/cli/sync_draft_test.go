package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestReconcilePageTargetsFlagsDraftMatches(t *testing.T) {
	existing := []api.Page{
		{ID: "p1", SpaceSlug: "docs", Slug: "guide", Title: "Guide", Status: api.PageStatusDraft},
		{ID: "p2", SpaceSlug: "docs", Slug: "intro", Title: "Intro", Status: api.PageStatusReleased},
		{ID: "p3", SpaceSlug: "docs", Slug: "legacy", Title: "Legacy"},
	}
	targets := []syncTarget{
		pageTarget("docs", "guide", "Guide"),
		pageTarget("docs", "intro", "Intro"),
		pageTarget("docs", "legacy", "Legacy"),
		pageTarget("docs", "new", "New"),
	}

	plan, _ := reconcilePageTargets(existing, targets, "")
	if len(plan) != 4 {
		t.Fatalf("plan = %+v", plan)
	}
	for i, want := range []struct {
		update bool
		draft  bool
	}{{true, true}, {true, false}, {true, false}, {false, false}} {
		if plan[i].update != want.update || plan[i].draft != want.draft {
			t.Errorf("plan[%d] = %+v, want update=%v draft=%v (a page with no status is released)",
				i, plan[i], want.update, want.draft)
		}
	}
}

func TestRunSyncRequestsDraftsAndLabelsThem(t *testing.T) {
	var pagesQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/whoami"):
			_ = json.NewEncoder(w).Encode(map[string]any{"organizationId": "org", "features": map[string]bool{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages"):
			pagesQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []api.Page{
				{ID: "p1", SpaceSlug: "docs", Slug: "guide", Title: "Guide", Status: api.PageStatusDraft},
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/spaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"space": map[string]any{"id": "sp", "slug": "docs", "name": "docs"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "guide", "status": "draft", "proposalId": "pr_1"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	targets := []syncTarget{pageTarget("docs", "guide", "Guide")}
	if err := runSync(context.Background(), api.New(srv.URL, "sk_live_test"), "site", targets, true, nil, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if pagesQuery.Get("include") != "draft" {
		t.Errorf("pages query = %v, want include=draft so an open proposal is reconciled, not duplicated", pagesQuery)
	}
	if !strings.Contains(out.String(), "plan: UPDATE docs/guide (draft") {
		t.Errorf("draft match not labeled:\n%s", out.String())
	}
}
