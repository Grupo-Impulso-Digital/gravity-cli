package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestIsUnavailable(t *testing.T) {
	cases := []struct {
		err  *api.APIError
		want bool
	}{
		{&api.APIError{StatusCode: http.StatusNotFound}, true},
		{&api.APIError{StatusCode: http.StatusNotImplemented}, true},
		{&api.APIError{StatusCode: http.StatusBadGateway, Code: "not_implemented"}, true},
		{&api.APIError{StatusCode: http.StatusOK, Code: "feature_disabled"}, true},
		{&api.APIError{StatusCode: http.StatusBadRequest}, false},
		{&api.APIError{StatusCode: http.StatusUnauthorized}, false},
		{&api.APIError{StatusCode: http.StatusInternalServerError}, false},
	}
	for _, c := range cases {
		if got := c.err.IsUnavailable(); got != c.want {
			t.Errorf("IsUnavailable(%+v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestStartDocAgentRun(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotMethod = r.URL.Path, r.Header.Get("Authorization"), r.Method
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runId":     "dar_7c19",
			"status":    "queued",
			"statusUrl": "/api/v1/sites/orbit/doc-agent/runs/dar_7c19",
		})
	}))
	defer srv.Close()

	run, err := api.New(srv.URL, "tok").StartDocAgentRun(context.Background(), "orbit", api.DocAgentRunRequest{
		SpaceSlug:       "guides",
		ConnectionLabel: "staging",
		Brief:           "Focus on the new billing screens.",
		Async:           true,
		Repo:            &api.RepoRef{RemoteKey: "github.com/Acme/orbit-web", Name: "orbit-web"},
	})
	if err != nil {
		t.Fatalf("StartDocAgentRun: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/sites/orbit/doc-agent/runs" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if captured["spaceSlug"] != "guides" || captured["connectionLabel"] != "staging" {
		t.Errorf("request body = %+v", captured)
	}
	if captured["async"] != true {
		t.Errorf("async = %v, want true", captured["async"])
	}
	repo, ok := captured["repo"].(map[string]any)
	if !ok || repo["remoteKey"] != "github.com/Acme/orbit-web" {
		t.Errorf("repo attribution not sent: %v", captured["repo"])
	}
	if run.RunID != "dar_7c19" || run.Status != api.RunStatusQueued {
		t.Errorf("run = %+v", run)
	}
	if run.Done() {
		t.Error("queued run should not be Done()")
	}
}

func TestStartDocAgentRunOmitsEmptyOptionals(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"runId": "dar_1", "status": "queued"})
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").StartDocAgentRun(context.Background(), "orbit",
		api.DocAgentRunRequest{SpaceSlug: "guides"})
	if err != nil {
		t.Fatalf("StartDocAgentRun: %v", err)
	}
	for _, key := range []string{"connectionLabel", "brief", "repo"} {
		if _, present := captured[key]; present {
			t.Errorf("%s should be omitted when empty; got %v", key, captured[key])
		}
	}
	if captured["async"] != false {
		t.Errorf("async should always be sent; got %v", captured["async"])
	}
}

func TestDocAgentRunStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/orbit/doc-agent/runs/dar_7c19" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"runId": "dar_7c19", "status": "succeeded", "spaceSlug": "guides",
			"connectionLabel": "staging", "trigger": "cli",
			"proposalId": "prp_1", "reviewUrl": "/app/proposals/prp_1",
			"createdAt": "2026-07-27T14:10:00.000Z", "finishedAt": "2026-07-27T14:12:00.000Z",
			"stats": {"artifacts": 12, "screenshots": 9, "errors": 0},
			"artifacts": [
				{"id": "art_1", "kind": "screenshot", "url": "https://x/media/1",
				 "createdAt": "2026-07-27T14:11:00.000Z",
				 "meta": {"path": "/billing", "width": 1440, "height": 900}}
			]
		}`))
	}))
	defer srv.Close()

	run, err := api.New(srv.URL, "tok").DocAgentRunStatus(context.Background(), "orbit", "dar_7c19")
	if err != nil {
		t.Fatalf("DocAgentRunStatus: %v", err)
	}
	if !run.Done() {
		t.Error("succeeded run should be Done()")
	}
	if run.Trigger != "cli" || run.ProposalID != "prp_1" {
		t.Errorf("run = %+v", run)
	}
	if run.Stats == nil || run.Stats.Screenshots != 9 {
		t.Fatalf("stats = %+v", run.Stats)
	}
	if len(run.Artifacts) != 1 || run.Artifacts[0].Kind != "screenshot" {
		t.Fatalf("artifacts = %+v", run.Artifacts)
	}
	var meta map[string]any
	if err := json.Unmarshal(run.Artifacts[0].Meta, &meta); err != nil {
		t.Fatalf("artifact meta is not verbatim JSON: %v", err)
	}
	if meta["path"] != "/billing" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestDocAgentRunDoneStates(t *testing.T) {
	cases := map[string]bool{
		api.RunStatusQueued:    false,
		api.RunStatusRunning:   false,
		api.RunStatusSucceeded: true,
		api.RunStatusFailed:    true,
		api.RunStatusCancelled: true,
		"":                     false,
	}
	for status, want := range cases {
		run := &api.DocAgentRun{Status: status}
		if got := run.Done(); got != want {
			t.Errorf("Done() for %q = %v, want %v", status, got, want)
		}
	}
}

func TestDocAgentDisabledDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":{"code":"feature_disabled","message":"doc agent is not configured"}}`))
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").StartDocAgentRun(context.Background(), "orbit",
		api.DocAgentRunRequest{SpaceSlug: "guides"})
	var ae *api.APIError
	if !errors.As(err, &ae) || !ae.IsUnavailable() {
		t.Fatalf("expected an unavailable API error, got %v", err)
	}
}
