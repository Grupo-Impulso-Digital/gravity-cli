package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type seen struct {
	method  string
	path    string
	query   url.Values
	body    map[string]any
	raw     []byte
	headers http.Header
}

func fixtureServer(t *testing.T, status int, response string) (*api.Client, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.method, s.path, s.query, s.headers = r.Method, r.URL.EscapedPath(), r.URL.Query(), r.Header
		s.raw, _ = io.ReadAll(r.Body)
		s.body = nil
		_ = json.Unmarshal(s.raw, &s.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	c, _ := newTestClient(srv.URL)
	return c, s
}

func expectRequest(t *testing.T, s *seen, method, path string) {
	t.Helper()
	if s.method != method || s.path != path {
		t.Fatalf("request = %s %s, want %s %s", s.method, s.path, method, path)
	}
}

func TestDeviceStartAndPoll(t *testing.T) {
	c, s := fixtureServer(t, 201, `{"deviceCode":"dc","userCode":"BCDF-GHJK","verificationUri":"https://app/x","verificationUriComplete":"https://app/x?code=BCDF-GHJK","expiresIn":600,"interval":5}`)
	start, err := c.StartDeviceLogin(context.Background(), api.DeviceStartRequest{ClientName: "gravity-cli", ClientVersion: "1.0.0", OS: "darwin", Org: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/auth/device/start")
	if s.body["org"] != "acme" || start.UserCode != "BCDF-GHJK" || start.Interval != 5 {
		t.Fatalf("start = %+v body=%v", start, s.body)
	}

	c, s = fixtureServer(t, 200, `{"status":"approved","token":"gr_user_x","tokenKind":"user","expiresAt":"2026-12-30T12:00:00Z","organization":{"id":"org_1","slug":"acme","name":"Acme"},"user":{"id":"usr_1","email":"dave@acme.io","name":"Dave"},"apiUrl":"https://api.gravitydocs.io"}`)
	poll, err := c.PollDeviceLogin(context.Background(), "dc")
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/auth/device/poll")
	if s.body["deviceCode"] != "dc" || poll.Token != "gr_user_x" || poll.Organization.Slug != "acme" || poll.User.Email != "dave@acme.io" {
		t.Fatalf("poll = %+v", poll)
	}
}

func TestDevicePollRefusals(t *testing.T) {
	c, _ := fixtureServer(t, 400, `{"error":{"code":"access_denied","message":"Denied"}}`)
	if _, err := c.PollDeviceLogin(context.Background(), "dc"); !errors.Is(err, api.ErrDeviceDenied) {
		t.Fatalf("denied: %v", err)
	}
	c, _ = fixtureServer(t, 410, `{"error":{"code":"expired_token","message":"Expired"}}`)
	if _, err := c.PollDeviceLogin(context.Background(), "dc"); !errors.Is(err, api.ErrDeviceExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestLogoutAndMint(t *testing.T) {
	c, s := fixtureServer(t, 204, ``)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/auth/logout")

	c, s = fixtureServer(t, 201, `{"token":"gr_repo_x","key":{"id":"key_1","keyHint":"x9Qa","kind":"repo","repoId":"cr_1","scopes":["repo:connect"],"expiresAt":null,"createdAt":"2026-10-01T00:00:00Z"}}`)
	minted, err := c.MintRepoToken(context.Background(), "cr_1", api.MintTokenRequest{Name: "GitHub Actions · billing-api", Scopes: []string{"repo:connect"}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/repos/cr_1/tokens")
	if _, ok := s.body["expiresInDays"]; !ok || s.body["expiresInDays"] != nil {
		t.Fatalf("expiresInDays must be sent as null: %v", s.body)
	}
	if minted.Token != "gr_repo_x" || minted.Key.Kind != "repo" || minted.Key.ExpiresAt != nil {
		t.Fatalf("minted = %+v", minted)
	}
}

func TestWhoAmIV2(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"organizationId":"org_1","organizationName":"Acme","defaultSiteSlug":null,"keyHint":"x9Qa","features":{"pipelines":true},"apiUrl":"https://api.gravitydocs.io","principal":{"kind":"user","user":{"id":"usr_1","email":"dave@acme.io","name":"Dave"},"role":"editor","permissions":["docs.read","docs.repos.manage"]},"organization":{"id":"org_1","slug":"acme","name":"Acme"},"organizations":[{"id":"org_1","slug":"acme","name":"Acme","role":"editor"},{"id":"org_2","slug":"acme-labs","name":"Acme Labs","role":"admin"}],"token":{"kind":"user","scopes":[],"expiresAt":"2026-12-30T12:00:00Z"},"modules":{"cli":true,"agent":false}}`)
	who, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/whoami")
	if who.Principal.Kind != "user" || !who.HasPermission("docs.repos.manage") || who.HasPermission("docs.repos.tokens") || len(who.Organizations) != 2 || who.Token.Kind != "user" || !who.Features["pipelines"] || who.Modules["agent"] {
		t.Fatalf("who = %+v", who)
	}

	c, _ = fixtureServer(t, 200, `{"organizationId":"org_1","principal":{"kind":"repo","repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","product":{"id":"prod_1","slug":"acme-platform"}}},"token":{"kind":"repo","scopes":["repo:connect","runs:write"],"expiresAt":null}}`)
	who, err = c.WhoAmI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if who.Principal.Repo.Product.Slug != "acme-platform" || len(who.Token.Scopes) != 2 {
		t.Fatalf("repo principal = %+v", who.Principal)
	}
}

func TestProducts(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"products":[{"id":"prod_1","slug":"acme-platform","name":"Acme Platform","nucleusNamespace":"product:acme-platform","repos":[{"id":"cr_1","name":"gateway","remoteKey":"github.com/acme/gateway"}],"targets":[{"siteSlug":"dev-portal","spaceSlug":"api","passes":2}]}]}`)
	products, err := c.Products(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/products")
	if len(products) != 1 || products[0].Slug != "acme-platform" || products[0].Repos[0].Label() != "gateway" || products[0].Targets[0].Passes != 2 {
		t.Fatalf("products = %+v", products)
	}
}

const connectResponse = `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","created":true,"createdVia":"cli","product":{"id":"prod_1","slug":"acme-platform","name":"Acme Platform"},"appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"manifest":{"version":2,"hash":"sha256:4f1c","accepted":true,"persisted":true,"reason":null,"authoritativeBranch":"main","passesUpserted":["developer-api"],"passesConverted":[],"passesArchived":[],"warnings":[{"code":"target_unapproved","path":"passes[0].target","message":"dev-portal/api needs approval"}]},
"effective":{"appPasses":"allow","overlay":false,"passes":[{"name":"developer-api","kind":"reference","source":"manifest","locked":true,"enabled":true,"triggers":["push","pr"],"target":{"ref":"dev-portal/api","status":"unapproved","siteSlug":"dev-portal","spaceSlug":"api","collectionPath":[],"approval":null,"approveUrl":"https://app.gravitydocs.io/app/repos/cr_1/passes/rp_1#approve"}},{"name":"product-guides","kind":"guides","source":"app","locked":false,"enabled":true,"triggers":["push"],"target":{"ref":"product/guides","status":"ok","siteSlug":"product","spaceSlug":"guides","collectionPath":[],"approval":{"by":"dave@acme.io","at":"2026-09-30T09:00:00Z"}}}]},
"createdTargets":[{"site":"dev-portal","space":"api","id":"sp_1"}],"siblings":[{"name":"gateway","remoteKey":"github.com/acme/gateway","lastRunAt":"2026-09-30T10:00:00Z","passes":3,"health":"live"}],"serverFeatures":{"pipelines":true}}`

func TestConnect(t *testing.T) {
	c, s := fixtureServer(t, 201, connectResponse)
	resp, err := c.Connect(context.Background(), api.ConnectRequest{
		CLI:     api.CLIInfo{Version: "1.0.0", OS: "linux", Arch: "amd64"},
		Repo:    api.ConnectRepo{Remote: "git@github.com:acme/billing-api.git", Name: "billing-api", DefaultBranch: "main", Branch: "main", Commit: "a1b2"},
		Context: api.ConnectContext{Trigger: "push", Origin: "ci"},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/repos/connect")
	if v, ok := s.body["manifest"]; !ok || v != nil {
		t.Fatalf("manifest must be null when absent: %v", s.body)
	}
	if s.body["dryRun"] != false || s.body["context"].(map[string]any)["trigger"] != "push" {
		t.Fatalf("body = %v", s.body)
	}
	if !resp.Repo.Created || resp.Manifest.Reason != nil || resp.Effective.Passes[0].Target.Status != api.TargetUnapproved ||
		resp.Effective.Passes[1].Target.Approval.By != "dave@acme.io" || resp.Siblings[0].Health != "live" || !resp.ServerFeatures["pipelines"] {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUpsertPass(t *testing.T) {
	c, s := fixtureServer(t, 201, `{"name":"docs","kind":"guides","source":"app","locked":false,"enabled":true,"target":{"ref":"product/guides","status":"ok"}}`)
	p, err := c.UpsertPass(context.Background(), "cr_1", "docs", api.PassSpec{Kind: "guides", Target: "product/guides"})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "PUT", "/api/v1/repos/cr_1/passes/docs")
	if s.body["kind"] != "guides" || p.Source != "app" {
		t.Fatalf("p=%+v body=%v", p, s.body)
	}
}

const planResponse = `{"planHash":"sha256:9a0e","overlay":false,
"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","defaultBranch":"main","authoritativeBranch":"main","webUrl":"https://github.com/acme/billing-api","manifestHash":"sha256:4f1c","appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"product":{"id":"prod_1","slug":"acme-platform","name":"Acme Platform","nucleusNamespace":"product:acme-platform"},
"trigger":"push","branch":"main",
"passes":[{"id":"rp_1","name":"developer-api","title":"Developer API","kind":"reference","template":"api-reference","source":"manifest","locked":true,"enabled":true,"applies":true,"skipReason":null,"triggers":["push","pr"],"branches":[],
"target":{"status":"ok","ref":"dev-portal/api","site":{"id":"site_1","slug":"dev-portal","name":"Developer Portal"},"space":{"id":"sp_1","slug":"api","name":"API","type":"api-reference"},"collection":null,"viewerUrl":"https://docs.acme.io/api"},
"scope":{"paths":["api/**","src/server/**"],"exclude":[],"units":["api","service"]},"audiences":["developers"],"publishMode":"propose","trusted":false,"options":{"pageStrategy":"per-tag","prose":true},
"instructions":{"hash":"sha256:51be","layers":[{"source":"org","label":"Organization voice","text":"Friendly."},{"source":"pass","id":"rp_1","label":"Pass developer-api","text":"Keep endpoint reference."}]},
"prompt":{"name":"pass-reference","version":"sha256:77c1"},
"watermark":{"branch":"main","commitSha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","runId":"prun_1","updatedAt":"2026-09-30T10:00:00Z"},
"hints":[{"id":"hint_1","kind":"contradiction","claim":"Refund webhooks retry 5 times","unitKey":"api:post:/v1/refunds","page":{"id":"pg_1","slug":"webhooks"},"blockKey":"guide:webhooks:retries","raisedBy":{"repo":"github.com/acme/gateway","runId":"prun_2"}}]},
{"id":"rp_2","name":"changelog","kind":"changelog","applies":false,"skipReason":"trigger_mismatch"}],
"inventory":{"units":[{"key":"api:post:/v1/refunds","kind":"api","title":"Create a refund","contributors":[{"repo":"github.com/acme/billing-api","role":"implements","sourceRefs":["api/openapi.yaml#/paths/~1v1~1refunds/post","src/server/refunds/**"]},{"repo":"github.com/acme/gateway","role":"declares","sourceRefs":["routes/billing.ts"]}]}],"truncated":false},
"capabilities":{"features":{"pipelines":true},"modules":{"cli":true,"memory":true,"agent":false},"llm":{"configured":true,"provider":"anthropic","model":"claude-sonnet-4-5"},"limits":{"maxChangesPerRun":500,"maxBlocksPerChange":500,"maxAssetBytes":10485760,"leaseTtlSeconds":600,"heartbeatSeconds":60,"maxCostUsdPerRun":5.0}},
"siblings":[{"name":"gateway","remoteKey":"github.com/acme/gateway","health":"live"}],"warnings":[]}`

func TestPlan(t *testing.T) {
	c, s := fixtureServer(t, 200, planResponse)
	plan, err := c.Plan(context.Background(), api.PlanQuery{Repo: "github.com/acme/billing-api", Trigger: "push", Branch: "main", Passes: []string{"a", "b"}, Mode: api.ModeDry, ManifestHash: "sha256:x"})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/repos/self/plan")
	want := url.Values{"repo": {"github.com/acme/billing-api"}, "trigger": {"push"}, "branch": {"main"}, "pass": {"a", "b"}, "mode": {"dry"}, "manifestHash": {"sha256:x"}}
	if !reflect.DeepEqual(s.query, want) {
		t.Fatalf("query = %v", s.query)
	}
	p, ok := plan.PassByName("developer-api")
	if !ok || !p.Applies || p.Target.Space.Type != "api-reference" || p.Watermark.CommitSHA[:4] != "9f8e" || len(p.Instructions.Layers) != 2 || p.Hints[0].RaisedBy.Repo != "github.com/acme/gateway" {
		t.Fatalf("pass = %+v", p)
	}
	cl, _ := plan.PassByName("changelog")
	if cl.Applies || cl.SkipReason != api.SkipTriggerMismatch {
		t.Fatalf("changelog = %+v", cl)
	}
	u := plan.Inventory.Units[0]
	if u.Contributors[0].Repo.RemoteKey != "github.com/acme/billing-api" || u.Contributors[1].Role != "declares" {
		t.Fatalf("unit = %+v", u)
	}
	if plan.Capabilities.Limits.HeartbeatSeconds != 60 || plan.Capabilities.LLM.Provider != "anthropic" {
		t.Fatalf("caps = %+v", plan.Capabilities)
	}
}

func TestStatus(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","health":"live","cliVersion":"1.0.0","appUrl":"https://app/x"},"product":{"slug":"acme-platform","name":"Acme Platform"},
"passes":[{"name":"developer-api","kind":"reference","source":"manifest","enabled":true,"target":{"ref":"dev-portal/api","status":"ok"},"lastRun":{"runId":"prun_1","status":"succeeded","at":"t"},"watermarks":[{"branch":"main","commitSha":"9f8e","updatedAt":"t"}]}],
"runs":[{"id":"prun_1","trigger":"push","mode":"write","status":"succeeded","branch":"main","headSha":"a1b2","changes":4,"findings":0,"costUsd":0.42}],
"openBundles":[{"runId":"prun_1","pending":4,"appUrl":"https://app/r"}],"tokens":[{"keyHint":"x9Qa","kind":"repo","lastUsedAt":"t","expiresAt":null}],"health":{"status":"live","reasons":[]}}`)
	st, err := c.Status(context.Background(), "github.com/acme/billing-api", 5)
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/repos/self/status")
	if s.query.Get("runs") != "5" || s.query.Get("repo") != "github.com/acme/billing-api" {
		t.Fatalf("query = %v", s.query)
	}
	if st.Health.Status != api.HealthLive || st.Passes[0].Watermarks[0].CommitSHA != "9f8e" || st.Runs[0].CostUSD != 0.42 || st.OpenBundles[0].Pending != 4 {
		t.Fatalf("status = %+v", st)
	}
}

func TestRunsLifecycle(t *testing.T) {
	c, s := fixtureServer(t, 201, `{"run":{"id":"prun_1","status":"running","mode":"write","trigger":"push","leaseKey":"branch:main","authoritative":true,"expiresAt":"t","heartbeatSeconds":60,"appUrl":"https://app/r"},"passes":[{"runPassId":"ppr_1","name":"developer-api","kind":"reference","status":"pending","range":{"kind":"watermark","baseSha":"9f8e","headSha":"a1b2"},"instructionsHash":"sha256:51be"},{"runPassId":"ppr_2","name":"changelog","kind":"changelog","status":"skipped","skipReason":"trigger_mismatch"}]}`)
	seenWM := "9f8e"
	started, err := c.StartRun(context.Background(), "github.com/acme/billing-api", api.StartRunRequest{
		ClientKey: "c6c0b1f0-3f4d-4f5c-9d6e-0b1c2d3e4f50", Trigger: "push", Mode: "write", Origin: "ci", Branch: "main", HeadSHA: "a1b2", PlanHash: "sha256:9a0e",
		Passes: []api.RunPassStart{{Name: "developer-api", RangeKind: "watermark", BaseSHA: "9f8e", WatermarkSeen: &seenWM}, {Name: "changelog", Skip: "trigger_mismatch"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs")
	if s.query.Get("repo") != "github.com/acme/billing-api" {
		t.Fatalf("query = %v", s.query)
	}
	for _, k := range []string{"pr", "release", "manifestHash"} {
		if v, ok := s.body[k]; !ok || v != nil {
			t.Fatalf("%s must be null: %v", k, s.body)
		}
	}
	if started.Run.LeaseKey != "branch:main" || started.Passes[0].Range.BaseSHA != "9f8e" || started.Passes[1].SkipReason != "trigger_mismatch" {
		t.Fatalf("started = %+v", started)
	}

	c, s = fixtureServer(t, 200, `{"expiresAt":"2026-10-01T12:20:00Z"}`)
	exp, err := c.Heartbeat(context.Background(), "prun_1")
	if err != nil || exp != "2026-10-01T12:20:00Z" {
		t.Fatalf("heartbeat %q %v", exp, err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/heartbeat")

	c, s = fixtureServer(t, 200, `{"run":{"id":"prun_1","status":"running","mode":"write","trigger":"push","origin":"ci","branch":"main","authoritative":true,"leaseKey":"branch:main","headSha":"a1b2","baseSha":"9f8e","rangeKind":"watermark","pr":null,"release":null,"note":null,"expiresAt":"t","startedAt":"t","finishedAt":null,"changesCount":4,"findingsCount":0,"llmCalls":7,"inputTokens":48211,"outputTokens":6120,"costUsd":0.31,"error":null,"appUrl":"u"},
"passes":[{"runPassId":"ppr_1","name":"developer-api","kind":"reference","source":"manifest","overlay":false,"status":"succeeded","skipReason":null,"range":{"kind":"watermark","baseSha":"9f8e","headSha":"a1b2"},"watermarkSeen":"9f8e","watermarkAdvanced":false,"watermarkNote":null,"instructionsHash":"h","changesCount":4,"findingsCount":0,"llmCalls":0,"inputTokens":0,"outputTokens":0,"costUsd":0,"error":null,"startedAt":"t","finishedAt":"t"}],
"bundle":{"changes":4,"applied":3,"held":1,"competing":1,"accepted":0,"declined":0,"proposals":["prop_1"],"appUrl":"u"},"captureRuns":[{"runPassId":"ppr_3","docAgentRunId":"dar_1","status":"running"}]}`)
	run, err := c.GetRun(context.Background(), "prun_1")
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/runs/prun_1")
	if run.Run.LLMCalls != 7 || run.Passes[0].WatermarkSeen == nil || run.Bundle.Held != 1 || run.CaptureRuns[0].DocAgentRunID != "dar_1" {
		t.Fatalf("run = %+v", run)
	}

	c, s = fixtureServer(t, 200, `{"passRun":{"runPassId":"ppr_1","name":"developer-api","status":"succeeded","skipReason":null,"changesCount":2,"findingsCount":1,"llmCalls":3,"costUsd":0.12,"startedAt":"t","finishedAt":"t"}}`)
	pr, err := c.ReportPass(context.Background(), "prun_1", "ppr_1", api.PassReportRequest{
		Status: "succeeded",
		Report: &api.PassReport{Summary: "3 pages updated", Findings: []api.Finding{{Severity: "error", Code: "claim_contradicted", Title: "x", Verdict: api.VerdictContradicted, Line: 42, Evidence: []api.Evidence{{Kind: "commit", Ref: "a1b2c3d"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/passes/ppr_1")
	if v, ok := s.body["skipReason"]; !ok || v != nil {
		t.Fatalf("skipReason must be null: %v", s.body)
	}
	if pr.ChangesCount != 2 || pr.CostUSD != 0.12 {
		t.Fatalf("passRun = %+v", pr)
	}

	c, s = fixtureServer(t, 200, `{"run":{"id":"prun_1","status":"succeeded","finishedAt":"t","costUsd":0.42,"appUrl":"u"},"watermarks":[{"pass":"developer-api","branch":"main","commitSha":"a1b2","advanced":true,"reason":null},{"pass":"product-guides","branch":"main","commitSha":"a1b2","advanced":false,"reason":"watermark_moved"}],"bundle":{"changes":4,"applied":3,"held":1,"autoAccepted":0,"appUrl":"u"}}`)
	fin, err := c.FinishRun(context.Background(), "prun_1", api.FinishRunRequest{Status: "succeeded", Report: &api.FinishReport{Summary: "2 passes ran"}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/finish")
	if !fin.Watermarks[0].Advanced || *fin.Watermarks[1].Reason != "watermark_moved" || fin.Bundle.Applied != 3 {
		t.Fatalf("finish = %+v", fin)
	}
}

const pageResponse = `{"page":{"id":"pg_1","slug":"refunds","title":"Refunds","position":3,"site":{"id":"site_1","slug":"dev-portal"},"space":{"id":"sp_1","slug":"api"},"collectionPath":["payments"],"status":"changed","publishedVersion":7,"lock":null,"openProposal":{"id":"prop_1","status":"in_review","pipelineRunId":"prun_1","createdBy":"Gravity · gateway"},"units":["api:post:/v1/refunds"],"updatedAt":"t"},
"blocks":[{"key":"api:POST:/v1/refunds","type":"api","ownership":"machine","audiences":["developers"],"position":0,"content":{"method":"POST","path":"/v1/refunds","summary":"Create a refund"},"sourceBinding":{"kind":"endpoint","ref":"POST /v1/refunds","hash":"h","generator":"gravity-cli"},"units":["api:post:/v1/refunds"],"provenance":{"repo":"billing-api","pass":"developer-api","commitSha":"a1b2c3d","runId":"prun_1","at":"t","state":"published","previous":{"repo":"gateway","commitSha":"9f8e7d","at":"t"}},"text":"POST /v1/refunds — Create a refund"}]}`

func TestContentReads(t *testing.T) {
	c, s := fixtureServer(t, 200, pageResponse)
	page, err := c.Page(context.Background(), "pg_1", api.PageQuery{State: "published", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/content/pages/pg_1")
	if s.query.Get("state") != "published" || s.query.Get("format") != "json" {
		t.Fatalf("query = %v", s.query)
	}
	if page.Page.OpenProposal.PipelineRunID != "prun_1" || page.Blocks[0].Provenance.Previous.Repo != "gateway" || page.Blocks[0].SourceBinding.Kind != "endpoint" {
		t.Fatalf("page = %+v", page)
	}

	c, s = fixtureServer(t, 200, pageResponse)
	if _, err := c.PageBySlug(context.Background(), "sp_1", "refunds", api.PageQuery{}); err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/content/pages")
	if s.query.Get("spaceId") != "sp_1" || s.query.Get("slug") != "refunds" {
		t.Fatalf("query = %v", s.query)
	}

	c, s = fixtureServer(t, 200, `{"pageId":"pg_1","siteSlug":"dev-portal","spaceSlug":"api","pageSlug":"refunds"}`)
	res, err := c.ResolvePage(context.Background(), "https://docs.acme.io/api/refunds")
	if err != nil || res.PageID != "pg_1" {
		t.Fatalf("resolve %+v %v", res, err)
	}
	expectRequest(t, s, "GET", "/api/v1/content/resolve")
	if s.query.Get("ref") != "https://docs.acme.io/api/refunds" {
		t.Fatalf("query = %v", s.query)
	}

	c, s = fixtureServer(t, 200, `{"hits":[{"pageId":"pg_1","pageSlug":"webhooks","title":"Webhooks","siteSlug":"dev-portal","spaceSlug":"api","blockKey":"guide:webhooks:retries","anchor":"retries","snippet":"…","score":0.82,"source":"published"}]}`)
	hits, err := c.Search(context.Background(), api.SearchRequest{Query: "refund webhook retries", SpaceIDs: []string{"sp_1"}, Limit: 10, IncludeDrafts: true})
	if err != nil || len(hits) != 1 || hits[0].Score != 0.82 {
		t.Fatalf("hits %+v %v", hits, err)
	}
	expectRequest(t, s, "POST", "/api/v1/content/search")
	if ids, ok := s.body["siteIds"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("siteIds must be []: %v", s.body)
	}

	c, s = fixtureServer(t, 200, `{"page":{"id":"pg_1","slug":"refunds","title":"Refunds"},"blocks":[{"key":"api:POST:/v1/refunds","history":[{"action":"update","state":"published","repo":"billing-api","pass":"developer-api","runId":"prun_1","commitSha":"a1b2","sourceRefs":["api/openapi.yaml"],"units":["api:post:/v1/refunds"],"at":"t"}]}]}`)
	prov, err := c.Provenance(context.Background(), "pg_1")
	if err != nil || prov.Blocks[0].History[0].Pass != "developer-api" {
		t.Fatalf("prov %+v %v", prov, err)
	}
	expectRequest(t, s, "GET", "/api/v1/content/pages/pg_1/provenance")
}

func TestSpaceTreeFollowsCursor(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/content/spaces/sp_1/tree" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"space":{"id":"sp_1","slug":"handbook","name":"Handbook","type":"handbook"},"collections":[{"id":"col_1","slug":"oncall","path":["oncall"],"name":"On-call","parentId":null}],"pages":[{"id":"pg_1","slug":"a","title":"A","collectionPath":["oncall"],"position":0,"status":"published","units":[],"lock":{"pass":"handbook","path":"docs/a.md","hash":"h"},"lastWriter":{"repo":"billing-api","at":"t"},"openProposal":null}],"nextCursor":"c2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"space":{"id":"sp_1","slug":"handbook"},"collections":[],"pages":[{"id":"pg_2","slug":"b","title":"B","collectionPath":[],"position":1,"status":"draft","units":[],"lock":null,"lastWriter":null,"openProposal":null}],"nextCursor":null}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	tree, err := c.SpaceTree(context.Background(), "sp_1")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(tree.Pages) != 2 || len(tree.Collections) != 1 || tree.Pages[0].Lock.Path != "docs/a.md" || tree.NextCursor != nil {
		t.Fatalf("tree = %+v calls=%d", tree, calls)
	}
}

func TestBundleWrites(t *testing.T) {
	c, s := fixtureServer(t, 201, `{"change":{"id":"chg_1","op":"update","status":"applied","reach":"target","page":{"id":"pg_1","slug":"refunds","title":"Refunds"},"proposalId":"prop_1","competing":true,"competingWith":[{"changeId":"chg_0","runId":"prun_0","repo":"gateway","blockKeys":["api:POST:/v1/refunds"]}],"heldReason":null,"warnings":[],"reviewUrl":"u"}}`)
	ch, err := c.ProposeChange(context.Background(), "prun_1", api.ChangeRequest{
		RunPassID: "ppr_1", Op: api.OpUpdate, Target: api.ChangeTarget{PageID: "pg_1"}, Summary: "x",
		Blocks: []api.ChangeBlock{{Key: "api:POST:/v1/refunds", Type: "api", Ownership: "machine", Content: map[string]any{"method": "POST"}, Rationale: &api.Rationale{Summary: "s", Commits: []string{"a1b2"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/changes")
	blocks := s.body["blocks"].([]any)
	if _, has := blocks[0].(map[string]any)["after"]; has {
		t.Fatalf("after must be omitted when unset: %v", blocks[0])
	}
	if !ch.Competing || ch.CompetingWith[0].Repo != "gateway" || ch.HeldReason != nil {
		t.Fatalf("change = %+v", ch)
	}

	c, s = fixtureServer(t, 201, `{"change":{"id":"chg_2","op":"import","status":"applied","page":{"id":"pg_2","slug":"rotation"},"proposalId":"prop_2","adopted":false,"lock":{"kind":"repo","pass":"handbook","path":"docs/handbook/oncall/rotation.md","hash":"h"},"pendingLock":null,"competing":false,"heldReason":null}}`)
	vr, err := c.ImportVerbatim(context.Background(), "prun_1", api.VerbatimRequest{RunPassID: "ppr_2", File: api.VerbatimFile{Path: "docs/handbook/oncall/rotation.md", Hash: "h", Branch: "main", CommitSHA: "a1b2"}, Page: api.VerbatimPage{Slug: "rotation", Title: "On-call rotation", CollectionPath: []string{"oncall"}}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/verbatim")
	if langs, ok := s.body["languages"].([]any); !ok || len(langs) != 0 {
		t.Fatalf("languages must be []: %v", s.body)
	}
	if vr.Change.Lock.Pass != "handbook" || vr.Unchanged() {
		t.Fatalf("verbatim = %+v", vr)
	}

	c, s = fixtureServer(t, 200, `{"change":null,"status":"unchanged","page":{"id":"pg_2","slug":"rotation"}}`)
	vr, err = c.DeleteVerbatim(context.Background(), "prun_1", api.VerbatimDeleteRequest{RunPassID: "ppr_2", Path: "docs/handbook/old.md", Reason: "File deleted in a1b2c3d"})
	if err != nil || !vr.Unchanged() {
		t.Fatalf("delete %+v %v", vr, err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/verbatim/delete")

	c, s = fixtureServer(t, 201, `{"key":"k","url":"https://cdn/media/k","deduplicated":false}`)
	asset, err := c.UploadAsset(context.Background(), "prun_1", "ppr_3", "docs/img/flow.png", "image/png", "abc123", []byte{0x89, 'P', 'N', 'G'})
	if err != nil || asset.URL == "" {
		t.Fatalf("asset %+v %v", asset, err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/assets")
	if s.headers.Get("Content-Type") != "image/png" || s.headers.Get("X-Gravity-Asset-Path") != "docs/img/flow.png" || s.headers.Get("X-Gravity-Asset-Sha256") != "abc123" || s.headers.Get("X-Gravity-Run-Pass-Id") != "ppr_3" || string(s.raw) != "\x89PNG" {
		t.Fatalf("asset headers %v raw %q", s.headers, s.raw)
	}

	c, s = fixtureServer(t, 201, `{"created":["hint_1"],"deduplicated":1}`)
	hr, err := c.RaiseHints(context.Background(), "prun_1", "ppr_1", []api.HintInput{{Kind: "contradiction", UnitKey: "api:post:/v1/refunds", Claim: "Refund webhooks retry 5 times"}})
	if err != nil || hr.Deduplicated != 1 {
		t.Fatalf("hints %+v %v", hr, err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/hints")
	hint := s.body["hints"].([]any)[0].(map[string]any)
	if s.body["runPassId"] != "ppr_1" || hint["forRepos"] == nil {
		t.Fatalf("hints body %v", s.body)
	}
}

func TestVerbatimTranslationWire(t *testing.T) {
	c, s := fixtureServer(t, 201, `{"change":{"id":"chg_9","op":"import","status":"accepted","page":{"id":"pg_2","slug":"rotation","title":"On-call rotation"},"proposalId":null,"competing":false,"heldReason":null,"language":"fr","lock":{"kind":"repo","path":"docs/handbook/rotation.fr.md","branch":"main","hash":"sha256:f"},"translation":{"language":"fr","title":"Astreinte","slug":"astreinte","status":"live"}}}`)
	vr, err := c.ImportVerbatim(context.Background(), "prun_1", api.VerbatimRequest{
		RunPassID: "ppr_2", Language: "fr",
		File:   api.VerbatimFile{Path: "docs/handbook/rotation.fr.md", Hash: "sha256:f", Branch: "main", CommitSHA: "a1b2"},
		Page:   api.VerbatimPage{Slug: "rotation", Title: "Astreinte", CollectionPath: []string{"oncall"}},
		Blocks: []api.ChangeBlock{{Key: "verbatim:1", Type: "prose", Ownership: api.OwnershipMachine, Content: map[string]any{"text": "Qui est d'astreinte"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/verbatim")
	page := s.body["page"].(map[string]any)
	file := s.body["file"].(map[string]any)
	if s.body["language"] != "fr" || page["slug"] != "rotation" || page["title"] != "Astreinte" || file["path"] != "docs/handbook/rotation.fr.md" || s.body["runPassId"] != "ppr_2" {
		t.Fatalf("translation body = %v", s.body)
	}
	if langs, ok := s.body["languages"].([]any); !ok || len(langs) != 0 {
		t.Fatalf("a translation never asks for auto-translation: %v", s.body["languages"])
	}
	if vr.Change.Language != "fr" || vr.Change.Translation == nil || vr.Change.Translation.Slug != "astreinte" || vr.Change.Lock.Path != "docs/handbook/rotation.fr.md" {
		t.Fatalf("translation result = %+v", vr.Change)
	}

	c, s = fixtureServer(t, 200, `{"change":null,"status":"unchanged","page":{"id":"pg_2","slug":"rotation"}}`)
	if _, err := c.ImportVerbatim(context.Background(), "prun_1", api.VerbatimRequest{RunPassID: "ppr_2", Page: api.VerbatimPage{Slug: "rotation"}}); err != nil {
		t.Fatal(err)
	}
	if _, present := s.body["language"]; present {
		t.Fatalf("a source import carries no language: %v", s.body)
	}

	c, _ = fixtureServer(t, 422, `{"error":{"code":"language_not_enabled","message":"The site has no de translation"}}`)
	_, err = c.ImportVerbatim(context.Background(), "prun_1", api.VerbatimRequest{RunPassID: "ppr_2", Language: "de", Page: api.VerbatimPage{Slug: "rotation"}})
	if !api.HasCode(err, api.CodeLanguageNotEnabled) || api.IsLicenseError(err) {
		t.Fatalf("language_not_enabled = %v", err)
	}

	c, _ = fixtureServer(t, 201, `{"change":{"id":"chg_d","op":"delete","status":"accepted","language":"fr","page":{"id":"pg_2","slug":"rotation"},"proposalId":null,"competing":false,"heldReason":null}}`)
	dr, err := c.DeleteVerbatim(context.Background(), "prun_1", api.VerbatimDeleteRequest{RunPassID: "ppr_2", Path: "docs/handbook/rotation.fr.md", Reason: "Translation file deleted"})
	if err != nil || dr.Change.Language != "fr" {
		t.Fatalf("translation delete = %+v %v", dr, err)
	}
}

func TestInventory(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"product":{"id":"prod_1","slug":"acme-platform","nucleusNamespace":"product:acme-platform"},"units":[{"key":"api:post:/v1/refunds","kind":"api","title":"Create a refund","status":"active","primaryRepo":{"id":"cr_1","name":"billing-api","remoteKey":"github.com/acme/billing-api"},"contributors":[{"repo":{"id":"cr_1","name":"billing-api","remoteKey":"github.com/acme/billing-api"},"role":"implements","active":true,"sourceRefs":["src/server/refunds/**"],"lastSeenAt":"t","lastSeenSha":"a1b2"}],"bindings":[{"pageId":"pg_1","pageSlug":"refunds","siteSlug":"dev-portal","spaceSlug":"api","blockKey":null}],"handoffs":[{"id":"ho_1","role":"implements","from":"gateway","to":"billing-api","status":"detected","detectedAt":"t"}]}],"nextCursor":null}`)
	inv, err := c.Inventory(context.Background(), api.InventoryQuery{Product: "acme-platform", Kind: "api", Unit: "api:post:/v1/refunds", IncludeInactive: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/v1/products/self/inventory")
	if s.query.Get("includeInactive") != "true" || s.query.Get("limit") != "50" || s.query.Get("unit") != "api:post:/v1/refunds" {
		t.Fatalf("query = %v", s.query)
	}
	u := inv.Units[0]
	if u.PrimaryRepo.Name != "billing-api" || u.Contributors[0].Repo.Name != "billing-api" || !*u.Contributors[0].Active || u.Handoffs[0].From != "gateway" {
		t.Fatalf("unit = %+v", u)
	}

	c, s = fixtureServer(t, 200, `{"counts":{"created":1,"updated":12,"unchanged":30,"deactivated":2},"handoffs":[{"id":"ho_1","unitKey":"api:post:/v1/refunds","role":"implements","from":"github.com/acme/gateway","to":"github.com/acme/billing-api"}],"conflicts":[{"key":"refunds","reason":"kind_mismatch","existingKind":"feature"}]}`)
	res, err := c.IngestInventory(context.Background(), api.IngestRequest{RunID: "prun_1", HeadSHA: "a1b2", Complete: true, Units: []api.IngestUnit{{Key: "api:post:/v1/refunds", Kind: "api", Roles: []string{"declares", "implements"}, SourceRefs: []string{"api/openapi.yaml"}}}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/products/self/inventory")
	if _, has := s.body["entries"]; has {
		t.Fatalf("entries must be omitted: %v", s.body)
	}
	if res.Counts.Updated != 12 || res.Conflicts[0].ExistingKind != "feature" {
		t.Fatalf("ingest = %+v", res)
	}

	c, s = fixtureServer(t, 200, `{"counts":{"created":0,"updated":0,"unchanged":0,"deactivated":3},"handoffs":[],"conflicts":[]}`)
	if _, err := c.IngestInventory(context.Background(), api.IngestRequest{RunID: "prun_1", HeadSHA: "a1b2", Complete: true, Entries: []api.IngestEntry{{Key: "refunds", Roles: []string{"implements"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, has := s.body["units"]; has {
		t.Fatalf("units must be omitted in the closing call: %v", s.body)
	}
}

func TestNucleus(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"scope":{"type":"org","id":"org_1"},"hits":[{"id":"mem_1","scope":{"type":"org","id":"org_1"},"kind":"fact","status":"active","title":"Refund reasons","body":"…","score":0.9,"namespace":"product:acme-platform","repo":"github.com/acme/billing-api"}]}`)
	shared := false
	rec, err := c.Recall(context.Background(), api.RecallRequest{Query: "refunds", Namespace: "product:acme-platform", IncludeShared: &shared, Repo: "github.com/acme/billing-api"})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/nucleus/recall")
	if s.body["includeShared"] != false || s.body["namespace"] != "product:acme-platform" {
		t.Fatalf("body = %v", s.body)
	}
	if *rec.Hits[0].Namespace != "product:acme-platform" || *rec.Hits[0].Repo == "" {
		t.Fatalf("recall = %+v", rec)
	}

	c, s = fixtureServer(t, 200, `{"outcome":"queued_for_review","atom":{"id":"mem_1","title":"Refund reasons","body":"old","status":"active","sources":[{"refType":"commit","refId":"a1b2"}]},"revisionId":"rev_1"}`)
	wr, err := c.WriteMemory(context.Background(), api.MemoryWrite{Title: "Refund reasons", Body: "new", Kind: "fact", Namespace: "product:acme-platform", RunID: "prun_1", Sources: []api.MemorySource{{Type: api.SourceCommit, ID: "a1b2"}}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/nucleus/memories")
	if wr.Outcome != api.OutcomeQueuedForReview || wr.Atom.Body != "old" || wr.RevisionID != "rev_1" {
		t.Fatalf("write = %+v", wr)
	}
	if src := wr.Atom.Sources; len(src) != 1 || src[0].Type != api.SourceCommit || src[0].ID != "a1b2" {
		t.Fatalf("stored sources decode from refType/refId: %+v", src)
	}
	if got := s.body["sources"].([]any)[0].(map[string]any); got["type"] != "commit" || got["id"] != "a1b2" {
		t.Fatalf("request sources = %v", got)
	}
}

func TestMessagesRunContext(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	_, err := c.Messages(context.Background(), api.MessagesRequest{
		Messages: []api.Message{api.UserText("hi")},
		Context:  &api.MessagesContext{RunID: "prun_1", RunPassID: "ppr_1", Purpose: api.PurposeAuthor},
	})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/llm/v1/messages")
	ctx := s.body["context"].(map[string]any)
	if ctx["runPassId"] != "ppr_1" || ctx["purpose"] != "author" {
		t.Fatalf("context = %v", ctx)
	}
}

func TestSitesAndLLMConfig(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"sites":[{"id":"site_1","slug":"dev-portal","name":"Developer Portal","position":0}]}`)
	sites, err := c.Sites(context.Background())
	if err != nil || sites[0].Slug != "dev-portal" {
		t.Fatalf("sites %+v %v", sites, err)
	}
	expectRequest(t, s, "GET", "/api/v1/sites")

	c, s = fixtureServer(t, 200, `{"site":{"id":"site_1","slug":"dev-portal","name":"Developer Portal"},"spaces":[{"id":"sp_1","slug":"api","name":"API","type":"api-reference"}],"collections":[]}`)
	tree, err := c.SiteTree(context.Background(), "dev-portal")
	if err != nil || tree.Spaces[0].Type != "api-reference" {
		t.Fatalf("tree %+v %v", tree, err)
	}
	expectRequest(t, s, "GET", "/api/v1/sites/dev-portal")

	c, s = fixtureServer(t, 200, `{"provider":"anthropic","model":"m","hasKey":true}`)
	cfg, err := c.LLMConfig(context.Background())
	if err != nil || !cfg.HasKey {
		t.Fatalf("llm config %+v %v", cfg, err)
	}
	expectRequest(t, s, "GET", "/api/llm/v1/config")
}
