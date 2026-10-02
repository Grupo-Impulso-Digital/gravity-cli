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
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
)

func TestCoverageRatio(t *testing.T) {
	cases := []struct {
		name   string
		totals api.CoverageTotals
		want   float64
	}{
		{"nothing claimed is fully covered", api.CoverageTotals{}, 1},
		{"all documented", api.CoverageTotals{Units: 4, Documented: 4}, 1},
		{"half documented", api.CoverageTotals{Units: 4, Documented: 2}, 0.5},
		{"stale still counts as documented", api.CoverageTotals{Units: 4, Documented: 4, Stale: 3}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coverageRatio(tc.totals); got != tc.want {
				t.Errorf("coverageRatio = %v, want %v", got, tc.want)
			}
		})
	}
}

func repoCoverage(units, documented, stale int) api.RepoCoverage {
	return api.RepoCoverage{
		RepoID: "cr_1", RemoteKey: "github.com/acme/api", Name: "api",
		Totals: api.CoverageTotals{
			Units: units, Documented: documented, Stale: stale,
			Undocumented: units - documented,
		},
	}
}

func TestEvaluateCoverage(t *testing.T) {
	cases := []struct {
		name         string
		cov          *api.CoverageResponse
		min          float64
		required     []requiredPage
		published    map[string]bool
		wantFindings int
		wantSeverity string
		wantContains string
	}{
		{
			name:         "below the bar is a warning",
			cov:          &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(4, 2, 0)}},
			min:          0.8,
			wantFindings: 1,
			wantSeverity: output.SeverityWarning,
			wantContains: "below the 80% bar",
		},
		{
			name: "at the bar passes",
			cov:  &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(5, 4, 2)}},
			min:  0.8,
		},
		{
			name: "no bar never fails",
			cov:  &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(4, 0, 0)}},
			min:  0,
		},
		{
			name: "a repo with no units claims nothing",
			cov:  &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(0, 0, 0)}},
			min:  0.9,
		},
		{
			name:         "a missing required page is an error",
			cov:          &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(4, 4, 0)}},
			min:          0.8,
			required:     []requiredPage{{name: "quickstart", slug: "quickstart"}},
			published:    map[string]bool{"overview": true},
			wantFindings: 1,
			wantSeverity: output.SeverityError,
			wantContains: `required page "quickstart" does not exist`,
		},
		{
			name:      "a required page found under its namespaced slug passes",
			cov:       &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(4, 4, 0)}},
			min:       0.8,
			required:  []requiredPage{{name: "overview", slug: "orbit-api/overview"}},
			published: map[string]bool{"orbit-api/overview": true},
		},
		{
			name:         "both gates fire independently",
			cov:          &api.CoverageResponse{Repos: []api.RepoCoverage{repoCoverage(4, 1, 0)}},
			min:          0.8,
			required:     []requiredPage{{name: "quickstart", slug: "quickstart"}},
			published:    map[string]bool{},
			wantFindings: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateCoverage(tc.cov, tc.min, tc.required, tc.published)
			if len(got) != tc.wantFindings {
				t.Fatalf("findings = %d (%+v), want %d", len(got), got, tc.wantFindings)
			}
			if tc.wantSeverity != "" && got[0].Severity != tc.wantSeverity {
				t.Errorf("severity = %q, want %q", got[0].Severity, tc.wantSeverity)
			}
			if tc.wantContains != "" && !strings.Contains(got[0].Title, tc.wantContains) {
				t.Errorf("title = %q, want it to contain %q", got[0].Title, tc.wantContains)
			}
		})
	}
}

func TestRequiredPages(t *testing.T) {
	proj := &config.Project{
		Product:  config.Product{Repo: "orbit-api"},
		Spaces:   config.Spaces{Default: "platform", Shared: []string{"platform"}},
		Coverage: config.Coverage{Require: []string{"overview", "quickstart"}},
	}
	got := requiredPages(proj)
	if len(got) != 2 {
		t.Fatalf("required = %+v, want 2", got)
	}
	if got[0].name != "overview" || got[0].slug != "orbit-api/overview" {
		t.Errorf("required[0] = %+v, want the namespaced slug", got[0])
	}
	if requiredPages(nil) != nil {
		t.Error("no manifest must produce no requirements")
	}
}

