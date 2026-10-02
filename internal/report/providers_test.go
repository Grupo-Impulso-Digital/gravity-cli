package report_test

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

type recorded struct {
	mu    sync.Mutex
	calls []string
	auth  []string
	body  []string
}

func (r *recorded) add(req *http.Request) {
	data, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.auth = append(r.auth, req.Header.Get("Authorization")+req.Header.Get("PRIVATE-TOKEN"))
	r.body = append(r.body, string(data))
}

func (r *recorded) count(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type providerCase struct {
	name      string
	server    func(rec *recorded, marker string, present bool) http.HandlerFunc
	commenter func(url string) report.Commenter
	update    string
	create    string
	wantURL   string
}

func providerCases() []providerCase {
	return []providerCase{
		{
			name: "gitlab",
			server: func(rec *recorded, marker string, present bool) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					rec.add(r)
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/api/v4/user":
						writeJSON(w, map[string]any{"id": 5})
					case r.Method == http.MethodGet:
						notes := []any{map[string]any{"id": 1, "body": "looks good", "author": map[string]any{"id": 9}}, map[string]any{"id": 2, "body": marker, "author": map[string]any{"id": 9}}}
						if present {
							notes = append(notes, map[string]any{"id": 3, "body": marker + "\nold", "author": map[string]any{"id": 5}})
						}
						writeJSON(w, notes)
					default:
						writeJSON(w, map[string]any{"id": 3})
					}
				}
			},
			commenter: func(u string) report.Commenter {
				return report.GitLab{API: u + "/api/v4", Token: "glpat", Project: "acme/billing api", MRURL: "https://gitlab.test/acme/billing-api/-/merge_requests/7"}
			},
			update: "PUT /api/v4/projects/acme/billing api/merge_requests/7/notes/3", create: "POST /api/v4/projects/acme/billing api/merge_requests/7/notes",
			wantURL: "https://gitlab.test/acme/billing-api/-/merge_requests/7#note_3",
		},
		{
			name: "bitbucket",
			server: func(rec *recorded, marker string, present bool) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					rec.add(r)
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/2.0/user":
						writeJSON(w, map[string]any{"uuid": "{me}"})
					case r.Method == http.MethodGet && r.URL.Query().Get("page") == "":
						values := []any{map[string]any{"id": 1, "content": map[string]any{"raw": marker}, "user": map[string]any{"uuid": "{other}"}}}
						writeJSON(w, map[string]any{"values": values, "next": "http://" + r.Host + r.URL.Path + "?pagelen=100&page=2"})
					case r.Method == http.MethodGet:
						values := []any{}
						if present {
							values = append(values, map[string]any{"id": 3, "content": map[string]any{"raw": marker}, "user": map[string]any{"uuid": "{me}"}})
						}
						writeJSON(w, map[string]any{"values": values})
					default:
						writeJSON(w, map[string]any{"id": 3, "links": map[string]any{"html": map[string]any{"href": "https://bitbucket.test/c/3"}}})
					}
				}
			},
			commenter: func(u string) report.Commenter {
				return report.Bitbucket{API: u + "/2.0", Token: "bbtok", Repo: "acme/billing-api"}
			},
			update: "PUT /2.0/repositories/acme/billing-api/pullrequests/7/comments/3", create: "POST /2.0/repositories/acme/billing-api/pullrequests/7/comments",
			wantURL: "https://bitbucket.test/c/3",
		},
		{
			name: "azure",
			server: func(rec *recorded, marker string, present bool) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					rec.add(r)
					switch r.Method {
					case http.MethodGet:
						threads := []any{map[string]any{"id": 1, "comments": []any{map[string]any{"id": 1, "content": "lgtm"}}}}
						if present {
							threads = append(threads, map[string]any{"id": 3, "comments": []any{map[string]any{"id": 1, "content": marker}}})
						}
						writeJSON(w, map[string]any{"value": threads})
					default:
						writeJSON(w, map[string]any{"id": 3})
					}
				}
			},
			commenter: func(u string) report.Commenter {
				return report.Azure{Collection: u + "/acme/", Project: "Billing", RepoID: "repo-1", Token: "sat", PRURL: "https://dev.azure.test/acme/Billing/_git/billing-api/pullrequest/7"}
			},
			update: "PATCH /acme/Billing/_apis/git/repositories/repo-1/pullRequests/7/threads/3/comments/1", create: "POST /acme/Billing/_apis/git/repositories/repo-1/pullRequests/7/threads",
			wantURL: "https://dev.azure.test/acme/Billing/_git/billing-api/pullrequest/7?discussionId=3",
		},
	}
}

