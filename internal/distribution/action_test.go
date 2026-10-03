package distribution_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type actionStep struct {
	ID    string            `yaml:"id"`
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	Shell string            `yaml:"shell"`
	Env   map[string]string `yaml:"env"`
}

type actionFile struct {
	Inputs map[string]struct {
		Required    bool   `yaml:"required"`
		Default     string `yaml:"default"`
		Description string `yaml:"description"`
	} `yaml:"inputs"`
	Outputs map[string]struct {
		Value string `yaml:"value"`
	} `yaml:"outputs"`
	Runs struct {
		Using string       `yaml:"using"`
		Steps []actionStep `yaml:"steps"`
	} `yaml:"runs"`
}

func readAction(t *testing.T) actionFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "ci", "github", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var a actionFile
	if err := yaml.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestActionInputsAndOutputs(t *testing.T) {
	a := readAction(t)
	if a.Runs.Using != "composite" {
		t.Fatalf("using = %s", a.Runs.Using)
	}
	want := map[string]string{"repo-token": "", "token": "", "command": "run", "args": "", "version": "1", "working-directory": ".", "api-url": ""}
	for name, def := range want {
		in, ok := a.Inputs[name]
		if !ok || in.Default != def {
			t.Fatalf("input %s = %+v, want default %q", name, in, def)
		}
	}
	if len(a.Inputs) != len(want) {
		t.Fatalf("inputs = %v (no site, format or since inputs in 1.x)", a.Inputs)
	}
	for name, in := range a.Inputs {
		if strings.Contains(in.Description, "${{") {
			t.Fatalf("input %s: GitHub evaluates expressions in action metadata, so a description must not contain one", name)
		}
	}
	if a.Inputs["token"].Required || a.Inputs["repo-token"].Required {
		t.Fatal("either token may be empty: the CLI decides, and skips forks and Dependabot")
	}
	for _, out := range []string{"run-url", "exit-code"} {
		if !strings.Contains(a.Outputs[out].Value, "steps.gravity.outputs."+out) {
			t.Fatalf("output %s = %+v", out, a.Outputs[out])
		}
	}
	cached := false
	for _, s := range a.Runs.Steps {
		if strings.HasPrefix(s.Uses, "actions/cache@") {
			cached = true
		}
		if s.Uses != "" && strings.HasPrefix(s.Uses, "actions/setup-go") && !strings.Contains(s.Name, "Go") {
			t.Fatalf("setup-go only for source builds: %+v", s)
		}
	}
	if !cached {
		t.Fatal("the binary is cached per resolved version")
	}
}

func gravityStep(t *testing.T) actionStep {
	t.Helper()
	for _, s := range readAction(t).Runs.Steps {
		if s.ID == "gravity" {
			return s
		}
	}
	t.Fatal("no gravity step")
	return actionStep{}
}

func TestActionRunStepPassesArgumentsAndRecordsTheExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash step")
	}
	step := gravityStep(t)
	if step.Shell != "bash" || step.Env["GITHUB_TOKEN"] != "${{ github.token }}" || step.Env["GRAVITY_TOKEN"] != "${{ inputs.token }}" || step.Env["GRAVITY_REPO_TOKEN"] != "${{ inputs.repo-token }}" {
		t.Fatalf("step = %+v", step)
	}
	if _, ok := step.Env["GRAVITY_API_URL"]; ok {
		t.Fatal("GRAVITY_API_URL is exported only when api-url is set, so the manifest wins otherwise")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nprintf '%s|' \"$@\" > \"$FAKE_ARGS\"\nprintf '%s' \"${GRAVITY_API_URL-unset}\" > \"$FAKE_API\"\nexit \"$FAKE_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gravity"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(command, args, apiURL, exit string) (string, string, string, int) {
		out := filepath.Join(dir, "output")
		_ = os.Remove(out)
		cmd := exec.Command("bash", "-e", "-c", step.Run)
		cmd.Env = []string{
			"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "GITHUB_OUTPUT=" + out,
			"INPUT_COMMAND=" + command, "INPUT_ARGS=" + args, "INPUT_API_URL=" + apiURL,
			"FAKE_ARGS=" + filepath.Join(dir, "args"), "FAKE_API=" + filepath.Join(dir, "api"), "FAKE_EXIT=" + exit,
		}
		code := 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		args2, _ := os.ReadFile(filepath.Join(dir, "args"))
		api, _ := os.ReadFile(filepath.Join(dir, "api"))
		outputs, _ := os.ReadFile(out)
		_ = os.Remove(filepath.Join(dir, "args"))
		return string(args2), string(api), strings.TrimSpace(string(outputs)), code
	}
	args, api, outputs, code := run("check", "--pass gate --fail-on '*'", "", "1")
	if args != "check|--pass|gate|--fail-on|'*'|" || api != "unset" || outputs != "exit-code=1" || code != 1 {
		t.Fatalf("args=%q api=%q outputs=%q code=%d", args, api, outputs, code)
	}
	_, api, outputs, code = run("run", "", "https://api.acme.test", "0")
	if api != "https://api.acme.test" || outputs != "exit-code=0" || code != 0 {
		t.Fatalf("api=%q outputs=%q code=%d", api, outputs, code)
	}
	args, _, outputs, code = run("sync", "", "", "0")
	if args != "" || outputs != "exit-code=2" || code != 2 {
		t.Fatalf("removed commands never reach the CLI: args=%q outputs=%q code=%d", args, outputs, code)
	}
}

func TestResolveStepUsesTheInstaller(t *testing.T) {
	for _, s := range readAction(t).Runs.Steps {
		if s.ID == "resolve" {
			if !strings.Contains(s.Run, "install.sh") || s.Env["GRAVITY_RESOLVE_ONLY"] != "1" || s.Env["GRAVITY_VERSION"] != "${{ inputs.version }}" {
				t.Fatalf("resolve = %+v", s)
			}
			return
		}
	}
	t.Fatal("no resolve step")
}
