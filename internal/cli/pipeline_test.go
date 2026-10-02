package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

const pipelineSpec = `openapi: 3.0.0
info: {title: Billing, version: 1.0.0}
paths:
  /v1/refunds:
    post:
      tags: [Refunds]
      summary: Create a refund
      responses: {'201': {description: created}}
`

func pipelinePlan(t *testing.T, passes ...map[string]any) string {
	t.Helper()
	plan := map[string]any{
		"planHash": "sha256:p", "overlay": false,
		"repo":    map[string]any{"id": "cr_1", "remoteKey": "github.com/acme/billing-api", "name": "billing-api", "defaultBranch": "main", "authoritativeBranch": "main", "webUrl": "https://github.com/acme/billing-api"},
		"product": map[string]any{"id": "prod_1", "slug": "acme-platform", "name": "Acme Platform", "nucleusNamespace": "product:acme-platform"},
		"trigger": "push", "branch": "main", "passes": passes, "inventory": map[string]any{"units": []any{}, "truncated": false},
		"capabilities": map[string]any{"features": map[string]bool{"pipelines": true, "product-inventory": true}, "limits": map[string]any{"heartbeatSeconds": 60}},
		"siblings":     []any{}, "warnings": []any{},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func referencePlanPass(kind string) map[string]any {
	return map[string]any{
		"id": "rp_1", "name": "developer-api", "kind": kind, "source": "manifest", "locked": true, "enabled": true, "applies": true, "triggers": []string{"push", "pr", "manual"},
		"target": map[string]any{"status": "ok", "ref": "dev-portal/api", "site": map[string]any{"id": "site_1", "slug": "dev-portal", "name": "Developer Portal"}, "space": map[string]any{"id": "sp_1", "slug": "api", "name": "API"}, "viewerUrl": "https://docs.acme.io/api"},
		"scope":  map[string]any{}, "options": map[string]any{"claims": false},
		"instructions": map[string]any{"hash": "sha256:i", "layers": []any{map[string]any{"source": "pass", "label": "Pass developer-api", "text": "Keep it short."}}},
	}
}

func (h *harness) pipelineRoutes(t *testing.T, plan string) {
	t.Helper()
	p := h.platform
	p.json("GET /api/v1/repos/self/plan", 200, plan)
	p.handle("POST /api/v1/runs", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		var list []map[string]any
		for i, raw := range body["passes"].([]any) {
			e := raw.(map[string]any)
			rp := map[string]any{"runPassId": fmt.Sprintf("ppr_%d", i+1), "name": e["name"], "status": "pending"}
			if s, _ := e["skip"].(string); s != "" {
				rp["status"], rp["skipReason"] = "skipped", s
			}
			list = append(list, rp)
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "prun_1", "status": "running", "mode": body["mode"], "authoritative": false, "heartbeatSeconds": 3600, "appUrl": "https://app.gravitydocs.io/app/repos/runs/prun_1"}, "passes": list})
	})
	p.handle("POST /api/v1/runs/prun_1/passes/ppr_1", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		_, _ = io.WriteString(w, `{"passRun":{"status":"`+fmt.Sprint(body["status"])+`","costUsd":0.25}}`)
	})
	p.json("POST /api/v1/runs/prun_1/finish", 200, `{"run":{"id":"prun_1","status":"succeeded","appUrl":"https://app.gravitydocs.io/app/repos/runs/prun_1"},"watermarks":[],"bundle":{"changes":1,"appUrl":"https://app.gravitydocs.io/app/repos/runs/prun_1"}}`)
	p.json("POST /api/v1/runs/prun_1/heartbeat", 200, `{"expiresAt":"2026-10-01T12:10:00Z"}`)
	p.json("POST /api/v1/runs/prun_1/changes", 201, `{"change":{"id":"chg_1","op":"create","status":"applied"}}`)
	p.json("GET /api/v1/content/pages", 404, `{"error":{"code":"not_found","message":"no page"}}`)
	p.json("GET /api/v1/content/spaces/sp_1/tree", 200, `{"space":{"id":"sp_1","slug":"api"},"collections":[],"pages":[],"nextCursor":null}`)
}

