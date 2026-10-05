package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const guidedManifest = `version: 2
product: polaris
structure:
  site: { slug: polaris, name: Polaris }
  spaces:
    - slug: docs
      name: Documentation
      type: product-docs
      visibility: public
      pages:
        - slug: overview
          title: Polaris
          source: README.md
      collections:
        - slug: admin
          title: Polaris Admin
          pages:
            - slug: admin-overview
              title: Polaris Admin
              source: packages/admin/README.md
            - slug: faq
              title: FAQ
    - slug: guides
      name: Guides
      type: product-docs
passes:
  - name: docs
    kind: verbatim
    target: polaris/docs
    triggers: [push]
    options:
      files:
        - include: README.md
          slug: overview
        - include: packages/admin/README.md
          collection: admin
          slug: admin-overview
        - include: packages/api/README.md
          collection: api
          slug: api-overview
  - name: user-guides
    kind: guides
    target: polaris/guides
    triggers: [push]
    audiences: [users]
    options:
      languages: [fr, es]
`

const guidedTree = `{"site":{"slug":"polaris","name":"Polaris"},"spaces":[{"slug":"docs","name":"Documentation","type":"product-docs","pages":[{"slug":"overview","title":"Polaris","status":"published","lockedByRepo":"github.com/acme/website"}]}]}`

const guidedValidate = `{"ok":false,"issues":[{"severity":"error","code":"slug_taken","pass":"docs","path":"README.md","message":"page overview already exists (Polaris, locked by github.com/acme/website)","hint":"set options.adopt: true to take it over, or give README.md another slug"},{"severity":"warning","code":"language_not_enabled","pass":"user-guides","message":"site polaris does not serve es","hint":"enable es on the site, or drop it from options.languages"}],
"i18n":[{"pass":"user-guides","languages":["fr","es"],"effective":["fr"],"reason":"es is not enabled on site polaris"}]}`

func guidedPlan() string {
	return `{"planHash":"sha256:p","repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","defaultBranch":"main","authoritativeBranch":"main"},"product":{"slug":"polaris"},"trigger":"manual","branch":"main",
"passes":[{"id":"rp_1","name":"docs","kind":"verbatim","source":"manifest","enabled":true,"applies":true,"target":{"status":"ok","ref":"polaris/docs"},"scope":{},"instructions":{"hash":"","layers":[]},"estimate":{"ai":false,"firstRun":true,"commits":0,"files":3,"approxInputTokens":0,"approxCostUsd":0,"model":null}},
{"id":"rp_2","name":"user-guides","kind":"guides","source":"manifest","enabled":true,"applies":true,"target":{"status":"unapproved","ref":"polaris/guides"},"scope":{},"instructions":{"hash":"","layers":[]},"estimate":{"ai":true,"firstRun":false,"commits":12,"files":40,"approxInputTokens":120000,"approxCostUsd":0.84,"model":"claude-sonnet"}}],
"inventory":{"units":[],"truncated":false},"capabilities":{"features":{"pipelines":true}},"siblings":[],"warnings":[]}`
}

func guidedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	if err := os.MkdirAll(filepath.Join(h.dir, "packages", "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.dir, "packages", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("README.md", "# Polaris\n\nPolaris keeps fleets moving.\n")
	h.write("packages/admin/README.md", "# @acme/polaris-admin\n\nThe admin console.\n")
	h.write("packages/api/README.md", "# polaris-api\n")
	h.write(".gravity.yaml", guidedManifest)
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "docs")
	h.platform.json("GET /api/v1/structure", 200, guidedTree)
	h.platform.json("POST /api/v1/repos/self/validate", 200, guidedValidate)
	h.platform.json("GET /api/v1/repos/self/plan", 200, guidedPlan())
	return h
}

func normalizeOut(h *harness, s string) string {
	s = strings.ReplaceAll(s, h.platform.srv.URL, "http://platform.test")
	head := gitCmd(h.t, h.dir, "rev-parse", "--short=7", "HEAD")
	s = strings.ReplaceAll(s, head, "abc1234")
	return manifestHashRE.ReplaceAllString(s, "sha256:000000000000")
}

var manifestHashRE = regexp.MustCompile(`sha256:[0-9a-f]{12}\b`)

