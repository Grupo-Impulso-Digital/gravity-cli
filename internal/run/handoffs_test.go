package run_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

const specGet = `openapi: 3.0.0
info: {title: Billing, version: 1.0.0}
paths:
  /v1/refunds:
    get:
      summary: List refunds
      responses: {'200': {description: ok}}
`

func TestPullRequestRunsReportExpectedHandoffs(t *testing.T) {
	r := newRepo(t)
	base := r.commit("init", map[string]string{"api/openapi.yaml": specGet})
	r.git("update-ref", "refs/remotes/origin/main", base)
	r.git("checkout", "-q", "-b", "feature")
	r.commit("feat: create refunds here, list moves out", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	yes, no := true, false
	me := api.RepoRef{Name: "billing-api", RemoteKey: "github.com/acme/billing-api"}
	p.plan.Inventory.Units = []api.Unit{
		{Key: "api:post:/v1/refunds", Kind: "api", Contributors: []api.Contributor{{Repo: api.RepoRef{Name: "gateway", RemoteKey: "github.com/acme/gateway"}, Role: api.RoleImplements, Active: &no}}},
		{Key: "api:get:/v1/refunds", Kind: "api", Contributors: []api.Contributor{{Repo: me, Role: api.RoleImplements, Active: &yes, SourceRefs: []string{"api/openapi.yaml#/paths/~1v1~1refunds/get"}}, {Repo: api.RepoRef{Name: "refunds-svc", RemoteKey: "github.com/acme/refunds-svc"}, Role: api.RoleImplements, Active: &yes}}},
	}
	opts := run.Options{Trigger: "pr", Branch: "feature", PR: &api.PRInfo{Number: 7, TargetBranch: "main"}, Origin: "ci", ConnectContext: "pr"}
	res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []run.Handoff{
		{UnitKey: "api:get:/v1/refunds", Role: api.RoleImplements, From: "billing-api", To: "refunds-svc", Status: run.HandoffExpected},
		{UnitKey: "api:post:/v1/refunds", Role: api.RoleImplements, From: "gateway", To: "billing-api", Status: run.HandoffExpected},
	}
	if len(res.Handoffs) != len(want) {
		t.Fatalf("handoffs = %+v", res.Handoffs)
	}
	for i, h := range want {
		if res.Handoffs[i] != h {
			t.Fatalf("handoff %d = %+v, want %+v", i, res.Handoffs[i], h)
		}
	}
}

func TestWriteRunsReportTheHandoffsTheIngestDetected(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	p.routes["POST /api/v1/products/self/inventory"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, 200, map[string]any{"counts": map[string]int{"created": 1}, "handoffs": []any{map[string]any{"id": "ho_1", "unitKey": "api:post:/v1/refunds", "role": "implements", "from": "github.com/acme/gateway", "to": "github.com/acme/billing-api"}}, "conflicts": []any{}})
	}
	res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Handoffs) != 1 || res.Handoffs[0].Status != run.HandoffDetected || res.Handoffs[0].From != "github.com/acme/gateway" {
		t.Fatalf("handoffs = %+v", res.Handoffs)
	}
}

func TestWriteRunsKeepAndRecordDocumentsRoles(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("docs: explain refunds", map[string]string{"api/openapi.yaml": spec, "src/refunds.ts": "x"})
	p := newPlatform(t)
	guides := refPass("product-guides", wm)
	guides.Kind = "guides"
	guides.Scope = api.PassScope{}
	p.plan.Passes = []api.PlanPass{guides}
	yes := true
	me := api.RepoRef{Name: "billing-api", RemoteKey: "github.com/acme/billing-api"}
	gw := api.RepoRef{Name: "gateway", RemoteKey: "github.com/acme/gateway"}
	p.plan.Inventory.Units = []api.Unit{
		{Key: "feature:handbook", Kind: "feature", Title: "Handbook", Contributors: []api.Contributor{{Repo: me, Role: api.RoleDocuments, Active: &yes, SourceRefs: []string{"pass:handbook"}}}},
		{Key: "feature:routing", Kind: "feature", Title: "Routing", Contributors: []api.Contributor{{Repo: gw, Role: api.RoleImplements, Active: &yes}}},
	}
	page := map[string]any{"page": map[string]any{"id": "pg_1", "slug": "routing", "title": "Routing"}, "blocks": []any{}}
	p.routes["GET /api/v1/content/spaces/sp_1/tree"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, 200, map[string]any{"space": map[string]any{"id": "sp_1", "slug": "api"}, "collections": []any{}, "pages": []any{map[string]any{"id": "pg_1", "slug": "routing", "title": "Routing", "collectionPath": []any{}}}, "nextCursor": nil})
	}
	p.routes["POST /api/v1/content/search"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, 200, map[string]any{"hits": []any{map[string]any{"pageId": "pg_1", "pageSlug": "routing", "title": "Routing"}}})
	}
	p.routes["GET /api/v1/content/pages/pg_1"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, 200, page)
	}
	p.llm = func(body map[string]any) any {
		tools := body["tools"].([]any)
		name := ""
		for _, raw := range tools {
			n := raw.(map[string]any)["name"].(string)
			if strings.HasPrefix(n, "submit_") {
				name = n
			}
		}
		var input any
		switch name {
		case "submit_page_plan":
			input = map[string]any{"actions": []any{map[string]any{"action": "update", "pageId": "pg_1", "slug": "routing", "reason": "routing explained", "units": []any{"feature:routing"}}}}
		default:
			input = map[string]any{"summary": "Explains routing", "upserts": []any{map[string]any{"key": "guide:routing:intro", "type": "prose", "content": map[string]any{"text": "Routes go through the gateway."}, "units": []any{"feature:routing"}, "rationale": map[string]any{"summary": "docs"}}}}
		}
		return map[string]any{"stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}, "content": []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": name, "input": input}}}
	}
	res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passes[0].Status != api.StatusSucceeded {
		t.Fatalf("pass = %+v", res.Passes[0])
	}
	calls := p.find("POST", "/products/self/inventory")
	if len(calls) != 2 {
		t.Fatalf("ingest calls = %d", len(calls))
	}
	var kept map[string]any
	for _, raw := range calls[0].Body["units"].([]any) {
		if u := raw.(map[string]any); u["key"] == "feature:handbook" {
			kept = u
		}
	}
	if calls[0].Body["complete"] != true || kept == nil || fmt.Sprint(kept["roles"]) != "[documents]" {
		t.Fatalf("a complete ingest keeps the documents rows: %+v", calls[0].Body["units"])
	}
	docs := calls[1].Body
	unit := docs["units"].([]any)[0].(map[string]any)
	if docs["complete"] != false || unit["key"] != "feature:routing" || fmt.Sprint(unit["roles"]) != "[documents]" || fmt.Sprint(unit["sourceRefs"]) != "[pass:product-guides]" {
		t.Fatalf("documents ingest = %+v", docs)
	}
}
