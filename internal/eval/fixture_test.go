package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func makeFixture(t *testing.T, env ...string) string {
	t.Helper()
	for _, bin := range []string{"bash", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed", bin)
		}
	}
	script, err := filepath.Abs("../../testdata/eval/make-fixture.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "topics-api")
	cmd := exec.Command("bash", script, dir, "git@github.com:acme/topics-eval.git")
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make-fixture.sh: %v\n%s", err, out)
	}
	return dir
}

func gitOut(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestMakeFixtureMatchesTheScorer(t *testing.T) {
	dir := makeFixture(t, "EVAL_TARGET=eval-topics/guides")

	data, err := os.ReadFile(filepath.Join(dir, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for path, item := range doc.Paths {
		for method, op := range item {
			if method == "parameters" {
				continue
			}
			internal := false
			if m, ok := op.(map[string]any); ok {
				internal, _ = m["x-internal"].(bool)
			}
			got = append(got, Endpoint{Method: strings.ToUpper(method), Path: path, Internal: internal}.String()+map[bool]string{true: " internal"}[internal])
		}
	}
	for _, e := range FixtureEndpoints {
		want = append(want, e.String()+map[bool]string{true: " internal"}[e.Internal])
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("openapi operations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(Public(FixtureEndpoints)) != 14 || len(Internal(FixtureEndpoints)) != 3 {
		t.Errorf("fixture has %d public and %d internal endpoints", len(Public(FixtureEndpoints)), len(Internal(FixtureEndpoints)))
	}
	for _, f := range []string{"src/routes/topics.ts", "src/routes/posts.ts", "src/routes/subscriptions.ts", "src/routes/search.ts", "src/routes/internal.ts", "docs/architecture.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}

	m, err := config.Load(filepath.Join(dir, config.ManifestFileName))
	if err != nil || m == nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Passes) != 1 || m.Passes[0].Kind != "guides" || m.Passes[0].Target != "eval-topics/guides" {
		t.Errorf("passes = %+v", m.Passes)
	}

	if remote, _ := gitOut(t, dir, "remote", "get-url", "origin"); remote != "git@github.com:acme/topics-eval.git" {
		t.Errorf("origin = %q", remote)
	}
	if up, err := gitOut(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Errorf("main tracks %q", up)
	}
	if branch, _ := gitOut(t, dir, "branch", "--show-current"); branch != "main" {
		t.Errorf("branch = %q", branch)
	}
	if status, _ := gitOut(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("dirty tree:\n%s", status)
	}
	log, err := gitOut(t, dir, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	subjects := strings.Split(log, "\n")
	french, english := 0, 0
	for _, s := range subjects {
		if strings.HasPrefix(s, "Ajoute") || strings.HasPrefix(s, "Documentation technique") {
			french++
		} else {
			english++
		}
	}
	if len(subjects) != 7 || french != 3 || english != 4 {
		t.Errorf("commits: %d (%d French, %d English):\n%s", len(subjects), french, english, log)
	}
}

func TestMakeFixtureIsDeterministic(t *testing.T) {
	a, _ := gitOut(t, makeFixture(t), "rev-parse", "HEAD")
	b, _ := gitOut(t, makeFixture(t), "rev-parse", "HEAD")
	if a == "" || a != b {
		t.Errorf("HEAD %q then %q", a, b)
	}
}
