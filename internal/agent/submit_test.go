package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
)

func TestParseDocPlan(t *testing.T) {
	raw := json.RawMessage(`{"pages":[
		{"space":"docs","slug":"overview","title":"Overview","summary":"What it is","audiences":["public","users"],"sources":["README.md"]},
		{"slug":"architecture","title":"Architecture","audiences":["developers"]}
	]}`)
	plan, err := agent.ParseDocPlan(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(plan.Pages))
	}
	if plan.Pages[0].Slug != "overview" || len(plan.Pages[0].Audiences) != 2 {
		t.Errorf("page 0 = %+v", plan.Pages[0])
	}
	if plan.Pages[1].Audiences[0] != "developers" {
		t.Errorf("page 1 audiences = %v", plan.Pages[1].Audiences)
	}
}

func TestParsePageDoc(t *testing.T) {
	raw := json.RawMessage(`{"title":"Overview","blocks":[
		{"key":"h1","type":"heading","ownership":"hybrid","audiences":["public"],"content":{"text":"Overview","level":2}},
		{"key":"p1","type":"prose","content":{"text":"Hello"},"sources":["main.go"]}
	]}`)
	page, err := agent.ParsePageDoc(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if page.Title != "Overview" || len(page.Blocks) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Blocks[0].Key != "h1" || page.Blocks[0].Audiences[0] != "public" {
		t.Errorf("block 0 = %+v", page.Blocks[0])
	}
	if page.Blocks[1].Sources[0] != "main.go" {
		t.Errorf("block 1 sources = %v", page.Blocks[1].Sources)
	}
}

func TestParsePageDoc_StringEncodedBlocks(t *testing.T) {
	inner := `[{"key":"h1","type":"heading","content":{"text":"Overview","level":2}}]`
	raw, err := json.Marshal(map[string]any{"title": "Overview", "blocks": inner})
	if err != nil {
		t.Fatal(err)
	}
	page, err := agent.ParsePageDoc(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if page.Title != "Overview" || len(page.Blocks) != 1 || page.Blocks[0].Key != "h1" {
		t.Fatalf("page = %+v", page)
	}
}

func TestParsePageDoc_StringEncodedInput(t *testing.T) {
	inner := `{"title":"Overview","blocks":[{"key":"p1","type":"prose","content":{"text":"Hi"}}]}`
	raw, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	page, err := agent.ParsePageDoc(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if page.Title != "Overview" || len(page.Blocks) != 1 {
		t.Fatalf("page = %+v", page)
	}
}

func TestParseDocPlan_StringEncodedPages(t *testing.T) {
	inner := `[{"slug":"overview","title":"Overview","audiences":["public"]}]`
	raw, err := json.Marshal(map[string]any{"pages": inner})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := agent.ParseDocPlan(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.Pages) != 1 || plan.Pages[0].Slug != "overview" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestValidateDocPlan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"pages":[{"slug":"overview","title":"Overview","audiences":["public"]}]}`, false},
		{"empty pages", `{"pages":[]}`, true},
		{"missing slug", `{"pages":[{"title":"Overview"}]}`, true},
		{"garbage", `{"pages":"not json at all"}`, true},
	} {
		err := agent.ValidateDocPlan(json.RawMessage(tc.raw))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestValidatePageDoc(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"title":"T","blocks":[{"key":"h1","type":"heading","content":{"text":"x"}}]}`, false},
		{"empty blocks", `{"title":"T","blocks":[]}`, true},
		{"missing key", `{"title":"T","blocks":[{"type":"prose","content":{"text":"x"}}]}`, true},
		{"missing content", `{"title":"T","blocks":[{"key":"p1","type":"prose"}]}`, true},
		{"garbage blocks", `{"title":"T","blocks":"nope"}`, true},
	} {
		err := agent.ValidatePageDoc(json.RawMessage(tc.raw))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestDocToolsAreTerminal(t *testing.T) {
	for _, tc := range []struct {
		tool agent.Tool
		name string
	}{
		{agent.SubmitDocPlanTool(), agent.ToolSubmitDocPlan},
		{agent.SubmitPageDocTool(), agent.ToolSubmitPageDoc},
	} {
		if !tc.tool.Terminal {
			t.Errorf("%s should be terminal", tc.name)
		}
		if tc.tool.Def.Name != tc.name {
			t.Errorf("tool name = %q, want %q", tc.tool.Def.Name, tc.name)
		}
		if tc.tool.Def.InputSchema["type"] != "object" {
			t.Errorf("%s schema type = %v", tc.name, tc.tool.Def.InputSchema["type"])
		}
	}
}

func TestParseDocPlanUnits(t *testing.T) {
	raw := json.RawMessage(`{
		"units":[{"key":"svc.billing.invoicing","kind":"service","title":"Invoicing service",
			"summary":"Numbers and dispatches invoices.","sourceRefs":["src/billing/invoice.ts"],
			"audiences":["developers"],"pageSlugs":["invoicing"],"changed":true}],
		"pages":[{"slug":"invoicing","title":"Invoicing","audiences":["developers"],
			"units":["svc.billing.invoicing"],"kind":"service"}]
	}`)
	plan, err := agent.ParseDocPlan(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.Units) != 1 {
		t.Fatalf("units = %d, want 1", len(plan.Units))
	}
	u := plan.Units[0]
	if u.Key != "svc.billing.invoicing" || u.Kind != "service" || !u.Changed {
		t.Errorf("unit = %+v", u)
	}
	if u.SourceRefs[0] != "src/billing/invoice.ts" || u.PageSlugs[0] != "invoicing" {
		t.Errorf("unit refs = %+v", u)
	}
	if plan.Pages[0].Kind != "service" || plan.Pages[0].Units[0] != "svc.billing.invoicing" {
		t.Errorf("page = %+v", plan.Pages[0])
	}
}

func TestParseDocPlan_StringEncodedUnits(t *testing.T) {
	inner := `[{"key":"sys.auth","kind":"system","title":"Auth"}]`
	raw, err := json.Marshal(map[string]any{
		"pages": []any{map[string]any{"slug": "auth", "title": "Auth", "audiences": []any{"developers"}}},
		"units": inner,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := agent.ParseDocPlan(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.Units) != 1 || plan.Units[0].Key != "sys.auth" {
		t.Fatalf("units = %+v", plan.Units)
	}
}

func TestValidateDocPlanUnits(t *testing.T) {
	page := `{"slug":"invoicing","title":"Invoicing","audiences":["developers"],"units":["svc.invoicing"]}`
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"units":[{"key":"svc.invoicing","kind":"service","title":"Invoicing"}],"pages":[` + page + `]}`, false},
		{"bad key", `{"units":[{"key":"Svc Invoicing","kind":"service","title":"Invoicing"}],"pages":[` + page + `]}`, true},
		{"duplicate key", `{"units":[{"key":"svc.invoicing","kind":"service","title":"A"},{"key":"svc.invoicing","kind":"api","title":"B"}],"pages":[` + page + `]}`, true},
		{"bad kind", `{"units":[{"key":"svc.invoicing","kind":"microservice","title":"Invoicing"}],"pages":[` + page + `]}`, true},
		{"missing title", `{"units":[{"key":"svc.invoicing","kind":"service","title":" "}],"pages":[` + page + `]}`, true},
		{"dangling page unit", `{"units":[{"key":"svc.other","kind":"service","title":"Other"}],"pages":[` + page + `]}`, true},
		{"bad page kind", `{"units":[{"key":"svc.invoicing","kind":"service","title":"Invoicing"}],"pages":[{"slug":"a","title":"A","audiences":["public"],"kind":"marketing"}]}`, true},
		{"no units at all stays valid", `{"pages":[{"slug":"a","title":"A","audiences":["public"]}]}`, false},
	} {
		err := agent.ValidateDocPlan(json.RawMessage(tc.raw))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestSubmitAtomsToolAsksForTitleAndBody(t *testing.T) {
	schema := agent.SubmitAtomsTool().Def.InputSchema
	items := schema["properties"].(map[string]any)["atoms"].(map[string]any)["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	for _, want := range []string{"title", "body"} {
		if _, ok := props[want]; !ok {
			t.Errorf("submit_atoms items missing %q: %v", want, props)
		}
	}
	if _, ok := props["content"]; ok {
		t.Error("submit_atoms still offers the retired content field")
	}
	raw := json.RawMessage(`{"atoms":[{"title":"Invoice numbering","body":"Per-org sequence.","kind":"procedure","tags":["billing"]}]}`)
	in, err := agent.ParseAtoms(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if in.Atoms[0].Title != "Invoice numbering" || in.Atoms[0].Kind != "procedure" {
		t.Errorf("atom = %+v", in.Atoms[0])
	}
}
