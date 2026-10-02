package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestStatusRendersOneView(t *testing.T) {
	h := newHarness(t)
	writeProfiles(t, h.config, fmt.Sprintf("version: 2\ncurrent: acme\nprofiles:\n  acme:\n    apiUrl: %s\n    org: acme\n    token: gr_user_abc\n    tokenKind: user\n    user: dave@acme.io\n", h.platform.srv.URL))
	h.write(".gravity.yaml", "version: 2\nproduct: acme-platform\ncode:\n  openapi: [api/openapi.yaml]\n")
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `"expiresAt":"2026-12-30T12:00:00Z"`, `"expiresAt":"2026-10-05T12:00:00Z"`, 1))
	plan := strings.Replace(planBody, `"llm":{"configured":true}`, `"llm":{"configured":false}`, 1)
	plan = strings.Replace(plan, `"modules":{"cli":true}`, `"modules":{"cli":true,"memory":false}`, 1)
	plan = strings.Replace(plan, `{"id":"rp_3","name":"changelog","kind":"changelog"`, `{"id":"rp_4","name":"memory","kind":"nucleus","source":"app","locked":false,"enabled":true,"applies":false,"skipReason":"scope_missing","missingScopes":["nucleus:write"],"triggers":["push"],"target":{"status":"none","ref":""},"scope":{},"instructions":{"hash":"sha256:02","layers":[]}},{"id":"rp_3","name":"changelog","kind":"changelog"`, 1)
	h.platform.json("GET /api/v1/repos/self/plan", 200, plan)
	expectCode(t, h, h.run("status"), 0)
	h.golden("status.txt")
	reqs := h.platform.find("GET", "/api/v1/repos/self/plan")
	if len(reqs) != 1 || reqs[0].Query["mode"][0] != "dry" || reqs[0].Query["trigger"][0] != "push" || reqs[0].Query["branch"][0] != "main" || !strings.HasPrefix(reqs[0].Query["manifestHash"][0], "sha256:") {
		t.Fatalf("plan query = %+v", reqs)
	}
	expectCode(t, h, h.run("status", "--json"), 0)
	data := h.envelope()["data"].(map[string]any)
	var codes []string
	for _, w := range data["capabilityWarnings"].([]any) {
		codes = append(codes, w.(map[string]any)["code"].(string))
	}
	if strings.Join(codes, ",") != "token_expiring,module_disabled,scope_missing,llm_not_configured" {
		t.Fatalf("capability warnings = %v", codes)
	}
}

func TestStatusSurvivesAMissingPlan(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/repos/self/plan", 500, `{"error":{"code":"internal","message":"boom"}}`)
	expectCode(t, h, h.run("status"), 0)
	if !strings.Contains(h.stdout.String(), "developer-api") {
		t.Fatalf("out = %s", h.stdout.String())
	}
}

func TestStatusReportsAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_user_abc"
	h.platform.json("GET /api/v1/whoami", 200, strings.Replace(whoamiUser, `"expiresAt":"2026-12-30T12:00:00Z"`, `"expiresAt":"2026-09-30T12:00:00Z"`, 1))
	expectCode(t, h, h.run("status", "--json"), 0)
	warnings := h.envelope()["data"].(map[string]any)["capabilityWarnings"].([]any)
	if len(warnings) == 0 || warnings[0].(map[string]any)["code"] != "token_expired" {
		t.Fatalf("capability warnings = %v", warnings)
	}
}
