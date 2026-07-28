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

// hierarchyServer mocks the platform for runSync hierarchy tests, recording
// space ensures (in order), page upserts, and space PATCHes.
type hierarchyServer struct {
	features     map[string]bool
	spaceEnsures []map[string]any
	pageUpserts  []map[string]any
	spacePatches []map[string]any
}

func (h *hierarchyServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		decode := func() map[string]any {
			var m map[string]any
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &m)
			return m
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/whoami"):
			_ = json.NewEncoder(w).Encode(map[string]any{"organizationId": "org", "features": h.features})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/spaces"):
			m := decode()
			h.spaceEnsures = append(h.spaceEnsures, m)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"space": map[string]any{"id": "sp", "slug": m["slug"], "name": m["slug"]},
			})
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/spaces/"):
			m := decode()
			m["_path"] = r.URL.Path
			h.spacePatches = append(h.spacePatches, m)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"space": map[string]any{"id": "sp", "slug": "connect", "name": "connect"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages"):
			m := decode()
			h.pageUpserts = append(h.pageUpserts, m)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pageSlug": m["slug"], "status": "draft", "proposalId": "pr_1",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// On a hierarchy-capable platform: the parent space is ensured first, the
// default space is nested under it, page upserts carry their collection, and
// the declared home page is pinned via a space PATCH.
func TestRunSyncHierarchyCapable(t *testing.T) {
	h := &hierarchyServer{features: map[string]bool{featureSpaceHierarchy: true}}
	srv := httptest.NewServer(h.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{
		{
			kind: "page", space: "connect", label: "home", home: true,
			page: api.PageUpsertRequest{SpaceSlug: "connect", Slug: "overview", Title: "Overview"},
		},
		{
			kind: "page", space: "connect", label: "guide",
			page: api.PageUpsertRequest{SpaceSlug: "connect", Slug: "device-api/guide", Title: "Guide", Collection: "device-api"},
		},
	}
	hier := &hierarchySpec{defaultSpace: "connect", parent: "fundamentum", home: "overview"}
	var out bytes.Buffer
	if err := runSync(context.Background(), client, "site", targets, true, hier, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}

	if len(h.spaceEnsures) != 2 {
		t.Fatalf("space ensures = %d, want 2 (parent then default)", len(h.spaceEnsures))
	}
	if h.spaceEnsures[0]["slug"] != "fundamentum" {
		t.Errorf("parent space must be ensured first; got %v", h.spaceEnsures[0])
	}
	if h.spaceEnsures[1]["slug"] != "connect" || h.spaceEnsures[1]["parent"] != "fundamentum" {
		t.Errorf("default space should nest under the parent; got %v", h.spaceEnsures[1])
	}
	if len(h.pageUpserts) != 2 {
		t.Fatalf("page upserts = %d, want 2", len(h.pageUpserts))
	}
	if _, hasCol := h.pageUpserts[0]["collection"]; hasCol {
		t.Errorf("home page upsert must not carry a collection: %v", h.pageUpserts[0])
	}
	if h.pageUpserts[1]["collection"] != "device-api" {
		t.Errorf("guide upsert should carry its collection; got %v", h.pageUpserts[1])
	}
	if len(h.spacePatches) != 1 {
		t.Fatalf("space patches = %d, want 1 (home pin)", len(h.spacePatches))
	}
	if h.spacePatches[0]["homePage"] != "overview" || !strings.HasSuffix(h.spacePatches[0]["_path"].(string), "/spaces/connect") {
		t.Errorf("home pin wrong: %v", h.spacePatches[0])
	}
	if !strings.Contains(out.String(), "Home page: connect/overview") {
		t.Errorf("home pin should be reported; got:\n%s", out.String())
	}
}

// On a platform without the feature: collections are stripped from upserts, no
// parent is sent on space ensures, no home PATCH happens, and one notice says
// the hierarchy declarations are inert.
func TestRunSyncHierarchyFallback(t *testing.T) {
	h := &hierarchyServer{features: map[string]bool{}}
	srv := httptest.NewServer(h.handler(t))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{
		{
			kind: "page", space: "connect", label: "guide", home: true,
			page: api.PageUpsertRequest{SpaceSlug: "connect", Slug: "device-api/guide", Title: "Guide", Collection: "device-api"},
		},
	}
	hier := &hierarchySpec{defaultSpace: "connect", parent: "fundamentum", home: "guide"}
	var out bytes.Buffer
	if err := runSync(context.Background(), client, "site", targets, true, hier, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}

	if len(h.spaceEnsures) != 1 || h.spaceEnsures[0]["slug"] != "connect" {
		t.Fatalf("only the target space should be ensured; got %v", h.spaceEnsures)
	}
	if _, hasParent := h.spaceEnsures[0]["parent"]; hasParent {
		t.Errorf("parent must not be sent to an old platform: %v", h.spaceEnsures[0])
	}
	if _, hasCol := h.pageUpserts[0]["collection"]; hasCol {
		t.Errorf("collection must be stripped for an old platform: %v", h.pageUpserts[0])
	}
	if len(h.spacePatches) != 0 {
		t.Errorf("no home pin on an old platform; got %v", h.spacePatches)
	}
	if !strings.Contains(out.String(), "predates subspaces/collections") {
		t.Errorf("expected a degradation notice; got:\n%s", out.String())
	}
}
