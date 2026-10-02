package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func expectCode(t *testing.T, h *harness, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, want, h.stdout.String(), h.stderr.String())
	}
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	expectCode(t, h, h.run("version"), 0)
	if !strings.HasPrefix(h.stdout.String(), "gravity dev (") {
		t.Fatalf("version = %q", h.stdout.String())
	}
	expectCode(t, h, h.run("version", "--json"), 0)
	env := h.envelope()
	data := env["data"].(map[string]any)
	if env["ok"] != true || env["command"] != "version" || data["version"] != "dev" || data["platform"] == "" {
		t.Fatalf("envelope = %v", env)
	}
	expectCode(t, h, h.run("--version"), 0)
	if !strings.HasPrefix(h.stdout.String(), "gravity dev (") {
		t.Fatalf("--version = %q", h.stdout.String())
	}
}

func TestWhoamiNeedsToken(t *testing.T) {
	h := newHarness(t)
	expectCode(t, h, h.run("whoami"), 2)
	if !strings.Contains(h.stderr.String(), "not signed in") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	expectCode(t, h, h.run("whoami", "--json"), 2)
	env := h.envelope()
	if env["ok"] != false || env["error"].(map[string]any)["exitCode"].(float64) != 2 {
		t.Fatalf("envelope = %v", env)
	}
}

func TestWhoamiUser(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("whoami"), 0)
	out := h.stdout.String()
	for _, want := range []string{"Signed in as dave@acme.io (editor) - Acme", "user token ...x9Qa - expires 2026-12-30T12:00:00Z - from env", "acme-labs (admin)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	reqs := h.platform.find("GET", "/api/v1/whoami")
	if len(reqs) != 1 || reqs[0].Token != "gr_user_abc" {
		t.Fatalf("requests = %+v", reqs)
	}
	expectCode(t, h, h.run("whoami", "--json"), 0)
	h.golden("whoami.json")
}

func TestWhoamiRepoToken(t *testing.T) {
	h := newHarness(t)
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.env["GRAVITY_TOKEN"] = "gr_repo_abc"
	expectCode(t, h, h.run("whoami"), 0)
	if !strings.Contains(h.stdout.String(), "Repository token for github.com/acme/billing-api (product acme-platform)") || !strings.Contains(h.stdout.String(), "repo:connect, runs:write") {
		t.Fatalf("out = %s", h.stdout.String())
	}
}

func TestLicenceRefusalExitsThree(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/whoami", 403, `{"error":{"code":"module_disabled","message":"off","module":"cli"}}`)
	expectCode(t, h, h.run("whoami", "--json"), 3)
	env := h.envelope()
	e := env["error"].(map[string]any)
	if e["code"] != "module_disabled" || e["exitCode"].(float64) != 3 {
		t.Fatalf("envelope = %v", env)
	}
}

