package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/impulso/gravity-cli/internal/agent"
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
	// The failure that sank a full authoring run: the model passed blocks as a
	// JSON-encoded string instead of an array.
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
