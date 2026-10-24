package distribution_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDogfoodWorkflowIsTheSingleStepForm(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			Steps []struct {
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("jobs = %d", len(wf.Jobs))
	}
	for _, job := range wf.Jobs {
		if len(job.Steps) != 2 || !strings.HasPrefix(job.Steps[0].Uses, "actions/checkout@") || job.Steps[1].Uses != "./ci/github" {
			t.Fatalf("steps = %+v", job.Steps)
		}
		with := job.Steps[1].With
		if with["repo-token"] != "${{ secrets.GRAVITY_REPO_TOKEN }}" || with["token"] != "${{ secrets.GRAVITY_TOKEN }}" || with["version"] != "source" || with["command"] != "" {
			t.Fatalf("with = %v", with)
		}
	}
	for _, trigger := range []string{"push", "pull_request", "workflow_dispatch"} {
		if _, ok := wf.On[trigger]; !ok {
			t.Fatalf("docs.yml must run on %s", trigger)
		}
	}
}

func TestNoTemplateRunsWithSecretsOnForkCode(t *testing.T) {
	for _, root := range []string{filepath.Join("..", "..", "ci"), filepath.Join("..", "..", ".github")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || (filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "pull_request_target") {
				t.Errorf("%s uses pull_request_target", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTemplatesPinMajorOne(t *testing.T) {
	for _, path := range []string{"gitlab/gravity.yml", "bitbucket/bitbucket-pipelines.yml", "azure/azure-pipelines.gravity.yml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "ci", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "GRAVITY_VERSION=1 ") || !strings.Contains(strings.ReplaceAll(string(data), `gravity" run`, "gravity run"), "gravity run") {
			t.Fatalf("%s must install major 1 and run gravity run", path)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "ci", "github", "gravity.yml"))
	if err != nil || !strings.Contains(string(data), "gravity-cli/ci/github@v1") {
		t.Fatalf("the GitHub workflow uses the v1 action: %s %v", data, err)
	}
}

func readWorkflow(t *testing.T, name string, v any) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReleasesComeFromTheWorkflow(t *testing.T) {
	var wf struct {
		On map[string]struct {
			Inputs map[string]any `yaml:"inputs"`
		} `yaml:"on"`
	}
	s := readWorkflow(t, "release.yml", &wf)
	if len(wf.On) != 1 || wf.On["workflow_dispatch"].Inputs["bump"] == nil || wf.On["workflow_dispatch"].Inputs["prerelease"] == nil || wf.On["workflow_dispatch"].Inputs["commit"] == nil {
		t.Fatalf("release.yml must be a workflow_dispatch with bump, prerelease and commit: %+v", wf.On)
	}
	for _, gate := range []string{"vars.PRODUCTION_APPROVERS", "check_name=ci-ok", "refs/heads/develop", "GORELEASER_CURRENT_TAG", "contains(needs.prepare.outputs.tag, '-')"} {
		if !strings.Contains(s, gate) {
			t.Errorf("release.yml lacks %s", gate)
		}
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		var other struct {
			On map[string]any `yaml:"on"`
		}
		body := readWorkflow(t, e.Name(), &other)
		if e.Name() != "release.yml" && strings.Contains(body, "goreleaser release --clean") {
			t.Errorf("%s publishes a release; only release.yml may", e.Name())
		}
		if push, ok := other.On["push"].(map[string]any); ok && strings.Contains(fmt.Sprint(push["branches"]), "main") {
			t.Errorf("%s runs on pushes to the frozen main branch", e.Name())
		}
	}
}

func TestCIGuardsTheTrunk(t *testing.T) {
	var wf struct {
		On   map[string]map[string][]string `yaml:"on"`
		Jobs map[string]struct {
			Needs []string `yaml:"needs"`
			Steps []struct {
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readWorkflow(t, "ci.yml", &wf)
	for _, event := range []string{"push", "pull_request"} {
		if got := strings.Join(wf.On[event]["branches"], ","); got != "develop,hotfix/**" {
			t.Errorf("ci.yml %s branches = %s", event, got)
		}
	}
	if got := strings.Join(wf.Jobs["ci-ok"].Needs, ","); got != "check,test,build" {
		t.Errorf("ci-ok needs %s", got)
	}
	var args []string
	for _, step := range wf.Jobs["build"].Steps {
		args = append(args, step.With["args"])
	}
	if got := strings.Join(args, ","); !strings.Contains(got, "check") || !strings.Contains(got, "release --snapshot --clean") {
		t.Errorf("the build job must run goreleaser check and a snapshot: %v", args)
	}
}

func TestPreReleasesNeverReachTheTapOrTheBucket(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Casks  []map[string]any `yaml:"homebrew_casks"`
		Scoops []map[string]any `yaml:"scoops"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Casks) == 0 || len(cfg.Scoops) == 0 {
		t.Fatal("the cask and the scoop manifest are published")
	}
	for _, c := range append(cfg.Casks, cfg.Scoops...) {
		if c["skip_upload"] != "auto" {
			t.Errorf("%v must skip pre-releases (skip_upload: auto)", c["repository"])
		}
	}
}
