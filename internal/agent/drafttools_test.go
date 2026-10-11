package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func toolNames(tools []agent.Tool) string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Def.Name)
	}
	return strings.Join(out, ",")
}

func TestDraftToolsPerMode(t *testing.T) {
	cases := []struct {
		mode, tools string
		uncovered   bool
	}{
		{agent.DraftCreate, "draft_view,draft_append,draft_replace,draft_delete,check_page,raise_hint", true},
		{agent.DraftReferenceProse, "draft_view,draft_append,draft_replace,draft_delete,check_page,raise_hint", true},
		{agent.DraftReach, "draft_view,draft_replace,check_page,raise_hint", false},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			d := agent.NewDraft(agent.DraftOptions{Mode: tc.mode, Slug: "x", Audiences: []string{"developers"}}, nil, nil)
			if got := toolNames(agent.DraftTools(d)); got != tc.tools {
				t.Fatalf("tools = %s", got)
			}
			submit := agent.SubmitPageTool(d)
			_, has := submit.Def.InputSchema["properties"].(map[string]any)["uncovered"]
			if !submit.Terminal || submit.Def.Name != agent.ToolSubmitPage || has != tc.uncovered {
				t.Fatalf("submit_page terminal=%v uncovered=%v", submit.Terminal, has)
			}
		})
	}
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "x", Audiences: []string{"developers"}}, nil, nil)
	schema, _ := json.Marshal(agent.DraftTools(d)[1].Def.InputSchema)
	for _, want := range []string{`"enum":["heading","prose","list","code","table","callout","quote","divider"]`, `"enum":["developers"]`, `"enum":[1,2,3]`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("draft_append schema lacks %s: %s", want, schema)
		}
	}
	if strings.Contains(string(schema), "anyOf") || strings.Contains(string(schema), "oneOf") {
		t.Errorf("schemas are flat: %s", schema)
	}
}

func runTool(t *testing.T, tools []agent.Tool, name, input string) (string, error) {
	t.Helper()
	return tool(t, tools, name).Run(context.Background(), json.RawMessage(input))
}

func TestDraftToolCalls(t *testing.T) {
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "topics", Language: "en", MustCover: []agent.MustCover{{Token: "GET /topics"}}}, nil, nil)
	tools := agent.DraftTools(d)
	cases := []struct {
		name, tool, input, want string
		err                     bool
	}{
		{"empty view", "draft_view", `{}`, "(empty)", false},
		{"append", "draft_append", `{"rationale":{"summary":"routes"},"blocks":[{"type":"heading","text":"Topics","level":2},{"type":"prose","text":"Call GET /topics to list them."}]}`, "Added guide:topics:1, guide:topics:2.", false},
		{"append wrapped in a string", "draft_append", `"{\"after\":\"^\",\"blocks\":[{\"type\":\"callout\",\"variant\":\"note\",\"text\":\"Beta.\"}]}"`, "Added guide:topics:3.", false},
		{"append invalid", "draft_append", `{"blocks":[{"type":"code","text":"ls"}]}`, "code needs language", true},
		{"replace", "draft_replace", `{"key":"guide:topics:2","block":{"type":"prose","text":"Call GET /topics to list topics."}}`, "Replaced guide:topics:2.", false},
		{"replace without block", "draft_replace", `{"key":"guide:topics:2"}`, "block is required", true},
		{"delete unknown", "draft_delete", `{"keys":["guide:topics:9"]}`, "no block", true},
		{"view", "draft_view", `{}`, "[guide:topics:3] callout info: Beta.\n[guide:topics:1] heading h2: Topics\n[guide:topics:2] prose: Call GET /topics to list topics.", false},
		{"check", "check_page", `{}`, "W1 warning", false},
		{"hint", "raise_hint", `{"kind":"contradiction","claim":"Gateway caps at 3","detail":"routes say 5"}`, "Hint recorded (1 so far).", false},
		{"hint without detail", "raise_hint", `{"kind":"impact","claim":"x"}`, "detail is required", true},
		{"hint bad kind", "raise_hint", `{"kind":"other","claim":"x","detail":"y"}`, "must be one of", true},
		{"delete", "draft_delete", `{"keys":["guide:topics:3"]}`, "Deleted guide:topics:3.", false},
	}
	for _, tc := range cases {
		out, err := runTool(t, tools, tc.tool, tc.input)
		if tc.err {
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
			}
			continue
		}
		if err != nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %q, %v; want %q", tc.name, out, err, tc.want)
		}
	}
	if h := d.Hints(); len(h) != 1 || h[0].Claim != "Gateway caps at 3" {
		t.Fatalf("hints = %+v", h)
	}
}

