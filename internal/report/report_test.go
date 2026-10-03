package report_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
)

func sample() report.Doc {
	return report.Doc{Repo: "github.com/acme/billing-api", PR: 42, RunURL: "https://app.test/runs/prun_1", Passes: []report.Pass{
		{Name: "developer-api", Kind: "reference", Target: "Developer Portal › API", Status: "succeeded", Impact: []api.Impact{{Page: api.PageRef{Slug: "refunds"}, Action: "update"}, {Page: api.PageRef{Slug: "refund-reasons"}, Action: "create"}}},
		{Name: "product-guides", Kind: "guides", Target: "Product › Guides", Status: "succeeded"},
		{Name: "changelog", Kind: "changelog", Target: "Product › Changelog", Status: "skipped", SkipReason: "trigger_mismatch"},
		{
			Name: "gate", Kind: "check", Target: "-", Status: "succeeded",
			Findings: []api.Finding{{Severity: "error", Code: "claim_contradicted", Verdict: api.VerdictContradicted, Title: "`Webhooks` says `retry_count` is in the payload; it was removed in a1b2c3d", File: "src/events.ts", Line: 42}},
			Notes:    []api.Note{{Verdict: api.VerdictUnverifiable, Title: "Daily limit"}},
			Claims:   []agent.ClaimFinding{{Verdict: api.VerdictTrueElsewhere, Title: "Auth", Repo: "gateway"}},
		},
	}}
}

func TestMarkdownGolden(t *testing.T) {
	got := report.Markdown(sample())
	want := strings.Join([]string{
		"<!-- gravity:doc-impact repo=github.com/acme/billing-api -->",
		"### Gravity · doc impact for #42",
		"",
		"| Pass | Target | Impact |",
		"| --- | --- | --- |",
		"| developer-api | Developer Portal › API | 2 pages would change: refunds (update), refund-reasons (new) |",
		"| product-guides | Product › Guides | no impact |",
		"| changelog | Product › Changelog | skipped: trigger mismatch |",
		"| gate | - | 1 finding |",
		"",
		"**1 finding**: `Webhooks` says `retry_count` is in the payload; it was removed in a1b2c3d (contradicted here).",
		"1 claim true elsewhere (gateway) · 1 note.",
		"",
		"[Open run in Gravity](https://app.test/runs/prun_1)",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("markdown:\n%s\n--- want\n%s", got, want)
	}
	empty := report.Doc{Repo: "r", PR: 1, Passes: []report.Pass{{Name: "x", Status: "succeeded"}}}
	if !strings.Contains(report.Markdown(empty), report.NoImpact) || empty.HasImpact() {
		t.Fatal("no impact")
	}
}

func TestGitHubAnnotations(t *testing.T) {
	lines := report.GitHubAnnotations(sample().Findings())
	want := "::error file=src/events.ts,line=42,title=Gravity%3A `Webhooks` says `retry_count` is in the payload; it was removed in a1b2c3d::`Webhooks` says `retry_count` is in the payload; it was removed in a1b2c3d"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("annotations = %q", lines)
	}
}

func TestUpsertCreatesOnlyWhenAsked(t *testing.T) {
	var posted int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 1, "body": "unrelated"}})
		case http.MethodPost:
			posted++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "html_url": "https://gh/c/2"})
		}
	}))
	defer srv.Close()
	gh := report.GitHub{API: srv.URL, Token: "t", Repo: "acme/x"}
	url, err := gh.Upsert(context.Background(), 3, report.Marker("r"), "body", false)
	if err != nil || url != "" || posted != 0 {
		t.Fatalf("no comment is created on a PR that never had impact: %q %v %d", url, err, posted)
	}
	url, err = gh.Upsert(context.Background(), 3, report.Marker("r"), "body", true)
	if err != nil || url != "https://gh/c/2" || posted != 1 {
		t.Fatalf("url=%q err=%v posted=%d", url, err, posted)
	}
}

