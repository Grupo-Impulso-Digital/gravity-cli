package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const moduleDisabledMsg = "The cli module is not included in this workspace's licence. Ask an administrator." //nolint:misspell // platform spelling

func moduleDisabledBody() map[string]any {
	return map[string]any{"error": map[string]any{
		"code":    "module_disabled",
		"message": "Module CLI is not included in this workspace's licence.", //nolint:misspell // platform spelling
		"module":  "cli",
	}}
}

// A command that wraps the refusal in its usual CodeError still exits with the
// distinct license code and prints the clear message, not "token rejected".
func TestRunPingModuleDisabled(t *testing.T) {
	srv, _ := reposServer(t, http.StatusForbidden, moduleDisabledBody())

	err := runPing(context.Background(), api.New(srv.URL, "sk_live_ok"), pingRequestFixture(), false, io.Discard)
	if err == nil {
		t.Fatal("expected a license error")
	}
	if got := CodeFor(err); got != CodeLicense {
		t.Errorf("exit code = %d, want %d", got, CodeLicense)
	}
	if !strings.Contains(err.Error(), moduleDisabledMsg) {
		t.Errorf("message %q should contain %q", err.Error(), moduleDisabledMsg)
	}
	if strings.Contains(err.Error(), "token rejected") {
		t.Errorf("a license refusal must not read as a token problem: %q", err.Error())
	}
}

func TestRunReposModuleDisabled(t *testing.T) {
	srv, _ := reposServer(t, http.StatusForbidden, moduleDisabledBody())

	err := runRepos(context.Background(), api.New(srv.URL, "sk_live_ok"), pingRequestFixture(), false, io.Discard)
	if got := CodeFor(err); got != CodeLicense {
		t.Errorf("exit code = %d, want %d (err: %v)", got, CodeLicense, err)
	}
	if err == nil || !strings.Contains(err.Error(), moduleDisabledMsg) {
		t.Errorf("message = %v, want it to contain %q", err, moduleDisabledMsg)
	}
}

// Other 403s keep their existing exit code and auth wording.
func TestRunPingOtherForbiddenUnchanged(t *testing.T) {
	srv, _ := reposServer(t, http.StatusForbidden, map[string]any{
		"error": map[string]string{"code": "forbidden", "message": "key not authorized"},
	})

	err := runPing(context.Background(), api.New(srv.URL, "sk_live_ok"), pingRequestFixture(), false, io.Discard)
	if got := CodeFor(err); got != CodeError {
		t.Errorf("exit code = %d, want %d", got, CodeError)
	}
	if err == nil || !strings.Contains(err.Error(), "token rejected (403): key not authorized") {
		t.Errorf("message = %v, want the auth wording", err)
	}
}

func TestSyncAPIErrorKeepsLicenceChain(t *testing.T) {
	lic := &api.ModuleDisabledError{Module: "cli", APIError: &api.APIError{StatusCode: 403, Code: api.CodeModuleDisabled}}
	err := syncAPIError(lic, "page x")
	if got := CodeFor(err); got != CodeLicense {
		t.Errorf("exit code = %d, want %d", got, CodeLicense)
	}
	if !strings.Contains(err.Error(), moduleDisabledMsg) {
		t.Errorf("message %q should contain %q", err.Error(), moduleDisabledMsg)
	}
}

func TestCodeForLicence(t *testing.T) {
	lic := &api.SeatLimitError{APIError: &api.APIError{StatusCode: 403, Code: api.CodeSeatLimit}}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, CodeOK},
		{"bare license", lic, CodeLicense},
		{"wrapped in ExitError", Fail(CodeError, fmt.Errorf("upsert: %w", lic)), CodeLicense},
		{"plain api error", Fail(CodeError, &api.APIError{StatusCode: 403, Code: "forbidden"}), CodeError},
		{"findings", Fail(CodeFindings, nil), CodeFindings},
		{"untyped", errors.New("boom"), CodeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeFor(tc.err); got != tc.want {
				t.Errorf("CodeFor = %d, want %d", got, tc.want)
			}
		})
	}
}
