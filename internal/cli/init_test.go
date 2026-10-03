package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

const productsBody = `{"products":[
{"id":"prod_2","slug":"zeta","name":"Zeta","repos":[],"targets":[]},
{"id":"prod_1","slug":"acme-platform","name":"Acme Platform","nucleusNamespace":"product:acme-platform","repos":[{"id":"cr_9","name":"gateway","remoteKey":"github.com/acme/gateway"}],"targets":[{"siteSlug":"dev-portal","spaceSlug":"api","passes":2}]}]}`

const sitesBody = `{"sites":[{"id":"site_2","slug":"product","name":"Product docs","position":0},{"id":"site_1","slug":"dev-portal","name":"Developer Portal","position":1}]}`

const devPortalTree = `{"site":{"id":"site_1","slug":"dev-portal","name":"Developer Portal"},"spaces":[{"id":"sp_1","slug":"api","name":"API","type":"api-reference"},{"id":"sp_2","slug":"guides","name":"Guides","type":"product-docs"}],"collections":[]}`

const productTree = `{"site":{"id":"site_2","slug":"product","name":"Product docs"},"spaces":[{"id":"sp_3","slug":"changelog","name":"Changelog","type":"release-notes"}],"collections":[]}`

const connectFresh = `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","created":true,"createdVia":"cli","product":{"id":"prod_1","slug":"acme-platform","name":"Acme Platform"},"appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"manifest":{"version":2,"hash":"sha256:abc","accepted":true,"persisted":true,"reason":null,"authoritativeBranch":"main","passesUpserted":[],"passesConverted":[],"passesArchived":[],"warnings":[]},
"effective":{"appPasses":"allow","overlay":false,"passes":[]},"createdTargets":[],"siblings":[],"serverFeatures":{"pipelines":true}}`

func seedRepo(t *testing.T, h *harness) {
	t.Helper()
	files := map[string]string{
		"api/openapi.yaml":        "openapi: 3.1.0\ninfo: {title: Billing, version: '1'}\npaths:\n  /refunds:\n    get: {responses: {'200': {description: ok}}}\n    post: {responses: {'201': {description: ok}}}\n",
		"src/routes/index.tsx":    "export default function Home() { return null }\n",
		"src/routes/refunds.tsx":  "export default function Refunds() { return null }\n",
		"docs/handbook/deploy.md": "# Deploy\n",
		"docs/handbook/oncall.md": "# On call\n",
		"CHANGELOG.md":            "# Changelog\n",
	}
	for name, body := range files {
		full := filepath.Join(h.dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd(t, h.dir, "add", "-A")
	gitCmd(t, h.dir, "commit", "-q", "-m", "app")
	gitCmd(t, h.dir, "tag", "v1.0.0")
}

func initPlatform(h *harness, connect string) {
	p := h.platform
	p.json("GET /api/v1/products", 200, productsBody)
	p.json("GET /api/v1/sites", 200, sitesBody)
	p.json("GET /api/v1/sites/dev-portal", 200, devPortalTree)
	p.json("GET /api/v1/sites/product", 200, productTree)
	p.json("POST /api/v1/repos/connect", 200, connect)
	p.handle("PUT /api/v1/repos/cr_1/passes/*", func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		target, _ := body["target"].(string)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+name+`","kind":"`+body["kind"].(string)+`","source":"app","locked":false,"enabled":true,"target":{"ref":"`+target+`","status":"ok"}}`)
	})
	p.handle("POST /api/v1/repos/cr_1/tokens", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		w.WriteHeader(http.StatusCreated)
		scopes, _ := jsonString(body["scopes"])
		_, _ = io.WriteString(w, `{"token":"gr_repo_minted_secret","key":{"id":"key_1","keyHint":"m1nt","kind":"repo","repoId":"cr_1","scopes":`+scopes+`,"expiresAt":null,"createdAt":"2026-10-01T10:00:00Z"}}`)
	})
}

func jsonString(v any) (string, error) {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, `"`+e.(string)+`"`)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func putPasses(h *harness) []string {
	var names []string
	for _, r := range h.platform.requests {
		if r.Method == "PUT" && strings.HasPrefix(r.Path, "/api/v1/repos/cr_1/passes/") {
			names = append(names, strings.TrimPrefix(r.Path, "/api/v1/repos/cr_1/passes/"))
		}
	}
	sort.Strings(names)
	return names
}

func mintedScopes(t *testing.T, h *harness) []string {
	t.Helper()
	reqs := h.platform.find("POST", "/api/v1/repos/cr_1/tokens")
	if len(reqs) != 1 {
		t.Fatalf("token mints = %d", len(reqs))
	}
	var out []string
	for _, s := range reqs[0].Body["scopes"].([]any) {
		out = append(out, s.(string))
	}
	return out
}

