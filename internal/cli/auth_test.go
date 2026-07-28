package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the root command with args, isolating credentials to a temp
// XDG dir and forcing a non-TTY stdin so interactive prompts never run.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GRAVITY_TOKEN", "")

	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("")) // non-TTY => isInteractive is false
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func whoamiServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/whoami" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "unauthorized", "message": "bad token"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organizationId": "org_1", "organizationName": "Acme", "keyHint": "sk_live_…abc",
		})
	}))
}

func TestAuthLoginWritesAndVerifies(t *testing.T) {
	srv := whoamiServer(t, http.StatusOK)
	defer srv.Close()

	out, err := runCLI(t, "auth", "login", "--token", "sk_live_abc", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Saved credentials") || !strings.Contains(out, "Verified: org Acme") {
		t.Errorf("output missing save/verify lines:\n%s", out)
	}
	// The credentials file landed in the isolated XDG dir.
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "gravity", "config.yaml")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected credentials at %s: %v", path, statErr)
	}
}

func TestAuthLoginSavesButWarnsOnBadToken(t *testing.T) {
	srv := whoamiServer(t, http.StatusUnauthorized)
	defer srv.Close()

	out, err := runCLI(t, "auth", "login", "--token", "sk_live_bad", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("login should not fail when verify fails: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Saved credentials") || !strings.Contains(out, "warning") {
		t.Errorf("expected save + warning, got:\n%s", out)
	}
}

func TestAuthLogoutNoFile(t *testing.T) {
	out, err := runCLI(t, "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("expected 'nothing to remove', got:\n%s", out)
	}
}

func TestAuthStatusVerified(t *testing.T) {
	srv := whoamiServer(t, http.StatusOK)
	defer srv.Close()

	out, err := runCLI(t, "--token", "sk_live_abc", "--api-url", srv.URL, "auth", "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Token:       set") || !strings.Contains(out, "Verified: org Acme") {
		t.Errorf("status output unexpected:\n%s", out)
	}
}

func TestAuthStatusNotSignedIn(t *testing.T) {
	out, err := runCLI(t, "auth", "status")
	if err != nil {
		t.Fatalf("status with no token should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Not signed in") {
		t.Errorf("expected 'Not signed in', got:\n%s", out)
	}
}
