package passes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func kickoffOf(req api.MessagesRequest) string {
	if len(req.Messages) == 0 || len(req.Messages[0].Content) == 0 {
		return ""
	}
	return req.Messages[0].Content[0].Text
}

func toolErrors(reqs []api.MessagesRequest) string {
	var out []string
	for _, req := range reqs {
		for _, m := range req.Messages {
			for _, p := range m.Content {
				if p.IsError {
					out = append(out, p.Content)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestGuidesCreatesAPageThroughTheDraft(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	create := agent.PageAction{
		Action: agent.ActionCreate, Slug: "refund-reasons", Title: "Refund reasons", CollectionPath: []string{"Guides"},
		Why: "the refund reason field is new", Units: []string{"feature:refunds"}, MustCover: []agent.MustCover{{Token: "POST /v1/refunds", Text: "Create a refund with a reason."}},
	}
	fake.llm = guidesLLM(t, pagePlan{Actions: []agent.PageAction{create}},
		calls(
			call(agent.ToolDraftAppend, map[string]any{"rationale": map[string]any{"summary": "routes", "sourceRefs": []string{"src/refunds.ts:2"}}, "blocks": []any{
				map[string]any{"type": "heading", "text": "Refund reasons", "level": 2},
				map[string]any{"type": "prose", "text": "Every refund carries a reason."},
			}}),
			call(agent.ToolSubmitPage, map[string]any{"summary": "Adds refund reasons"}),
		),
		calls(
			call(agent.ToolDraftAppend, map[string]any{"after": "guide:refund-reasons:1", "blocks": []any{
				map[string]any{"type": "code", "language": "bash", "text": "curl -X POST /v1/refunds -d reason=duplicate", "units": []string{"feature:refunds", "feature:unknown"}},
			}}),
			call(agent.ToolSubmitPage, map[string]any{"summary": "Adds refund reasons"}),
		),
	)
	in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite)
	in.DebugDir = filepath.Join(t.TempDir(), ".gravity", "runs", "prun_1", "ppr_1")
	w := &writes{}
	rep, err := passes.Guides{}.Run(context.Background(), in, sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 1 {
		t.Fatalf("changes = %+v", w.changes)
	}
	c := w.changes[0]
	if c.Op != api.OpCreate || c.Title != "Refund reasons" || c.Target.SpaceID != "sp_1" || c.Target.Slug != "refund-reasons" || strings.Join(c.Target.CollectionPath, "/") != "guides" || c.Summary != "Adds refund reasons\n\nWhy: the refund reason field is new" {
		t.Fatalf("change = %+v", c)
	}
	var got []string
	for _, b := range c.Blocks {
		got = append(got, b.Key+"@"+*b.After)
	}
	if strings.Join(got, " ") != "guide:refund-reasons:1@^ guide:refund-reasons:3@guide:refund-reasons:1 guide:refund-reasons:2@guide:refund-reasons:3" {
		t.Fatalf("blocks = %v", got)
	}
	code := c.Blocks[1]
	if code.Type != "code" || code.Content.(map[string]any)["language"] != "bash" || strings.Join(code.Units, ",") != "feature:refunds" || code.Ownership != api.OwnershipHybrid || strings.Join(code.Audiences, ",") != "developers" {
		t.Fatalf("code block = %+v", code)
	}
	if r := c.Blocks[0].Rationale; r == nil || r.Summary != "routes" || r.SourceRefs[0] != "src/refunds.ts:2" || r.Commits[0] != head {
		t.Fatalf("rationale = %+v", r)
	}
	if rep.Counts.Created != 1 || strings.Join(c.Units, ",") != "feature:refunds" {
		t.Fatalf("counts = %+v units = %v", rep.Counts, c.Units)
	}
	if errs := toolErrors(fake.requests); !strings.Contains(errs, `E7 error: must_cover token "POST /v1/refunds" is not on the page`) {
		t.Fatalf("the first submit is refused by E7: %s", errs)
	}
	var writer api.MessagesRequest
	for _, req := range fake.requests {
		if lastTool(req) == agent.ToolSubmitPage {
			writer = req
			break
		}
	}
	kickoff := kickoffOf(writer)
	for _, want := range []string{`<task mode="create">`, "Page: Refund reasons (slug refund-reasons) · Target: api/Guides", "Editable: all", "- [token: POST /v1/refunds] Create a refund with a reason.", "<planner_notes>the refund reason field is new</planner_notes>", "<units>", "<change_summary>"} {
		if !strings.Contains(kickoff, want) {
			t.Errorf("kickoff lacks %q:\n%s", want, kickoff)
		}
	}
	if writer.Context.Phase != api.PhaseGuidesWrite || writer.SystemBlocks[0].Text != "system for guides.write" {
		t.Fatalf("writer context = %+v", writer.Context)
	}
	data, err := os.ReadFile(filepath.Join(in.DebugDir, "new-refund-reasons.json"))
	if err != nil || !strings.Contains(string(data), `"submitted": true`) {
		t.Fatalf("debug draft = %s, %v", data, err)
	}
}

func TestGuidesUpdateKickoffAndUnchangedDraft(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	clean := true
	fake.pages["pg_1"].Blocks[0].Clean = &clean
	fake.llm = guidesLLM(t, pagePlan{Actions: []agent.PageAction{{Action: agent.ActionDeprecate, PageID: "pg_1", Slug: "refunds", Why: "the unit was removed"}}})
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite)
	rep, err := passes.Guides{}.Run(context.Background(), in, sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 0 || rep.Counts.Unchanged != 1 {
		t.Fatalf("an untouched draft sends nothing: %+v %+v", w.changes, rep.Counts)
	}
	kickoff := kickoffOf(fake.requests[2])
	if !strings.Contains(kickoff, `<task mode="deprecate">`) || !strings.Contains(kickoff, "Editable: keys: guide:refunds:intro") || strings.Contains(kickoff, "Written by a person.") {
		t.Fatalf("kickoff = %s", kickoff)
	}
	if in.DebugDir != "" {
		t.Fatal("no debug directory without --debug")
	}
}

func TestGuidesFailedPageSendsNothing(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	ended := api.MessagesResponse{StopReason: api.StopEndTurn, Usage: api.Usage{InputTokens: 10, OutputTokens: 5}, Content: []api.ContentPart{{Type: api.PartText, Text: "Done."}}}
	fake.llm = guidesLLM(t, pagePlan{Actions: []agent.PageAction{{Action: agent.ActionCreate, Slug: "new-page", Title: "New page", Why: "x"}}},
		calls(call(agent.ToolDraftAppend, map[string]any{"blocks": []any{map[string]any{"type": "prose", "text": "Half a page."}}})),
		ended, ended, ended, ended, ended, ended,
	)
	w := &writes{}
	rep, err := passes.Guides{}.Run(context.Background(), input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite), sink(w))
	if err == nil || len(w.changes) != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "new-page: author: no_submit") || !strings.Contains(rep.Errors[0], "model anthropic/claude-test") {
		t.Fatalf("err = %v changes = %d errors = %v", err, len(w.changes), rep.Errors)
	}
	if !strings.Contains(err.Error(), "1 page(s) failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestReferenceProseWritesAroundTheAPIBlocks(t *testing.T) {
	r := newRepo(t)
	head := r.commit("spec", map[string]string{"api/openapi.yaml": specV1})
	fake := newFakeAPI()
	clean := true
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_ref", Slug: "refunds", Title: "Refunds"}, Blocks: []api.PageBlock{
		{Key: "ref:refunds:2", Type: "prose", Ownership: api.OwnershipHybrid, Clean: &clean, Text: "Old intro."},
	}})
	fake.llm = func(req api.MessagesRequest) api.MessagesResponse {
		if lastTool(req) != agent.ToolSubmitPage {
			t.Fatalf("unexpected tools %v", req.Tools)
		}
		if strings.Contains(kickoffOf(req), "slug charges") {
			return submit(agent.ToolSubmitPage, map[string]any{"summary": "nothing"})
		}
		switch assistantTurns(req) {
		case 0:
			return calls(
				call(agent.ToolDraftView, map[string]any{}),
				call(agent.ToolDraftAppend, map[string]any{"after": "^", "blocks": []any{map[string]any{"type": "prose", "text": "Refunds return money."}}}),
				call(agent.ToolDraftDelete, map[string]any{"keys": []string{"ref:refunds:2"}}),
				call(agent.ToolDraftReplace, map[string]any{"key": "api:POST:/v1/refunds", "block": map[string]any{"type": "prose", "text": "x"}}),
			)
		}
		return submit(agent.ToolSubmitPage, map[string]any{"summary": "Introduces refunds"})
	}
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindReference, "api-ref", map[string]any{"prose": true}), manifest(), "", head, api.ModeWrite)
	if _, err := (passes.Reference{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	var prose *api.ChangeRequest
	for i, c := range w.changes {
		if c.Target.PageID == "pg_ref" && c.Summary == "Introduces refunds" {
			prose = &w.changes[i]
		}
	}
	if prose == nil {
		t.Fatalf("changes = %+v", w.changes)
	}
	if prose.Op != api.OpUpdate || len(prose.Blocks) != 1 || prose.Blocks[0].Key != "ref:refunds:3" || *prose.Blocks[0].After != "^" || strings.Join(prose.RemoveBlockKeys, ",") != "ref:refunds:2" {
		t.Fatalf("prose change = %+v", prose)
	}
	view := ""
	for _, req := range fake.requests {
		for _, m := range req.Messages {
			for _, p := range m.Content {
				if strings.HasPrefix(p.Content, "Draft (reference-prose, refunds)") {
					view = p.Content
				}
			}
		}
	}
	if !strings.Contains(view, "[api:POST:/v1/refunds] (locked) api") || !strings.Contains(view, "[api:GET:/v1/refunds] (locked) api") {
		t.Fatalf("the computed api blocks are in the draft: %s", view)
	}
	if !strings.Contains(toolErrors(fake.requests), "api:POST:/v1/refunds is locked") {
		t.Fatalf("api blocks cannot be replaced: %s", toolErrors(fake.requests))
	}
	data, _ := json.Marshal(prose.Blocks[0].Content)
	if string(data) != `{"text":"Refunds return money."}` {
		t.Fatalf("content = %s", data)
	}
}