func TestCoverageBar(t *testing.T) {
	proj := &config.Project{Coverage: config.Coverage{Min: 0.8}}
	if got := coverageBar(proj, 0.5, true); got != 0.5 {
		t.Errorf("--min should win, got %v", got)
	}
	if got := coverageBar(proj, 0, false); got != 0.8 {
		t.Errorf("manifest bar should apply, got %v", got)
	}
	if got := coverageBar(nil, 0, false); got != 0 {
		t.Errorf("no manifest means no bar, got %v", got)
	}
}

func TestValidateUnitKind(t *testing.T) {
	for _, kind := range []string{"", api.UnitKindService, api.UnitKindCapability} {
		if err := validateUnitKind(kind); err != nil {
			t.Errorf("validateUnitKind(%q) = %v, want nil", kind, err)
		}
	}
	err := validateUnitKind("widget")
	if err == nil || CodeFor(err) != CodeError {
		t.Errorf("an unknown kind must be an operational error, got %v", err)
	}
}

func coverageServer(t *testing.T, status int, body any, pages []any) (*httptest.Server, *string) {
	t.Helper()
	query := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/coverage"):
			query = r.URL.RawQuery
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		case strings.HasSuffix(r.URL.Path, "/pages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"pages": pages})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &query
}

func fullCoverageBody() map[string]any {
	return map[string]any{
		"siteSlug":    "orbit",
		"generatedAt": "2026-07-27T14:05:00.000Z",
		"repos": []any{map[string]any{
			"repoId": "cr_9f13", "remoteKey": "github.com/acme/orbit-api", "name": "orbit-api",
			"totals": map[string]any{"units": 4, "documented": 2, "stale": 1, "undocumented": 2, "ratio": 0.5},
			"byKind": []any{
				map[string]any{"kind": "service", "units": 4, "documented": 2, "stale": 1, "undocumented": 2, "ratio": 0.5},
				map[string]any{"kind": "feature", "units": 0, "documented": 0, "stale": 0, "undocumented": 0, "ratio": 1},
			},
			"units": []any{
				map[string]any{"key": "svc.billing", "kind": "service", "title": "Billing", "state": "stale"},
				map[string]any{"key": "svc.webhooks", "kind": "service", "title": "Webhooks", "state": "undocumented"},
			},
			"uncoveredPages": []any{
				map[string]any{"pageId": "pg_44", "slug": "legacy-webhooks", "spaceSlug": "platform", "title": "Legacy webhooks"},
			},
		}},
	}
}

func TestRunCoverageBelowBar(t *testing.T) {
	srv, query := coverageServer(t, http.StatusOK, fullCoverageBody(), nil)
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", remoteKey: "github.com/acme/orbit-api", min: 0.8, format: output.FormatText,
	}, io.Discard, &out)

	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit code = %d (err %v), want %d", CodeFor(err), err, CodeFindings)
	}
	if !strings.Contains(*query, "repo=github.com%2Facme%2Forbit-api") {
		t.Errorf("coverage was not scoped to this repo; query = %q", *query)
	}
	s := out.String()
	for _, want := range []string{
		"orbit-api  (github.com/acme/orbit-api)",
		"total         2/4",
		"service       2/4",
		"undocumented: svc.webhooks",
		"stale:        svc.billing",
		"unclaimed pages: platform/legacy-webhooks",
		"below the 80% bar",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
	if strings.Contains(s, "feature      ") {
		t.Errorf("kinds with no units must not be printed; got:\n%s", s)
	}
}

func TestRunCoveragePasses(t *testing.T) {
	body := fullCoverageBody()
	body["repos"].([]any)[0].(map[string]any)["totals"] = map[string]any{
		"units": 4, "documented": 4, "stale": 1, "undocumented": 0, "ratio": 1,
	}
	srv, _ := coverageServer(t, http.StatusOK, body, []any{
		map[string]any{"id": "pg_1", "slug": "overview", "title": "Overview", "spaceSlug": "platform"},
	})
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", remoteKey: "github.com/acme/orbit-api", min: 0.8,
		required: []requiredPage{{name: "overview", slug: "overview"}},
		format:   output.FormatText,
	}, io.Discard, &out)
	if err != nil {
		t.Fatalf("expected a pass, got %v\n%s", err, out.String())
	}
}