func TestChangeDiffAppliesPatchSemantics(t *testing.T) {
	current := &api.PageContent{Blocks: []api.PageBlock{
		{Key: "guide:a:intro", Type: "prose", Ownership: "hybrid", Content: json.RawMessage(`{"text":"Old intro"}`)},
		{Key: "guide:a:human", Type: "prose", Ownership: "human", Content: json.RawMessage(`{"text":"Mine"}`)},
		{Key: "guide:a:gone", Type: "prose", Ownership: "machine", Content: json.RawMessage(`{"text":"Bye"}`)},
	}}
	after := "guide:a:intro"
	diff := report.ChangeDiff(current, api.ChangeRequest{
		Op: "update",
		Blocks: []api.ChangeBlock{
			{Key: "guide:a:intro", Type: "prose", Ownership: "hybrid", Content: map[string]any{"text": "New intro"}},
			{Key: "guide:a:human", Type: "prose", Ownership: "hybrid", Content: map[string]any{"text": "Overwrite"}},
			{Key: "guide:a:new", Type: "callout", Ownership: "hybrid", Content: map[string]any{"text": "Deprecated", "variant": "warning"}, After: &after},
		},
		RemoveBlockKeys: []string{"guide:a:gone", "guide:a:human"},
	})
	for _, want := range []string{"-   Old intro", "+   New intro", "+   (warning) Deprecated", "-   Bye", "    Mine"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "Overwrite") {
		t.Fatalf("human blocks are never replaced:\n%s", diff)
	}
}

func TestAppendFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.md")
	if err := report.AppendFile(p, "one"); err != nil {
		t.Fatal(err)
	}
	if err := report.AppendFile(p, "two"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "one\ntwo\n" {
		t.Fatalf("%q", data)
	}
}

func TestUpsertOnlyUpdatesItsOwnComment(t *testing.T) {
	marker := report.Marker("r")
	var patched []string
	var posted int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"id": 1, "body": marker + "\nfake", "user": map[string]any{"login": "mallory", "type": "User"}},
				map[string]any{"id": 7, "body": marker + "\nreal", "user": map[string]any{"login": "github-actions[bot]", "type": "Bot"}},
			})
		case r.Method == http.MethodPatch:
			patched = append(patched, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "html_url": "https://gh/c/7"})
		case r.Method == http.MethodPost:
			posted++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "html_url": "https://gh/c/9"})
		}
	}))
	defer srv.Close()
	gh := report.GitHub{API: srv.URL, Token: "t", Repo: "acme/x"}
	url, err := gh.Upsert(context.Background(), 3, marker, "body", true)
	if err != nil || url != "https://gh/c/7" || len(patched) != 1 || !strings.HasSuffix(patched[0], "/comments/7") || posted != 0 {
		t.Fatalf("url=%q err=%v patched=%v posted=%d", url, err, patched, posted)
	}
}

func TestUpsertIgnoresAMarkerPostedBySomeoneElse(t *testing.T) {
	marker := report.Marker("r")
	var posted int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "ci-user", "type": "User"})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 1, "body": marker, "user": map[string]any{"login": "mallory", "type": "User"}}})
		case r.Method == http.MethodPatch:
			t.Errorf("patched someone else's comment")
		case r.Method == http.MethodPost:
			posted++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "html_url": "https://gh/c/9"})
		}
	}))
	defer srv.Close()
	gh := report.GitHub{API: srv.URL, Token: "t", Repo: "acme/x"}
	if url, err := gh.Upsert(context.Background(), 3, marker, "body", true); err != nil || url != "https://gh/c/9" || posted != 1 {
		t.Fatalf("url=%q err=%v posted=%d", url, err, posted)
	}
}

func TestNotesLineIsNotATableRow(t *testing.T) {
	d := sample()
	for i := range d.Passes {
		d.Passes[i].Findings = nil
		d.Passes[i].Claims = nil
	}
	got := report.Markdown(d)
	if !strings.Contains(got, "|\n\n1 note.\n") {
		t.Fatalf("the notes line needs a blank line after the table:\n%s", got)
	}
}
