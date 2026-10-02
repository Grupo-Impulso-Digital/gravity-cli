package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func readTemplate(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGitHubActionDefaults(t *testing.T) {
	var action struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []struct {
				Name string `yaml:"name"`
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal([]byte(readTemplate(t, "ci/github/action.yml")), &action); err != nil {
		t.Fatal(err)
	}
	if got := action.Inputs["api-url"].Default; got != "" {
		t.Errorf("api-url default = %q, want empty so the manifest wins", got)
	}
	if got := action.Inputs["args"].Default; strings.Contains(got, "--format") {
		t.Errorf("args default %q must not force --format on every command", got)
	}
	if got := action.Inputs["version"].Default; got != "latest" {
		t.Errorf("version default = %q, want latest", got)
	}
	var installs, caches, windows bool
	for _, s := range action.Runs.Steps {
		if strings.Contains(s.Run, "go install") {
			t.Errorf("step %q still go-installs the CLI", s.Name)
		}
		if strings.Contains(s.Run, "install.sh") {
			installs = true
		}
		if strings.Contains(s.Run, "install.ps1") {
			windows = true
		}
		if strings.Contains(s.Run, "# ") {
			t.Errorf("step %q carries a narrative shell comment", s.Name)
		}
		if strings.HasPrefix(s.Uses, "actions/cache@") {
			caches = true
		}
		if s.Name == "Run gravity" && !regexp.MustCompile(`"check api"\|"check docs"\|coverage\)`).MatchString(s.Run) {
			t.Errorf("--format must be appended only for check api, check docs and coverage:\n%s", s.Run)
		}
	}
	if !installs || !caches || !windows {
		t.Errorf("the action must install the release binary (%v, windows %v) and cache it (%v)", installs, windows, caches)
	}
}

func TestPipelineTemplatesDeferToTheManifest(t *testing.T) {
	for _, rel := range []string{"ci/gitlab/.gitlab-ci.yml", "ci/bitbucket/pipe"} {
		body := readTemplate(t, rel)
		var doc any
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Errorf("%s is not valid YAML: %v", rel, err)
		}
		for _, bad := range []string{"GRAVITY_SITE:", `GRAVITY_SITE="`, "go install", "--space changelog", "GRAVITY_API_URL:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s still contains %q", rel, bad)
			}
		}
		if !strings.Contains(body, "install.sh") {
			t.Errorf("%s must install the prebuilt release binary", rel)
		}
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "git push") && strings.HasPrefix(trimmed, "#") {
				t.Errorf("%s leaves the docs-synced marker push commented out: %s", rel, trimmed)
			}
		}
		if !strings.Contains(body, "docs-synced") || !strings.Contains(body, "git push") {
			t.Errorf("%s must push the docs-synced marker", rel)
		}
	}
}

func TestMarkerPushNeverTriggersReleaseNotes(t *testing.T) {
	type gitlabJob struct {
		Rules []struct {
			If string `yaml:"if"`
		} `yaml:"rules"`
		Script []string `yaml:"script"`
	}
	var gitlab struct {
		ReleaseNotes gitlabJob `yaml:"release-notes"`
		Generate     gitlabJob `yaml:"docs:generate"`
	}
	if err := yaml.Unmarshal([]byte(readTemplate(t, "ci/gitlab/.gitlab-ci.yml")), &gitlab); err != nil {
		t.Fatal(err)
	}
	if len(gitlab.ReleaseNotes.Rules) == 0 {
		t.Fatal("gitlab template has no release-notes job rules")
	}
	for _, r := range gitlab.ReleaseNotes.Rules {
		if !strings.Contains(r.If, "$CI_COMMIT_TAG =~ /^v/") {
			t.Errorf("release-notes rule %q must match release tags only", r.If)
		}
	}
	var pushes int
	for _, line := range strings.Split(strings.Join(gitlab.Generate.Script, "\n"), "\n") {
		if strings.Contains(line, "git push") {
			pushes++
			if !strings.Contains(line, "-o ci.skip") {
				t.Errorf("gitlab marker push must skip CI: %s", strings.TrimSpace(line))
			}
		}
	}
	if pushes == 0 {
		t.Error("gitlab docs:generate must push the docs-synced marker")
	}

	var bitbucket struct {
		Pipelines struct {
			Tags    map[string]any `yaml:"tags"`
			Default any            `yaml:"default"`
		} `yaml:"pipelines"`
	}
	if err := yaml.Unmarshal([]byte(readTemplate(t, "ci/bitbucket/pipe")), &bitbucket); err != nil {
		t.Fatal(err)
	}
	if bitbucket.Pipelines.Default != nil {
		t.Error("bitbucket template must not define a default pipeline the marker push would start")
	}
	for pattern := range bitbucket.Pipelines.Tags {
		if !strings.HasPrefix(pattern, "v") {
			t.Errorf("bitbucket tag pipeline %q would also run on the docs-synced marker", pattern)
		}
	}
}
