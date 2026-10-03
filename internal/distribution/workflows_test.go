package distribution_test

import (
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

func TestReleaseRequiresTheMajorPinnedZeroRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "needs: gate") || !strings.Contains(s, "startsWith(inputs.tag || github.ref_name, 'v1.')") {
		t.Fatal("goreleaser must wait for the v0 gate on v1 tags")
	}
}