func (h *harness) commitSpec(t *testing.T) {
	t.Helper()
	h.write(".gravity.yaml", "version: 2\ncode:\n  openapi: [api/openapi.yaml]\n")
	if err := os.MkdirAll(filepath.Join(h.dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("api/openapi.yaml", pipelineSpec)
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "feat: refunds api")
}

func TestRunCommandWritesAndPrintsPasses(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("run", "--json"), 0)
	env := h.envelope()
	data := env["data"].(map[string]any)
	pass := data["passes"].([]any)[0].(map[string]any)
	if env["ok"] != true || env["command"] != "run" || pass["status"] != "succeeded" || pass["costUsd"] != 0.25 {
		t.Fatalf("envelope = %v", env)
	}
	if !strings.Contains(h.stderr.String(), "developer-api") || !strings.Contains(h.stderr.String(), "1 new") || !strings.Contains(h.stderr.String(), "Bundle: 1 changes awaiting review") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	start := h.platform.find("POST", "/api/v1/runs")[0].Body
	if start["trigger"] != "manual" || start["mode"] != "write" || start["origin"] != "local" {
		t.Fatalf("start = %v", start)
	}
	if q := h.platform.find("GET", "/api/v1/repos/self/plan")[0].Query; q["repo"] != nil {
		t.Fatalf("a repo token never sends ?repo=: %v", q)
	}
	if n := len(h.platform.find("POST", "/api/v1/runs/prun_1/heartbeat")); n > 1 {
		t.Fatalf("heartbeats follow the heartbeat interval, got %d", n)
	}
}

