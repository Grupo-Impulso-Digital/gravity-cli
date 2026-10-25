package cli

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const llmConfigKeyed = `"provider":"anthropic","model":"claude-sonnet-4-5","tone":"neutral","hasKey":true,"availableProviders":["anthropic","openai"],"contextInjection":false,"serverTools":[]`

const llmSpend = `"spend":{"monthStart":"2026-10-01T00:00:00.000Z","resetsAt":"2026-11-01T00:00:00.000Z","daysElapsed":24.5,"daysInMonth":31,
"org":{"feature":"all","spentUsd":42.1,"projectedUsd":53.3,"maxUsdPerRun":5,"maxUsdPerMonth":200},
"features":[{"feature":"ask","spentUsd":3.1,"projectedUsd":3.9,"maxUsdPerRun":null,"maxUsdPerMonth":20},
{"feature":"doc-agent","spentUsd":0,"projectedUsd":0,"maxUsdPerRun":null,"maxUsdPerMonth":null},
{"feature":"cli-gateway","spentUsd":30,"projectedUsd":38,"maxUsdPerRun":2.5,"maxUsdPerMonth":null}],"unpricedCalls":0}`

func doctorHarness(t *testing.T, config string) *harness {
	t.Helper()
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/llm/v1/config", 200, "{"+config+"}")
	return h
}

func TestDoctorShowsTheSpendCaps(t *testing.T) {
	h := doctorHarness(t, llmConfigKeyed+","+llmSpend)
	expectCode(t, h, h.run("doctor"), 0)
	h.golden("doctor.txt")
	if reqs := h.platform.find("GET", "/api/llm/v1/config"); len(reqs) != 1 || reqs[0].Token != "gr_user_abc" {
		t.Fatalf("requests = %+v", reqs)
	}

	expectCode(t, h, h.run("doctor", "--json"), 0)
	env := h.envelope()
	data := env["data"].(map[string]any)
	ai := data["ai"].(map[string]any)
	spend := data["spend"].(map[string]any)
	org := spend["org"].(map[string]any)
	if env["ok"] != true || env["command"] != "doctor" || ai["hasKey"] != true || ai["model"] != "claude-sonnet-4-5" || spend["resetsAt"] != "2026-11-01T00:00:00Z" ||
		org["maxUsdPerRun"] != float64(5) || org["maxUsdPerMonth"] != float64(200) || org["spentUsd"] != 42.1 || len(spend["features"].([]any)) != 3 {
		t.Fatalf("envelope = %v", env)
	}
	if providers := ai["connectedProviders"].([]any); len(providers) != 2 || providers[0] != "anthropic" {
		t.Fatalf("providers = %v", providers)
	}
}

func TestDoctorWhenTheServerReportsNoCaps(t *testing.T) {
	h := doctorHarness(t, llmConfigKeyed)
	expectCode(t, h, h.run("doctor"), 0)
	if out := h.stdout.String(); !strings.Contains(out, "spend caps: this Gravity server does not report them") || strings.Contains(out, "SCOPE") {
		t.Fatalf("out = %s", out)
	}
	expectCode(t, h, h.run("doctor", "--json"), 0)
	if data := h.envelope()["data"].(map[string]any); data["spend"] != nil {
		t.Fatalf("spend = %v", data["spend"])
	}
}

func TestDoctorWhenNoCapIsSet(t *testing.T) {
	h := doctorHarness(t, llmConfigKeyed+`,"spend":{"resetsAt":"2026-11-01T00:00:00Z","org":{"feature":"all","spentUsd":12.5,"maxUsdPerRun":null,"maxUsdPerMonth":null},"features":[{"feature":"ask","spentUsd":12.5,"maxUsdPerRun":null,"maxUsdPerMonth":null}]}`)
	expectCode(t, h, h.run("doctor"), 0)
	if out := h.stdout.String(); !strings.Contains(out, "no spend caps are set ($12.50 spent this month)") || strings.Contains(out, "SCOPE") {
		t.Fatalf("out = %s", out)
	}
}

func TestDoctorWithoutAProviderExitsThree(t *testing.T) {
	h := doctorHarness(t, `"provider":"anthropic","model":"claude-sonnet-4-5","tone":"neutral","hasKey":false,"availableProviders":["origin"],`+llmSpend)
	expectCode(t, h, h.run("doctor"), CodeLicense)
	want := "gravity: No AI provider is connected for this organization. An organization admin can connect Origin or add a provider key in Gravity > Settings > AI."
	if out := h.stdout.String(); !strings.Contains(out, "x no AI provider is connected") || !strings.Contains(out, "Organization") || strings.Contains(out, "Default model") {
		t.Fatalf("out = %s", out)
	}
	if !strings.Contains(h.stderr.String(), want) {
		t.Fatalf("stderr = %s", h.stderr.String())
	}

	expectCode(t, h, h.run("doctor", "--json"), CodeLicense)
	env := h.envelope()
	e := env["error"].(map[string]any)
	data := env["data"].(map[string]any)
	ai := data["ai"].(map[string]any)
	if env["ok"] != false || e["code"] != api.CodeNoProviderKey || e["exitCode"] != float64(CodeLicense) || ai["hasKey"] != false || len(ai["connectedProviders"].([]any)) != 0 || data["spend"] == nil {
		t.Fatalf("envelope = %v", env)
	}
}

func TestDoctorNeedsAToken(t *testing.T) {
	h := newHarness(t)
	expectCode(t, h, h.run("doctor", "--json"), CodeCredentials)
	if e := h.envelope()["error"].(map[string]any); e["code"] != "token_missing" {
		t.Fatalf("error = %v", e)
	}
	if len(h.platform.requests) != 0 {
		t.Fatalf("requests = %+v", h.platform.requests)
	}
}

func TestDoctorRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		code   int
		errors string
	}{
		{"license refusal", 403, `{"error":{"code":"module_disabled","message":"off","module":"cli"}}`, CodeLicense, "module_disabled"},
		{"rejected token", 401, `{"error":{"code":"unauthorized","message":"bad token"}}`, CodeCredentials, "unauthorized"},
		{"missing scope", 403, `{"error":{"code":"scope_missing","message":"Scope missing","details":[{"code":"scope","message":"llm"}]}}`, CodeError, "scope_missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
			h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
			h.platform.json("GET /api/llm/v1/config", tc.status, tc.body)
			expectCode(t, h, h.run("doctor", "--json"), tc.code)
			if e := h.envelope()["error"].(map[string]any); e["code"] != tc.errors || e["exitCode"] != float64(tc.code) {
				t.Fatalf("error = %v", e)
			}
		})
	}
}
