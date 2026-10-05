package cisetup

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

func renderPlan(p *Plan) string {
	var b strings.Builder
	b.WriteString("provider: " + p.Provider + " (" + p.Label + ")\n")
	for _, f := range p.Files {
		b.WriteString("=== " + f.Action + " " + f.Path)
		if f.Note != "" {
			b.WriteString(" (" + f.Note + ")")
		}
		b.WriteString("\n" + f.Content)
	}
	if p.Snippet != "" {
		b.WriteString("=== snippet for " + p.SnippetTarget + "\n" + p.Snippet)
	}
	b.WriteString("=== paste: " + p.PasteHint + "\n")
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test -update): %v", err)
	}
	if string(want) != got {
		t.Fatalf("%s mismatch\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

var webURLs = map[string]string{
	GitHub:    "https://github.com/acme/billing-api",
	GitLab:    "https://gitlab.com/acme/billing-api",
	Bitbucket: "https://bitbucket.org/acme/billing-api",
	Azure:     "https://dev.azure.com/acme/billing/_git/billing-api",
}

func TestTemplatesPerProvider(t *testing.T) {
	for _, provider := range Providers {
		for _, variant := range []struct {
			name string
			opts Options
		}{
			{"default", Options{DefaultBranch: "main", WebURL: webURLs[provider]}},
			{"schedule", Options{DefaultBranch: "trunk", Schedule: true, APIURL: "https://api.acme.test"}},
		} {
			t.Run(provider+"-"+variant.name, func(t *testing.T) {
				p, err := Build(t.TempDir(), provider, variant.opts)
				if err != nil {
					t.Fatal(err)
				}
				out := renderPlan(p)
				if strings.Contains(out, "pull_request_target") {
					t.Fatal("templates never use pull_request_target")
				}
				if provider != None && provider != GitHub && !strings.Contains(out, "GRAVITY_VERSION=1") {
					t.Fatal("templates pin major 1")
				}
				golden(t, provider+"-"+variant.name, out)
			})
		}
	}
}

func TestGitHubActionPinsMajorOne(t *testing.T) {
	p, err := Build(t.TempDir(), GitHub, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Files[0].Content, "gravity-cli/ci/github@v1") || !strings.Contains(p.Files[0].Content, "fetch-depth: 0") || !strings.Contains(p.Files[0].Content, "branches: [main]") {
		t.Fatalf("workflow = %s", p.Files[0].Content)
	}
}

func TestExistingFilesAreKeptOrExtended(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, ".github/workflows/gravity.yml", "name: mine\n")
	p, err := Build(root, GitHub, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Files[0].Action != ActionKeep || p.Files[0].Content != "name: mine\n" {
		t.Fatalf("github = %+v", p.Files[0])
	}

	mustWrite(t, root, ".gitlab-ci.yml", "stages: [test]")
	p, err = Build(root, GitLab, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Files[1].Action != ActionAppend || p.Files[1].Content != "\n\ninclude:\n  - local: .gitlab/gravity.yml\n" {
		t.Fatalf("gitlab-ci = %+v", p.Files[1])
	}
	written, err := Write(root, p)
	if err != nil || len(written) != 2 {
		t.Fatalf("written = %v %v", written, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	if string(data) != "stages: [test]\n\ninclude:\n  - local: .gitlab/gravity.yml\n" {
		t.Fatalf(".gitlab-ci.yml = %q", data)
	}
	p, err = Build(root, GitLab, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files {
		if f.Action != ActionKeep {
			t.Fatalf("second run changes nothing: %+v", f)
		}
	}

	other := t.TempDir()
	mustWrite(t, other, ".gitlab-ci.yml", "include:\n  - template: Security.gitlab-ci.yml\n")
	p, err = Build(other, GitLab, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 || !strings.Contains(p.SnippetTarget, "include") {
		t.Fatalf("an existing include: list is never duplicated: %+v", p)
	}

	mustWrite(t, other, "bitbucket-pipelines.yml", "pipelines: {}\n")
	p, err = Build(other, Bitbucket, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 0 || p.Snippet == "" {
		t.Fatalf("an existing bitbucket-pipelines.yml gets a snippet: %+v", p)
	}
}

func mustWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownProvider(t *testing.T) {
	if _, err := Build(t.TempDir(), "travis", Options{}); err == nil {
		t.Fatal("unknown providers are refused")
	}
}

type call struct {
	name  string
	args  []string
	stdin string
}

func recorder(calls *[]call, fail map[string]bool) Runner {
	return func(_ context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
		in := ""
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			in = string(b)
		}
		*calls = append(*calls, call{name, args, in})
		if fail[name+" "+args[0]] {
			return []byte("boom " + in), errors.New("exit 1")
		}
		return nil, nil
	}
}

func TestInstallersUseStdin(t *testing.T) {
	var calls []call
	gh := FindInstaller(context.Background(), GitHub, "github.com/acme/billing-api", recorder(&calls, nil))
	if gh == nil || gh.Repo != "acme/billing-api" {
		t.Fatalf("gh = %+v", gh)
	}
	if err := gh.Install(context.Background(), SecretName, "gr_repo_x"); err != nil {
		t.Fatal(err)
	}
	last := calls[len(calls)-1]
	if strings.Join(last.args, " ") != "secret set GRAVITY_REPO_TOKEN --repo acme/billing-api" || last.stdin != "gr_repo_x" {
		t.Fatalf("gh call = %+v", last)
	}

	ghe := FindInstaller(context.Background(), GitHub, "git.acme.io/team/api", recorder(&calls, nil))
	if ghe.Repo != "git.acme.io/team/api" {
		t.Fatalf("enterprise repo = %q", ghe.Repo)
	}
	glab := FindInstaller(context.Background(), GitLab, "gitlab.acme.io/group/sub/api", recorder(&calls, nil))
	if glab.Repo != "https://gitlab.acme.io/group/sub/api" || !strings.Contains(glab.Describe(SecretName), "--masked") {
		t.Fatalf("glab = %+v", glab)
	}
	if err := glab.Install(context.Background(), SecretName, "gr_repo_y"); err != nil {
		t.Fatal(err)
	}
	last = calls[len(calls)-1]
	if last.name != "glab" || strings.Join(last.args, " ") != "variable set GRAVITY_REPO_TOKEN --masked --repo https://gitlab.acme.io/group/sub/api" || last.stdin != "gr_repo_y" {
		t.Fatalf("glab call = %+v", last)
	}
	for _, c := range calls {
		for _, a := range c.args {
			if strings.HasPrefix(a, "gr_repo_") {
				t.Fatalf("token in argv: %+v", c)
			}
		}
	}
}

func TestInstallerUnavailableOrFailing(t *testing.T) {
	var calls []call
	if FindInstaller(context.Background(), GitHub, "github.com/acme/api", recorder(&calls, map[string]bool{"gh auth": true})) != nil {
		t.Fatal("an unauthenticated gh is not offered")
	}
	if FindInstaller(context.Background(), Bitbucket, "bitbucket.org/acme/api", recorder(&calls, nil)) != nil {
		t.Fatal("bitbucket has no installer")
	}
	inst := FindInstaller(context.Background(), GitHub, "github.com/acme/api", recorder(&calls, map[string]bool{"gh secret": true}))
	err := inst.Install(context.Background(), SecretName, "gr_repo_secret")
	if err == nil || strings.Contains(err.Error(), "gr_repo_secret") {
		t.Fatalf("install error must not echo the token: %v", err)
	}
}

func collectStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []any:
		for _, e := range x {
			collectStrings(e, out)
		}
	case map[string]any:
		for k, e := range x {
			*out = append(*out, k)
			collectStrings(e, out)
		}
	}
}

func TestTemplatesQuoteBranchAndAPIURL(t *testing.T) {
	branch := "release/1,2]"
	apiURL := "https://api.acme.test/x y'z"
	for _, provider := range []string{GitHub, GitLab, Bitbucket, Azure, CircleCI} {
		t.Run(provider, func(t *testing.T) {
			p, err := Build(t.TempDir(), provider, Options{DefaultBranch: branch, Schedule: true, APIURL: apiURL})
			if err != nil {
				t.Fatal(err)
			}
			docs := []string{p.Snippet}
			for _, f := range p.Files {
				docs = append(docs, f.Content)
			}
			var values []string
			for _, doc := range docs {
				var v any
				if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
					t.Fatalf("invalid YAML: %v\n%s", err, doc)
				}
				collectStrings(v, &values)
			}
			hasBranch, hasURL := false, false
			for _, v := range values {
				hasBranch = hasBranch || v == branch
				hasURL = hasURL || v == apiURL || v == "export GRAVITY_API_URL="+shellQuote(apiURL)
			}
			if !hasURL || (!hasBranch && provider != GitLab && provider != CircleCI) {
				t.Fatalf("branch %v, api url %v in %q", hasBranch, hasURL, values)
			}
		})
	}
}

func TestShellAndGroovyQuoting(t *testing.T) {
	for in, want := range map[string]string{"https://api.acme.test": "https://api.acme.test", "a b": "'a b'", "it's": `'it'\''s'`} {
		if got := shellQuote(in); got != want {
			t.Fatalf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := groovyQuote(`a'b\c`); got != `'a\'b\\c'` {
		t.Fatalf("groovyQuote = %s", got)
	}
	for in, want := range map[string]string{"main": "main", "yes": "'yes'", "1.0": "'1.0'", "a,b": "'a,b'"} {
		if got := yamlQuote(in); got != want {
			t.Fatalf("yamlQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSharedWorkflowCallerKeepsTheGitHubFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "docs.yml"), []byte("jobs:\n  docs:\n    uses: Grupo-Impulso-Digital/workflows/.github/workflows/gravity-docs.yml@main\n    secrets: inherit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Build(root, GitHub, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := p.Files[0]; f.Action != ActionKeep || !strings.Contains(f.Note, ".github/workflows/docs.yml calls the shared gravity-docs.yml workflow") {
		t.Fatalf("file = %+v", f)
	}
}

func TestOutdatedGravityCIFiles(t *testing.T) {
	current, err := Build(t.TempDir(), GitHub, Options{})
	if err != nil {
		t.Fatal(err)
	}
	newGitHub := current.Files[0].Content
	azure, err := Build(t.TempDir(), Azure, Options{})
	if err != nil {
		t.Fatal(err)
	}
	newAzure := azure.Files[0].Content
	write := func(root, rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, provider, rel, body, action string
	}{
		{"github 1.0.0", GitHub, ".github/workflows/gravity.yml", strings.Replace(newGitHub, githubTokenLines, "          token: ${{ secrets.GRAVITY_TOKEN }}\n", 1), ActionUpdate},
		{"github 1.0.2", GitHub, ".github/workflows/gravity.yml", strings.Replace(newGitHub, githubTokenLines, "          token: ${{ secrets.GRAVITY_REPO_TOKEN || secrets.GRAVITY_TOKEN }}\n", 1), ActionUpdate},
		{"github edited", GitHub, ".github/workflows/gravity.yml", "on: push\njobs:\n  g:\n    steps:\n      - uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1\n        with:\n          token: ${{ secrets.GRAVITY_TOKEN }}\n", ActionKeep},
		{"azure 1.0.1", Azure, "azure-pipelines.gravity.yml", strings.Replace(newAzure, azureTokenLines, "      GRAVITY_TOKEN: $(GRAVITY_TOKEN)\n", 1), ActionUpdate},
		{"github current", GitHub, ".github/workflows/gravity.yml", newGitHub, ActionKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(root, tc.rel, tc.body)
			p, err := Build(root, tc.provider, Options{})
			if err != nil {
				t.Fatal(err)
			}
			f := p.Files[0]
			if f.Action != tc.action {
				t.Fatalf("action = %s (%s), want %s", f.Action, f.Note, tc.action)
			}
			outdated := Outdated(root)
			if tc.name == "github current" {
				if len(outdated) != 0 {
					t.Fatalf("current file reported outdated: %+v", outdated)
				}
				return
			}
			if len(outdated) != 1 || outdated[0].Path != tc.rel {
				t.Fatalf("outdated = %+v", outdated)
			}
			if tc.action == ActionKeep && !strings.Contains(f.Note, "repo-token: ${{ secrets.GRAVITY_REPO_TOKEN }}") {
				t.Fatalf("an edited file is kept with the fix: %s", f.Note)
			}
			if tc.action == ActionUpdate {
				if _, err := Write(root, p); err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(filepath.Join(root, tc.rel)); string(got) != f.Content || len(Outdated(root)) != 0 {
					t.Fatalf("updated file = %s", got)
				}
			}
		})
	}
	root := t.TempDir()
	write(root, "Jenkinsfile", "stage('Gravity') {\n  environment {\n    GRAVITY_TOKEN = credentials('gravity-token')\n  }\n}\n")
	if got := Outdated(root); len(got) != 1 || got[0].Path != "Jenkinsfile" {
		t.Fatalf("jenkins = %+v", got)
	}
}