func TestRunOnATerminalShowsLiveProgressAndASummaryCard(t *testing.T) {
	h := newHarness(t)
	h.terminal = true
	h.env["NO_COLOR"] = "1"
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("run"), 0)
	out := h.stdout.String()
	for _, want := range []string{"developer-api  reference → dev-portal/api", "Gravity run finished", "1 pass ran, 0 skipped, 0 failed", "Review bundle", "https://app.gravitydocs.io/app/repos/runs/prun_1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func githubPR(t *testing.T, h *harness, number int) {
	t.Helper()
	head := gitCmd(t, h.dir, "rev-parse", "HEAD")
	event := filepath.Join(t.TempDir(), "event.json")
	data := fmt.Sprintf(`{"pull_request":{"number":%d,"html_url":"https://github.com/acme/billing-api/pull/%d","head":{"sha":"%s","ref":"feature"},"base":{"ref":"main"}}}`, number, number, head)
	if err := os.WriteFile(event, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	h.env["CI"] = "true"
	h.env["GITHUB_ACTIONS"] = "true"
	h.env["GITHUB_EVENT_NAME"] = "pull_request"
	h.env["GITHUB_EVENT_PATH"] = event
	h.env["GITHUB_REPOSITORY"] = "acme/billing-api"
	h.env["GITHUB_SHA"] = head
	h.env["GITHUB_STEP_SUMMARY"] = filepath.Join(t.TempDir(), "summary.md")
}

func TestForkPullRequestWithoutATokenExitsZero(t *testing.T) {
	h := newHarness(t)
	githubPR(t, h, 7)
	for _, cmd := range []string{"run", "check"} {
		expectCode(t, h, h.run(cmd), 0)
		if !strings.Contains(h.stderr.String(), "secrets are not shared with forks") {
			t.Fatalf("%s: %s", cmd, h.stderr.String())
		}
	}
	summary, _ := os.ReadFile(h.env["GITHUB_STEP_SUMMARY"])
	if !strings.Contains(string(summary), "skipping the doc check") {
		t.Fatalf("summary = %s", summary)
	}
	if len(h.platform.find("GET", "/api/v1/whoami")) != 0 {
		t.Fatal("a fork PR without a token makes no API call")
	}
	delete(h.env, "GITHUB_EVENT_NAME")
	h.env["GITHUB_EVENT_NAME"] = "push"
	h.env["GITHUB_REF_NAME"] = "main"
	expectCode(t, h, h.run("run"), 2)
}

type fakeGitHub struct {
	mu       sync.Mutex
	comments []map[string]any
	posted   []string
	patched  []string
	srv      *httptest.Server
}

func newFakeGitHub(t *testing.T, existing ...map[string]any) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{comments: existing}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer ghs_test" {
			w.WriteHeader(401)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/billing-api/issues/42/comments":
			_ = json.NewEncoder(w).Encode(g.comments)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing-api/issues/42/comments":
			g.posted = append(g.posted, body["body"])
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "html_url": "https://github.com/acme/billing-api/pull/42#issuecomment-9"})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/billing-api/issues/comments/"):
			g.patched = append(g.patched, r.URL.Path+" "+body["body"])
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 5, "html_url": "https://github.com/acme/billing-api/pull/42#issuecomment-5"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func driftFixture(t *testing.T, h *harness) {
	t.Helper()
	blocks, err := docs.APIBlocks([]byte(pipelineSpec), "gravity-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	stale := blocks[0]
	stale.SourceBinding.Hash = "sha256:stale"
	content, _ := json.Marshal(stale.Content)
	page := map[string]any{
		"page":   map[string]any{"id": "pg_ref", "slug": "refunds", "title": "Refunds", "site": map[string]any{"slug": "dev-portal"}, "space": map[string]any{"slug": "api"}, "status": "published"},
		"blocks": []any{map[string]any{"key": stale.Key, "type": "api", "ownership": "machine", "position": 0, "content": json.RawMessage(content), "sourceBinding": stale.SourceBinding, "units": stale.Units}},
	}
	data, _ := json.Marshal(page)
	h.platform.json("GET /api/v1/content/pages/pg_ref", 200, string(data))
	h.platform.json("GET /api/v1/content/spaces/sp_1/tree", 200, `{"space":{"id":"sp_1","slug":"api"},"collections":[],"pages":[{"id":"pg_ref","slug":"refunds","title":"Refunds","collectionPath":[],"position":0,"status":"published","units":["api:post:/v1/refunds"]}],"nextCursor":null}`)
}

func TestCheckCommandIsThePullRequestGate(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	gitCmd(t, h.dir, "update-ref", "refs/remotes/origin/main", gitCmd(t, h.dir, "rev-parse", "HEAD~1"))
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	driftFixture(t, h)
	githubPR(t, h, 42)
	gh := newFakeGitHub(t, map[string]any{"id": 5, "body": "<!-- gravity:doc-impact repo=github.com/acme/billing-api -->\nold", "user": map[string]any{"login": "github-actions[bot]", "type": "Bot"}})
	h.env["GITHUB_TOKEN"] = "ghs_test"
	h.env["GITHUB_API_URL"] = gh.srv.URL

	expectCode(t, h, h.run("check"), 1)
	out := h.stdout.String() + h.stderr.String()
	if !strings.Contains(out, "::error file=api/openapi.yaml,title=Gravity%3A Refunds%3A POST /v1/refunds is out of date::") {
		t.Fatalf("annotations missing:\n%s", out)
	}
	if len(gh.patched) != 1 || !strings.Contains(gh.patched[0], "/issues/comments/5 ") || !strings.Contains(gh.patched[0], "### Gravity · doc impact for #42") || !strings.Contains(gh.patched[0], "**1 finding**") {
		t.Fatalf("the existing comment must be updated: %v (posted %v)", gh.patched, gh.posted)
	}
	summary, _ := os.ReadFile(h.env["GITHUB_STEP_SUMMARY"])
	if !strings.Contains(string(summary), "| developer-api | Developer Portal › API | 1 page would change: refunds (new)") {
		t.Fatalf("summary = %s", summary)
	}
	start := h.platform.find("POST", "/api/v1/runs")[0].Body
	if start["trigger"] != "pr" || start["mode"] != "dry" || len(h.platform.find("POST", "/api/v1/runs/prun_1/changes")) != 0 {
		t.Fatalf("check runs dry: %v", start)
	}

	h.platform.json("GET /api/v1/content/spaces/sp_1/tree", 200, `{"space":{"id":"sp_1","slug":"api"},"collections":[],"pages":[],"nextCursor":null}`)
	expectCode(t, h, h.run("check", "--fail-on", "coverage"), 0)
	expectCode(t, h, h.run("check", "--fail-on", "bogus"), 2)
	expectCode(t, h, h.run("check", "api"), 2)
	if !strings.Contains(h.stderr.String(), "gravity check") {
		t.Fatal(h.stderr.String())
	}
}

func TestPreviewShowsWorkingTreeDiffWithoutWriting(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_dev"
	h.write(".gravity.yaml", "version: 2\ncode:\n  openapi: [api/openapi.yaml]\n")
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "manifest")
	if err := os.MkdirAll(filepath.Join(h.dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("api/openapi.yaml", pipelineSpec)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("preview", "--format", "diff"), 0)
	out := h.stdout.String()
	if !strings.Contains(out, "create developer-api refunds") || !strings.Contains(out, "+ [api api:POST:/v1/refunds machine]") || !strings.Contains(out, "Cost: $0.25") {
		t.Fatalf("preview output:\n%s\nstderr: %s", out, h.stderr.String())
	}
	start := h.platform.find("POST", "/api/v1/runs")[0].Body
	if start["mode"] != "dry" || start["rangeKind"] != api.RangeWorkingTree {
		t.Fatalf("preview start = %v", start)
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")[0].Body
	if conn["context"].(map[string]any)["trigger"] != "preview" || conn["dryRun"] != true {
		t.Fatalf("connect = %v", conn)
	}
	if q := h.platform.find("GET", "/api/v1/repos/self/plan")[0].Query; q["repo"][0] != "github.com/acme/billing-api" {
		t.Fatalf("a user token names the repo: %v", q)
	}
	if len(h.platform.find("POST", "/api/v1/runs/prun_1/changes")) != 0 {
		t.Fatal("preview never writes")
	}
	expectCode(t, h, h.run("preview", "--format", "json", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if len(data["pages"].([]any)) != 1 || data["instructions"].(map[string]any)["developer-api"] == nil {
		t.Fatalf("json preview = %v", data)
	}
}
