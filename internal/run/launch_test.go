package run_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func guidesPass(name string) api.PlanPass {
	pp := refPass(name, "")
	pp.Kind = config.KindGuides
	return pp
}

func terminalOf(body map[string]any) string {
	for _, raw := range body["tools"].([]any) {
		n := raw.(map[string]any)["name"].(string)
		if strings.HasPrefix(n, "finish_") || strings.HasPrefix(n, "submit_") {
			return n
		}
	}
	return ""
}

func turnsOf(body map[string]any) int {
	n := 0
	for _, m := range body["messages"].([]any) {
		if m.(map[string]any)["role"] == "assistant" {
			n++
		}
	}
	return n
}

func toolReply(name string, input any) map[string]any {
	return map[string]any{"stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}, "content": []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": name, "input": input}}}
}

func indexOf(p *platform, method, suffix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, c := range p.calls {
		if c.Method == method && strings.HasSuffix(c.Path, suffix) {
			return i
		}
	}
	return -1
}

func TestBundlesAreFetchedForModelPassesBeforeIngest(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", ""), guidesPass("guides")}
	p.llm = func(body map[string]any) any {
		return toolReply("finish_plan", map[string]any{"summary": "nothing to change"})
	}
	e := newEnv(t, p, r, manifest(t, ""))
	opts := pushOpts()
	opts.Debug = true
	res, err := run.Execute(context.Background(), e.Env, opts)
	if err != nil {
		t.Fatal(err)
	}
	fetches := p.find("GET", "/api/llm/v1/instructions")
	if len(fetches) != 1 || fetches[0].Query["runPassId"][0] != "ppr_2" {
		t.Fatalf("bundle fetches = %+v", fetches)
	}
	if b, i := indexOf(p, "GET", "/api/llm/v1/instructions"), indexOf(p, "POST", "/products/self/inventory"); b < 0 || i < 0 || b > i {
		t.Fatalf("the bundle is fetched before ingest: bundle %d ingest %d", b, i)
	}
	if !strings.Contains(strings.Join(e.log.lines, "\n"), "guides: Claude Test · recommended · English (en) · instructions test") {
		t.Fatalf("log = %v", e.log.lines)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".gravity", "runs", "prun_1", "ppr_2.instructions.json")); err != nil {
		t.Fatalf("--debug stores the bundle: %v", err)
	}
	reports := p.find("POST", "/passes/ppr_2")
	gen, _ := reports[len(reports)-1].Body["generation"].(map[string]any)
	if gen["model"] != "anthropic/claude-test" || gen["provider"] != "anthropic" || gen["instructionsVersion"] != "sha256:test" {
		t.Fatalf("generation = %+v", reports[len(reports)-1].Body)
	}
	if ref := p.find("POST", "/passes/ppr_1"); ref[len(ref)-1].Body["generation"] != nil {
		t.Fatalf("a pass without a model reports no generation: %+v", ref[len(ref)-1].Body)
	}
	if res.Passes[1].Status != api.StatusSucceeded {
		t.Fatalf("guides = %+v", res.Passes[1])
	}
}

