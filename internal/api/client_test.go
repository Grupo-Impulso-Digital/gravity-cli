package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

func TestUserAgentIsVersioned(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := api.New(srv.URL, "t").WhoAmI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ua != version.UserAgent() || !strings.HasPrefix(ua, "gravity-cli/") {
		t.Errorf("User-Agent = %q, want %q", ua, version.UserAgent())
	}
}

func errorFrom(t *testing.T, status int, contentType, body string) *api.APIError {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := api.New(srv.URL, "t").WhoAmI(context.Background())
	var ae *api.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *api.APIError, got %T: %v", err, err)
	}
	return ae
}

func TestNonJSONErrorBodiesAreSummarized(t *testing.T) {
	html := "<!DOCTYPE html>\n<html><head><title>502 Bad Gateway | cloudflare</title></head><body>" +
		strings.Repeat("<div>noise</div>", 500) + "</body></html>"
	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        string
	}{
		{"html with title", http.StatusBadGateway, "text/html", html, "502 Bad Gateway | cloudflare"},
		{"html without title", http.StatusServiceUnavailable, "text/html", "<html><body>down</body></html>", "Service Unavailable"},
		{"plain multi-line", http.StatusInternalServerError, "text/plain", "\n  boom: database exploded\nstack trace line 1\nline 2", "boom: database exploded"},
		{"empty body", http.StatusBadGateway, "", "", "Bad Gateway"},
		{"long line clipped", http.StatusInternalServerError, "text/plain", strings.Repeat("x", 500), strings.Repeat("x", 200) + "…"},
		{"json envelope wins", http.StatusBadRequest, "application/json", `{"error":{"code":"bad_request","message":"slug is invalid"}}`, "slug is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := errorFrom(t, tc.status, tc.contentType, tc.body)
			if ae.Message != tc.want {
				t.Errorf("message = %q, want %q", ae.Message, tc.want)
			}
			if strings.Contains(ae.Error(), "<") {
				t.Errorf("error leaks markup: %q", ae.Error())
			}
		})
	}
}

func TestNotFoundIsNotUnavailable(t *testing.T) {
	ae := errorFrom(t, http.StatusNotFound, "application/json", `{"error":{"code":"not_found","message":"Site not found."}}`)
	if ae.IsUnavailable() {
		t.Error("a plain 404 must not read as a missing feature")
	}
	if !ae.IsNotFound() {
		t.Error("a plain 404 must read as not found")
	}
	explicit := errorFrom(t, http.StatusNotFound, "application/json", `{"error":{"code":"unknown_route","message":"no route"}}`)
	if !explicit.IsUnavailable() || explicit.IsNotFound() {
		t.Errorf("unknown_route: unavailable=%v notFound=%v", explicit.IsUnavailable(), explicit.IsNotFound())
	}
}
