package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvSpace, "")
	t.Setenv(config.EnvAPIURL, "")
	t.Setenv("CI", "")
	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func platformServer(t *testing.T, features map[string]bool, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.Method+" "+r.URL.Path]; ok {
			h(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/v1/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"organizationName": "Acme", "features": features})
		case "/api/v1/sites":
			_ = json.NewEncoder(w).Encode(map[string]any{"sites": []any{
				map[string]any{"slug": "orbit", "name": "Orbit"},
				map[string]any{"slug": "docs", "name": "Docs"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"Site not found."}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTypoedSiteIsNotFoundNotASkippedFeature(t *testing.T) {
	srv := platformServer(t, map[string]bool{"coverage": true}, nil)
	stdout, stderr, err := runRoot(t, "coverage", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "orbitt", "--all")
	if err == nil {
		t.Fatalf("a typo'd site must fail; stdout=%s stderr=%s", stdout, stderr)
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
	want := "site 'orbitt' not found; available sites: docs, orbit"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if strings.Contains(stdout+stderr, "not yet available") {
		t.Errorf("a 404 must not read as an unavailable feature:\n%s%s", stdout, stderr)
	}
}

func TestFeatureAvailabilityComesFromWhoami(t *testing.T) {
	called := false
	srv := platformServer(t, map[string]bool{}, map[string]http.HandlerFunc{
		"GET /api/v1/sites/orbit/coverage": func(w http.ResponseWriter, _ *http.Request) {
			called = true
			_, _ = w.Write([]byte(`{"repos":[]}`))
		},
	})
	_, stderr, err := runRoot(t, "coverage", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "orbit", "--all")
	if err != nil {
		t.Fatalf("a feature whoami does not advertise must skip, got %v", err)
	}
	if called {
		t.Error("coverage was requested although whoami does not advertise it")
	}
	if !strings.Contains(stderr, "coverage reporting is not yet available") {
		t.Errorf("expected a skip notice, got %q", stderr)
	}
	_, _, err = runRoot(t, "coverage", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "orbit", "--all", "--require")
	if CodeFor(err) != CodeError {
		t.Errorf("--require exit code = %d, want %d", CodeFor(err), CodeError)
	}
}

func TestMissingSpaceListsAvailableSpaces(t *testing.T) {
	srv := platformServer(t, nil, map[string]http.HandlerFunc{
		"GET /api/v1/sites/orbit": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site":   map[string]any{"slug": "orbit"},
				"spaces": []any{map[string]any{"id": "s1", "slug": "guides"}, map[string]any{"id": "s2", "slug": "api"}},
			})
		},
	})
	e := &env{cfg: config.Config{Site: "orbit"}, targetSpace: "changelgo"}
	e.client = newTestClient(srv.URL)
	err := explainNotFound(context.Background(), e, Fail(CodeError, apiNotFound("Space not found.")))
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
	want := "space 'changelgo' not found on site 'orbit'; available spaces: api, guides"
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err.Error(), want)
	}
}

func TestPageNotFoundMentioningASpaceIsNotASpaceError(t *testing.T) {
	srv := platformServer(t, nil, map[string]http.HandlerFunc{
		"GET /api/v1/sites/orbit": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"site": map[string]any{"slug": "orbit"}, "spaces": []any{}})
		},
	})
	e := &env{cfg: config.Config{Site: "orbit"}, targetSpace: "guides"}
	e.client = newTestClient(srv.URL)
	orig := &api.APIError{StatusCode: http.StatusNotFound, Code: "page_not_found", Message: "Page not found in space guides."}
	err := explainNotFound(context.Background(), e, orig)
	if strings.Contains(err.Error(), "space 'guides' not found") || !errors.Is(err, orig) {
		t.Errorf("a page 404 must not be reported as a missing space, got %v", err)
	}
	coded := &api.APIError{StatusCode: http.StatusNotFound, Code: "space_not_found", Message: "Unknown."}
	if err := explainNotFound(context.Background(), e, coded); !strings.HasPrefix(err.Error(), "space 'guides' not found on site 'orbit'") {
		t.Errorf("space_not_found must be explained as a missing space, got %v", err)
	}
}

func TestExplainNotFoundLeavesOtherErrorsAlone(t *testing.T) {
	e := &env{cfg: config.Config{Site: "orbit"}}
	e.client = newTestClient("http://127.0.0.1:1")
	orig := Failf(CodeFindings, "findings")
	if got := explainNotFound(context.Background(), e, orig); !errors.Is(got, orig) || got.Error() != "findings" {
		t.Errorf("non-404 errors must pass through untouched, got %v", got)
	}
}

func newTestClient(url string) *api.Client {
	return api.New(url, "sk_live_x")
}

func apiNotFound(msg string) error {
	return &api.APIError{StatusCode: http.StatusNotFound, Code: "not_found", Message: msg}
}
