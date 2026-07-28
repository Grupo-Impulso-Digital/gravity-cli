package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

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

func TestConfigRemoteKey(t *testing.T) {
	cases := []struct {
		name string
		proj *config.Project
		want string
	}{
		{"no manifest", nil, ""},
		{"product declared", &config.Project{Product: config.Product{Slug: "orbit", Repo: "orbit-api"}}, "orbit/orbit-api"},
		{"slug missing", &config.Project{Product: config.Product{Repo: "orbit-api"}}, ""},
		{"repo missing", &config.Project{Product: config.Product{Slug: "orbit"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := configRemoteKey(tc.proj); got != tc.want {
				t.Errorf("configRemoteKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDocSourcesSummary(t *testing.T) {
	proj := &config.Project{
		Product:      config.Product{Slug: "orbit", Repo: "orbit-api", Role: config.RoleAPI},
		Spaces:       config.Spaces{Default: "platform", Shared: []string{"platform"}, Parent: "fundamentum"},
		Sources:      []config.SourceMap{{Source: "openapi.yaml", Page: "api"}},
		Documents:    []config.DocMap{{File: "README.md", Page: "overview", Space: "guides"}},
		ReleaseNotes: config.ReleaseNotes{Space: "changelog"},
		I18n:         config.I18n{Languages: []string{"fr"}},
		Discovery:    config.Discovery{Units: config.UnitsAuto},
	}
	got := docSourcesSummary(proj)
	if got == nil {
		t.Fatal("summary is nil")
	}
	if got.Sources != 1 || got.Documents != 1 {
		t.Errorf("counts = %d source(s)/%d document(s), want 1/1", got.Sources, got.Documents)
	}
	want := []string{"changelog", "fundamentum", "guides", "platform"}
	if !reflect.DeepEqual(got.Spaces, want) {
		t.Errorf("spaces = %v, want %v", got.Spaces, want)
	}
	if !reflect.DeepEqual(got.Kinds, []string{"openapi"}) {
		t.Errorf("kinds = %v, want [openapi]", got.Kinds)
	}
	if !reflect.DeepEqual(got.Languages, []string{"fr"}) {
		t.Errorf("languages = %v, want [fr]", got.Languages)
	}
	if got.Units != config.UnitService {
		t.Errorf("units = %q, want %q", got.Units, config.UnitService)
	}
	if docSourcesSummary(nil) != nil {
		t.Error("no manifest must produce no summary")
	}
}

func TestProjectConfigJSON(t *testing.T) {
	proj := &config.Project{
		Version:   1,
		Site:      "orbit",
		Product:   config.Product{Slug: "orbit", Repo: "orbit-api", Role: "api"},
		Spaces:    config.Spaces{Default: "platform"},
		Coverage:  config.Coverage{Min: 0.8, Require: []string{"overview"}},
		I18n:      config.I18n{Languages: []string{"fr"}},
		Discovery: config.Discovery{Units: "service"},
	}
	got := projectConfigJSON(proj)
	if got == nil {
		t.Fatal("configFull is nil")
	}
	if got["site"] != "orbit" {
		t.Errorf("site = %v, want orbit", got["site"])
	}
	product, ok := got["product"].(map[string]any)
	if !ok || product["repo"] != "orbit-api" {
		t.Errorf("product = %v, want the manifest's product block", got["product"])
	}
	if _, ok := got["coverage"].(map[string]any); !ok {
		t.Errorf("coverage section missing: %v", got["coverage"])
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("configFull is not JSON-encodable: %v", err)
	}
	if projectConfigJSON(nil) != nil {
		t.Error("no manifest must produce no configFull")
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
		Repo: api.PingRepo{
			Name: "acme-api", Remote: "github.com/acme/api", Branch: "main",
			Commit: "9f2c1ab4e7d09f2c1ab4e7d09f2c1ab4e7d09f2c", RemoteKeySource: api.RemoteKeySourceRemote,
		},
		ConfigFull: map[string]any{"site": "acme-docs"},
		ConfigYAML: "site: acme-docs\n",
		DocSources: &api.PingDocSources{Sources: 1, Documents: 2, Units: "service"},
	}
	var out bytes.Buffer
	if err := runPing(context.Background(), client, req, false, &out); err != nil {
		t.Fatalf("runPing: %v", err)
	}

	if gotBody.CLI.Version != "1.3.0" || gotBody.CLI.OS != "darwin" {
		t.Errorf("cli block not sent: %+v", gotBody.CLI)
	}
	if gotBody.Config.Site != "acme-docs" || gotBody.Repo.Remote != "github.com/acme/api" {
		t.Errorf("config/repo block not sent: %+v %+v", gotBody.Config, gotBody.Repo)
	}
	if gotBody.Repo.Commit == "" || gotBody.Repo.RemoteKeySource != api.RemoteKeySourceRemote {
		t.Errorf("v2 repo fields not sent: %+v", gotBody.Repo)
	}
	if gotBody.ConfigYAML == "" || gotBody.ConfigFull == nil || gotBody.DocSources == nil {
		t.Errorf("v2 manifest payload not sent: %+v", gotBody)
	}

	s := out.String()
	for _, want := range []string{"Acme", "…a1b2", "acme-docs", "github.com/acme/api", "main", "1 source(s), 2 document(s)", "Handshake OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
}

func TestRunPingRendersRegistrationAndSiblings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"organizationName": "Acme",
			"repo":             map[string]any{"id": "cr_9f13", "firstSeenAt": "2026-05-02T11:04:19.220Z"},
			"siblings": []any{
				map[string]any{
					"name": "orbit-web", "remoteKey": "github.com/acme/orbit-web",
					"spaces": []string{"platform", "guides"}, "collections": []string{"orbit-web"},
					"lastPingAt": "2026-07-26T09:12:00.000Z", "lastWriteAt": "2026-07-26T09:14:31.881Z",
					"cliVersion": "0.6.0",
				},
				map[string]any{
					"name": "orbit-mobile", "remoteKey": "github.com/acme/orbit-mobile",
					"spaces": []string{"platform"}, "lastPingAt": "2026-07-20T09:12:00.000Z",
					"cliVersion": "0.5.2",
				},
			},
			"serverFeatures": map[string]bool{"repos": true, "coverage": true, "inventory": false},
		})
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk_live_test")
	var out bytes.Buffer
	if err := runPing(context.Background(), client, api.SetupPingRequest{}, false, &out); err != nil {
		t.Fatalf("runPing: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"cr_9f13", "first seen 2026-05-02",
		"Sibling repos on this site (2)",
		"orbit-web", "spaces platform,guides", "collections orbit-web", "wrote 2026-07-26", "cli 0.6.0",
		"orbit-mobile", "wrote never",
		"Features:     coverage repos",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
	if strings.Contains(s, "inventory") {
		t.Errorf("a disabled feature must not be listed as available; got:\n%s", s)
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
