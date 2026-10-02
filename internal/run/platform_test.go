package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

type call struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   map[string]any
}

type platform struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	plan   api.Plan
	starts []func(w http.ResponseWriter, body map[string]any)
	routes map[string]func(w http.ResponseWriter, r *http.Request, body map[string]any)
	llm    func(body map[string]any) any
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, code string, extra map[string]any) {
	e := map[string]any{"code": code, "message": code}
	for k, v := range extra {
		e[k] = v
	}
	reply(w, status, map[string]any{"error": e})
}

func newPlatform(t *testing.T) *platform {
	t.Helper()
	p := &platform{t: t, routes: map[string]func(http.ResponseWriter, *http.Request, map[string]any){}}
	p.plan = api.Plan{
		PlanHash:     "sha256:plan",
		Repo:         api.PlanRepo{ID: "cr_1", RemoteKey: "github.com/acme/billing-api", Name: "billing-api", DefaultBranch: "main", AuthoritativeBranch: "main", WebURL: "https://github.com/acme/billing-api"},
		Product:      api.Product{ID: "prod_1", Slug: "acme", Name: "Acme", NucleusNamespace: "product:acme"},
		Capabilities: api.Capabilities{Features: map[string]bool{"pipelines": true, "product-inventory": true, "cross-repo-hints": true}, Limits: api.Limits{HeartbeatSeconds: 60}},
	}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *platform) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	p.mu.Lock()
	p.calls = append(p.calls, call{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body})
	custom := p.routes[r.Method+" "+r.URL.Path]
	p.mu.Unlock()
	if custom != nil {
		custom(w, r, body)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/api/v1/repos/connect":
		reply(w, 200, map[string]any{"repo": map[string]any{"id": "cr_1", "name": "billing-api"}, "manifest": map[string]any{"accepted": true}, "serverFeatures": map[string]bool{"pipelines": true}})
	case r.Method == http.MethodGet && path == "/api/v1/repos/self/plan":
		p.mu.Lock()
		pl := p.plan
		p.mu.Unlock()
		reply(w, 200, pl)
	case r.Method == http.MethodPost && path == "/api/v1/runs":
		p.mu.Lock()
		var next func(http.ResponseWriter, map[string]any)
		if len(p.starts) > 0 {
			next = p.starts[0]
			p.starts = p.starts[1:]
		}
		p.mu.Unlock()
		if next != nil {
			next(w, body)
			return
		}
		reply(w, 201, startedFor(body))
	case strings.HasSuffix(path, "/heartbeat"):
		reply(w, 200, map[string]any{"expiresAt": "2026-10-01T12:10:00Z"})
	case strings.Contains(path, "/passes/"):
		reply(w, 200, map[string]any{"passRun": map[string]any{"status": body["status"], "costUsd": 0.12, "llmCalls": 2}})
	case strings.HasSuffix(path, "/finish"):
		reply(w, 200, map[string]any{"run": map[string]any{"id": "prun_1", "status": body["status"], "appUrl": "https://app.test/runs/prun_1"}, "watermarks": []any{}, "bundle": map[string]any{"changes": 1, "appUrl": "https://app.test/runs/prun_1"}})
	case strings.HasSuffix(path, "/changes"):
		reply(w, 201, map[string]any{"change": map[string]any{"id": "chg_1", "op": body["op"], "status": "applied"}})
	case path == "/api/v1/products/self/inventory":
		reply(w, 200, map[string]any{"counts": map[string]int{"created": 1}, "handoffs": []any{}, "conflicts": []any{}})
	case strings.HasSuffix(path, "/tree"):
		reply(w, 200, map[string]any{"space": map[string]any{"id": "sp_1", "slug": "api"}, "collections": []any{}, "pages": []any{}, "nextCursor": nil})
	case path == "/api/v1/content/pages":
		apiErr(w, 404, "not_found", nil)
	case path == "/api/v1/content/search":
		reply(w, 200, map[string]any{"hits": []any{}})
	case path == "/api/llm/v1/messages":
		if p.llm == nil {
			apiErr(w, 402, "no_provider_key", nil)
			return
		}
		reply(w, 200, p.llm(body))
	case strings.HasPrefix(path, "/api/llm/v1/prompts/"):
		apiErr(w, 404, "not_found", nil)
	default:
		apiErr(w, 404, "not_found", map[string]any{"message": "no route " + path})
	}
}

