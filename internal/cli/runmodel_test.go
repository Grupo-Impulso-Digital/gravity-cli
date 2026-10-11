package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

func TestPlanViewShowsEachAIPassModel(t *testing.T) {
	en := &api.Language{Code: "en", Name: "English"}
	cases := []struct {
		name     string
		model    *api.PlanModel
		run      bool
		want     []string
		warnings []string
	}{
		{name: "recommended", model: &api.PlanModel{ID: "anthropic/claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Quality: api.QualityRecommended}, run: true, want: []string{"guides model: Claude Sonnet 4.6 - recommended - English (en)"}},
		{name: "unlabeled untested", model: &api.PlanModel{ID: "origin/auto"}, run: true, want: []string{"guides model: origin/auto - untested - English (en)"}},
		{name: "not recommended", model: &api.PlanModel{ID: "openai/gpt-4o", Label: "GPT-4o", Quality: api.QualityNotRecommended}, run: true, want: []string{"guides model: GPT-4o - not_recommended"}, warnings: []string{"model_not_recommended"}},
		{name: "fallback", model: &api.PlanModel{ID: "anthropic/claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Quality: api.QualitySupported, Fallback: &api.ModelFallback{From: "gpt-5", Reason: "no key"}}, run: true, want: []string{"guides model: Claude Sonnet 4.6 - supported"}, warnings: []string{"model_fallback"}},
		{name: "skipped pass", model: &api.PlanModel{ID: "openai/gpt-4o", Quality: api.QualityNotRecommended}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := ui.New(&out, &out, ui.Options{})
			view := engine.PlanView{Mode: api.ModeDry, Trigger: "manual", Branch: "main", Passes: []engine.PlannedPass{{Name: "guides", Kind: "guides", Target: "docs/guides", Run: tc.run, AI: true, Model: tc.model, Language: en}}}
			renderPlanView(p, view)
			text := out.String()
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Fatalf("missing %q in\n%s", w, text)
				}
			}
			if !tc.run && strings.Contains(text, "model:") {
				t.Fatalf("a skipped pass shows no model:\n%s", text)
			}
			var codes []string
			for _, w := range p.Warnings() {
				codes = append(codes, w.Code)
			}
			if strings.Join(codes, ",") != strings.Join(tc.warnings, ",") {
				t.Fatalf("warnings = %v", codes)
			}
		})
	}
}

func guidesPlanPass(model map[string]any) map[string]any {
	pp := referencePlanPass("guides")
	pp["name"], pp["id"] = "user-guides", "rp_2"
	pp["options"] = map[string]any{}
	pp["model"] = model
	pp["language"] = map[string]any{"code": "en", "name": "English"}
	return pp
}

func TestRunModelFlags(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		code  int
		err   string
		model string
	}{
		{name: "model and output limit", args: []string{"run", "--dry-run", "--json", "--model", "openai/gpt-4o", "--max-output", "2048"}, model: "openai/gpt-4o"},
		{name: "configured model", args: []string{"run", "--dry-run", "--json"}},
		{name: "negative output limit", args: []string{"run", "--dry-run", "--json", "--max-output", "-1"}, code: 2, err: "--max-output"},
		{name: "send with a model", args: []string{"run", "--send", "latest", "--model", "openai/gpt-4o", "--json"}, code: 2, err: "do not apply to --send"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
			h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
			h.commitSpec(t)
			h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference")))
			expectCode(t, h, h.run(tc.args...), tc.code)
			if tc.code != 0 {
				if e := h.envelope()["error"].(map[string]any); !strings.Contains(e["message"].(string), tc.err) {
					t.Fatalf("error = %v", e)
				}
				if len(h.platform.find("POST", "/api/v1/runs")) != 0 {
					t.Fatal("no run starts")
				}
				return
			}
			for _, c := range h.platform.find("GET", "/api/v1/repos/self/plan") {
				if got := strings.Join(c.Query["model"], ","); got != tc.model {
					t.Fatalf("plan model = %q", got)
				}
			}
			start := h.platform.find("POST", "/api/v1/runs")[0].Body
			if got, _ := start["model"].(string); got != tc.model {
				t.Fatalf("start = %v", start)
			}
		})
	}
}

func TestRunStopsBeforeTheConfirmWhenAModelCannotRun(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	model := map[string]any{"id": "openai/gpt-4o", "label": "GPT-4o", "quality": "untested", "limits": map[string]any{"maxOutputTokens": 16384, "contextWindow": 128000, "known": true}, "unavailable": map[string]any{"reason": "no_key"}}
	h.pipelineRoutes(t, pipelinePlan(t, referencePlanPass("reference"), guidesPlanPass(model)))
	expectCode(t, h, h.run("run", "--dry-run", "--json", "--model", "openai/gpt-4o"), 2)
	e := h.envelope()["error"].(map[string]any)
	if e["code"] != api.CodeModelUnavailable || !strings.Contains(e["message"].(string), "pass user-guides: model GPT-4o has no provider key in Gravity") {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.find("POST", "/api/v1/runs")) != 0 {
		t.Fatal("no run starts when a model cannot run")
	}
}

func TestShowCarriesThePlanModel(t *testing.T) {
	h := guidedHarness(t)
	h.platform.json("GET /api/v1/repos/self/plan", 200, strings.Replace(guidedPlan(), `"model":"claude-sonnet"}}]`, `"model":"claude-sonnet"},"model":{"id":"anthropic/claude-sonnet-4-6","label":"Claude Sonnet 4.6","quality":"recommended","limits":{"maxOutputTokens":64000,"contextWindow":200000,"known":true}},"language":{"code":"fr","name":"French"}}]`, 1))
	expectCode(t, h, h.run("show", "--json", "--model", "claude-sonnet-4-6"), 0)
	plans := h.platform.find("GET", "/api/v1/repos/self/plan")
	if got := strings.Join(plans[len(plans)-1].Query["model"], ","); got != "claude-sonnet-4-6" {
		t.Fatalf("plan model = %q", got)
	}
	data := h.envelope()["data"].(map[string]any)
	for _, raw := range data["passes"].([]any) {
		sp := raw.(map[string]any)
		switch sp["name"] {
		case "user-guides":
			m, _ := sp["model"].(map[string]any)
			l, _ := sp["language"].(map[string]any)
			if m["label"] != "Claude Sonnet 4.6" || m["quality"] != "recommended" || l["code"] != "fr" {
				t.Fatalf("pass = %v", sp)
			}
		case "docs":
			if sp["model"] != nil {
				t.Fatalf("a verbatim pass has no model: %v", sp)
			}
		}
	}
	expectCode(t, h, h.run("show"), 0)
	if !strings.Contains(h.stdout.String(), "user-guides model: Claude Sonnet 4.6 - recommended - French (fr)") {
		t.Fatalf("stdout = %s", h.stdout.String())
	}
}

func TestRunOnAServerWithoutInstructionsBundles(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	pp := referencePlanPass("reference")
	pp["options"] = map[string]any{"prose": true, "claims": false}
	h.pipelineRoutes(t, pipelinePlan(t, pp))
	h.platform.json("GET /api/llm/v1/instructions", 404, `{}`)
	expectCode(t, h, h.run("run", "--dry-run", "--json"), 2)
	e := h.envelope()["error"].(map[string]any)
	if e["code"] != "server_too_old" || !strings.Contains(e["message"].(string), "upgrade the server or use gravity 1.1") {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.find("POST", "/api/llm/v1/messages")) != 0 {
		t.Fatal("no model call without a bundle")
	}
}
