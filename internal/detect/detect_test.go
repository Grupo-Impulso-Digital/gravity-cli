package detect

import (
	"errors"
	"reflect"
	"testing"
)

func run(t *testing.T, files map[string]string, tags ...string) *Result {
	t.Helper()
	list := make([]string, 0, len(files))
	for f := range files {
		list = append(list, f)
	}
	r, err := Run(Input{Files: list, Tags: tags, ReadFile: func(rel string) ([]byte, error) {
		if body, ok := files[rel]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("missing " + rel)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenAPIDetection(t *testing.T) {
	r := run(t, map[string]string{
		"api/openapi.yaml":            "openapi: 3.1.0\npaths:\n  /a:\n    get: {}\n    post: {}\n    parameters: []\n  /b:\n    delete: {}\n",
		"spec/swagger.json":           `{"swagger":"2.0","paths":{"/x":{"get":{}}}}`,
		"config/app.yaml":             "name: app\n",
		"node_modules/x/openapi.yaml": "openapi: 3.0.0\npaths: {}\n",
		"testdata/openapi.yaml":       "openapi: 3.0.0\npaths: {}\n",
		"package.json":                `{"openapi":"not a spec"}`,
	})
	want := []OpenAPIDoc{{Path: "api/openapi.yaml", Operations: 3, Version: "3.1.0"}, {Path: "spec/swagger.json", Operations: 1, Version: "swagger 2.0"}}
	if !reflect.DeepEqual(r.OpenAPI, want) {
		t.Fatalf("openapi = %+v", r.OpenAPI)
	}
}

func TestLanguagesAreRankedAndVendoredSkipped(t *testing.T) {
	files := map[string]string{"vendor/a.go": "", "dist/b.js": ""}
	for _, f := range []string{"a.ts", "b.ts", "c.tsx", "d.go", "e.py"} {
		files["src/"+f] = ""
	}
	r := run(t, files)
	var keys []string
	for _, l := range r.Languages {
		keys = append(keys, l.Key)
	}
	if !reflect.DeepEqual(keys, []string{"typescript", "go", "python"}) || r.Languages[0].Files != 3 {
		t.Fatalf("languages = %+v", r.Languages)
	}
}

func TestUIRoutes(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		framework string
		routes    int
		path      string
	}{
		{"next app", map[string]string{"app/page.tsx": "", "app/billing/page.tsx": "", "app/layout.tsx": ""}, "Next.js", 2, "app/**"},
		{"tanstack", map[string]string{"src/routes/index.tsx": "", "src/routes/about.tsx": "", "src/routes/-helper.tsx": ""}, "TanStack Router", 2, "src/routes/**"},
		{"sveltekit", map[string]string{"src/routes/+page.svelte": "", "src/routes/a/+page.svelte": "", "src/routes/+layout.svelte": ""}, "SvelteKit", 2, "src/routes/**"},
		{"nuxt", map[string]string{"pages/index.vue": "", "pages/x.vue": "", "pages/y.vue": ""}, "Nuxt", 3, "pages/**"},
		{"next pages", map[string]string{"pages/index.tsx": "", "pages/_app.tsx": "", "pages/api/x.ts": ""}, "Next.js", 1, "pages/**"},
		{"angular", map[string]string{"src/app/app-routing.module.ts": "const routes = [{ path: 'a' }, { path: 'b' }]"}, "Angular", 2, "src/app/**"},
	}
	for _, c := range cases {
		r := run(t, c.files)
		if r.UIFramework != c.framework || r.UIRoutes != c.routes || len(r.UIPaths) != 1 || r.UIPaths[0] != c.path {
			t.Errorf("%s: %s %d %v", c.name, r.UIFramework, r.UIRoutes, r.UIPaths)
		}
	}
}

func TestServerRoutesAndCLI(t *testing.T) {
	r := run(t, map[string]string{
		"server/routes.ts":       "app.get('/refunds', h)\nrouter.post(\"/refunds\", h)\n",
		"server/routes.test.ts":  "app.get('/ignored', h)\n",
		"cmd/root.go":            "var a = &cobra.Command{}\nvar b = &cobra.Command{Use: \"x\"}\n",
		"internal/http/serve.go": "mux.HandleFunc(\"/health\", h)\nr.Get(\"/items\", h)\n",
	})
	if r.ServerRoutes != 4 || r.CLICommands != 2 || r.ServerFramework != "Go" && r.ServerFramework != "Node.js" {
		t.Fatalf("server = %+v", r)
	}
	if !reflect.DeepEqual(r.ServerPaths, []string{"cmd/**", "internal/http/**", "server/**"}) {
		t.Fatalf("paths = %v", r.ServerPaths)
	}
}

func TestDocsRunbooksReleasesAndCI(t *testing.T) {
	r := run(t, map[string]string{
		"README.md": "", "CHANGELOG.md": "", "LICENSE.md": "", "AGENTS.md": "", "CONTRIBUTING.md": "", ".github/pull_request_template.md": "",
		"docs/intro.md": "", "docs/handbook/a.md": "", "docs/handbook/b.mdx": "", "docs/runbooks/deploy.md": "", "runbooks/oncall.md": "",
		".github/workflows/ci.yml": "", ".gitlab-ci.yml": "", "Jenkinsfile": "", ".circleci/config.yml": "", "azure-pipelines.yml": "", "bitbucket-pipelines.yml": "",
	}, "v1.0.0", "v1.1.0", "docs-synced", "release-1")
	if r.MarkdownFiles != 6 {
		t.Fatalf("markdown = %d", r.MarkdownFiles)
	}
	if !reflect.DeepEqual(r.DocsFolders, []Folder{{Path: "docs/handbook", Files: 2}, {Path: "docs", Files: 1}}) {
		t.Fatalf("docs = %+v", r.DocsFolders)
	}
	if !reflect.DeepEqual(r.Runbooks, []Folder{{Path: "docs/runbooks", Files: 1}, {Path: "runbooks", Files: 1}}) {
		t.Fatalf("runbooks = %+v", r.Runbooks)
	}
	if r.ReleaseTags != 2 || r.Changelog != "CHANGELOG.md" {
		t.Fatalf("releases = %d %q", r.ReleaseTags, r.Changelog)
	}
	if !reflect.DeepEqual(r.CI, []string{"azure", "bitbucket", "circleci", "github", "gitlab", "jenkins"}) {
		t.Fatalf("ci = %v", r.CI)
	}
}

func TestEmptyRepository(t *testing.T) {
	r := run(t, map[string]string{})
	if r.HasUI() || r.HasServer() || len(r.OpenAPI) != 0 || r.CI == nil || r.DocsFolders == nil {
		t.Fatalf("empty = %+v", r)
	}
}