func TestRunHalts(t *testing.T) {
	cases := []struct {
		name     string
		route    string
		code     string
		details  map[string]any
		summary  string
		typed    func(error) bool
		status   int
		passes   int
		ingested bool
	}{
		{name: "unusable model at bundle fetch", route: "GET /api/llm/v1/instructions", code: api.CodeModelUnavailable, status: http.StatusConflict},
		{name: "budget exceeded mid pass", route: "POST /api/llm/v1/messages", code: api.CodeBudgetExceeded, details: map[string]any{"scope": "run", "limitUsd": 5, "spentUsd": 5.2}, summary: "This run's AI budget is used up: $5.20 spent of a $5.00 limit.", typed: api.IsBudgetError, status: http.StatusPaymentRequired, passes: 2, ingested: true},
		{name: "budget exceeded at the bundle fetch", route: "GET /api/llm/v1/instructions", code: api.CodeBudgetExceeded, details: map[string]any{"scope": "month", "resetsAt": "2026-11-01"}, summary: "The monthly AI budget is used up; it resets on 2026-11-01.", typed: api.IsBudgetError, status: http.StatusPaymentRequired},
		{name: "ai disabled mid pass", route: "POST /api/llm/v1/messages", code: api.CodeAIDisabled, details: map[string]any{}, summary: "AI is turned off for this organization.", typed: api.IsLicenseError, status: http.StatusForbidden, passes: 2, ingested: true},
		{name: "ai disabled at the bundle fetch", route: "GET /api/llm/v1/instructions", code: api.CodeAIDisabled, details: map[string]any{}, summary: "AI is turned off for this organization.", typed: api.IsLicenseError, status: http.StatusForbidden},
		{name: "no provider key mid pass", route: "POST /api/llm/v1/messages", code: api.CodeNoProviderKey, status: http.StatusPaymentRequired, passes: 2, ingested: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.commit("init", map[string]string{"api/openapi.yaml": spec})
			p := newPlatform(t)
			p.plan.Passes = []api.PlanPass{guidesPass("guides"), refPass("developer-api", "")}
			p.routes[tc.route] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				details := tc.details
				if details == nil {
					details = map[string]any{"reason": "no_key", "requested": "claude-x"}
				}
				apiErr(w, tc.status, tc.code, map[string]any{"details": details})
			}
			res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
			if !errors.Is(err, run.ErrHalted) || !api.HasCode(err, tc.code) {
				t.Fatalf("err = %v", err)
			}
			if tc.typed != nil && (!tc.typed(err) || !strings.Contains(err.Error(), tc.summary)) {
				t.Fatalf("the halt carries the typed error: %T %v", err, err)
			}
			wantSummary := tc.summary
			if wantSummary == "" {
				wantSummary = tc.code
			}
			fin := p.find("POST", "/finish")
			if len(fin) != 1 || fin[0].Body["status"] != api.StatusFailed || !strings.Contains(fin[0].Body["report"].(map[string]any)["summary"].(string), wantSummary) {
				t.Fatalf("finish = %+v", fin)
			}
			if got := len(p.find("POST", "/products/self/inventory")) > 0; got != tc.ingested {
				t.Fatalf("ingested = %v", got)
			}
			if len(res.Passes) != tc.passes {
				t.Fatalf("passes = %+v", res.Passes)
			}
			if tc.passes == 0 {
				if len(p.find("POST", "/passes/ppr_1")) != 0 {
					t.Fatal("no pass starts after a bundle refusal")
				}
				return
			}
			if res.Passes[0].Status != api.StatusFailed || res.Passes[1].Status != api.StatusFailed || !strings.Contains(res.Passes[1].Error, "run halted") {
				t.Fatalf("passes = %+v", res.Passes)
			}
			reports := p.find("POST", "/passes/ppr_1")
			if last := reports[len(reports)-1].Body; last["status"] != api.StatusFailed {
				t.Fatalf("the halting pass is reported failed: %+v", last)
			}
			if len(p.find("POST", "/passes/ppr_2")) != 0 {
				t.Fatal("no new pass starts once the run is halted")
			}
		})
	}
}

func TestMapUnitsIngestsOnlyACompleteList(t *testing.T) {
	cases := []struct {
		name     string
		complete bool
		forced   bool
		mapped   bool
		full     bool
	}{
		{name: "complete list", complete: true, mapped: true, full: true},
		{name: "incomplete list", complete: false},
		{name: "forced at the turn cap", complete: true, forced: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.commit("init", map[string]string{"api/openapi.yaml": spec, "src/cmd/refund.ts": "export function refund() {}\n"})
			p := newPlatform(t)
			p.plan.Passes = []api.PlanPass{guidesPass("guides")}
			p.plan.Inventory.Units = []api.Unit{{Key: "feature:old", Kind: "feature", Title: "Old"}}
			if tc.forced {
				p.routes["GET /api/llm/v1/instructions"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
					b := testBundle()
					b["harness"] = map[string]any{"maxTurns": 4, "maxToolCalls": 200, "stepTokens": 4000, "maxContinueNudges": 4}
					reply(w, 200, b)
				}
			}
			var kickoff string
			p.llm = func(body map[string]any) any {
				if terminalOf(body) != "finish_units" {
					return toolReply("finish_plan", map[string]any{"summary": "nothing"})
				}
				if turnsOf(body) == 0 {
					first := body["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
					kickoff, _ = first["text"].(string)
					return toolReply("add_units", map[string]any{"units": []any{map[string]any{"key": "feature:refunds", "kind": "feature", "title": "Refunds", "sourceRefs": []any{"src/cmd/refund.ts"}}}})
				}
				return toolReply("finish_units", map[string]any{"complete": tc.complete})
			}
			if _, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "  entrypoints: [src/cmd]\n")).Env, pushOpts()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(kickoff, "<existing_units>\n- feature:old (feature) Old\n</existing_units>") || !strings.Contains(kickoff, "<entrypoint_files>\nsrc/cmd/refund.ts\n</entrypoint_files>") {
				t.Fatalf("kickoff = %s", kickoff)
			}
			ingest := p.find("POST", "/products/self/inventory")
			if len(ingest) == 0 {
				t.Fatal("no ingest")
			}
			body := ingest[0].Body
			data, _ := json.Marshal(body["units"])
			if got := strings.Contains(string(data), "feature:refunds"); got != tc.mapped || body["complete"] != tc.full {
				t.Fatalf("mapped=%v complete=%v units=%s", got, body["complete"], data)
			}
		})
	}
}