func TestProviderCommentsAreUpsertedByMarker(t *testing.T) {
	marker := report.Marker("github.com/acme/billing-api")
	for _, tc := range providerCases() {
		t.Run(tc.name+"/updates its comment", func(t *testing.T) {
			rec := &recorded{}
			srv := httptest.NewServer(tc.server(rec, marker, true))
			defer srv.Close()
			url, err := tc.commenter(srv.URL).Upsert(context.Background(), 7, marker, marker+"\nnew", true)
			if err != nil || url != tc.wantURL || rec.count(tc.update) != 1 || rec.count(tc.create) != 0 {
				t.Fatalf("url=%q err=%v calls=%v", url, err, rec.calls)
			}
			for _, a := range rec.auth {
				if a == "" {
					t.Fatal("every call authenticates")
				}
			}
		})
		t.Run(tc.name+"/creates when impact appears", func(t *testing.T) {
			rec := &recorded{}
			srv := httptest.NewServer(tc.server(rec, marker, false))
			defer srv.Close()
			url, err := tc.commenter(srv.URL).Upsert(context.Background(), 7, marker, marker+"\nnew", true)
			if err != nil || url != tc.wantURL || rec.count(tc.create) != 1 || rec.count(tc.update) != 0 {
				t.Fatalf("url=%q err=%v calls=%v", url, err, rec.calls)
			}
			if !strings.Contains(strings.Join(rec.body, ""), "new") {
				t.Fatalf("bodies = %v", rec.body)
			}
		})
		t.Run(tc.name+"/stays silent without prior impact", func(t *testing.T) {
			rec := &recorded{}
			srv := httptest.NewServer(tc.server(rec, marker, false))
			defer srv.Close()
			url, err := tc.commenter(srv.URL).Upsert(context.Background(), 7, marker, report.NoImpact, false)
			if err != nil || url != "" || rec.count("POST") != 0 || rec.count("PUT") != 0 || rec.count("PATCH") != 0 {
				t.Fatalf("url=%q err=%v calls=%v", url, err, rec.calls)
			}
		})
	}
}

func TestAzureAnnotationsAndSummary(t *testing.T) {
	lines := report.AzureAnnotations([]api.Finding{
		{Severity: "error", Code: "claim_contradicted", Title: "Retries; now 3", File: "src/events.ts", Line: 42},
		{Severity: "warning", Code: "drift", Title: "100% stale\nblock", Page: &api.PageRef{Slug: "refunds"}},
	})
	want := []string{
		"##vso[task.logissue type=error;sourcepath=src/events.ts;linenumber=42;code=claim_contradicted;]Gravity: Retries; now 3",
		"##vso[task.logissue type=warning;code=drift;]Gravity: 100%AZP25 stale%0Ablock (page refunds)",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
	if got := report.AzureSummary("/w/gravity-report.md"); got != "##vso[task.uploadsummary]/w/gravity-report.md" {
		t.Fatal(got)
	}
}

func TestGitLabCodeQualityGolden(t *testing.T) {
	data, err := report.GitLabCodeQuality(append(sample().Findings(), api.Finding{Severity: "warning", Code: "drift", Title: "Refunds is out of date", Page: &api.PageRef{ID: "pg_1", Slug: "refunds"}, BlockKey: "api:POST:/v1/refunds"}), ".gravity.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "code-quality.golden.json")
	if *update {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("code quality report differs from %s:\n%s", path, data)
	}
}

func TestRunSummaryShowsHandoffsAndCompetingChanges(t *testing.T) {
	d := report.Doc{
		Repo: "github.com/acme/billing-api", Heading: "Gravity · push main", Applied: true, BundleURL: "https://app.test/runs/prun_1",
		Passes: []report.Pass{{Name: "product-guides", Kind: "guides", Target: "Product › Guides", Status: "succeeded", Impact: []api.Impact{{Page: api.PageRef{Slug: "refund-flows"}, Action: "update", Reason: "unit reach (implements feature:refunds): x"}}}},
		Competing: []report.Competing{
			{Pass: "product-guides", Page: "refund-flows", Repos: []string{"gateway"}, Keys: []string{"a", "b"}},
			{Pass: "product-guides", Page: "refunds", Runs: []string{"prun_9"}, Pending: true},
		},
		Handoffs: []report.Handoff{{UnitKey: "api:post:/v1/refunds", Role: "implements", From: "gateway", To: "billing-api", Status: "detected"}},
	}
	got := report.Markdown(d)
	for _, want := range []string{
		"### Gravity · push main",
		"| product-guides | Product › Guides | 1 page changed: refund-flows (update) |",
		"**Competing changes**: refund-flows also changed by gateway (2 blocks); refunds already has an open change from run prun_9. The review shows both versions side by side.",
		"**Handoffs**: `api:post:/v1/refunds` implements moves from gateway to billing-api (detected).",
		"[Review the bundle in Gravity](https://app.test/runs/prun_1)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	quiet := report.Doc{Repo: "r", Heading: "Gravity · push main", Applied: true, Passes: []report.Pass{{Name: "x", Status: "succeeded"}}}
	if !strings.Contains(report.Markdown(quiet), report.NoChanges) {
		t.Fatal(report.Markdown(quiet))
	}
}
