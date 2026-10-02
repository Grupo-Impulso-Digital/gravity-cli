package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func respond(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestModuleDisabledIsTyped(t *testing.T) {
	srv := respond(http.StatusForbidden, `{"error":{"code":"module_disabled","message":"Module CLI is off.","module":"cli"}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	var md *api.ModuleDisabledError
	if !errors.As(err, &md) || md.Module != "cli" {
		t.Fatalf("expected *api.ModuleDisabledError for cli, got %T: %v", err, err)
	}
	want := "The cli module is not included in this workspace's licence. Ask an administrator." //nolint:misspell // platform spelling
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if !api.IsLicenseError(err) {
		t.Error("IsLicenseError should be true")
	}
	var ae *api.APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusForbidden || ae.IsAuth() {
		t.Fatalf("underlying *APIError: %+v", ae)
	}
}

func TestModuleDisabledWithoutModuleKey(t *testing.T) {
	srv := respond(http.StatusForbidden, `{"error":{"code":"module_disabled","message":"nope"}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	want := "A module this command needs is not included in this workspace's licence. Ask an administrator." //nolint:misspell // platform spelling
	if err == nil || err.Error() != want {
		t.Errorf("message = %v, want %q", err, want)
	}
}

func TestSeatLimitIsTyped(t *testing.T) {
	srv := respond(http.StatusForbidden, `{"error":{"code":"seat_limit","message":"Seat limit reached."}}`)
	defer srv.Close()

	_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
	var sl *api.SeatLimitError
	if !errors.As(err, &sl) || !api.IsLicenseError(err) {
		t.Fatalf("expected *api.SeatLimitError, got %T: %v", err, err)
	}
}

func TestOtherForbiddenUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"plain forbidden", http.StatusForbidden, `{"error":{"code":"forbidden","message":"No."}}`},
		{"scope missing", http.StatusForbidden, `{"error":{"code":"scope_missing","message":"Scope missing","details":[{"code":"scope","message":"runs:write"}]}}`},
		{"non-json 403", http.StatusForbidden, `forbidden`},
		{"module_disabled on non-403", http.StatusBadRequest, `{"error":{"code":"module_disabled","module":"cli"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := respond(tc.status, tc.body)
			defer srv.Close()

			_, err := api.New(srv.URL, "tok").WhoAmI(context.Background())
			if api.IsLicenseError(err) {
				t.Fatalf("should not be a license error: %v", err)
			}
			var ae *api.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("expected an *api.APIError, got %T: %v", err, err)
			}
			if tc.status == http.StatusForbidden && !ae.IsAuth() {
				t.Error("a non-license 403 should be an auth failure")
			}
		})
	}
}
