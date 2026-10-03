package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

type fakeGitLab struct {
	mu     sync.Mutex
	posted []string
	srv    *httptest.Server
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	t.Helper()
	g := &fakeGitLab{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 5})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/77/merge_requests/42/notes":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/77/merge_requests/42/notes":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.posted = append(g.posted, body["body"])
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 11})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func TestCheckOnGitLabPostsANoteAndACodeQualityReport(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	driftFixture(t, h)
	gl := newFakeGitLab(t)
	for k, v := range map[string]string{
		"CI": "true", "GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "merge_request_event", "CI_MERGE_REQUEST_IID": "42",
		"CI_COMMIT_SHA": gitCmd(t, h.dir, "rev-parse", "HEAD"), "CI_MERGE_REQUEST_DIFF_BASE_SHA": gitCmd(t, h.dir, "rev-parse", "HEAD~1"),
		"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "main", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature", "CI_PROJECT_URL": "https://gitlab.com/acme/billing-api",
		"CI_API_V4_URL": gl.srv.URL + "/api/v4", "CI_PROJECT_ID": "77", "GITLAB_TOKEN": "glpat-test",
	} {
		h.env[k] = v
	}
	expectCode(t, h, h.run("check", "--fail-on", "coverage"), 0)
	if len(gl.posted) != 1 || !strings.Contains(gl.posted[0], "### Gravity · doc impact for #42") || !strings.Contains(gl.posted[0], "<!-- gravity:doc-impact repo=github.com/acme/billing-api -->") {
		t.Fatalf("notes = %v\nstderr: %s", gl.posted, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String()+h.stderr.String(), "https://gitlab.com/acme/billing-api/-/merge_requests/42#note_11") {
		t.Fatalf("output = %s", h.stdout.String()+h.stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(h.dir, codeQualityFile))
	if err != nil {
		t.Fatal(err)
	}
	var issues []map[string]any
	if err := json.Unmarshal(data, &issues); err != nil || len(issues) != 1 || issues[0]["check_name"] != "gravity/drift" {
		t.Fatalf("code quality = %s", data)
	}
	if strings.Contains(h.stdout.String(), "::error") {
		t.Fatal("GitLab never receives GitHub workflow commands")
	}
}

func TestCheckWithoutAProviderTokenFallsBackToTheReportFile(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	driftFixture(t, h)
	for k, v := range map[string]string{
		"CI": "true", "BITBUCKET_BUILD_NUMBER": "9", "BITBUCKET_PR_ID": "42", "BITBUCKET_BRANCH": "feature", "BITBUCKET_REPO_FULL_NAME": "acme/billing-api",
		"BITBUCKET_COMMIT": gitCmd(t, h.dir, "rev-parse", "HEAD"), "BITBUCKET_PR_DESTINATION_BRANCH": "main", "BITBUCKET_PR_DESTINATION_COMMIT": gitCmd(t, h.dir, "rev-parse", "--short", "HEAD~1"),
	} {
		h.env[k] = v
	}
	expectCode(t, h, h.run("check", "--comment"), 0)
	if !strings.Contains(h.stderr.String(), "BITBUCKET_ACCESS_TOKEN is not set") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(h.dir, reportFile))
	if err != nil || !strings.Contains(string(body), "**1 finding**") {
		t.Fatalf("report = %s (%v)", body, err)
	}
}

func TestRunOnAzureAnnotatesAndUploadsTheBuildSummary(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	for k, v := range map[string]string{
		"TF_BUILD": "True", "BUILD_REASON": "IndividualCI", "BUILD_SOURCEBRANCH": "refs/heads/main", "BUILD_SOURCEBRANCHNAME": "main",
		"BUILD_SOURCEVERSION": gitCmd(t, h.dir, "rev-parse", "HEAD"),
	} {
		h.env[k] = v
	}
	expectCode(t, h, h.run("run"), 0)
	path := filepath.Join(h.dir, reportFile)
	if !strings.Contains(h.stdout.String(), "##vso[task.uploadsummary]"+path) {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "### Gravity · push main") || !strings.Contains(string(body), "1 page changed: refunds (new)") || !strings.Contains(string(body), "[Review the bundle in Gravity]") {
		t.Fatalf("summary = %s (%v)", body, err)
	}
}

func TestRunOnGitHubPushWritesTheStepSummaryAndTheRunURLOutput(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
	out := filepath.Join(t.TempDir(), "output")
	for k, v := range map[string]string{
		"CI": "true", "GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main", "GITHUB_REF_NAME": "main",
		"GITHUB_SHA": gitCmd(t, h.dir, "rev-parse", "HEAD"), "GITHUB_STEP_SUMMARY": filepath.Join(t.TempDir(), "summary.md"), "GITHUB_OUTPUT": out,
	} {
		h.env[k] = v
	}
	expectCode(t, h, h.run("run"), 0)
	summary, _ := os.ReadFile(h.env["GITHUB_STEP_SUMMARY"])
	if !strings.Contains(string(summary), "### Gravity · push main") {
		t.Fatalf("summary = %s", summary)
	}
	outputs, _ := os.ReadFile(out)
	if strings.TrimSpace(string(outputs)) != "run-url=https://app.gravitydocs.io/app/repos/runs/prun_1" {
		t.Fatalf("outputs = %q", outputs)
	}
	if _, err := os.Stat(filepath.Join(h.dir, reportFile)); err == nil {
		t.Fatal("a push run on GitHub writes no report file")
	}
}

func TestCheckAnnotateOptionTurnsAnnotationsOff(t *testing.T) {
	gate := &passes.Report{}
	gate.Findings = []api.Finding{{Severity: api.SeverityError, Code: passes.CodeDrift, Title: "drift"}}
	other := &passes.Report{}
	other.Findings = []api.Finding{{Severity: api.SeverityError, Code: passes.CodeClaimContradicted, Title: "claim"}}
	res := &engine.Result{
		Plan:   &api.Plan{Passes: []api.PlanPass{{Name: "gate", Options: map[string]any{"annotate": false}}, {Name: "other", Options: map[string]any{}}}},
		Passes: []engine.PassResult{{Name: "gate", Report: gate}, {Name: "other", Report: other}},
	}
	got := annotatedFindings(res)
	if len(got) != 1 || got[0].Title != "claim" {
		t.Fatalf("annotate: false drops that pass's annotations only: %+v", got)
	}
}
