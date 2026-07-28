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
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/api.git":          "github.com/acme/api",
		"https://github.com/acme/api.git":      "github.com/acme/api",
		"https://user:tok@github.com/acme/api": "github.com/acme/api",
		"ssh://git@github.com/acme/api.git":    "github.com/acme/api",
		"git@gitlab.com:group/sub/proj.git":    "gitlab.com/group/sub/proj",
		"":                                     "",
	}
	for in, want := range cases {
		if got := normalizeRemote(in); got != want {
			t.Errorf("normalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoName(t *testing.T) {
	proj := &config.Project{Product: config.Product{Repo: "acme-api"}}
	if got := repoName(proj, "github.com/acme/other", "/tmp/whatever"); got != "acme-api" {
		t.Errorf("manifest repo should win, got %q", got)
	}
	if got := repoName(nil, "github.com/acme/api", "/tmp/whatever"); got != "api" {
		t.Errorf("remote basename fallback, got %q", got)
	}
	if got := repoName(nil, "", "/tmp/my-repo"); got != "my-repo" {
		t.Errorf("cwd basename fallback, got %q", got)
	}
}

func TestRunPingPostsAndPrints(t *testing.T) {
	var gotBody api.SetupPingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/setup/ping" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_live_test" {
			t.Errorf("missing/wrong auth header: %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"organizationName": "Acme",
			"keyHint":          "a1b2",
			"defaultSiteSlug":  "acme-docs",
		})
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	req := api.SetupPingRequest{
		CLI:    api.PingCLI{Version: "1.3.0", OS: "darwin", Arch: "arm64"},
		Config: api.PingConfig{APIURL: srv.URL, Site: "acme-docs", Space: "guides"},
		Repo:   api.PingRepo{Name: "acme-api", Remote: "github.com/acme/api", Branch: "main"},
	}
	var out bytes.Buffer
	if err := runPing(context.Background(), client, req, false, &out); err != nil {
		t.Fatalf("runPing: %v", err)
	}

	// The full request round-tripped to the server intact.
	if gotBody.CLI.Version != "1.3.0" || gotBody.CLI.OS != "darwin" {
		t.Errorf("cli block not sent: %+v", gotBody.CLI)
	}
	if gotBody.Config.Site != "acme-docs" || gotBody.Repo.Remote != "github.com/acme/api" {
		t.Errorf("config/repo block not sent: %+v %+v", gotBody.Config, gotBody.Repo)
	}

	s := out.String()
	for _, want := range []string{"Acme", "…a1b2", "acme-docs", "github.com/acme/api", "main", "Handshake OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
}

func TestRunPingJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "organizationName": "Acme"})
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	req := api.SetupPingRequest{CLI: api.PingCLI{Version: "1.0.0"}}
	var out bytes.Buffer
	if err := runPing(context.Background(), client, req, true, &out); err != nil {
		t.Fatalf("runPing json: %v", err)
	}
	var payload struct {
		Request  api.SetupPingRequest  `json:"request"`
		Response api.SetupPingResponse `json:"response"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if payload.Request.CLI.Version != "1.0.0" || payload.Response.OrganizationName != "Acme" {
		t.Errorf("json payload wrong: %+v", payload)
	}
}

func TestRunPingAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "unauthorized", "message": "bad key"},
		})
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_bad")
	err := runPing(context.Background(), client, api.SetupPingRequest{}, false, io.Discard)
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
}
