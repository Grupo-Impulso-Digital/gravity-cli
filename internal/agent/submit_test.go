package agent_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
)

func TestSubmitToolsAreTerminalAndNamed(t *testing.T) {
	for name, tool := range map[string]agent.Tool{
		agent.ToolSubmitPagePlan:     agent.SubmitPagePlanTool(),
		agent.ToolSubmitPageChanges:  agent.SubmitPageChangesTool(),
		agent.ToolSubmitReleaseNotes: agent.SubmitReleaseNotesTool(),
		agent.ToolSubmitAtoms:        agent.SubmitAtomsTool(),
		agent.ToolReportFindings:     agent.ReportFindingsTool(),
		agent.ToolSubmitUnits:        agent.SubmitUnitsTool(),
	} {
		if !tool.Terminal || tool.Def.Name != name || tool.Validate == nil {
			t.Errorf("%s: terminal=%v name=%q validate=%v", name, tool.Terminal, tool.Def.Name, tool.Validate != nil)
		}
	}
}

func TestValidatePagePlan(t *testing.T) {
	cases := map[string]string{
		`{"actions":[{"action":"update","pageId":"pg_1","slug":"refunds","reason":"a1b2"}]}`:                     "",
		`{"actions":[{"action":"create","slug":"refund-reasons","title":"Refund reasons","reason":"new unit"}]}`: "",
		`{"actions":[{"action":"create","slug":"Bad Slug","title":"x","reason":"r"}]}`:                           "kebab-case",
		`{"actions":[{"action":"delete","slug":"x","reason":"r"}]}`:                                              "must be update",
		`{"actions":[{"action":"update","reason":"r"}]}`:                                                         "pageId or slug",
		`"{\"actions\":[]}"`: "",
	}
	for in, want := range cases {
		err := agent.ValidatePagePlan(json.RawMessage(in))
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("ValidatePagePlan(%s) = %v, want %q", in, err, want)
		}
	}
}

func TestValidatePageChanges(t *testing.T) {
	ok := `{"summary":"s","upserts":[{"key":"guide:refunds:reasons","type":"prose","content":{"text":"x"},"rationale":{"summary":"added in a1b2c3d","commits":["a1b2c3d"]}}],"removeKeys":["guide:refunds:old"]}`
	if err := agent.ValidatePageChanges(json.RawMessage(ok)); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		`{"summary":"s","upserts":[{"key":"k","type":"api","content":{},"rationale":{"summary":"r"}}]}`:                                                                       "type",
		`{"summary":"s","upserts":[{"key":"k","type":"prose","content":"x","rationale":{"summary":"r"}}]}`:                                                                    "content must be an object",
		`{"summary":"s","upserts":[{"key":"k","type":"prose","content":{},"rationale":{}}]}`:                                                                                  "rationale.summary",
		`{"summary":"s","upserts":[{"key":"k","type":"prose","content":{},"rationale":{"summary":"r"}},{"key":"k","type":"prose","content":{},"rationale":{"summary":"r"}}]}`: "twice",
	} {
		if err := agent.ValidatePageChanges(json.RawMessage(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidatePageChanges(%s) = %v, want %q", in, err, want)
		}
	}
}

func TestReleaseNotesAcceptStringAndObjectItems(t *testing.T) {
	in, err := agent.ParseReleaseNotes(json.RawMessage(`{"summary":"s","sections":[{"heading":"Added","items":["plain",{"text":"rich","commits":["a1"],"units":["api:post:/v1/refunds"]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	items := in.Sections[0].Items
	if len(items) != 2 || items[0].Text != "plain" || items[1].Commits[0] != "a1" || items[1].Units[0] != "api:post:/v1/refunds" {
		t.Fatalf("items = %+v", items)
	}
	if err := agent.ValidateReleaseNotes(json.RawMessage(`{"summary":"s","sections":[{"heading":"Misc","items":[]}]}`)); err == nil {
		t.Fatal("unknown section accepted")
	}
}

func TestValidateUnitsAndFindings(t *testing.T) {
	if err := agent.ValidateUnits(json.RawMessage(`{"units":[{"key":"feature:billing.refunds","kind":"feature","title":"Refunds","sourceRefs":["src/refunds/**"]}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := agent.ValidateUnits(json.RawMessage(`{"units":[{"key":"Bad Key","kind":"feature","title":"x","sourceRefs":["a"]}]}`)); err == nil {
		t.Fatal("bad key accepted")
	}
	if err := agent.ValidateUnits(json.RawMessage(`{"units":[{"key":"feature:billing","kind":"feature","title":" ","sourceRefs":["a"]}]}`)); err == nil {
		t.Fatal("blank unit title accepted")
	}
	if err := agent.SubmitAtomsTool().Validate(json.RawMessage(`{"atoms":[{"title":"` + strings.Repeat("t", 201) + `","body":"x"}]}`)); err == nil {
		t.Fatal("atom title over the platform limit accepted")
	}
	if err := agent.ReportFindingsTool().Validate(json.RawMessage(`{"findings":[{"verdict":"maybe","title":"x"}]}`)); err == nil {
		t.Fatal("unknown verdict accepted")
	}
	if err := agent.SubmitAtomsTool().Validate(json.RawMessage(`{"atoms":[{"title":"","body":"x"}]}`)); err == nil {
		t.Fatal("atom without a title accepted")
	}
}
