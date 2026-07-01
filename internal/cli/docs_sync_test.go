package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
)

// A saved target set must round-trip through disk unchanged so a failed sync can
// be replayed with --from at no AI cost.
func TestSaveLoadTargetsRoundTrip(t *testing.T) {
	targets := []syncTarget{
		{
			kind:  "page",
			space: "docs",
			label: "docs -> docs/overview",
			page: api.PageUpsertRequest{
				SpaceSlug: "docs", Slug: "overview", Title: "Overview",
				Blocks: []api.BlockInput{{
					Key: "h", Type: "heading", Ownership: "hybrid",
					Content: map[string]any{"text": "Overview", "level": float64(2)}, Position: 0,
				}},
			},
		},
		{
			kind:    "release",
			space:   "changelog",
			label:   "release CHANGELOG.md -> changelog",
			release: api.ReleaseNotesRequest{SpaceSlug: "changelog", Title: "v1.0", BodyMarkdown: "# v1.0"},
		},
	}

	path := filepath.Join(t.TempDir(), "nested", "docs.json")
	if err := saveTargets(path, targets); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d targets, want 2", len(got))
	}
	if got[0].kind != "page" || got[0].page.Slug != "overview" || got[0].page.Title != "Overview" {
		t.Errorf("page target did not round-trip: %+v", got[0])
	}
	if len(got[0].page.Blocks) != 1 || got[0].page.Blocks[0].Key != "h" {
		t.Errorf("page blocks did not round-trip: %+v", got[0].page.Blocks)
	}
	if got[1].kind != "release" || got[1].release.Title != "v1.0" || got[1].release.BodyMarkdown != "# v1.0" {
		t.Errorf("release target did not round-trip: %+v", got[1])
	}
}

// A replayed artifact saved by an earlier CLI may carry the legacy array-header
// table shape (which the server 400s) and machine-owned narrative blocks.
// loadTargets must sanitize both so `docs generate --from` succeeds after the
// contract fix instead of replaying the same failure forever.
func TestLoadTargetsSanitizesLegacyArtifact(t *testing.T) {
	targets := []syncTarget{{
		kind:  "page",
		space: "docs",
		label: "docs -> docs/commands",
		page: api.PageUpsertRequest{
			SpaceSlug: "docs", Slug: "commands", Title: "Commands",
			Blocks: []api.BlockInput{{
				Key: "cmd-table", Type: "table", Ownership: "machine",
				SourceBinding: &api.SourceBinding{Kind: "cli", Ref: "internal/cli/root.go", Hash: "abc"},
				Content: map[string]any{
					"header": []string{"Command", "Purpose"},
					"rows":   [][]string{{"gravity sync", "Author docs."}},
				},
			}},
		},
	}}
	path := filepath.Join(t.TempDir(), "docs.json")
	if err := saveTargets(path, targets); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := got[0].page.Blocks[0]
	if b.Ownership != "hybrid" || b.SourceBinding != nil {
		t.Errorf("machine table should replay as hybrid + unbound, got ownership=%q binding=%+v", b.Ownership, b.SourceBinding)
	}
	m, ok := b.Content.(map[string]any)
	if !ok {
		t.Fatalf("content type = %T", b.Content)
	}
	if hdr, _ := m["header"].(bool); !hdr {
		t.Errorf("header = %v, want true (array header folded)", m["header"])
	}
	rows, ok := m["rows"].([][]string)
	if !ok || len(rows) != 2 || rows[0][0] != "Command" || rows[1][1] != "Author docs." {
		t.Errorf("rows = %v (%T), want header row folded first", m["rows"], m["rows"])
	}
}

// runSync must author every target it can even when one fails: a single bad
// target (a 400) does not discard the siblings that authored cleanly.
func TestRunSyncPartialFailureAuthorsSurvivors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/spaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sp", "slug": "docs", "name": "Docs"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages"):
			body, _ := io.ReadAll(r.Body)
			var req api.PageUpsertRequest
			_ = json.Unmarshal(body, &req)
			if req.Slug == "bad" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": "bad_request", "message": "Block 1 (prose) is invalid"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pageSlug": req.Slug, "status": "draft", "proposalId": "pr_" + req.Slug,
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{
		pageTarget("docs", "good-one", "Good One"),
		pageTarget("docs", "bad", "Bad"),
		pageTarget("docs", "good-two", "Good Two"),
	}
	var out bytes.Buffer
	err := runSync(context.Background(), client, "docs", targets, true, io.Discard, &out)

	if err == nil {
		t.Fatal("expected an error because one target failed")
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
	s := out.String()
	if !strings.Contains(s, "Proposed: good-one") || !strings.Contains(s, "Proposed: good-two") {
		t.Errorf("both good pages should have been proposed; got:\n%s", s)
	}
	if !strings.Contains(s, "FAILED") || !strings.Contains(s, "bad") {
		t.Errorf("the bad page should be reported as FAILED; got:\n%s", s)
	}
}

// A systemic auth failure is fail-fast: no point hammering every target with a
// request that will 401 identically.
func TestRunSyncAuthFailsFast(t *testing.T) {
	var pageUpserts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/spaces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sp", "slug": "docs", "name": "Docs"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pages"):
			pageUpserts++
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "unauthorized", "message": "bad key"},
			})
		}
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	targets := []syncTarget{
		pageTarget("docs", "one", "One"),
		pageTarget("docs", "two", "Two"),
	}
	err := runSync(context.Background(), client, "docs", targets, true, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if pageUpserts != 1 {
		t.Errorf("auth failure should stop after the first upsert; got %d upserts", pageUpserts)
	}
}