func TestSubmitPageRunsTheChecks(t *testing.T) {
	cases := []struct {
		name, input, err string
		blocks           []agent.Block
	}{
		{"empty new page", `{"summary":"x"}`, "the draft is empty", nil},
		{"no summary", `{}`, "summary is required", []agent.Block{{Type: "prose", Text: "Lists topics."}}},
		{"uncovered without reason", `{"summary":"x","uncovered":[{"token":"GET /topics"}]}`, "uncovered[0] needs a token and a reason", []agent.Block{{Type: "prose", Text: "Lists topics."}}},
		{"error findings", `{"summary":"x"}`, "E7 error", []agent.Block{{Type: "prose", Text: "Lists topics."}}},
		{"declared uncovered", `{"summary":"Adds topics","uncovered":[{"token":"GET /topics","reason":"internal"}]}`, "", []agent.Block{{Type: "prose", Text: "Lists topics."}}},
		{"covered", `{"summary":"Adds topics"}`, "", []agent.Block{{Type: "prose", Text: "Call GET /topics."}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "topics", MustCover: []agent.MustCover{{Token: "GET /topics"}}}, nil, nil)
			if len(tc.blocks) > 0 {
				if _, err := d.Append("", nil, tc.blocks); err != nil {
					t.Fatal(err)
				}
			}
			submit := agent.SubmitPageTool(d)
			err := submit.Validate(json.RawMessage(tc.input))
			if err == nil {
				_, err = submit.Run(context.Background(), json.RawMessage(tc.input))
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) || d.Submitted() {
					t.Fatalf("err = %v, submitted = %v; want %q", err, d.Submitted(), tc.err)
				}
				return
			}
			if err != nil || !d.Submitted() || d.Summary() != "Adds topics" {
				t.Fatalf("err = %v, submitted = %v", err, d.Submitted())
			}
		})
	}
}

func TestWriterLoopBuildsAPageIncrementally(t *testing.T) {
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "topics", Language: "en", MustCover: []agent.MustCover{{Token: "GET /topics/{id}"}}}, nil, nil)
	llm := script(
		uses(toolUse{"t1", "draft_append", `{"blocks":[{"type":"heading","text":"Topics","level":2}]}`}),
		uses(toolUse{"t2", "draft_append", `{"blocks":[{"type":"prose","text":"Topics group pages."}]}`}, toolUse{"t3", "submit_page", `{"summary":"Adds topics"}`}),
		func(req api.MessagesRequest) (*api.MessagesResponse, error) {
			last := req.Messages[len(req.Messages)-1]
			if !strings.Contains(last.Content[1].Content, "E7 error") || !last.Content[1].IsError {
				return nil, errors.New("submit_page must return the failing checks as a tool error")
			}
			r := uses(toolUse{"t4", "draft_append", `{"after":"guide:topics:1","blocks":[{"type":"prose","text":"Read one with GET /topics/:id."}]}`}, toolUse{"t5", "submit_page", `{"summary":"Adds topics"}`})
			return &r, nil
		},
	)
	tools := append(agent.DraftTools(d), agent.SubmitPageTool(d))
	res, err := (&agent.Runner{Client: llm, System: "s", Tools: tools}).Run(context.Background(), "<task mode=\"create\">")
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalTool != agent.ToolSubmitPage || !d.Submitted() {
		t.Fatalf("result = %+v", res)
	}
	var keys []string
	for _, u := range d.Diff().Upserts {
		keys = append(keys, u.Block.Key+"@"+u.After)
	}
	if strings.Join(keys, " ") != "guide:topics:1@^ guide:topics:3@guide:topics:1 guide:topics:2@guide:topics:3" {
		t.Fatalf("upserts = %v", keys)
	}
}

func TestPageLines(t *testing.T) {
	text := "one\ntwo\nthree\nfour\n"
	cases := []struct {
		name          string
		offset, limit int
		max           int
		want          string
		err           bool
	}{
		{"whole text", 0, 0, 100, text, false},
		{"limit", 1, 2, 100, "one\ntwo\n[lines 1–2 of 4; call again with offset=3]", false},
		{"offset to the end", 3, 0, 100, "three\nfour\n[lines 3–4 of 4]", false},
		{"byte cap", 1, 0, 9, "one\ntwo\n[lines 1–2 of 4; call again with offset=3]", false},
		{"cut line", 3, 0, 3, "thr\n[lines 3–3 of 4; line 3 is cut at 3 bytes; call again with offset=4]", false},
		{"past the end", 9, 0, 100, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agent.PageLines(text, tc.offset, tc.limit, tc.max, "file")
			if tc.err {
				if err == nil {
					t.Fatalf("PageLines = %q, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("PageLines = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