func TestRunCoverageMissingRequiredPage(t *testing.T) {
	body := fullCoverageBody()
	body["repos"].([]any)[0].(map[string]any)["totals"] = map[string]any{
		"units": 4, "documented": 4, "stale": 0, "undocumented": 0, "ratio": 1,
	}
	srv, _ := coverageServer(t, http.StatusOK, body, []any{
		map[string]any{"id": "pg_1", "slug": "overview", "title": "Overview", "spaceSlug": "platform"},
	})
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", min: 0.8,
		required: []requiredPage{{name: "quickstart", slug: "quickstart"}},
		format:   output.FormatText,
	}, io.Discard, &out)

	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit code = %d, want %d", CodeFor(err), CodeFindings)
	}
	if !strings.Contains(out.String(), `required page "quickstart" does not exist`) {
		t.Errorf("missing-page finding not reported; got:\n%s", out.String())
	}
}

func TestRunCoverageJSON(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusOK, fullCoverageBody(), nil)
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", remoteKey: "github.com/acme/orbit-api", min: 0.8, format: output.FormatJSON,
	}, io.Discard, &out)
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit code = %d, want %d", CodeFor(err), CodeFindings)
	}
	var view coverageView
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if view.Site != "orbit" || view.Min != 0.8 || len(view.Repos) != 1 {
		t.Errorf("view = %+v, want the scoped report", view)
	}
	if len(view.Findings) != 1 || view.Findings[0].Kind != "coverage" {
		t.Errorf("findings = %+v, want one coverage finding", view.Findings)
	}
	if len(view.Repos[0].Units) != 2 {
		t.Errorf("json must carry the full per-unit detail, got %+v", view.Repos[0].Units)
	}
}

func TestRunCoverageGitHubFormat(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusOK, fullCoverageBody(), nil)
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", min: 0.8, format: output.FormatGitHub,
	}, io.Discard, &out)
	if CodeFor(err) != CodeFindings {
		t.Fatalf("exit code = %d, want %d", CodeFor(err), CodeFindings)
	}
	if !strings.HasPrefix(out.String(), "::warning ") {
		t.Errorf("expected a workflow annotation; got:\n%s", out.String())
	}
}

func TestRunCoverageNotFoundIsAnError(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusNotFound, map[string]any{
		"error": map[string]string{"code": "not_found", "message": "Site not found."},
	}, nil)
	client := api.New(srv.URL, "sk_live_test")

	var logs bytes.Buffer
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", format: output.FormatText,
	}, &logs, io.Discard)
	if CodeFor(err) != CodeError {
		t.Fatalf("a 404 must be an operational error, not a skipped feature; got %v", err)
	}
	if strings.Contains(logs.String(), "not yet available") {
		t.Errorf("a 404 must never read as an unavailable feature; got:\n%s", logs.String())
	}
}

func TestRunCoverageExplicitUnknownRouteSkips(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusNotFound, map[string]any{
		"error": map[string]string{"code": "unknown_route", "message": "no such route"},
	}, nil)
	client := api.New(srv.URL, "sk_live_test")

	var logs bytes.Buffer
	if err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", format: output.FormatText,
	}, &logs, io.Discard); err != nil {
		t.Fatalf("an explicit unknown_route must skip, got %v", err)
	}
	if !strings.Contains(logs.String(), "coverage reporting is not available") {
		t.Errorf("expected a skip notice; got:\n%s", logs.String())
	}
	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", format: output.FormatText, require: true,
	}, io.Discard, io.Discard)
	if CodeFor(err) != CodeError {
		t.Errorf("--require exit code = %d, want %d", CodeFor(err), CodeError)
	}
}

func TestRunCoverageAuthError(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]string{"code": "unauthorized", "message": "bad key"},
	}, nil)
	client := api.New(srv.URL, "sk_live_bad")

	err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", format: output.FormatText,
	}, io.Discard, io.Discard)
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
}

func TestRunCoverageEmptyReport(t *testing.T) {
	srv, _ := coverageServer(t, http.StatusOK, map[string]any{"siteSlug": "orbit", "repos": []any{}}, nil)
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	if err := runCoverage(context.Background(), client, coverageOpts{
		site: "orbit", remoteKey: "github.com/acme/orbit-api", min: 0.8, format: output.FormatText,
	}, io.Discard, &out); err != nil {
		t.Fatalf("an empty report is not a finding, got %v", err)
	}
	if !strings.Contains(out.String(), "no inventory published for github.com/acme/orbit-api yet") {
		t.Errorf("expected an explanatory note; got:\n%s", out.String())
	}
}