func (h *harness) goldenText(name, got string) {
	h.t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("read golden %s (run go test -update): %v", path, err)
	}
	if string(want) != got {
		h.t.Fatalf("golden %s mismatch\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestShowRendersTheManifest(t *testing.T) {
	h := guidedHarness(t)
	expectCode(t, h, h.run("show"), 0)
	h.goldenText("show.txt", normalizeOut(h, h.stdout.String()))
	h.terminal = true
	h.env["NO_COLOR"] = "1"
	expectCode(t, h, h.run("show"), 0)
	h.goldenText("show.tty.txt", normalizeOut(h, h.stdout.String()))
}

func TestShowJSONCarriesMappingFlagsAndI18n(t *testing.T) {
	h := guidedHarness(t)
	expectCode(t, h, h.run("show", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	pages := data["verbatim"].([]any)[0].(map[string]any)["pages"].([]any)
	flags := map[string]string{}
	for _, raw := range pages {
		pg := raw.(map[string]any)
		var fl []string
		for _, f := range pg["flags"].([]any) {
			fl = append(fl, f.(string))
		}
		flags[pg["source"].(string)] = strings.Join(fl, ",")
	}
	if flags["README.md"] != "existing_page" || flags["packages/admin/README.md"] != "package_title" || flags["packages/api/README.md"] != "empty,package_title,not_in_structure" {
		t.Fatalf("flags = %v", flags)
	}
	guides := data["passes"].([]any)[1].(map[string]any)
	if guides["ai"] != true || strings.Join(toStrings(guides["effectiveLanguages"]), ",") != "fr" || guides["i18n"] != "es is not enabled on site polaris" || guides["targetStatus"] != "unapproved" {
		t.Fatalf("guides = %v", guides)
	}
	if data["errors"].(float64) != 1 || data["server"] != "checked" {
		t.Fatalf("data = %v", data)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestValidateGroupsIssuesAndExitsOne(t *testing.T) {
	h := guidedHarness(t)
	expectCode(t, h, h.run("validate"), 1)
	h.goldenText("validate.txt", normalizeOut(h, h.stdout.String()))
	expectCode(t, h, h.run("validate", "--json"), 1)
	env := h.envelope()
	if env["ok"] != false || env["error"].(map[string]any)["code"] != "validate_failed" {
		t.Fatalf("env = %v", env)
	}
	data := env["data"].(map[string]any)
	codes := map[string]string{}
	for _, raw := range data["issues"].([]any) {
		is := raw.(map[string]any)
		codes[is["code"].(string)] = is["source"].(string)
	}
	if codes["slug_taken"] != "server" || codes["empty_source"] != "local" || codes["package_title"] != "local" || codes["language_not_enabled"] != "server" {
		t.Fatalf("issues = %v", codes)
	}
	body := h.platform.find("POST", "/api/v1/repos/self/validate")[0].Body
	if body["branch"] != "main" || body["structure"] == nil || len(body["verbatim"].([]any)) != 3 {
		t.Fatalf("validate body = %v", body)
	}
}

func TestValidateFallsBackToLocalChecksOnAnOlderServer(t *testing.T) {
	h := guidedHarness(t)
	h.platform.json("POST /api/v1/repos/self/validate", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	h.platform.json("GET /api/v1/structure", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	expectCode(t, h, h.run("validate", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["server"] != "unavailable" || data["ok"] != true {
		t.Fatalf("data = %v", data)
	}
	found := false
	for _, raw := range data["issues"].([]any) {
		if raw.(map[string]any)["code"] == "server_validate_unsupported" {
			found = true
		}
	}
	if !found {
		t.Fatal("the fallback is announced")
	}
}

func TestValidateCatchesStructureSourcesNoPassImports(t *testing.T) {
	h := guidedHarness(t)
	h.write(".gravity.yaml", strings.Replace(guidedManifest, "            - slug: faq\n              title: FAQ\n", "            - slug: faq\n              title: FAQ\n              source: docs/faq.md\n", 1))
	expectCode(t, h, h.run("validate", "--json"), 1)
	data := h.envelope()["data"].(map[string]any)
	codes := ""
	for _, raw := range data["issues"].([]any) {
		codes += raw.(map[string]any)["code"].(string) + " "
	}
	if !strings.Contains(codes, "structure_source_missing") {
		t.Fatalf("codes = %s", codes)
	}
	if err := os.MkdirAll(filepath.Join(h.dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("docs/faq.md", "# FAQ\n\nAnswers.\n")
	expectCode(t, h, h.run("validate", "--json"), 1)
	data = h.envelope()["data"].(map[string]any)
	codes = ""
	for _, raw := range data["issues"].([]any) {
		codes += raw.(map[string]any)["code"].(string) + " "
	}
	if !strings.Contains(codes, "structure_source_unmapped") {
		t.Fatalf("codes = %s", codes)
	}
}

func TestStructurePlanDraftsFromTheLayoutAndWrites(t *testing.T) {
	h := guidedHarness(t)
	h.write(".gravity.yaml", "version: 2\nproduct: polaris\n")
	if err := os.MkdirAll(filepath.Join(h.dir, "docs", "guides"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("docs/guides/start.md", "# Start here\n\nSteps.\n")
	expectCode(t, h, h.run("structure", "plan"), 0)
	h.goldenText("structure-plan.txt", normalizeOut(h, h.stdout.String()))
	expectCode(t, h, h.run("structure", "plan", "--write", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["written"] != true || data["pass"].(map[string]any)["name"] != "docs" {
		t.Fatalf("data = %v", data)
	}
	written, _ := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml"))
	for _, want := range []string{"product: polaris", "structure:", "source: packages/admin/README.md", "slug: admin-overview", "- name: docs", "kind: verbatim"} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("missing %q in\n%s", want, written)
		}
	}
	if strings.Contains(string(written), "packages/api/README.md") {
		t.Fatalf("an empty README is not proposed:\n%s", written)
	}
	expectCode(t, h, h.run("structure", "plan", "--write", "--json"), 0)
	again, _ := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml"))
	if string(again) != string(written) {
		t.Fatalf("plan --write is idempotent:\n%s\n---\n%s", written, again)
	}
}

func TestStructureApplyReportsAndNeedsANewerServer(t *testing.T) {
	h := guidedHarness(t)
	h.platform.json("POST /api/v1/structure/apply", 200, `{"created":[{"kind":"space","path":"guides","title":"Guides"},{"kind":"page","path":"docs/admin#faq","title":"FAQ"}],"updated":[],"unchanged":[{"kind":"space","path":"docs"}],"extra":[{"kind":"page","path":"docs#legacy","title":"Legacy"}],"conflicts":[],"deferred":[{"kind":"page","path":"docs#overview","reason":"deferred_to_import"},{"kind":"page","path":"docs/admin#admin-overview","reason":"deferred_to_import"}]}`)
	expectCode(t, h, h.run("structure", "apply", "--dry-run"), 0)
	h.goldenText("structure-apply.txt", normalizeOut(h, h.stdout.String()))
	body := h.platform.find("POST", "/api/v1/structure/apply")[0].Body
	if body["dryRun"] != true || body["structure"].(map[string]any)["site"].(map[string]any)["slug"] != "polaris" {
		t.Fatalf("body = %v", body)
	}
	h.platform.json("POST /api/v1/structure/apply", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	expectCode(t, h, h.run("structure", "apply", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "server_unsupported" {
		t.Fatalf("error = %v", e)
	}
}

func TestSetupWritesTheManifestAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	if err := os.MkdirAll(filepath.Join(h.dir, "packages", "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write("packages/admin/README.md", "# @acme/polaris-admin\n\nThe admin console.\n")
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "admin")
	h.platform.json("GET /api/v1/products", 200, `{"products":[{"slug":"acme-platform","name":"Acme Platform","repos":[],"targets":[]}]}`)
	h.platform.json("GET /api/v1/sites", 200, `{"sites":[{"id":"site_1","slug":"dev-portal","name":"Developer Portal"}]}`)
	h.platform.json("GET /api/v1/structure", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	h.platform.json("GET /api/v1/sites/dev-portal", 200, `{"site":{"id":"site_1","slug":"dev-portal","name":"Developer Portal"},"spaces":[{"id":"sp_1","slug":"handbook","name":"Handbook","type":"knowledge-base"}],"collections":[]}`)
	h.platform.json("POST /api/v1/repos/self/validate", 200, `{"ok":true,"issues":[],"i18n":[]}`)
	expectCode(t, h, h.run("setup", "--json"), 0)
	proposal := h.envelope()["data"].(map[string]any)
	if proposal["proposal"] != true || proposal["manifest"].(map[string]any)["action"] != "create" {
		t.Fatalf("proposal = %v", proposal)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("--json alone writes nothing")
	}
	expectCode(t, h, h.run("setup"), 2)
	if !strings.Contains(h.stderr.String(), "needs a terminal") {
		t.Fatal(h.stderr.String())
	}
	expectCode(t, h, h.run("setup", "--yes"), 0)
	written, err := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"product: acme-platform", "slug: dev-portal", "slug: handbook", "source: packages/admin/README.md", "- name: docs", "target: dev-portal/handbook"} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("missing %q in\n%s", want, written)
		}
	}
	if !strings.Contains(h.stdout.String(), "Setup done") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
	if c := h.platform.find("POST", "/api/v1/repos/connect"); len(c) == 0 || c[0].Body["dryRun"] != false {
		t.Fatalf("setup registers the repository: %v", c)
	}
	expectCode(t, h, h.run("setup", "--yes", "--json"), 0)
	if a := h.envelope()["data"].(map[string]any)["manifest"].(map[string]any)["action"]; a != "keep" {
		t.Fatalf("second setup action = %v", a)
	}
	again, _ := os.ReadFile(filepath.Join(h.dir, ".gravity.yaml"))
	if string(again) != string(written) {
		t.Fatalf("setup is idempotent:\n%s\n---\n%s", written, again)
	}
}

func TestDryRunIsRecordedAndSentWithoutRecomputing(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("run", "--dry-run", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["recording"] != ".gravity/runs/prun_1.json" || data["mode"] != "dry" || len(data["pages"].([]any)) != 1 {
		t.Fatalf("data = %v", data)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity", ".gitignore")); err != nil {
		t.Fatal("the recording directory ignores itself")
	}
	if len(h.platform.find("POST", "/api/v1/runs/prun_1/changes")) != 0 {
		t.Fatal("a dry run writes nothing")
	}
	if !strings.Contains(h.stderr.String(), "Send it:  gravity run --send prun_1") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if out := gitCmd(t, h.dir, "status", "--porcelain"); out != "" {
		t.Fatalf("a dry run leaves the tree clean: %q", out)
	}
	h.write("NOTES.md", "later\n")
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "notes")
	expectCode(t, h, h.run("run", "--send", "prun_1", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "send_mismatch" {
		t.Fatalf("error = %v", e)
	}
	expectCode(t, h, h.run("run", "--dry-run", "--json"), 0)
	starts := len(h.platform.find("POST", "/api/v1/runs"))
	expectCode(t, h, h.run("run", "--send", "latest", "--json"), 0)
	data = h.envelope()["data"].(map[string]any)
	if data["sentFrom"] != "prun_1" || data["reviewUrl"] != "https://app.gravitydocs.io/app/repos/runs/prun_1" {
		t.Fatalf("data = %v", data)
	}
	all := h.platform.find("POST", "/api/v1/runs")
	if len(all) != starts+1 {
		t.Fatalf("send starts exactly one run: %d", len(all)-starts)
	}
	start := all[len(all)-1].Body
	if start["mode"] != "write" || start["manifestHash"] == nil || !strings.Contains(start["note"].(string), "prun_1") {
		t.Fatalf("start = %v", start)
	}
	if n := len(h.platform.find("POST", "/api/v1/runs/prun_1/changes")); n != 1 {
		t.Fatalf("the recorded change is sent once, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity", "runs", "prun_1.json")); !os.IsNotExist(err) {
		t.Fatal("a sent recording is removed")
	}
}

func TestRunRefusesABranchBehindItsUpstream(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitCmd(t, h.dir, "init", "-q", "--bare", bare)
	gitCmd(t, h.dir, "remote", "add", "local", bare)
	gitCmd(t, h.dir, "push", "-q", "-u", "local", "main")
	other := filepath.Join(t.TempDir(), "other")
	gitCmd(t, filepath.Dir(other), "clone", "-q", "-b", "main", bare, other)
	gitCmd(t, other, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(other, "x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, other, "add", "-A")
	gitCmd(t, other, "commit", "-q", "-m", "x")
	gitCmd(t, other, "push", "-q", "origin", "main")
	expectCode(t, h, h.run("run", "--dry-run", "--json"), 2)
	e := h.envelope()["error"].(map[string]any)
	if e["code"] != "branch_behind" || !strings.Contains(e["message"].(string), "git pull --ff-only") || !strings.Contains(e["message"].(string), "1 commit behind local/main") {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.find("POST", "/api/v1/runs")) != 0 {
		t.Fatal("nothing starts on a stale branch")
	}
	gitCmd(t, h.dir, "pull", "-q", "--ff-only")
	expectCode(t, h, h.run("run", "--dry-run"), 0)
	if !strings.Contains(h.stdout.String(), "up to date with local/main") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
}

func TestLeaseWaitIsAnnouncedEvenWhenQuiet(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	held := 0
	start := h.platform.routes["POST /api/v1/runs"]
	h.platform.handle("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if held < 2 {
			held++
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"error":{"code":"lease_held","message":"Another run holds main","retryAfter":5,"holder":{"runId":"prun_9","headSha":"abcdef1234","expiresAt":"2026-10-01T12:10:00Z"}}}`)
			return
		}
		start(w, r, body)
	})
	h.platform.json("GET /api/v1/runs/prun_9", 200, `{"run":{"id":"prun_9","status":"running","mode":"write","trigger":"push","branch":"main","headSha":"abcdef1234","startedAt":"2026-10-01T11:46:00Z","appUrl":""},"passes":[],"bundle":{"changes":0},"captureRuns":[]}`)
	expectCode(t, h, h.run("run", "-q"), 0)
	out := h.stdout.String()
	if strings.Count(out, "waiting for run prun_9 (push on main, started 14m ago), which holds main - timeout 20m0s") != 1 || !strings.Contains(out, "gravity runs cancel prun_9") {
		t.Fatalf("stdout = %s", out)
	}
	if !strings.Contains(out, "still waiting") {
		t.Fatalf("plain output reports each retry: %s", out)
	}
}

func TestRunsListShowCancelAndReview(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/repos/self/runs", 200, `{"runs":[{"id":"prun_2","trigger":"manual","mode":"write","status":"running","branch":"feat/x","startedAt":"2026-10-01T11:58:00Z","changes":3,"findings":0,"costUsd":0.31},{"id":"prun_1","trigger":"push","mode":"dry","status":"succeeded","branch":"main","startedAt":"2026-09-30T10:00:00Z","changes":0,"findings":0,"costUsd":0}]}`)
	expectCode(t, h, h.run("runs"), 0)
	h.goldenText("runs.txt", normalizeOut(h, h.stdout.String()))
	h.platform.json("GET /api/v1/runs/prun_2", 200, `{"run":{"id":"prun_2","status":"running","mode":"write","trigger":"manual","branch":"feat/x","headSha":"0123456789abcdef","startedAt":"2026-10-01T11:58:00Z","changesCount":3,"llmCalls":7,"costUsd":0.31,"appUrl":"https://app.gravitydocs.io/app/repos/runs/prun_2","lease":{"key":"cr_1:feat/x","expiresAt":"2026-10-01T12:05:00Z"}},
"passes":[{"runPassId":"ppr_1","name":"docs","kind":"verbatim","status":"succeeded","changesCount":3,"costUsd":0},{"runPassId":"ppr_2","name":"user-guides","kind":"guides","status":"running","changesCount":0,"costUsd":0.31,"progress":{"step":"writing pages","done":2,"total":5}}],
"bundle":{"changes":3,"accepted":1,"declined":0,"appUrl":"https://app.gravitydocs.io/app/changes/cr_9"},"captureRuns":[]}`)
	expectCode(t, h, h.run("runs", "show", "latest"), 0)
	h.goldenText("runs-show.txt", normalizeOut(h, h.stdout.String()))
	expectCode(t, h, h.run("review", "--json"), 0)
	if d := h.envelope()["data"].(map[string]any); d["url"] != "https://app.gravitydocs.io/app/changes/cr_9" || d["runId"] != "prun_2" {
		t.Fatalf("review = %v", d)
	}
	h.platform.json("POST /api/v1/runs/prun_2/cancel", 200, `{"run":{"id":"prun_2","status":"cancelled"}}`) //nolint:misspell // platform status value
	expectCode(t, h, h.run("runs", "cancel", "prun_2"), 0)
	if !strings.Contains(h.stdout.String(), "run prun_2 cancelled") { //nolint:misspell // platform status value
		t.Fatalf("stdout = %s", h.stdout.String())
	}
	h.platform.json("POST /api/v1/runs/prun_2/cancel", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	expectCode(t, h, h.run("runs", "cancel", "prun_2", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "server_unsupported" {
		t.Fatalf("error = %v", e)
	}
}

func TestApproveListsApprovesAndFallsBack(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/repos/self/approvals", 200, `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api"},"canApprove":true,"pending":[{"target":"product/handbook","site":"product","space":"handbook","passes":["handbook"],"reasons":["skips_review"],"why":"verbatim imports go live without review","mayApprove":true},{"target":"ops/runbooks","site":"ops","space":"runbooks","passes":["internal"],"reasons":["private_space"],"mayApprove":false,"reason":"needs docs.write on ops"}],"granted":[]}`)
	expectCode(t, h, h.run("approve"), 0)
	out := h.stdout.String()
	for _, want := range []string{"Allow billing-api to write to product/handbook - verbatim imports go live without review", "the space is not public", "needs docs.write on ops"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out)
		}
	}
	h.platform.json("POST /api/v1/repos/self/approvals", 200, `{"approved":[{"target":"product/handbook","site":"product","space":"handbook","passes":["handbook"]}],"refused":[]}`)
	expectCode(t, h, h.run("approve", "product/handbook", "--json"), 0)
	body := h.platform.find("POST", "/api/v1/repos/self/approvals")[0].Body
	if spaces := body["spaces"].([]any); len(spaces) != 1 || spaces[0] != "product/handbook" || body["passes"] != nil {
		t.Fatalf("body = %v", body)
	}
	expectCode(t, h, h.run("approve", "handbook", "--json"), 0)
	body = h.platform.find("POST", "/api/v1/repos/self/approvals")[1].Body
	if names := body["passes"].([]any); len(names) != 1 || names[0] != "handbook" {
		t.Fatalf("body = %v", body)
	}
	h.platform.json("GET /api/v1/repos/self/approvals", 404, `{"error":{"code":"not_found","message":"no route"}}`)
	expectCode(t, h, h.run("approve", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if p := data["pending"].([]any); len(p) != 1 || p[0].(map[string]any)["approveUrl"] == "" {
		t.Fatalf("fallback = %v", data)
	}
}

func TestAgentInstallWritesThePluginSkill(t *testing.T) {
	h := newHarness(t)
	expectCode(t, h, h.run("agent", "install"), 0)
	src := filepath.Join("..", "..", "plugin", "skills", "gravity")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		want, _ := os.ReadFile(p)
		got, rerr := os.ReadFile(filepath.Join(h.dir, ".claude", "skills", "gravity", rel))
		if rerr != nil || string(got) != string(want) {
			t.Fatalf("%s differs from the plugin copy", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.write("AGENTS.md", "# Repo rules\n\nBe kind.\n")
	expectCode(t, h, h.run("agent", "install", "--tool", "agents-md"), 0)
	expectCode(t, h, h.run("agent", "install", "--tool", "agents-md"), 0)
	data, _ := os.ReadFile(filepath.Join(h.dir, "AGENTS.md"))
	if !strings.HasPrefix(string(data), "# Repo rules\n\nBe kind.\n\n<!-- gravity:begin -->\n") || strings.Count(string(data), "<!-- gravity:begin -->") != 1 || !strings.Contains(string(data), "## Hard rules") {
		t.Fatalf("AGENTS.md = %s", data[:min(len(data), 300)])
	}
	expectCode(t, h, h.run("agent", "install", "--tool", "cursor", "--json"), 0)
	rule, _ := os.ReadFile(filepath.Join(h.dir, ".cursor", "rules", "gravity.mdc"))
	if !strings.HasPrefix(string(rule), "---\ndescription: ") || strings.Contains(string(rule), "\nname: gravity\n") {
		t.Fatalf("cursor rule = %s", rule[:min(len(rule), 200)])
	}
	expectCode(t, h, h.run("agent", "install", "--tool", "vim"), 2)
}

func TestOrgListAndUse(t *testing.T) {
	h := newHarness(t)
	writeProfiles(t, h.config, "version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: "+h.platform.srv.URL+"\n    org: acme\n    token: gr_user_a\n    tokenKind: user\n  labs:\n    apiUrl: "+h.platform.srv.URL+"\n    org: acme-labs\n    token: gr_user_b\n    tokenKind: user\n")
	expectCode(t, h, h.run("org", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["current"] != "acme" || len(data["organizations"].([]any)) != 2 {
		t.Fatalf("data = %v", data)
	}
	expectCode(t, h, h.run("org", "use", "acme-labs"), 0)
	raw, _ := os.ReadFile(filepath.Join(h.config, "profiles.yaml"))
	if !strings.Contains(string(raw), "current: labs") {
		t.Fatalf("profiles = %s", raw)
	}
	expectCode(t, h, h.run("org", "use", "nope", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); !strings.Contains(e["message"].(string), "gravity login --org nope") {
		t.Fatalf("error = %v", e)
	}
}

func TestCISetupWritesTheWorkflowAndChecksIt(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.write(".gravity.yaml", "version: 2\n")
	expectCode(t, h, h.run("ci", "check", "--json"), 1)
	h.platform.json("POST /api/v1/repos/cr_1/tokens", 201, `{"token":"gr_repo_secret","key":{"keyHint":"r2D2","scopes":["repo:connect","runs:write"],"expiresAt":null}}`)
	expectCode(t, h, h.run("ci", "setup", "--provider", "github", "--no-secret", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if w := data["written"].([]any); len(w) != 1 || w[0] != ".github/workflows/gravity.yml" {
		t.Fatalf("written = %v", data["written"])
	}
	if !strings.Contains(h.stderr.String(), "gr_repo_secret") || strings.Contains(h.stdout.String(), "gr_repo_secret") {
		t.Fatal("the token is printed once, on stderr only")
	}
	expectCode(t, h, h.run("ci", "check", "--json"), 0)
	if d := h.envelope()["data"].(map[string]any); d["ok"] != true {
		t.Fatalf("check = %v", d)
	}
}

func TestRunJSONCarriesThePlanView(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	expectCode(t, h, h.run("run", "--dry-run", "--json"), 0)
	var env struct {
		Data struct {
			Plan struct {
				Mode   string `json:"mode"`
				Passes []struct {
					Name string `json:"name"`
					Run  bool   `json:"run"`
				} `json:"passes"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Plan.Mode != "dry" || len(env.Data.Plan.Passes) != 1 || !env.Data.Plan.Passes[0].Run {
		t.Fatalf("plan = %+v", env.Data.Plan)
	}
}

func TestDryRunRendersThePlanAndTheResult(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	plan := referencePlanPass("reference")
	plan["estimate"] = map[string]any{"ai": false, "firstRun": true, "commits": 2, "files": 1, "approxInputTokens": 0, "approxCostUsd": 0, "model": nil}
	guides := referencePlanPass("guides")
	guides["name"], guides["id"], guides["applies"], guides["skipReason"] = "user-guides", "rp_2", false, "target_unapproved"
	h.pipelineRoutes(t, pipelinePlan(t, plan, guides))
	expectCode(t, h, h.run("run", "--dry-run"), 0)
	h.goldenText("run-dry.txt", normalizeOut(h, h.stdout.String()))
	if os.Getenv("GRAVITY_SHOW_TTY") != "" {
		h.terminal = true
		h.env["NO_COLOR"] = "1"
		expectCode(t, h, h.run("run", "--dry-run", "--yes"), 0)
		t.Log("\n" + h.stdout.String())
	}
}