func startedFor(body map[string]any) map[string]any {
	var list []map[string]any
	for i, raw := range body["passes"].([]any) {
		e := raw.(map[string]any)
		rp := map[string]any{"runPassId": fmt.Sprintf("ppr_%d", i+1), "name": e["name"], "status": "pending"}
		if s, ok := e["skip"].(string); ok && s != "" {
			rp["status"], rp["skipReason"] = "skipped", s
		}
		list = append(list, rp)
	}
	return map[string]any{"run": map[string]any{"id": "prun_1", "status": "running", "mode": body["mode"], "authoritative": body["branch"] == "main" && body["mode"] == "write", "heartbeatSeconds": 60, "appUrl": "https://app.test/runs/prun_1"}, "passes": list}
}

func (p *platform) find(method, suffix string) []call {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []call
	for _, c := range p.calls {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			out = append(out, c)
		}
	}
	return out
}

type repoT struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repoT {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &repoT{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *repoT) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=ana@acme.io", "GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=ana@acme.io", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repoT) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for name, body := range files {
		p := filepath.Join(r.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

type logger struct {
	mu    sync.Mutex
	lines []string
}

func (l *logger) Infof(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logger) Warn(code, msg string) { l.Infof("warn %s: %s", code, msg) }

func (l *logger) Debugf(string, ...any) {}

type env struct {
	*run.Env
	log   *logger
	slept []time.Duration
}

func newEnv(t *testing.T, p *platform, r *repoT, m *config.Manifest) *env {
	t.Helper()
	repo, err := git.Open(context.Background(), r.dir)
	if err != nil {
		t.Fatal(err)
	}
	client := api.New(p.srv.URL, "gr_repo_test")
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	e := &env{log: &logger{}}
	e.Env = &run.Env{
		Client: client, Repo: repo, Manifest: m, Log: e.log, Generator: "gravity-cli/test",
		Info:    passes.RepoInfo{RemoteKey: "github.com/acme/billing-api", Name: "billing-api", WebURL: "https://github.com/acme/billing-api", Provider: "github", Branch: "main"},
		Connect: api.ConnectRequest{Repo: api.ConnectRepo{Remote: "git@github.com:acme/billing-api.git", Name: "billing-api"}},
		NewKey:  func() string { return "clientkey-0001" },
		Sleep: func(ctx context.Context, d time.Duration) error {
			if d == 60*time.Second {
				<-ctx.Done()
				return ctx.Err()
			}
			e.slept = append(e.slept, d)
			return nil
		},
	}
	return e
}

func refPass(name, wm string) api.PlanPass {
	pp := api.PlanPass{
		ID: "rp_" + name, Name: name, Kind: config.KindReference, Source: "manifest", Enabled: true, Applies: true, Triggers: []string{"push", "pr", "manual"},
		Target: api.PassTarget{Status: api.TargetOK, Ref: "dev/api", Site: &api.NamedRef{ID: "site_1", Slug: "dev", Name: "Dev"}, Space: &api.NamedRef{ID: "sp_1", Slug: "api", Name: "API"}},
		Scope:  api.PassScope{Paths: []string{"api/**"}},
	}
	if wm != "" {
		pp.Watermark = &api.Watermark{Branch: "main", CommitSHA: wm}
	}
	return pp
}

const spec = `openapi: 3.0.0
info: {title: Billing, version: 1.0.0}
paths:
  /v1/refunds:
    post:
      tags: [Refunds]
      summary: Create a refund
      responses: {'201': {description: created}}
`

func manifest(t *testing.T, extra string) *config.Manifest {
	t.Helper()
	m, err := config.Parse([]byte("version: 2\ncode:\n  openapi: [api/openapi.yaml]\n" + extra))
	if err != nil {
		t.Fatal(err)
	}
	return m
}
