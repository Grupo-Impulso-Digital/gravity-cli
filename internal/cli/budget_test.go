package cli

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const spendCapAction = "Ask an organization admin to raise the cap in Gravity"

func spendCapHarness(t *testing.T, details string) *harness {
	t.Helper()
	h := newHarness(t)
	h.env["GRAVITY_TOKEN"] = "gr_repo_ci"
	h.platform.json("GET /api/v1/whoami", 200, whoamiRepo)
	h.commitSpec(t)
	pp := referencePlanPass("reference")
	pp["options"] = map[string]any{"prose": true, "claims": false}
	h.pipelineRoutes(t, pipelinePlan(t, pp))
	h.platform.json("GET /api/llm/v1/instructions", 402, `{"error":{"code":"budget_exceeded","message":"Cap reached.","details":`+details+`}}`)
	return h
}

func TestRunHaltedByASpendCapExitsFive(t *testing.T) {
	h := spendCapHarness(t, `{"scope":"month","limitUsd":50,"spentUsd":50.4,"resetsAt":"2026-11-01T00:00:00Z"}`)
	expectCode(t, h, h.run("run", "--dry-run", "--json"), CodeBudget)
	env := h.envelope()
	e := env["error"].(map[string]any)
	want := "The monthly AI budget is used up: $50.40 spent of a $50.00 limit; it resets on 2026-11-01. " + spendCapAction + " › AI usage."
	if env["ok"] != false || e["code"] != api.CodeBudgetExceeded || e["exitCode"] != float64(CodeBudget) || e["message"] != want {
		t.Fatalf("error = %v", e)
	}
	details := e["details"].(map[string]any)
	if details["scope"] != "month" || details["limitUsd"] != float64(50) || details["spentUsd"] != 50.4 || details["resetsAt"] != "2026-11-01T00:00:00Z" || details["feature"] != nil {
		t.Fatalf("details = %v", details)
	}
	finish := h.platform.find("POST", "/api/v1/runs/prun_1/finish")
	if len(finish) != 1 || finish[0].Body["status"] != api.StatusFailed {
		t.Fatalf("the halted run is finished as failed: %+v", finish)
	}
	if len(h.platform.find("POST", "/api/llm/v1/messages")) != 0 {
		t.Fatal("no model call once the cap is reached")
	}
}

func TestRunHaltedByASpendCapPrintsAClearMessage(t *testing.T) {
	h := spendCapHarness(t, `{"scope":"run","limitUsd":5,"spentUsd":5.2}`)
	expectCode(t, h, h.run("run", "--dry-run"), CodeBudget)
	want := "gravity: This run's AI budget is used up: $5.20 spent of a $5.00 limit. " + spendCapAction + " > AI usage."
	if !strings.Contains(h.stderr.String(), want) || strings.Contains(h.stderr.String(), "run halted") {
		t.Fatalf("stderr = %s", h.stderr.String())
	}
}

func TestRunHaltedByASpendCapWithoutDetails(t *testing.T) {
	h := spendCapHarness(t, `null`)
	expectCode(t, h, h.run("run", "--dry-run", "--json"), CodeBudget)
	e := h.envelope()["error"].(map[string]any)
	if e["code"] != api.CodeBudgetExceeded || e["message"] != "The AI budget is used up. "+spendCapAction+" › AI usage." {
		t.Fatalf("error = %v", e)
	}
	if _, ok := e["details"].(map[string]any); !ok {
		t.Fatalf("an empty details object is still carried: %v", e)
	}
}
