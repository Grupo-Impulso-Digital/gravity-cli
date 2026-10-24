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

func TestDependabotTargetsTheTrunk(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Updates []struct {
			Ecosystem    string `yaml:"package-ecosystem"`
			TargetBranch string `yaml:"target-branch"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, u := range cfg.Updates {
		if u.TargetBranch != "develop" {
			t.Errorf("dependabot %s targets %q, want develop", u.Ecosystem, u.TargetBranch)
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

func TestTapAndBucketChangeThroughPullRequests(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type repository struct {
		Owner       string `yaml:"owner"`
		Name        string `yaml:"name"`
		Branch      string `yaml:"branch"`
		PullRequest struct {
			Enabled bool `yaml:"enabled"`
			Base    struct {
				Owner  string `yaml:"owner"`
				Name   string `yaml:"name"`
				Branch string `yaml:"branch"`
			} `yaml:"base"`
		} `yaml:"pull_request"`
	}
	var cfg struct {
		Casks []struct {
			Repository repository `yaml:"repository"`
		} `yaml:"homebrew_casks"`
		Scoops []struct {
			Repository repository `yaml:"repository"`
		} `yaml:"scoops"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var repos []repository
	for _, c := range cfg.Casks {
		repos = append(repos, c.Repository)
	}
	for _, s := range cfg.Scoops {
		repos = append(repos, s.Repository)
	}
	if len(repos) != 2 {
		t.Fatalf("one cask and one scoop manifest are published, got %d repositories", len(repos))
	}
	workflow := readWorkflow(t, "release.yml", &struct{}{})
	for _, r := range repos {
		slug := r.Owner + "/" + r.Name
		base := r.PullRequest.Base
		if !r.PullRequest.Enabled || r.Branch == "" || base.Owner != r.Owner || base.Name != r.Name || base.Branch != "main" {
			t.Errorf("%s must be updated through a pull request into main (the default branch is protected): %+v", slug, r)
		}
		if !strings.Contains(r.Branch, "{{ .Tag }}") {
			t.Errorf("%s: the release branch %q must carry the tag so releases never share one", slug, r.Branch)
		}
		if !strings.Contains(workflow, slug) {
			t.Errorf("release.yml does not merge the pull request of %s", slug)
		}
		if head := strings.ReplaceAll(r.Branch, "{{ .Tag }}", "${TAG}"); !strings.Contains(workflow, `branch="`+head+`"`) {
			t.Errorf("release.yml looks for another head branch than %q", head)
		}
	}
	for _, want := range []string{"GH_TOKEN: ${{ secrets.GORELEASER_TOKEN }}", "gh pr list", "gh pr merge", "--squash", "git/refs/heads/", "::warning::"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml lacks %s in the pull request merge step", want)
		}
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readWorkflow(t, "release.yml", &wf)
	found := false
	for _, step := range wf.Jobs["publish"].Steps {
		if strings.Contains(step.Run, "gh pr merge") {
			found = true
			if !strings.Contains(step.If, "contains(needs.prepare.outputs.tag, '-')") || !strings.Contains(step.If, "!") {
				t.Errorf("pre-releases skip the cask and the manifest, so only stable releases merge: if = %q", step.If)
			}
			if strings.Contains(step.Run, "set -e") || strings.Contains(step.Run, "exit 1") {
				t.Errorf("a failed merge must warn, never fail the release: %s", step.Name)
			}
		}
	}
	if !found {
		t.Error("the publish job has no step merging the tap and bucket pull requests")
	}
}
