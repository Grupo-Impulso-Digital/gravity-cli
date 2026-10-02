package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

const overrideManifest = `site: acme
spaces:
  default: guides
documents:
  - file: docs/intro.md
    page: intro
  - file: docs/api.md
    space: developers
    page: api
`

func overrideRepo(t *testing.T) {
	t.Helper()
	dir := newGitRepo(t, map[string]string{
		config.ProjectFileName: overrideManifest,
		"docs/intro.md":        "# Intro\n\nHello.\n",
		"docs/api.md":          "# API\n\nCalls.\n",
	})
	chdirTemp(t, dir)
}

func dryRunSpaces(t *testing.T, args ...string) map[string]string {
	t.Helper()
	stdout, _, err := runRoot(t, append([]string{"sync", "--dry-run", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	var rep syncReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("dry-run --json is not JSON: %v\n%s", err, stdout)
	}
	got := map[string]string{}
	for _, tg := range rep.Targets {
		raw, _ := json.Marshal(tg.Payload)
		var p struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(raw, &p)
		got[p.Slug] = tg.Space
	}
	return got
}

func TestSyncSpaceFlagOverridesDefaultTarget(t *testing.T) {
	overrideRepo(t)
	if got := dryRunSpaces(t); got["intro"] != "guides" || got["api"] != "developers" {
		t.Fatalf("baseline spaces = %v", got)
	}
	got := dryRunSpaces(t, "--space", "staging-guides")
	if got["intro"] != "staging-guides" {
		t.Errorf("--space must retarget mappings without a space; intro -> %q", got["intro"])
	}
	if got["api"] != "developers" {
		t.Errorf("an explicit mapping space must win over --space; api -> %q", got["api"])
	}
}

func TestSyncHonoursGravitySpaceEnv(t *testing.T) {
	overrideRepo(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvSpace, "from-env")
	stdout, _, err := func() (string, string, error) {
		root := NewRootCommand()
		var out, errOut strings.Builder
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"sync", "--dry-run", "--json"})
		err := root.Execute()
		return out.String(), errOut.String(), err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"space": "from-env"`) {
		t.Errorf("GRAVITY_SPACE must override spaces.default:\n%s", stdout)
	}
}

func TestSyncJSONReport(t *testing.T) {
	overrideRepo(t)
	fp := &fakePlatform{
		features: map[string]bool{},
		routes: map[string]http.HandlerFunc{
			"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"pages":[]}`))
			},
			"POST /api/v1/sites/acme/spaces": func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"space":{"id":"s","slug":"x"}}`))
			},
			"POST /api/v1/sites/acme/pages": func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Slug string `json:"slug"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": req.Slug, "status": "open", "proposalId": "p-" + req.Slug})
			},
		},
	}
	srv := fp.serve(t)
	stdout, stderr, err := runRoot(t, "sync", "--json", "--api-url", srv.URL, "--token", "sk_live_x")
	if err != nil {
		t.Fatalf("sync --json: %v\n%s", err, stderr)
	}
	var rep syncReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("stdout must be pure JSON: %v\n%s", err, stdout)
	}
	if rep.Site != "acme" || len(rep.Plan) != 2 || len(rep.Proposed) != 2 {
		t.Errorf("report = %+v", rep)
	}
	if !strings.Contains(stderr, "plan: CREATE") {
		t.Errorf("human progress belongs on stderr in --json mode; stderr=%q", stderr)
	}
}