func questions(h *harness) []string {
	var out []string
	for _, title := range h.prompts.titles {
		if title != "Site" {
			out = append(out, title)
		}
	}
	return out
}

func readFile(t *testing.T, h *harness, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestInitFreshAsksThreeQuestions(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n0\n\n"
	expectCode(t, h, h.run("init"), 0)
	if q := questions(h); len(q) != 3 || q[0] != "Product" || q[2] != "Write these and wire CI?" {
		t.Fatalf("questions = %v\nstderr: %s", q, h.stderr.String())
	}
	manifest := readFile(t, h, ".gravity.yaml")
	if manifest != "version: 2\nproduct: acme-platform\ncode:\n  openapi: [api/openapi.yaml]\n" {
		t.Fatalf("manifest = %q", manifest)
	}
	if _, err := config.Load(filepath.Join(h.dir, ".gravity.yaml")); err != nil {
		t.Fatalf("written manifest must be valid: %v", err)
	}
	workflow := readFile(t, h, ".github/workflows/gravity.yml")
	if !strings.Contains(workflow, "branches: [main]") || strings.Contains(workflow, "pull_request_target") {
		t.Fatalf("workflow = %s", workflow)
	}
	if got := putPasses(h); !reflect.DeepEqual(got, []string{"changelog", "developer-api", "memory", "product-guides"}) {
		t.Fatalf("registered passes = %v", got)
	}
	put := h.platform.find("PUT", "/api/v1/repos/cr_1/passes/developer-api")[0].Body
	if put["kind"] != "reference" || put["template"] != "api-reference" || put["target"] != "dev-portal/api" {
		t.Fatalf("developer-api = %v", put)
	}
	guides := h.platform.find("PUT", "/api/v1/repos/cr_1/passes/product-guides")[0].Body
	if guides["target"] != "dev-portal/guides" || guides["scope"].(map[string]any)["paths"].([]any)[0] != "src/routes/**" {
		t.Fatalf("product-guides = %v", guides)
	}
	want := []string{"repo:connect", "runs:write", "content:read", "content:propose", "inventory:write", "nucleus:read", "nucleus:write", "llm"}
	if got := mintedScopes(t, h); !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %v, want %v", got, want)
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	last := conn[len(conn)-1].Body
	if last["dryRun"] != false || last["product"] != nil && last["product"] != "acme-platform" {
		t.Fatalf("connect = %v", last)
	}
	ct, _ := last["createTargets"].([]any)
	if len(ct) != 1 || ct[0].(map[string]any)["space"] != "changelog" || ct[0].(map[string]any)["site"] != "dev-portal" {
		t.Fatalf("createTargets = %v", ct)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "gr_repo_minted_secret") || !strings.Contains(stderr, "GRAVITY_TOKEN (shown once)") || !strings.Contains(stderr, "https://github.com/acme/billing-api/settings/secrets/actions/new") {
		t.Fatalf("the token is printed once on stderr:\n%s", stderr)
	}
	if strings.Contains(h.stdout.String(), "gr_repo_minted_secret") {
		t.Fatal("the token never goes to stdout")
	}
	if !strings.Contains(h.stdout.String(), "gravity preview") {
		t.Fatalf("out = %s", h.stdout.String())
	}
}

func TestInitYesAsksNothing(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--json", "--ci", "gitlab"), 0)
	if len(h.prompts.titles) != 0 {
		t.Fatalf("--yes asked %v", h.prompts.titles)
	}
	data := h.envelope()["data"].(map[string]any)
	if data["questions"].(float64) != 0 || data["product"] != "acme-platform" || data["mode"] != "fresh" {
		t.Fatalf("data = %v", data)
	}
	for _, f := range []string{".gitlab/gravity.yml", ".gitlab-ci.yml", ".gravity.yaml"} {
		if _, err := os.Stat(filepath.Join(h.dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if strings.Contains(h.stdout.String(), "gr_repo_minted_secret") {
		t.Fatal("the token never goes to the JSON document")
	}
	if ci := data["ci"].(map[string]any); ci["commentToken"] != "GITLAB_TOKEN" || !strings.Contains(ci["commentHint"].(string), "api scope") {
		t.Fatalf("init names the GitLab comment token: %v", ci)
	}
	for _, c := range h.platform.find("POST", "/api/v1/repos/connect") {
		repo := c.Body["repo"].(map[string]any)
		if repo["defaultBranch"] == nil || repo["defaultBranch"] != repo["branch"] {
			t.Fatalf("without origin/HEAD init registers the checked-out branch as default: %v", repo)
		}
	}
}

func TestInitNeedsTerminalOrYes(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "needs_terminal" {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.requests) != 0 {
		t.Fatal("nothing is asked of the platform before the flags are valid")
	}
}

var connectAppPasses = strings.Replace(connectBody, `"source":"manifest","locked":true`, `"source":"app","locked":false`, 1)

func TestInitAlreadyConnectedAsksOnlyToWrite(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectAppPasses)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n"
	expectCode(t, h, h.run("init"), 0)
	if q := questions(h); len(q) != 1 || q[0] != "Write these and wire CI?" {
		t.Fatalf("questions = %v", q)
	}
	if len(putPasses(h)) != 0 {
		t.Fatal("passes already in the app are not registered again")
	}
	want := []string{"repo:connect", "runs:write", "content:read", "content:propose", "inventory:write", "nucleus:read", "llm"}
	if got := mintedScopes(t, h); !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %v, want %v", got, want)
	}
	if manifest := readFile(t, h, ".gravity.yaml"); manifest != "version: 2\nproduct: acme-platform\n" {
		t.Fatalf("manifest = %q", manifest)
	}
	if !strings.Contains(h.stdout.String(), "Approve product-guides") {
		t.Fatalf("summary links the approval: %s", h.stdout.String())
	}
}

func TestInitAdoptsPreRegisteredRepo(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectAppPasses)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--repo", "cr_other"), 2)
	if !strings.Contains(h.stderr.String(), "registered as cr_1, not cr_other") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	expectCode(t, h, h.run("init", "--yes", "--repo", "cr_1", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["mode"] != "connected" {
		t.Fatalf("data = %v", data)
	}
}

const v1Manifest = `version: 1
site: docs
product:
  slug: acme-platform
  repo: billing
  role: api
spaces:
  default: guides
  declare:
    - slug: handbook
      name: Handbook
      type: handbook
documents:
  - file: docs/handbook/deploy.md
    space: handbook
    page: deploy
    title: Deploying
    ownership: human
releaseNotes:
  space: changelog
`

func TestInitConvertsV1Manifest(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	h.write(".gravity.yaml", v1Manifest)
	initPlatform(h, connectFresh)
	h.platform.json("GET /api/v1/sites/docs", 200, `{"site":{"slug":"docs","name":"Docs"},"spaces":[{"slug":"guides","name":"Guides","type":"product-docs"}],"collections":[]}`)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n\n"
	before := snapshot(t, h.dir)
	expectCode(t, h, h.run("init"), 0)
	if q := questions(h); len(q) != 2 || !strings.HasPrefix(q[0], "Convert .gravity.yaml") {
		t.Fatalf("questions = %v", q)
	}
	if bak := readFile(t, h, ".gravity.v1.yaml.bak"); bak != v1Manifest {
		t.Fatalf("backup = %q", bak)
	}
	m, err := config.Load(filepath.Join(h.dir, ".gravity.yaml"))
	if err != nil {
		t.Fatalf("converted manifest: %v", err)
	}
	verbatim, ok := m.PassByName("docs-handbook")
	if !ok || verbatim.Options["adopt"] != true {
		t.Fatalf("verbatim pass = %+v", verbatim)
	}
	files := verbatim.Options["files"].([]any)[0].(map[string]any)
	if files["slug"] != "deploy" || files["title"] != "Deploying" || files["include"] != "docs/handbook/deploy.md" {
		t.Fatalf("files = %v", files)
	}
	if len(putPasses(h)) != 0 {
		t.Fatal("converted passes are declared in the manifest, not registered in the app")
	}
	after := snapshot(t, h.dir)
	var changed []string
	for name, sum := range after {
		if before[name] != sum {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	if !reflect.DeepEqual(changed, []string{".github/workflows/gravity.yml", ".gravity.v1.yaml.bak", ".gravity.yaml"}) {
		t.Fatalf("changed files = %v", changed)
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	ct := conn[len(conn)-1].Body["createTargets"].([]any)
	var spaces []string
	for _, c := range ct {
		spaces = append(spaces, c.(map[string]any)["space"].(string))
	}
	if !reflect.DeepEqual(spaces, []string{"handbook", "changelog"}) {
		t.Fatalf("createTargets = %v", spaces)
	}
	want := []string{"repo:connect", "runs:write", "content:read", "content:propose", "content:verbatim", "inventory:write", "nucleus:read", "llm"}
	if got := mintedScopes(t, h); !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %v, want %v", got, want)
	}

	h.stdin = "\n"
	h.platform.requests = nil
	expectCode(t, h, h.run("init"), 0)
	if q := questions(h); len(q) != 1 {
		t.Fatalf("a converted manifest is not converted again: %v", q)
	}
	if bak := readFile(t, h, ".gravity.v1.yaml.bak"); bak != v1Manifest {
		t.Fatal("the backup is never overwritten by a second init")
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInitAppPassesConversion(t *testing.T) {
	h := newHarness(t)
	h.write(".gravity.yaml", v1Manifest)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--app-passes", "--ci", "none"), 0)
	if got := putPasses(h); !reflect.DeepEqual(got, []string{"changelog", "docs", "docs-handbook"}) {
		t.Fatalf("registered = %v", got)
	}
	if manifest := readFile(t, h, ".gravity.yaml"); manifest != "version: 2\nproduct: acme-platform\ncode:\n  units:\n    kind: service\n" {
		t.Fatalf("manifest = %q", manifest)
	}
	if !strings.Contains(h.stdout.String(), "gravity run") {
		t.Fatalf("--ci none prints the generic snippet: %s", h.stdout.String())
	}
}

func TestInitDeveloperRoleAsksNothing(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(strings.Replace(whoamiUser, `"docs.read","docs.write","docs.repos.manage","docs.repos.tokens"`, `"docs.read"`, 1), `"role":"editor"`, `"role":"developer"`, 1))
	expectCode(t, h, h.run("init"), 2)
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "can't connect repositories in Acme (role: developer)") || !strings.Contains(stderr, "/app/repos/connect") || !strings.Contains(stderr, "gravity init --repo <id>") || !strings.Contains(stderr, "--dry-run") {
		t.Fatalf("stderr = %s", stderr)
	}
	if len(h.prompts.titles) != 0 || len(h.platform.find("POST", "/api/v1/repos/connect")) != 0 {
		t.Fatal("no question and no connect without permission")
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--dry-run", "--no-secret", "--product", "acme-platform"), 0)
	if len(h.prompts.titles) != 0 {
		t.Fatalf("no prompt without a terminal: %v", h.prompts.titles)
	}
	out := h.stdout.String()
	for _, want := range []string{"+ .gravity.yaml (4 lines)", "product: acme-platform", "+ .github/workflows/gravity.yml", "uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1", "+ app passes: developer-api -> dev-portal/api", "+ new space dev-portal/changelog", "+ repository token with repo:connect"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in preview:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("--dry-run must not write")
	}
	for _, r := range h.platform.requests {
		if r.Method == "PUT" || strings.HasSuffix(r.Path, "/tokens") || r.Body["dryRun"] == false {
			t.Fatalf("--dry-run made a write: %s %s", r.Method, r.Path)
		}
	}
}

func TestInitWritesNothingWhenConnectFails(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.handle("POST /api/v1/repos/connect", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["dryRun"] == true {
			_, _ = io.WriteString(w, connectFresh)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"Not allowed"}}`)
	})
	expectCode(t, h, h.run("init", "--yes"), 2)
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("a failed connect leaves no manifest behind")
	}
	if len(h.platform.find("POST", "/api/v1/repos/cr_1/tokens")) != 0 {
		t.Fatal("no token without a connection")
	}
}

func fakeTool(t *testing.T, dir, name string) {
	t.Helper()
	script := "#!/bin/sh\necho \"$@\" >> \"" + filepath.Join(dir, name+".args") + "\"\nif [ \"$1\" != auth ]; then cat > \"" + filepath.Join(dir, name+".stdin") + "\"; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInitInstallsSecretWithGhOnStdin(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	bin := t.TempDir()
	fakeTool(t, bin, "gh")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.secrets = cisetup.ExecRunner
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n0\n\n"
	expectCode(t, h, h.run("init"), 0)
	args, _ := os.ReadFile(filepath.Join(bin, "gh.args"))
	if !strings.Contains(string(args), "auth status --hostname github.com") || !strings.Contains(string(args), "secret set GRAVITY_TOKEN --repo acme/billing-api") {
		t.Fatalf("gh args = %s", args)
	}
	if strings.Contains(string(args), "gr_repo_minted_secret") {
		t.Fatal("the token must never be passed in argv")
	}
	if stdin, _ := os.ReadFile(filepath.Join(bin, "gh.stdin")); string(stdin) != "gr_repo_minted_secret" {
		t.Fatalf("gh stdin = %q", stdin)
	}
	if strings.Contains(h.stderr.String(), "gr_repo_minted_secret") || strings.Contains(h.stdout.String(), "gr_repo_minted_secret") {
		t.Fatal("an installed token is never printed")
	}
}

func TestInitInstallsSecretWithGlab(t *testing.T) {
	h := newHarness(t)
	gitCmd(t, h.dir, "remote", "set-url", "origin", "git@gitlab.com:acme/billing-api.git")
	initPlatform(h, strings.ReplaceAll(connectFresh, "github.com", "gitlab.com"))
	bin := t.TempDir()
	fakeTool(t, bin, "glab")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.secrets = cisetup.ExecRunner
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes"), 0)
	args, _ := os.ReadFile(filepath.Join(bin, "glab.args"))
	if !strings.Contains(string(args), "variable set GRAVITY_TOKEN --masked --repo acme/billing-api") || strings.Contains(string(args), "gr_repo_minted_secret") {
		t.Fatalf("glab args = %s", args)
	}
	if stdin, _ := os.ReadFile(filepath.Join(bin, "glab.stdin")); string(stdin) != "gr_repo_minted_secret" {
		t.Fatalf("glab stdin = %q", stdin)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gitlab", "gravity.yml")); err != nil {
		t.Fatal(err)
	}
	if out := h.stdout.String(); !strings.Contains(out, "Comments: create a project access token") || !strings.Contains(out, "GITLAB_TOKEN") {
		t.Fatalf("the closing summary names the GitLab comment token:\n%s", out)
	}
}

func TestInitNoSecretPrintsToken(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	bin := t.TempDir()
	fakeTool(t, bin, "gh")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.secrets = cisetup.ExecRunner
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--no-secret"), 0)
	if args, _ := os.ReadFile(filepath.Join(bin, "gh.args")); len(args) != 0 {
		t.Fatalf("--no-secret never calls gh: %s", args)
	}
	if !strings.Contains(h.stderr.String(), "gr_repo_minted_secret") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestInitChangeSiteStaysInsideTheSecondQuestion(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n6\n0\n1\n0\n\n"
	expectCode(t, h, h.run("init"), 0)
	if q := questions(h); len(q) != 4 || q[1] != q[2] {
		t.Fatalf("questions = %v", h.prompts.titles)
	}
	if n := len(h.prompts.titles) - len(questions(h)); n != 1 {
		t.Fatalf("site sub-list asked %d times", n)
	}
	put := h.platform.find("PUT", "/api/v1/repos/cr_1/passes/changelog")
	if len(put) != 1 || put[0].Body["target"] != "product/changelog" {
		t.Fatalf("changelog after changing site = %+v", put)
	}
}

func TestInitCancelWritesNothing(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n0\n2\n"
	expectCode(t, h, h.run("init"), 0)
	if !strings.Contains(h.stdout.String(), "Canceled; nothing was written.") {
		t.Fatalf("out = %s", h.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("cancel writes nothing")
	}
	for _, r := range h.platform.requests {
		if r.Body["dryRun"] == false {
			t.Fatal("cancel connects nothing")
		}
	}
}

func TestInitPassesAsCode(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--passes-as-code"), 0)
	m, err := config.Load(filepath.Join(h.dir, ".gravity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range m.Passes {
		names = append(names, p.Name)
	}
	if !reflect.DeepEqual(names, []string{"developer-api", "product-guides", "changelog", "memory"}) {
		t.Fatalf("passes = %v", names)
	}
	if len(putPasses(h)) != 0 {
		t.Fatal("passes-as-code registers nothing through PUT")
	}
	conn := h.platform.find("POST", "/api/v1/repos/connect")
	if conn[len(conn)-1].Body["manifest"].(map[string]any)["passes"] == nil {
		t.Fatal("the real connect carries the declared passes")
	}
}

func TestInitInteractiveLoginNeverFollowsManifestAPIURL(t *testing.T) {
	h := newHarness(t)
	h.terminal = true
	delete(h.env, "GRAVITY_API_URL")
	approveDevice(h)
	initPlatform(h, connectFresh)
	h.write(".gravity.yaml", "version: 2\napiUrl: "+h.platform.srv.URL+"\n")
	expectCode(t, h, h.run("init"), 2)
	if !strings.Contains(h.stderr.String(), "gravity login --api-url "+h.platform.srv.URL) {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if len(h.platform.requests) != 0 {
		t.Fatalf("no request may reach the manifest host without opt-in: %+v", h.platform.requests)
	}
	expectCode(t, h, h.run("init", "--yes", "--json"), 4)
	if e := h.envelope()["error"].(map[string]any); !strings.Contains(e["message"].(string), "not signed in") {
		t.Fatalf("non-interactive error = %v", e)
	}
	if len(h.platform.requests) != 0 {
		t.Fatalf("requests = %+v", h.platform.requests)
	}

	expectCode(t, h, h.run("login", "--api-url", h.platform.srv.URL), 0)
	if len(h.platform.find("POST", "/api/v1/auth/device/start")) != 1 {
		t.Fatal("an explicit --api-url opts in to that host")
	}
	h.stdin = "\n0\n\n"
	expectCode(t, h, h.run("init"), 0)
	if reqs := h.platform.find("GET", "/api/v1/whoami"); len(reqs) != 1 || reqs[0].Token != "gr_user_device" {
		t.Fatalf("whoami = %+v", reqs)
	}
	if len(h.platform.find("POST", "/api/v1/auth/device/start")) != 1 {
		t.Fatal("init reuses the stored profile")
	}
}

func TestInitInteractiveSignsIn(t *testing.T) {
	h := newHarness(t)
	h.terminal = true
	approveDevice(h)
	initPlatform(h, connectFresh)
	h.stdin = "0\n\n"
	expectCode(t, h, h.run("init", "--product", "acme-platform"), 0)
	if len(h.platform.find("POST", "/api/v1/auth/device/start")) != 1 || len(h.opened) != 1 {
		t.Fatalf("init signs in through the device flow: opened %v", h.opened)
	}
	if reqs := h.platform.find("GET", "/api/v1/whoami"); len(reqs) != 1 || reqs[0].Token != "gr_user_device" {
		t.Fatalf("whoami = %+v", reqs)
	}
	if q := questions(h); len(q) != 2 {
		t.Fatalf("--product skips the product question: %v", q)
	}
	if data := readFile(t, h, ".gravity.yaml"); data != "version: 2\nproduct: acme-platform\n" {
		t.Fatalf("manifest = %q", data)
	}
}

func TestInitMarksTargetsThatNeedApproval(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.platform.handle("POST /api/v1/repos/connect", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		m, _ := body["manifest"].(map[string]any)
		if body["dryRun"] == true && m != nil && m["passes"] != nil {
			_, _ = io.WriteString(w, strings.Replace(connectFresh, `"warnings":[]`, `"warnings":[{"code":"target_unapproved","path":"passes[1].target","message":"dev-portal/guides needs approval"}]`, 1))
			return
		}
		_, _ = io.WriteString(w, connectFresh)
	})
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	h.stdin = "\n0\n\n"
	expectCode(t, h, h.run("init"), 0)
	if !strings.Contains(h.stderr.String(), "Developer Portal › Guides        guides     ← TanStack Router routes in src/routes (2)  (needs approval)") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if strings.Count(h.stderr.String(), "(needs approval)") != 1 {
		t.Fatal("only the target the server flags needs approval")
	}
}

type refusingPrompter struct{ t *testing.T }

func (p refusingPrompter) Select(title, _ string, _ []ui.Choice, _ string) (string, error) {
	p.t.Fatalf("prompted %q without a terminal", title)
	return "", nil
}

func (p refusingPrompter) MultiSelect(title, _ string, _ []ui.Choice, _ []string) ([]string, error) {
	p.t.Fatalf("prompted %q without a terminal", title)
	return nil, nil
}

func TestInitDryRunWithoutTerminalAsksNothing(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.prompter = refusingPrompter{t}
	expectCode(t, h, h.run("init", "--dry-run", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["questions"].(float64) != 0 || data["product"] == "" || len(data["passes"].([]any)) == 0 {
		t.Fatalf("data = %v", data)
	}

	h.write(".gravity.yaml", v1Manifest)
	expectCode(t, h, h.run("init", "--dry-run", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["mode"] != "convert" || data["questions"].(float64) != 0 {
		t.Fatalf("conversion data = %v", data)
	}
}

func TestInitRefusesWhenManifestPassesAreNotInTheCheckout(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectBody)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes"), 2)
	if !strings.Contains(h.stderr.String(), "not in this checkout (developer-api)") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("no manifest that would archive the stored passes")
	}
	for _, r := range h.platform.requests {
		if r.Body["dryRun"] == false {
			t.Fatal("nothing is connected")
		}
	}
}

func TestInitOffTheDefaultBranchNeverPromisesSpaces(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	gitCmd(t, h.dir, "switch", "-q", "-c", "docs/gravity")
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--dry-run", "--no-secret", "--product", "acme-platform"), 0)
	out := h.stdout.String()
	if strings.Contains(out, "+ new space") || !strings.Contains(out, "! new space dev-portal/changelog (Changelog), created only from main") || !strings.Contains(out, "this is docs/gravity, not main") {
		t.Fatalf("preview = %s", out)
	}
	expectCode(t, h, h.run("init", "--yes", "--no-secret", "--product", "acme-platform", "--json"), 2)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "branch_not_authoritative" || !strings.Contains(e["message"].(string), "git switch main") {
		t.Fatalf("error = %v", e)
	}
	if len(putPasses(h)) != 0 || len(h.platform.find("POST", "/api/v1/repos/cr_1/tokens")) != 0 {
		t.Fatal("nothing is registered or minted")
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".gravity.yaml")); !os.IsNotExist(err) {
		t.Fatal("nothing is written")
	}
}

func TestInitReportsSpacesTheConnectDidNotCreate(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.platform.handle("POST /api/v1/repos/connect", func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["dryRun"] == true {
			_, _ = io.WriteString(w, connectFresh)
			return
		}
		_, _ = io.WriteString(w, strings.Replace(connectFresh, `"persisted":true,"reason":null`, `"persisted":false,"reason":"branch_not_authoritative"`, 1))
	})
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--no-secret", "--product", "acme-platform"), 0)
	if !strings.Contains(h.stderr.String(), "did not create dev-portal/changelog: spaces are created only from main") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Spaces not created: dev-portal/changelog") {
		t.Fatalf("summary = %s", h.stdout.String())
	}
}

func TestInitConnectedCreatesMissingManifestTargets(t *testing.T) {
	h := newHarness(t)
	h.write(".gravity.yaml", "version: 2\nproduct: acme-platform\npasses:\n  - name: developer-api\n    kind: reference\n    template: api-reference\n    target: dev-portal/reference\n")
	conn := strings.Replace(connectBody, `"target":{"ref":"dev-portal/api","status":"ok","siteSlug":"dev-portal","spaceSlug":"api","collectionPath":[]}`, `"target":{"ref":"dev-portal/reference","status":"missing","siteSlug":"dev-portal","spaceSlug":"reference","collectionPath":[]}`, 1)
	conn = strings.Replace(conn, `"kind":"reference","source":"manifest"`, `"kind":"reference","template":"api-reference","source":"manifest"`, 1)
	initPlatform(h, conn)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--no-secret"), 0)
	if !strings.Contains(h.stdout.String(), "+ new space dev-portal/reference (Reference)") {
		t.Fatalf("preview = %s", h.stdout.String())
	}
	reqs := h.platform.find("POST", "/api/v1/repos/connect")
	last := reqs[len(reqs)-1]
	ct, _ := last.Body["createTargets"].([]any)
	if last.Body["dryRun"] != false || len(ct) != 1 || ct[0].(map[string]any)["space"] != "reference" || ct[0].(map[string]any)["type"] != "api-reference" {
		t.Fatalf("real connect = %v", last.Body)
	}
}

func TestInitMergesPassesIntoAnEmptyPassesKey(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	initPlatform(h, connectFresh)
	h.write(".gravity.yaml", "version: 2\nproduct: acme-platform\npasses: []\n")
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--no-secret", "--passes-as-code"), 0)
	data := readFile(t, h, ".gravity.yaml")
	if strings.Contains(data, "passes: []") || strings.Count(data, "passes:") != 1 {
		t.Fatalf("manifest = %s", data)
	}
	m, err := config.Load(filepath.Join(h.dir, ".gravity.yaml"))
	if err != nil || len(m.Passes) == 0 {
		t.Fatalf("manifest = %v, %v", m, err)
	}
}

func TestInitWithoutTerminalNeverPrintsTheToken(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes"), 0)
	if len(h.platform.find("POST", "/api/v1/repos/cr_1/tokens")) != 0 || strings.Contains(h.stderr.String(), "gr_repo_minted_secret") {
		t.Fatal("no token is minted to be printed into a log")
	}
	if !strings.Contains(h.stdout.String(), "pass --no-secret to print it anyway") {
		t.Fatalf("summary = %s", h.stdout.String())
	}
}

func TestInitShowsTheTokenWhenAFileCannotBeWritten(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	locked := filepath.Join(h.dir, ".github")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	expectCode(t, h, h.run("init", "--yes", "--no-secret"), 2)
	if !strings.Contains(h.stderr.String(), "gr_repo_minted_secret") || !strings.Contains(h.stderr.String(), "connected, but writing .github/workflows/gravity.yml failed") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestInitWarnsUpFrontWithoutTokenPermission(t *testing.T) {
	h := newHarness(t)
	initPlatform(h, connectFresh)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `,"docs.repos.tokens"`, "", 1))
	expectCode(t, h, h.run("init", "--yes", "--no-secret"), 0)
	if !strings.Contains(h.stderr.String(), "can't mint repository tokens in Acme") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	if len(h.platform.find("POST", "/api/v1/repos/cr_1/tokens")) != 0 {
		t.Fatal("no mint without the permission")
	}
}

func TestInitConvertsASiteLessV1WithTheDefaultSite(t *testing.T) {
	h := newHarness(t)
	h.write(".gravity.yaml", strings.Replace(v1Manifest, "site: docs\n", "", 1))
	initPlatform(h, connectFresh)
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `"defaultSiteSlug":null`, `"defaultSiteSlug":"docs"`, 1))
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--dry-run", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	if data["conversion"].(map[string]any)["site"] != "docs" || !strings.Contains(data["manifest"].(map[string]any)["content"].(string), "target: docs/") {
		t.Fatalf("data = %v", data["manifest"])
	}

	h.env["GRAVITY_SITE"] = "handbook-site"
	expectCode(t, h, h.run("init", "--dry-run", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["conversion"].(map[string]any)["site"] != "handbook-site" {
		t.Fatalf("GRAVITY_SITE wins: %v", data["conversion"])
	}
}

func fakeGhWithSecret(t *testing.T, dir string) {
	t.Helper()
	script := "#!/bin/sh\necho \"$@\" >> \"" + filepath.Join(dir, "gh.args") + "\"\n" +
		"if [ \"$1 $2\" = \"secret list\" ]; then printf 'GRAVITY_TOKEN\\t2026-09-01\\nOTHER\\t2026-09-01\\n'; exit 0; fi\n" +
		"if [ \"$1\" != auth ]; then cat > \"" + filepath.Join(dir, "gh.stdin") + "\"; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func legacyWorkflow(t *testing.T, h *harness) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(h.dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.write(".github/workflows/docs.yml", "on: push\njobs:\n  docs:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: Grupo-Impulso-Digital/gravity-cli/ci/github@main\n        with:\n          command: sync\n          token: ${{ secrets.GRAVITY_TOKEN }}\n")
}

func TestInitKeepsTheTokenOfALive0xPipeline(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	legacyWorkflow(t, h)
	initPlatform(h, connectFresh)
	bin := t.TempDir()
	fakeGhWithSecret(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.secrets = cisetup.ExecRunner
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.terminal = true
	expectCode(t, h, h.run("init", "--yes"), 0)
	args, _ := os.ReadFile(filepath.Join(bin, "gh.args"))
	if strings.Contains(string(args), "secret set") || !strings.Contains(string(args), "secret list --repo acme/billing-api") {
		t.Fatalf("a secret feeding a 0.x pipeline is never replaced by default: %s", args)
	}
	out := h.stdout.String() + h.stderr.String()
	for _, want := range []string{"still feeds a gravity 0.x pipeline (.github/workflows/docs.yml)", "gr_repo_minted_secret", "Set it when this change is merged"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}

	ci := newHarness(t)
	seedRepo(t, ci)
	legacyWorkflow(t, ci)
	initPlatform(ci, connectFresh)
	ci.secrets = cisetup.ExecRunner
	ci.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, ci, ci.run("init", "--yes", "--json"), 0)
	if len(ci.platform.find("POST", "/api/v1/repos/cr_1/tokens")) != 0 {
		t.Fatal("without a terminal init mints no token it can neither install nor show")
	}
	if tok := ci.envelope()["data"].(map[string]any)["token"].(map[string]any); !strings.Contains(tok["note"].(string), "--replace-secret") || tok["legacyPipelines"].([]any)[0] != ".github/workflows/docs.yml" {
		t.Fatalf("token = %v", tok)
	}
}

func TestInitReplaceSecretOverridesTheGuard(t *testing.T) {
	h := newHarness(t)
	seedRepo(t, h)
	legacyWorkflow(t, h)
	initPlatform(h, connectFresh)
	bin := t.TempDir()
	fakeGhWithSecret(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.secrets = cisetup.ExecRunner
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	expectCode(t, h, h.run("init", "--yes", "--replace-secret"), 0)
	args, _ := os.ReadFile(filepath.Join(bin, "gh.args"))
	if !strings.Contains(string(args), "secret set GRAVITY_TOKEN --repo acme/billing-api") {
		t.Fatalf("gh args = %s", args)
	}
	if !strings.Contains(h.stdout.String(), "replacing its current value") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
	expectCode(t, h, h.run("init", "--yes", "--replace-secret", "--no-secret"), 2)
}
