package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "gravity")
}

func TestImportFromV0ConfigLeavesItUntouched(t *testing.T) {
	dir := isolate(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("token: sk_live_abc\napiUrl: https://api.example.com\n# keep me\n")
	legacyPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	p, imported, err := LoadProfiles()
	if err != nil || !imported {
		t.Fatalf("LoadProfiles = %v imported=%v", err, imported)
	}
	prof, ok := p.Get("default")
	if !ok || prof.Token != "sk_live_abc" || prof.APIURL != "https://api.example.com" || prof.TokenKind != api.TokenKindOrg || p.Current != "default" {
		t.Fatalf("imported = %+v current=%q", prof, p.Current)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || string(after) != string(legacy) {
		t.Fatalf("config.yaml changed: %q", after)
	}
	info, err := os.Stat(filepath.Join(dir, "profiles.yaml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profiles.yaml mode: %v %v", info, err)
	}
	again, imported, err := LoadProfiles()
	if err != nil || imported || again.Current != "default" {
		t.Fatalf("second load = %+v imported=%v err=%v", again, imported, err)
	}
}

func TestNoLegacyNoImport(t *testing.T) {
	dir := isolate(t)
	p, imported, err := LoadProfiles()
	if err != nil || imported || len(p.Profiles) != 0 {
		t.Fatalf("p=%+v imported=%v err=%v", p, imported, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.yaml")); !os.IsNotExist(err) {
		t.Fatal("profiles.yaml written without anything to import")
	}
}

func TestPutRemoveSave(t *testing.T) {
	isolate(t)
	p, _, err := LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	p.Put("acme", Profile{APIURL: "https://api.gravitydocs.io", Org: "acme", Token: "gr_user_a", TokenKind: "user"})
	p.Put("labs", Profile{Token: "gr_user_b"})
	if p.Current != "labs" {
		t.Fatal("Put must make current")
	}
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadProfiles()
	if err != nil || len(loaded.Profiles) != 2 || loaded.Version != 2 {
		t.Fatalf("loaded = %+v %v", loaded, err)
	}
	if !loaded.Remove("labs") || loaded.Current != "acme" || loaded.Remove("nope") {
		t.Fatalf("remove: current=%q", loaded.Current)
	}
	data, _ := os.ReadFile(loaded.Path())
	if !strings.Contains(string(data), "version: 2") {
		t.Fatalf("file = %s", data)
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePrecedence(t *testing.T) {
	profiles := &Profiles{Current: "acme", Profiles: map[string]Profile{
		"acme": {APIURL: "https://api.acme.example", Token: "gr_user_profile"},
		"labs": {APIURL: "https://api.labs.example", Token: "gr_user_labs"},
	}}
	cases := []struct {
		name      string
		in        Inputs
		token     string
		tokenSrc  string
		apiURL    string
		apiURLSrc string
		profile   string
	}{
		{"profile only", Inputs{}, "gr_user_profile", SourceProfile, "https://api.acme.example", SourceProfile, "acme"},
		{"env token wins", Inputs{Getenv: env(map[string]string{"GRAVITY_TOKEN": "gr_repo_env"})}, "gr_repo_env", SourceEnv, "https://api.acme.example", SourceProfile, "acme"},
		{"flag token wins", Inputs{FlagToken: "sk_live_flag", Getenv: env(map[string]string{"GRAVITY_TOKEN": "x"})}, "sk_live_flag", SourceFlag, "https://api.acme.example", SourceProfile, "acme"},
		{"env profile", Inputs{Getenv: env(map[string]string{"GRAVITY_PROFILE": "labs"})}, "gr_user_labs", SourceProfile, "https://api.labs.example", SourceProfile, "labs"},
		{"flag profile over env", Inputs{FlagProfile: "acme", Getenv: env(map[string]string{"GRAVITY_PROFILE": "labs"})}, "gr_user_profile", SourceProfile, "https://api.acme.example", SourceProfile, "acme"},
		{"manifest url over profile", Inputs{ManifestAPIURL: "https://api.manifest.example"}, "gr_user_profile", SourceProfile, "https://api.manifest.example", SourceManifest, "acme"},
		{"env url over manifest", Inputs{ManifestAPIURL: "https://m.example", Getenv: env(map[string]string{"GRAVITY_API_URL": "https://e.example"})}, "gr_user_profile", SourceProfile, "https://e.example", SourceEnv, "acme"},
		{"flag url wins", Inputs{FlagAPIURL: "https://f.example", ManifestAPIURL: "https://m.example", Getenv: env(map[string]string{"GRAVITY_API_URL": "https://e.example"})}, "gr_user_profile", SourceProfile, "https://f.example", SourceFlag, "acme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.in.Getenv == nil {
				tc.in.Getenv = env(nil)
			}
			c, err := Resolve(tc.in, profiles)
			if err != nil {
				t.Fatal(err)
			}
			if c.Token != tc.token || c.TokenSource != tc.tokenSrc || c.APIURL != tc.apiURL || c.APIURLSource != tc.apiURLSrc || c.ProfileName != tc.profile {
				t.Fatalf("got %+v", c)
			}
		})
	}
}

func TestResolveDefaultsAndErrors(t *testing.T) {
	c, err := Resolve(Inputs{Getenv: env(nil)}, &Profiles{})
	if err != nil || c.APIURL != "https://api.gravitydocs.io" || c.APIURLSource != SourceDefault || c.TokenSource != SourceNone {
		t.Fatalf("defaults = %+v %v", c, err)
	}
	if _, err := Resolve(Inputs{FlagProfile: "ghost", Getenv: env(nil)}, &Profiles{}); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("missing profile err = %v", err)
	}
	if _, err := Resolve(Inputs{FlagAPIURL: "https://gravitydocs.io", Getenv: env(nil)}, nil); err == nil {
		t.Fatal("marketing host accepted")
	}
}

func TestHostMismatch(t *testing.T) {
	profiles := &Profiles{Current: "a", Profiles: map[string]Profile{"a": {APIURL: "https://api.a.example", Token: "gr_user_x"}}}
	c, err := Resolve(Inputs{FlagAPIURL: "https://api.b.example", Getenv: env(nil)}, profiles)
	if err != nil || c.HostMismatch() != "https://api.a.example" {
		t.Fatalf("mismatch = %q %v", c.HostMismatch(), err)
	}
	c, _ = Resolve(Inputs{Getenv: env(nil)}, profiles)
	if c.HostMismatch() != "" {
		t.Fatal("no mismatch expected")
	}
}

func TestTokenKindOf(t *testing.T) {
	for tok, want := range map[string]string{"gr_user_x": "user", "gr_repo_x": "repo", "sk_live_x": "org", "x": ""} {
		if got := TokenKindOf(tok); got != want {
			t.Errorf("TokenKindOf(%q) = %q", tok, got)
		}
	}
}

type deviceScript struct {
	polls []string
	calls int
	start map[string]any
}

func (d *deviceScript) server(t *testing.T) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/auth/device/start":
			_ = json.Unmarshal(body, &d.start)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"deviceCode":"dc","userCode":"BCDF-GHJK","verificationUri":"https://app/cli/device","verificationUriComplete":"https://app/cli/device?code=BCDF-GHJK","expiresIn":600,"interval":5}`)
		case "/api/v1/auth/device/poll":
			resp := d.polls[min(d.calls, len(d.polls)-1)]
			d.calls++
			status, payload, _ := strings.Cut(resp, " ")
			switch status {
			case "400":
				w.WriteHeader(400)
			case "410":
				w.WriteHeader(410)
			}
			_, _ = io.WriteString(w, payload)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return api.New(srv.URL, "")
}

func TestDeviceLoginFlows(t *testing.T) {
	approved := `200 {"status":"approved","token":"gr_user_x","tokenKind":"user","expiresAt":"2026-12-30T12:00:00Z","organization":{"id":"org_1","slug":"acme","name":"Acme"},"user":{"id":"u","email":"dave@acme.io"},"apiUrl":"https://api.gravitydocs.io"}`
	pending := `200 {"status":"pending"}`
	cases := []struct {
		name    string
		polls   []string
		wantErr error
		sleeps  []time.Duration
	}{
		{"pending then approved", []string{pending, pending, approved}, nil, []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second}},
		{"slow down with interval", []string{`200 {"status":"slow_down","interval":10}`, approved}, nil, []time.Duration{5 * time.Second, 10 * time.Second}},
		{"slow down without interval", []string{`200 {"status":"slow_down"}`, approved}, nil, []time.Duration{5 * time.Second, 10 * time.Second}},
		{"denied", []string{pending, `400 {"error":{"code":"access_denied","message":"Denied"}}`}, api.ErrDeviceDenied, nil},
		{"expired", []string{`410 {"error":{"code":"expired_token","message":"Expired"}}`}, api.ErrDeviceExpired, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := &deviceScript{polls: tc.polls}
			client := script.server(t)
			var sleeps []time.Duration
			var shown *api.DeviceStart
			poll, err := DeviceLogin(context.Background(), client, DeviceLoginOptions{
				Org: "acme", Hostname: "box",
				Prompt: func(s *api.DeviceStart) { shown = s },
				Sleep: func(_ context.Context, d time.Duration) error {
					sleeps = append(sleeps, d)
					return nil
				},
			})
			if shown == nil || shown.UserCode != "BCDF-GHJK" || script.start["org"] != "acme" || script.start["hostname"] != "box" || script.start["clientName"] != "gravity-cli" {
				t.Fatalf("start = %v shown=%+v", script.start, shown)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || poll.Token != "gr_user_x" || poll.Organization.Slug != "acme" {
				t.Fatalf("poll = %+v err=%v", poll, err)
			}
			if len(sleeps) != len(tc.sleeps) {
				t.Fatalf("sleeps = %v, want %v", sleeps, tc.sleeps)
			}
			for i := range sleeps {
				if sleeps[i] != tc.sleeps[i] {
					t.Fatalf("sleeps = %v, want %v", sleeps, tc.sleeps)
				}
			}
		})
	}
}

func TestDeviceLoginExpiresLocally(t *testing.T) {
	script := &deviceScript{polls: []string{`200 {"status":"pending"}`}}
	client := script.server(t)
	_, err := DeviceLogin(context.Background(), client, DeviceLoginOptions{Sleep: func(context.Context, time.Duration) error { return nil }})
	if !errors.Is(err, api.ErrDeviceExpired) || script.calls != 120 {
		t.Fatalf("err=%v polls=%d", err, script.calls)
	}
}
