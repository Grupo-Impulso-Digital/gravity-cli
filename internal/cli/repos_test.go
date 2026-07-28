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

func reposServer(t *testing.T, status int, body map[string]any) (*httptest.Server, *api.SetupPingRequest) {
	t.Helper()
	var got api.SetupPingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/setup/ping" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func pingRequestFixture() api.SetupPingRequest {
	return api.SetupPingRequest{
		CLI:    api.PingCLI{Version: "1.3.0"},
		Config: api.PingConfig{Site: "orbit"},
		Repo: api.PingRepo{
			Name: "orbit-api", Remote: "github.com/acme/orbit-api", Branch: "live",
			RemoteKeySource: api.RemoteKeySourceRemote,
		},
	}
}

func registryResponse() map[string]any {
	return map[string]any{
		"ok":   true,
		"repo": map[string]any{"id": "cr_9f13", "firstSeenAt": "2026-05-02T11:04:19.220Z"},
		"siblings": []any{
			map[string]any{
				"name": "orbit-web", "productSlug": "orbit", "remoteKey": "github.com/acme/orbit-web",
				"spaces": []string{"platform", "guides"}, "collections": []string{"orbit-web"},
				"lastPingAt": "2026-07-26T09:12:00.000Z", "lastWriteAt": "2026-07-26T09:14:31.881Z",
				"cliVersion": "0.6.0",
			},
		},
		"serverFeatures": map[string]bool{"repos": true},
	}
}

func TestRunReposText(t *testing.T) {
	srv, got := reposServer(t, http.StatusOK, registryResponse())
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	if err := runRepos(context.Background(), client, pingRequestFixture(), false, &out); err != nil {
		t.Fatalf("runRepos: %v", err)
	}
	if got.Repo.Remote != "github.com/acme/orbit-api" {
		t.Errorf("handshake did not carry this repo: %+v", got.Repo)
	}
	s := out.String()
	for _, want := range []string{
		"Site:      orbit",
		"This repo: orbit-api  (github.com/acme/orbit-api)",
		"registered cr_9f13", "first seen 2026-05-02", "branch live",
		"Sibling repos (1)",
		"orbit-web", "spaces platform,guides", "collections orbit-web",
		"pinged 2026-07-26", "wrote 2026-07-26", "cli 0.6.0",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
}

func TestRunReposJSON(t *testing.T) {
	srv, _ := reposServer(t, http.StatusOK, registryResponse())
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	if err := runRepos(context.Background(), client, pingRequestFixture(), true, &out); err != nil {
		t.Fatalf("runRepos json: %v", err)
	}
	var view reposView
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if !view.Registered || view.Repo.ID != "cr_9f13" || view.Repo.RemoteKey != "github.com/acme/orbit-api" {
		t.Errorf("repo view wrong: %+v", view.Repo)
	}
	if len(view.Siblings) != 1 || view.Siblings[0].Name != "orbit-web" {
		t.Errorf("siblings wrong: %+v", view.Siblings)
	}
}

func TestRunReposUnregisteredPlatform(t *testing.T) {
	srv, _ := reposServer(t, http.StatusOK, map[string]any{"ok": true, "organizationName": "Acme"})
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	if err := runRepos(context.Background(), client, pingRequestFixture(), false, &out); err != nil {
		t.Fatalf("runRepos: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "does not register repos yet") {
		t.Errorf("expected a degradation note; got:\n%s", s)
	}
	if strings.Contains(s, "Sibling repos (") {
		t.Errorf("no sibling list without a registry; got:\n%s", s)
	}
}

func TestRunReposNoSiblings(t *testing.T) {
	srv, _ := reposServer(t, http.StatusOK, map[string]any{
		"ok":   true,
		"repo": map[string]any{"id": "cr_9f13", "firstSeenAt": "2026-05-02T11:04:19.220Z"},
	})
	client := api.New(srv.URL, "sk_live_test")

	var out bytes.Buffer
	if err := runRepos(context.Background(), client, pingRequestFixture(), false, &out); err != nil {
		t.Fatalf("runRepos: %v", err)
	}
	if !strings.Contains(out.String(), "No sibling repos") {
		t.Errorf("expected the single-repo message; got:\n%s", out.String())
	}
}

func TestRunReposAuthError(t *testing.T) {
	srv, _ := reposServer(t, http.StatusUnauthorized, map[string]any{
		"error": map[string]string{"code": "unauthorized", "message": "bad key"},
	})
	client := api.New(srv.URL, "sk_live_bad")

	err := runRepos(context.Background(), client, pingRequestFixture(), false, io.Discard)
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
}
