package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
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

func TestLaunchCapture(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody api.CaptureRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.CaptureRun{RunID: "run_1", Status: "queued", StatusURL: "https://x/run_1"})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "tok")
	run, err := c.LaunchCapture(context.Background(), "acme", api.CaptureRequest{
		Target:  api.CaptureTarget{URL: "https://app"},
		Capture: []string{"screenshots"},
	})
	if err != nil {
		t.Fatalf("LaunchCapture: %v", err)
	}
	if gotPath != "/api/v1/sites/acme/captures" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Target.URL != "https://app" {
		t.Errorf("body target url = %q", gotBody.Target.URL)
	}
	if run.RunID != "run_1" || run.Status != "queued" {
		t.Errorf("run = %+v", run)
	}
	if run.Done() {
		t.Error("queued run should not be Done()")
	}
}

func TestCaptureStatusAndDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sites/acme/captures/run_9" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.CaptureRun{RunID: "run_9", Status: "succeeded"})
	}))
	defer srv.Close()

	run, err := api.New(srv.URL, "tok").CaptureStatus(context.Background(), "acme", "run_9")
	if err != nil {
		t.Fatalf("CaptureStatus: %v", err)
	}
	if !run.Done() {
		t.Error("succeeded run should be Done()")
	}
}

func TestCaptureUnavailableDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"unknown_route","message":"not found"}}`))
	}))
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").LaunchCapture(context.Background(), "acme", api.CaptureRequest{Target: api.CaptureTarget{URL: "x"}})
	ae, ok := err.(*api.APIError)
	if !ok || !ae.IsUnavailable() {
		t.Fatalf("expected an unavailable API error, got %v", err)
	}
}