func TestProfileTokenRefusedForAnotherHost(t *testing.T) {
	h := newHarness(t)
	writeProfiles(t, h.config, "version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: https://api.other.example\n    token: gr_user_zzz\n    tokenKind: user\n")
	expectCode(t, h, h.run("whoami", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "token_host_mismatch" || !strings.Contains(e["message"].(string), "token was issued by https://api.other.example") {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.requests) != 0 {
		t.Fatalf("no request may leave: %+v", h.platform.requests)
	}
}

func TestManifestAPIURLNeverReceivesProfileToken(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "GRAVITY_API_URL")
	writeProfiles(t, h.config, "version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: https://api.gravitydocs.io\n    token: gr_user_secret\n    tokenKind: user\n")
	h.write(".gravity.yaml", "version: 2\napiUrl: "+h.platform.srv.URL+"\n")
	for _, args := range [][]string{{"status"}, {"passes"}, {"init"}, {"whoami"}, {"explain", "p_1"}} {
		expectCode(t, h, h.run(args...), 2)
		if !strings.Contains(h.stderr.String(), "token was issued by https://api.gravitydocs.io") || !strings.Contains(h.stderr.String(), ".gravity.yaml apiUrl") {
			t.Fatalf("%v stderr = %s", args, h.stderr.String())
		}
	}
	h.platform.mu.Lock()
	defer h.platform.mu.Unlock()
	for _, r := range h.platform.requests {
		if r.Token != "" {
			t.Fatalf("%s %s carried a token", r.Method, r.Path)
		}
	}
	if len(h.platform.requests) != 0 {
		t.Fatalf("requests = %+v", h.platform.requests)
	}
}

func TestManifestAPIURLWithEnvToken(t *testing.T) {
	h := newHarness(t)
	delete(h.env, "GRAVITY_API_URL")
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.write(".gravity.yaml", "version: 2\napiUrl: "+h.platform.srv.URL+"\n")
	expectCode(t, h, h.run("whoami"), 0)
	if reqs := h.platform.find("GET", "/api/v1/whoami"); len(reqs) != 1 || reqs[0].Token != "gr_repo_ci" {
		t.Fatalf("whoami follows the manifest apiUrl: %+v", reqs)
	}
}

func writeProfiles(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileImportedFromV0Config(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.config, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("token: sk_live_legacy\napiUrl: " + h.platform.srv.URL + "\n")
	if err := os.WriteFile(filepath.Join(h.config, "config.yaml"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	expectCode(t, h, h.run("whoami"), 0)
	if reqs := h.platform.find("GET", "/api/v1/whoami"); reqs[0].Token != "sk_live_legacy" {
		t.Fatalf("token = %q", reqs[0].Token)
	}
	if !strings.Contains(h.stderr.String(), "Imported the token") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	after, _ := os.ReadFile(filepath.Join(h.config, "config.yaml"))
	if string(after) != string(legacy) {
		t.Fatal("config.yaml must stay byte-identical")
	}
}

func TestLoginWithToken(t *testing.T) {
	h := newHarness(t)
	h.stdin = "gr_user_piped\n"
	expectCode(t, h, h.run("login", "--with-token"), 0)
	if !strings.Contains(h.stdout.String(), "Signed in as dave@acme.io - Acme (profile acme)") {
		t.Fatalf("out = %s", h.stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(h.config, "profiles.yaml"))
	if err != nil || !strings.Contains(string(data), "gr_user_piped") || !strings.Contains(string(data), "current: acme") || !strings.Contains(string(data), "user: dave@acme.io") {
		t.Fatalf("profiles = %s %v", data, err)
	}
	h.stdin = "not-a-token\n"
	expectCode(t, h, h.run("login", "--with-token"), 2)
}

func TestDeviceLogin(t *testing.T) {
	h := newHarness(t)
	polls := 0
	h.platform.json("POST /api/v1/auth/device/start", 201, `{"deviceCode":"dc","userCode":"BCDF-GHJK","verificationUri":"https://app/cli/device","verificationUriComplete":"https://app/cli/device?code=BCDF-GHJK","expiresIn":600,"interval":5}`)
	h.platform.handle("POST /api/v1/auth/device/poll", func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		polls++
		if polls < 2 {
			_, _ = io.WriteString(w, `{"status":"pending"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"approved","token":"gr_user_device","tokenKind":"user","expiresAt":"2026-12-30T12:00:00Z","organization":{"id":"org_2","slug":"acme-labs","name":"Acme Labs"},"user":{"id":"u","email":"dave@acme.io"},"apiUrl":"`+h.platform.srv.URL+`"}`)
	})
	expectCode(t, h, h.run("login", "--org", "acme-labs", "--json"), 0)
	if !strings.Contains(h.stderr.String(), "BCDF-GHJK") || !strings.Contains(h.stderr.String(), "https://app/cli/device?code=BCDF-GHJK") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	env := h.envelope()
	data := env["data"].(map[string]any)
	if data["profile"] != "acme-labs" || data["tokenKind"] != "user" || data["user"] != "dave@acme.io" {
		t.Fatalf("data = %v", data)
	}
	start := h.platform.find("POST", "/api/v1/auth/device/start")
	if len(start) != 1 || start[0].Body["org"] != "acme-labs" || start[0].Token != "" {
		t.Fatalf("start = %+v", start)
	}
	h.env["GRAVITY_API_URL"] = ""
	expectCode(t, h, h.run("whoami"), 0)
	reqs := h.platform.find("GET", "/api/v1/whoami")
	if reqs[len(reqs)-1].Token != "gr_user_device" {
		t.Fatalf("whoami used %q", reqs[len(reqs)-1].Token)
	}
}

func TestDeviceLoginDenied(t *testing.T) {
	h := newHarness(t)
	h.platform.json("POST /api/v1/auth/device/start", 201, `{"deviceCode":"dc","userCode":"X","verificationUri":"https://app","expiresIn":600,"interval":5}`)
	h.platform.json("POST /api/v1/auth/device/poll", 400, `{"error":{"code":"access_denied","message":"Denied"}}`)
	expectCode(t, h, h.run("login"), 2)
	if !strings.Contains(h.stderr.String(), "denied") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	writeProfiles(t, h.config, fmt.Sprintf("version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: %s\n    token: gr_user_a\n    tokenKind: user\n  default:\n    apiUrl: %s\n    token: sk_live_b\n    tokenKind: org\n", h.platform.srv.URL, h.platform.srv.URL))
	if err := os.WriteFile(filepath.Join(h.config, "config.yaml"), []byte("token: sk_live_b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.platform.json("POST /api/v1/auth/logout", 204, ``)
	expectCode(t, h, h.run("logout"), 0)
	if reqs := h.platform.find("POST", "/api/v1/auth/logout"); len(reqs) != 1 || reqs[0].Token != "gr_user_a" {
		t.Fatalf("logout requests = %+v", reqs)
	}
	data, _ := os.ReadFile(filepath.Join(h.config, "profiles.yaml"))
	if strings.Contains(string(data), "gr_user_a") || !strings.Contains(string(data), "current: default") {
		t.Fatalf("profiles = %s", data)
	}
	expectCode(t, h, h.run("logout", "--all"), 0)
	if !strings.Contains(h.stderr.String(), "config.yaml still holds a token") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if reqs := h.platform.find("POST", "/api/v1/auth/logout"); len(reqs) != 1 {
		t.Fatal("org tokens are never revoked through /auth/logout")
	}
	expectCode(t, h, h.run("logout"), 0)
	if !strings.Contains(h.stdout.String(), "nothing to sign out") {
		t.Fatalf("out = %s", h.stdout.String())
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "version: 2\nproduct: acme-platform\ncode:\n  openapi: [api/openapi.yaml]\n")
	expectCode(t, h, h.run("status"), 0)
	out := h.stdout.String()
	for _, want := range []string{"billing-api -> Acme Platform", "health: blocked", "awaits approval", "main@9f8e7d6", "developer-api", "Bundle awaiting review: 4 changes", "Same product: gateway (live)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if len(conn) != 1 {
		t.Fatalf("connect calls = %d", len(conn))
	}
	body := conn[0].Body
	ctx := body["context"].(map[string]any)
	if body["dryRun"] != true || ctx["trigger"] != "status" || ctx["origin"] != "local" || body["product"] != "acme-platform" || !strings.HasPrefix(body["manifestHash"].(string), "sha256:") {
		t.Fatalf("connect body = %v", body)
	}
	repo := body["repo"].(map[string]any)
	if repo["remote"] != "git@github.com:acme/billing-api.git" || repo["name"] != "billing-api" || repo["provider"] != "github" || repo["webUrl"] != "https://github.com/acme/billing-api" || repo["branch"] != "main" {
		t.Fatalf("repo = %v", repo)
	}
	st := h.platform.find("GET", "/api/v1/repos/self/status")
	if len(st) != 1 || st[0].Query["repo"][0] != "github.com/acme/billing-api" || st[0].Query["runs"][0] != "5" {
		t.Fatalf("status query = %+v", st)
	}
	expectCode(t, h, h.run("status", "--check"), 1)
	if !strings.Contains(h.stderr.String(), "health is blocked") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	expectCode(t, h, h.run("status", "--check", "--json"), 1)
	checked := h.envelope()
	e, _ := checked["error"].(map[string]any)
	if checked["ok"] != false || e == nil || e["code"] != "unhealthy" || e["exitCode"].(float64) != 1 || !strings.Contains(e["message"].(string), "health is blocked") {
		t.Fatalf("envelope = %v", checked)
	}
	if data, _ := checked["data"].(map[string]any); data == nil || data["status"] == nil {
		t.Fatalf("status data stays in the failure envelope: %v", checked)
	}
	expectCode(t, h, h.run("status", "--json"), 0)
	h.golden("status.json")
}

func TestStatusRepoTokenOmitsRepoParam(t *testing.T) {
	h := newHarness(t)
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.env["GRAVITY_TOKEN"] = "gr_repo_abc"
	h.env["CI"] = "true"
	expectCode(t, h, h.run("status"), 0)
	st := h.platform.find("GET", "/api/v1/repos/self/status")
	if _, ok := st[0].Query["repo"]; ok {
		t.Fatalf("repo principals resolve their own repo: %v", st[0].Query)
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if conn[0].Body["context"].(map[string]any)["origin"] != "ci" || conn[0].Body["manifest"] != nil {
		t.Fatalf("connect = %v", conn[0].Body)
	}
	for _, r := range h.stdout.String() {
		if r > 127 {
			t.Fatalf("CI output must be ASCII: %q", h.stdout.String())
		}
	}
}

func TestStatusWithV1ManifestWarns(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "site: docs\nspaces:\n  default: guides\n")
	expectCode(t, h, h.run("status"), 0)
	if !strings.Contains(h.stderr.String(), "v1 .gravity.yaml") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if conn := h.platform.find("POST", "/api/v1/repos/connect"); conn[0].Body["manifest"] != nil {
		t.Fatal("a v1 manifest is never sent")
	}
}

func TestInvalidManifestFailsWithSuggestion(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "version: 2\npasses:\n  - name: docs\n    kind: guides\n    target: product/guides\n    trigers: [push]\n")
	expectCode(t, h, h.run("status", "--json"), 2)
	env := h.envelope()
	e := env["error"].(map[string]any)
	if e["code"] != "manifest_invalid" || !strings.Contains(e["message"].(string), `passes[0].trigers: unknown key (did you mean "triggers"?)`) {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.find("POST", "/api/v1/repos/connect")) != 0 {
		t.Fatal("nothing is sent for an invalid manifest")
	}
}

func TestManifestFlagAndEnv(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write("alt.yaml", "version: 2\nproduct: from-flag\n")
	expectCode(t, h, h.run("status", "--manifest", "alt.yaml"), 0)
	if conn := h.platform.find("POST", "/api/v1/repos/connect"); conn[0].Body["product"] != "from-flag" {
		t.Fatalf("connect = %v", conn[0].Body)
	}
	h.write("env.yaml", "version: 2\nproduct: from-env\n")
	h.env["GRAVITY_MANIFEST"] = "env.yaml"
	expectCode(t, h, h.run("status"), 0)
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if conn[len(conn)-1].Body["product"] != "from-env" {
		t.Fatalf("connect = %v", conn[len(conn)-1].Body)
	}
}

func TestServerWithoutPipelines(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "sk_live_abc"
	h.platform.json("GET /api/v1/whoami", 200, `{"organizationId":"org_1","organizationName":"Acme","keyHint":"x","features":{"repos":true}}`)
	expectCode(t, h, h.run("status"), 2)
	if !strings.Contains(h.stderr.String(), "use gravity v0.3") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestNotFoundIsNeverSkipped(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/repos/self/status", 404, `{"error":{"code":"repo_not_connected","message":"Repository github.com/acme/billing-api is not connected."}}`)
	expectCode(t, h, h.run("status"), 2)
	if !strings.Contains(h.stderr.String(), "gravity init") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestPassesList(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("passes"), 0)
	out := h.stdout.String()
	for _, want := range []string{"passes for push main", "developer-api", "target awaits approval", "not on this trigger"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	plans := h.platform.find("GET", "/api/v1/repos/self/plan")
	q := plans[0].Query
	if q["trigger"][0] != "push" || q["branch"][0] != "main" || q["mode"][0] != "dry" || q["repo"][0] != "github.com/acme/billing-api" {
		t.Fatalf("plan query = %v", q)
	}
	if len(h.platform.find("POST", "/api/v1/repos/connect")) != 0 {
		t.Fatal("no manifest, no connect")
	}
	expectCode(t, h, h.run("passes", "list", "--json", "--trigger", "release"), 0)
	h.golden("passes.json")
	plans = h.platform.find("GET", "/api/v1/repos/self/plan")
	if last := plans[len(plans)-1].Query; last["trigger"][0] != "release" {
		t.Fatalf("plan query = %v", last)
	}
}

func TestPassesDetachedHeadNeedsBranch(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_abc"
	h.env["CI"] = "true"
	gitCmd(t, h.dir, "checkout", "-q", "--detach")
	expectCode(t, h, h.run("passes", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "branch_unknown" {
		t.Fatalf("error = %v", e)
	}
	expectCode(t, h, h.run("passes", "--branch", "main"), 0)
}

func TestPassesOverlayLocalManifest(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "version: 2\npasses:\n  - name: developer-api\n    kind: reference\n    target: dev-portal/api\n    triggers: [push]\n  - name: memory\n    kind: nucleus\n    triggers: [push]\n")
	expectCode(t, h, h.run("passes", "--json"), 0)
	env := h.envelope()
	data := env["data"].(map[string]any)
	byName := map[string]map[string]any{}
	for _, raw := range data["passes"].([]any) {
		p := raw.(map[string]any)
		byName[p["name"].(string)] = p
	}
	if byName["memory"]["pending"] != true || byName["memory"]["applies"] != true || byName["developer-api"]["pending"] != true {
		t.Fatalf("passes = %v", byName)
	}
	plans := h.platform.find("GET", "/api/v1/repos/self/plan")
	if !strings.HasPrefix(plans[0].Query["manifestHash"][0], "sha256:") {
		t.Fatalf("plan query = %v", plans[0].Query)
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if len(conn) != 1 || conn[0].Body["dryRun"] != true {
		t.Fatal("a manifest is snapshotted with a dry connect before planning")
	}
	warnings := env["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("want manifest_not_authoritative warning")
	}
}

func TestPassesShowAndEdit(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("passes", "show", "developer-api"), 0)
	out := h.stdout.String()
	for _, want := range []string{"developer-api - Developer API", "1. Organization voice", "Never describe UI.", "main@9f8e7d6", "approved by dave@acme.io", "pageStrategy=per-tag"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	expectCode(t, h, h.run("passes", "show", "nope"), 2)
	if !strings.Contains(h.stderr.String(), "changelog, developer-api, product-guides") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	expectCode(t, h, h.run("passes", "edit", "developer-api", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["url"] != "https://github.com/acme/billing-api/blob/main/.gravity.yaml" || data["locked"] != true {
		t.Fatalf("edit = %v", data)
	}
	expectCode(t, h, h.run("passes", "edit", "product-guides", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["url"] != "https://app.gravitydocs.io/app/repos/cr_1/passes/rp_2" {
		t.Fatalf("edit = %v", data)
	}
}

func TestExplain(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/content/resolve", 200, `{"pageId":"pg_1","siteSlug":"dev-portal","spaceSlug":"api","pageSlug":"refunds"}`)
	h.platform.json("GET /api/v1/content/pages/pg_1/provenance", 200, `{"page":{"id":"pg_1","slug":"refunds","title":"Refunds"},"blocks":[
{"key":"api:POST:/v1/refunds","history":[{"action":"update","state":"published","repo":"billing-api","pass":"developer-api","runId":"prun_1","commitSha":"a1b2c3d4e5","sourceRefs":["api/openapi.yaml"],"units":["api:post:/v1/refunds"],"at":"2026-09-30T10:00:00Z"},{"action":"create","state":"published","repo":"gateway","pass":"api","runId":"prun_0","commitSha":"9f8e7d6c","sourceRefs":[],"units":[],"at":"2026-09-01T10:00:00Z"}]},
{"key":"guide:refunds:intro","history":[{"action":"create","state":"proposed","repo":"billing-api","pass":"product-guides","runId":"prun_2","commitSha":"ffff0000","sourceRefs":[],"units":[],"at":"2026-10-01T10:00:00Z"}]}]}`)
	expectCode(t, h, h.run("explain", "dev-portal/api/refunds"), 0)
	out := h.stdout.String()
	if !strings.Contains(out, "billing-api/developer-api@a1b2c3d") || !strings.Contains(out, "2 writes, previously gateway") {
		t.Fatalf("out = %s", out)
	}
	if q := h.platform.find("GET", "/api/v1/content/resolve")[0].Query; q["ref"][0] != "dev-portal/api/refunds" {
		t.Fatalf("resolve query = %v", q)
	}
	expectCode(t, h, h.run("explain", "pg_1", "--block", "guide:refunds:intro", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if blocks := data["blocks"].([]any); len(blocks) != 1 {
		t.Fatalf("blocks = %v", blocks)
	}
	expectCode(t, h, h.run("explain", "pg_1", "--block", "missing"), 2)
}

func TestInitDryRunAndWrite(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--dry-run", "--product", "acme-platform"), 0)
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("--dry-run must not write")
	}
	if !strings.Contains(h.stdout.String(), "+ .gravity.yaml (2 lines)") || !strings.Contains(h.stdout.String(), "product: acme-platform") {
		t.Fatalf("preview = %s", h.stdout.String())
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if len(conn) != 1 || conn[0].Body["dryRun"] != true || conn[0].Body["context"].(map[string]any)["trigger"] != "init" || conn[0].Body["product"] != "acme-platform" {
		t.Fatalf("connect = %+v", conn)
	}
	expectCode(t, h, h.run("init", "--product", "acme-platform"), 0)
	data, err := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml"))
	if err != nil || string(data) != "version: 2\nproduct: acme-platform\n" {
		t.Fatalf("manifest = %q %v", data, err)
	}
	conn = h.platform.find("POST", "/api/v1/repos/connect")
	if len(conn) != 3 || conn[2].Body["dryRun"] != false {
		t.Fatalf("connect calls = %d", len(conn))
	}
	if !strings.Contains(h.stdout.String(), "Connected billing-api to Acme Platform with 2 passes") {
		t.Fatalf("out = %s", h.stdout.String())
	}
	if out, err := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml")); err != nil || len(out) == 0 {
		t.Fatal(err)
	}
	if _, err := config.Load(filepath.Join(h.dir, ".gravity.yaml")); err != nil {
		t.Fatalf("written manifest must be valid: %v", err)
	}
}

func TestInitWritesNothingWhenConnectFails(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.handle("POST /api/v1/repos/connect", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["dryRun"] == true {
			_, _ = io.WriteString(w, connectBody)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"Not allowed"}}`)
	})
	expectCode(t, h, h.run("init"), 2)
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("a failed connect leaves no manifest behind")
	}
}

func TestInitRefusesWithoutPermission(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `"docs.read","docs.write","docs.repos.manage","docs.repos.tokens"`, `"docs.read"`, 1))
	expectCode(t, h, h.run("init"), 2)
	if !strings.Contains(h.stderr.String(), "can't connect repositories in Acme (role: editor)") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if len(h.platform.find("POST", "/api/v1/repos/connect")) != 0 {
		t.Fatal("no connect without permission")
	}
}

func TestInitV1Manifest(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "site: docs\n")
	expectCode(t, h, h.run("init", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "manifest_v1" {
		t.Fatalf("error = %v", e)
	}
}

func TestRemovedCommandsPoint(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"sync":               "gravity run",
		"auth login":         "gravity login",
		"auth status":        "gravity status",
		"doctor":             "gravity status",
		"release-notes --ci": "gravity run",
		"coverage --json":    "gravity check",
		"check api":          "gravity check",
		"nucleus sync":       "gravity run",
		"docs generate":      "gravity run",
		"capture":            "gravity run",
		"spaces":             "gravity status",
	}
	for args, want := range cases {
		code := h.run(strings.Fields(args)...)
		if code != 2 || !strings.Contains(h.stderr.String(), "was removed in gravity 1.0; use `"+want+"`") {
			t.Errorf("%s: exit %d, stderr %q", args, code, h.stderr.String())
		}
	}
}

func TestPipelineCommandsNotAvailableYet(t *testing.T) {
	h := newHarness(t)
	for _, cmd := range []string{"run", "preview", "check"} {
		expectCode(t, h, h.run(cmd), 2)
		if !strings.Contains(h.stderr.String(), "not available in this build") {
			t.Fatalf("%s: %s", cmd, h.stderr.String())
		}
	}
}

func TestExitCodeTable(t *testing.T) {
	license := &api.ModuleDisabledError{Module: "cli", APIError: &api.APIError{StatusCode: 403, Code: api.CodeModuleDisabled}}
	cases := []struct {
		err  error
		code int
	}{
		{nil, CodeOK},
		{&ExitError{Code: CodeFindings}, CodeFindings},
		{errors.New("plain"), CodeError},
		{&api.APIError{StatusCode: 409, Code: api.CodeLeaseLost}, CodeError},
		{&api.APIError{StatusCode: 409, Code: api.CodeRunNotRunning}, CodeError},
		{license, CodeLicense},
		{Fail(CodeError, fmt.Errorf("wrapped: %w", license)), CodeLicense},
		{&api.SeatLimitError{APIError: &api.APIError{StatusCode: 403, Code: api.CodeSeatLimit}}, CodeLicense},
		{&config.ManifestError{}, CodeError},
	}
	for i, tc := range cases {
		if got := CodeFor(tc.err); got != tc.code {
			t.Errorf("case %d (%v): CodeFor = %d, want %d", i, tc.err, got, tc.code)
		}
	}
	if errorCode(&config.V1Error{}) != "manifest_v1" || errorCode(&config.ManifestError{}) != api.CodeManifestInvalid || errorCode(&api.APIError{Code: api.CodeLeaseHeld}) != api.CodeLeaseHeld {
		t.Fatal("errorCode mapping")
	}
}

func TestNoRemote(t *testing.T) {
	h := newHarness(t)
	gitCmd(t, h.dir, "remote", "remove", "origin")
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("status"), 2)
	if !strings.Contains(h.stderr.String(), "no git remote") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}
