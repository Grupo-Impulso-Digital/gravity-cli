package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
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
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type request struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   map[string]any
	Token  string
}

type platform struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	requests []request
	routes   map[string]func(w http.ResponseWriter, r *http.Request, body map[string]any)
}

func newPlatform(t *testing.T) *platform {
	t.Helper()
	p := &platform{t: t, routes: map[string]func(http.ResponseWriter, *http.Request, map[string]any){}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		p.mu.Lock()
		p.requests = append(p.requests, request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body, Token: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")})
		h, ok := p.routes[r.Method+" "+r.URL.Path]
		if !ok {
			for key, route := range p.routes {
				if prefix, wild := strings.CutSuffix(key, "*"); wild && strings.HasPrefix(r.Method+" "+r.URL.Path, prefix) {
					h, ok = route, true
				}
			}
		}
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"no route `+r.URL.Path+`"}}`)
			return
		}
		h(w, r, body)
	}))
	t.Cleanup(p.srv.Close)
	p.json("GET /api/v1/whoami", 200, whoamiUser)
	p.json("POST /api/v1/repos/connect", 200, connectBody)
	p.json("GET /api/v1/repos/self/status", 200, statusBody)
	p.json("GET /api/v1/repos/self/plan", 200, planBody)
	return p
}

func (p *platform) json(route string, status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[route] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func (p *platform) handle(route string, h func(w http.ResponseWriter, r *http.Request, body map[string]any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[route] = h
}

func (p *platform) find(method, path string) []request {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []request
	for _, r := range p.requests {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

type harness struct {
	t        *testing.T
	platform *platform
	dir      string
	config   string
	env      map[string]string
	stdin    string
	terminal bool
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	opened   []string
	bases    []string
	prompts  *countingPrompter
	prompter ui.Prompter
	secrets  cisetup.Runner
}

type countingPrompter struct {
	inner  ui.Prompter
	titles []string
}

func (c *countingPrompter) Select(title, description string, choices []ui.Choice, def string) (string, error) {
	c.titles = append(c.titles, title)
	return c.inner.Select(title, description, choices, def)
}

func (c *countingPrompter) MultiSelect(title, description string, choices []ui.Choice, selected []string) ([]string, error) {
	c.titles = append(c.titles, title)
	return c.inner.MultiSelect(title, description, choices, selected)
}

func noSecretTools(context.Context, io.Reader, string, ...string) ([]byte, error) {
	return nil, errors.New("not installed")
}

type offline struct{}

func (offline) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("offline: test refused a request to " + r.URL.Host)
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=ana@acme.io", "GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=ana@acme.io", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# billing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "init")
	gitCmd(t, dir, "remote", "add", "origin", "git@github.com:acme/billing-api.git")
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	p := newPlatform(t)
	return &harness{
		t: t, platform: p, dir: dir, config: filepath.Join(cfg, "gravity"),
		env:     map[string]string{"GRAVITY_API_URL": p.srv.URL, "ACCESSIBLE": "1"},
		secrets: noSecretTools,
	}
}

func (h *harness) write(name, body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, name), []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	stdin := strings.NewReader(h.stdin)
	var inner ui.Prompter = &ui.HuhPrompter{In: stdin, Out: &h.stderr, Accessible: true}
	if h.prompter != nil {
		inner = h.prompter
	}
	h.prompts = &countingPrompter{inner: inner}
	a := &app{
		prompts:      h.prompts,
		secretRunner: h.secrets,
		stdin:        stdin,
		stdout:       &h.stdout,
		stderr:       &h.stderr,
		terminal:     h.terminal,
		clock:        func() time.Time { return fixedNow },
		getenv:       func(k string) string { return h.env[k] },
		openBrowser: func(u string) error {
			h.opened = append(h.opened, u)
			return nil
		},
		configure: func(c *api.Client) {
			c.Sleep = func(context.Context, time.Duration) error { return nil }
			h.bases = append(h.bases, c.BaseURL)
			if c.BaseURL != h.platform.srv.URL {
				c.HTTPClient = &http.Client{Transport: offline{}}
			}
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			if d >= time.Hour {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
	}
	return run(context.Background(), a, append([]string{"-C", h.dir}, args...))
}

func (h *harness) envelope() map[string]any {
	h.t.Helper()
	var env map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &env); err != nil {
		h.t.Fatalf("stdout is not one JSON document: %v\n%s\nstderr: %s", err, h.stdout.String(), h.stderr.String())
	}
	return env
}

func (h *harness) golden(name string) {
	h.t.Helper()
	got := strings.ReplaceAll(h.stdout.String(), h.platform.srv.URL, "http://platform.test")
	got = strings.ReplaceAll(got, h.dir, "/repo")
	got = strings.ReplaceAll(got, h.config, "/config/gravity")
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

const whoamiUser = `{"organizationId":"org_1","organizationName":"Acme","defaultSiteSlug":null,"keyHint":"x9Qa","features":{"pipelines":true,"device-auth":true,"machine-tokens":true},"apiUrl":"https://api.gravitydocs.io",
"principal":{"kind":"user","user":{"id":"usr_1","email":"dave@acme.io","name":"Dave"},"role":"editor","permissions":["docs.read","docs.write","docs.repos.manage","docs.repos.tokens"]},
"organization":{"id":"org_1","slug":"acme","name":"Acme"},
"organizations":[{"id":"org_1","slug":"acme","name":"Acme","role":"editor"},{"id":"org_2","slug":"acme-labs","name":"Acme Labs","role":"admin"}],
"token":{"kind":"user","scopes":[],"expiresAt":"2026-12-30T12:00:00Z"},"modules":{"cli":true,"memory":true,"agent":false}}`

const whoamiRepo = `{"organizationId":"org_1","organizationName":"Acme","keyHint":"r2D2","features":{"pipelines":true},
"principal":{"kind":"repo","repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","product":{"id":"prod_1","slug":"acme-platform"}}},
"organization":{"id":"org_1","slug":"acme","name":"Acme"},"organizations":[{"id":"org_1","slug":"acme","name":"Acme"}],
"token":{"kind":"repo","scopes":["repo:connect","runs:write","content:read","inventory:write"],"expiresAt":null}}`

const connectBody = `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","created":false,"createdVia":"cli","product":{"id":"prod_1","slug":"acme-platform","name":"Acme Platform"},"appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"manifest":{"version":2,"hash":"sha256:abc","accepted":true,"persisted":false,"reason":"dry_run","authoritativeBranch":"main","passesUpserted":[],"passesConverted":[],"passesArchived":[],"warnings":[]},
"effective":{"appPasses":"allow","overlay":false,"passes":[
{"name":"developer-api","kind":"reference","source":"manifest","locked":true,"enabled":true,"triggers":["push","pr"],"target":{"ref":"dev-portal/api","status":"ok","siteSlug":"dev-portal","spaceSlug":"api","collectionPath":[]}},
{"name":"product-guides","kind":"guides","source":"app","locked":false,"enabled":true,"triggers":["push"],"target":{"ref":"product/guides","status":"unapproved","siteSlug":"product","spaceSlug":"guides","collectionPath":[],"approveUrl":"https://app.gravitydocs.io/app/repos/cr_1/passes/rp_2#approve"}}]},
"createdTargets":[],"siblings":[{"name":"gateway","remoteKey":"github.com/acme/gateway","lastRunAt":"2026-09-30T10:00:00Z","passes":3,"health":"live"}],"serverFeatures":{"pipelines":true}}`

const statusBody = `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","health":"blocked","lastConnectAt":"2026-10-01T09:00:00Z","lastRunAt":"2026-09-30T10:00:00Z","cliVersion":"1.0.0","appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"product":{"slug":"acme-platform","name":"Acme Platform"},
"passes":[
{"name":"developer-api","kind":"reference","source":"manifest","enabled":true,"target":{"ref":"dev-portal/api","status":"ok"},"lastRun":{"runId":"prun_1","status":"succeeded","at":"2026-09-30T10:00:00Z"},"watermarks":[{"branch":"main","commitSha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","updatedAt":"2026-09-30T10:00:00Z"}]},
{"name":"product-guides","kind":"guides","source":"app","enabled":true,"target":{"ref":"product/guides","status":"unapproved"},"watermarks":[]}],
"runs":[{"id":"prun_1","trigger":"push","mode":"write","status":"succeeded","branch":"main","headSha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","startedAt":"2026-09-30T09:58:00Z","finishedAt":"2026-09-30T10:00:00Z","changes":4,"findings":0,"costUsd":0.42,"appUrl":"https://app.gravitydocs.io/app/repos/runs/prun_1"}],
"openBundles":[{"runId":"prun_1","pending":4,"appUrl":"https://app.gravitydocs.io/app/repos/runs/prun_1"}],
"tokens":[{"keyHint":"r2D2","kind":"repo","lastUsedAt":"2026-09-30T10:00:00Z","expiresAt":null}],
"health":{"status":"blocked","reasons":["Pass product-guides: target product/guides awaits approval"]}}`

const planBody = `{"planHash":"sha256:9a0e","overlay":false,
"repo":{"id":"cr_1","remoteKey":"github.com/acme/billing-api","name":"billing-api","defaultBranch":"main","authoritativeBranch":"main","webUrl":"https://github.com/acme/billing-api","manifestHash":"sha256:stored","appUrl":"https://app.gravitydocs.io/app/repos/cr_1"},
"product":{"id":"prod_1","slug":"acme-platform","name":"Acme Platform","nucleusNamespace":"product:acme-platform"},
"trigger":"push","branch":"main",
"passes":[
{"id":"rp_1","name":"developer-api","title":"Developer API","kind":"reference","template":"api-reference","source":"manifest","locked":true,"enabled":true,"applies":true,"skipReason":null,"triggers":["push","pr"],"branches":[],
"target":{"status":"ok","ref":"dev-portal/api","site":{"id":"site_1","slug":"dev-portal","name":"Developer Portal"},"space":{"id":"sp_1","slug":"api","name":"API","type":"api-reference"},"collection":null,"viewerUrl":"https://docs.acme.io/api","approval":{"by":"dave@acme.io","at":"2026-09-30T09:00:00Z"}},
"scope":{"paths":["api/**"],"exclude":[],"units":["api"]},"audiences":["developers"],"publishMode":"propose","trusted":false,"options":{"pageStrategy":"per-tag"},
"instructions":{"hash":"sha256:51be","layers":[{"source":"org","label":"Organization voice","text":"Friendly, precise, second person."},{"source":"pass","id":"rp_1","label":"Pass developer-api","text":"Keep endpoint reference current.\nNever describe UI."}]},
"prompt":{"name":"pass-reference","version":"sha256:77c1"},
"watermark":{"branch":"main","commitSha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","runId":"prun_1","updatedAt":"2026-09-30T10:00:00Z"},"hints":[]},
{"id":"rp_2","name":"product-guides","kind":"guides","source":"app","locked":false,"enabled":true,"applies":false,"skipReason":"target_unapproved","triggers":["push"],"branches":[],"target":{"status":"unapproved","ref":"product/guides","approveUrl":"https://app.gravitydocs.io/app/repos/cr_1/passes/rp_2#approve"},"scope":{},"instructions":{"hash":"sha256:00","layers":[]}},
{"id":"rp_3","name":"changelog","kind":"changelog","source":"app","locked":false,"enabled":true,"applies":false,"skipReason":"trigger_mismatch","triggers":["release"],"branches":[],"target":{"status":"ok","ref":"product/changelog"},"scope":{},"instructions":{"hash":"sha256:01","layers":[]}}],
"inventory":{"units":[],"truncated":false},
"capabilities":{"features":{"pipelines":true},"modules":{"cli":true},"llm":{"configured":true},"limits":{"heartbeatSeconds":60}},
"siblings":[],"warnings":[]}`
