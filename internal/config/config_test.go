package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/impulso/gravity-cli/internal/config"
)

func TestResolvePrecedence_EnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	// Project file sets a site + apiUrl (never a token — see TestProjectTokenRejected).
	writeYAML(t, filepath.Join(dir, config.ProjectFileName), "site: file-site\napiUrl: https://file\n")

	// Isolate the user-level config so a real one on disk can't interfere.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	t.Setenv(config.EnvSite, "env-site")
	t.Setenv(config.EnvToken, "")  // empty env should NOT override
	t.Setenv(config.EnvAPIURL, "") // empty env should NOT override file

	cfg, err := config.Resolve(config.Flags{}, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Env wins for site.
	if cfg.Site != "env-site" {
		t.Errorf("env should beat file for site; got %q", cfg.Site)
	}
	// File still applies where env is empty.
	if cfg.APIURL != "https://file" {
		t.Errorf("file value should survive when env is empty; got %q", cfg.APIURL)
	}
}

// TestProjectTokenRejected asserts the hardening: a token committed to the
// project file is a loud error, never silently honored.
func TestProjectTokenRejected(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, filepath.Join(dir, config.ProjectFileName), "site: file-site\napiUrl: https://file\ntoken: sk_live_leaked\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvToken, "")

	_, err := config.Resolve(config.Flags{}, dir)
	if err == nil {
		t.Fatal("expected an error when a token is committed to .gravity.yaml")
	}
	if !strings.Contains(err.Error(), "token must not be committed") {
		t.Errorf("error should explain the token footgun; got %q", err.Error())
	}
}

func TestResolvePrecedence_FlagsBeatEverything(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, filepath.Join(dir, config.ProjectFileName), "site: file-site\napiUrl: https://file\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvSite, "env-site")

	cfg, err := config.Resolve(config.Flags{Site: "flag-site", APIURL: "https://flag"}, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Site != "flag-site" {
		t.Errorf("flag should beat env+file; got %q", cfg.Site)
	}
	if cfg.APIURL != "https://flag" {
		t.Errorf("flag apiUrl should win; got %q", cfg.APIURL)
	}
}

func TestResolvePrecedence_ProjectBeatsUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	userPath := filepath.Join(home, "gravity", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, userPath, "token: user-token\napiUrl: https://user\n")

	projDir := t.TempDir()
	writeYAML(t, filepath.Join(projDir, config.ProjectFileName), "site: project-site\n")

	// Clear env so files are the deciding factor.
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvAPIURL, "")

	cfg, err := config.Resolve(config.Flags{}, projDir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Site != "project-site" {
		t.Errorf("project file should provide the site; got %q", cfg.Site)
	}
	// User token survives because the project file cannot carry one.
	if cfg.Token != "user-token" {
		t.Errorf("user token should survive; got %q", cfg.Token)
	}
	if cfg.APIURL != "https://user" {
		t.Errorf("user apiUrl should survive; got %q", cfg.APIURL)
	}
}

// TestResolveDefaultAPIURL asserts the canonical default fills in when nothing
// else sets a URL.
func TestResolveDefaultAPIURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvAPIURL, "")
	cfg, err := config.Resolve(config.Flags{}, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.APIURL != config.DefaultAPIURL {
		t.Errorf("expected default API URL %q; got %q", config.DefaultAPIURL, cfg.APIURL)
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteProjectConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvAPIURL, "")

	if _, err := config.WriteProjectConfig(dir, config.ProjectConfig{
		Site: "docs", APIURL: "https://app.example", Space: "changelog",
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.Resolve(config.Flags{}, dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Site != "docs" {
		t.Errorf("site round-trip = %q", cfg.Site)
	}
	if cfg.APIURL != "https://app.example" {
		t.Errorf("apiUrl round-trip = %q", cfg.APIURL)
	}
	if cfg.Space != "changelog" {
		t.Errorf("space round-trip = %q", cfg.Space)
	}
}
