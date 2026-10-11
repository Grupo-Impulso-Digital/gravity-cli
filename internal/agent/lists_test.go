package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func callTool(t *testing.T, tool agent.Tool, input string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(input))
}

func TestListToolsAreGravityTools(t *testing.T) {
	plan, notes, atoms, review, units := agent.NewPlanList(5), agent.NewReleaseNotes(), agent.NewAtomList(5, nil), agent.NewReview(), agent.NewUnitList()
	cases := []struct {
		add, finish   agent.Tool
		addName, name string
	}{
		{plan.AddTool(), plan.FinishTool(), agent.ToolAddPages, agent.ToolFinishPlan},
		{notes.AddTool(), notes.FinishTool(), agent.ToolAddReleaseItems, agent.ToolFinishReleaseNotes},
		{atoms.AddTool(), atoms.FinishTool(), agent.ToolAddAtoms, agent.ToolFinishAtoms},
		{review.AddTool(), review.FinishTool(), agent.ToolAddFindings, agent.ToolFinishReview},
		{units.AddTool(), units.FinishTool(), agent.ToolAddUnits, agent.ToolFinishUnits},
	}
	for _, tc := range cases {
		if tc.add.Terminal || tc.add.Def.Name != tc.addName || tc.add.Run == nil {
			t.Errorf("%s: terminal=%v name=%q", tc.addName, tc.add.Terminal, tc.add.Def.Name)
		}
		if !tc.finish.Terminal || tc.finish.Def.Name != tc.name {
			t.Errorf("%s: terminal=%v name=%q", tc.name, tc.finish.Terminal, tc.finish.Def.Name)
		}
		for _, s := range []map[string]any{tc.add.Def.InputSchema, tc.finish.Def.InputSchema} {
			data, _ := json.Marshal(s)
			if strings.Contains(string(data), "anyOf") || strings.Contains(string(data), "oneOf") {
				t.Errorf("%s schema is not flat: %s", tc.name, data)
			}
		}
	}
}

func TestPlanList(t *testing.T) {
	page := func(extra string) string {
		return `{"action":"create","slug":"refunds","title":"Refunds","units":["feature:refunds"],"mustCover":[{"token":"POST /v1/refunds","text":"Create a refund"}],"why":"new unit"` + extra + `}`
	}
	cases := []struct {
		name    string
		max     int
		calls   []string
		wantErr []string
		want    []string
	}{
		{name: "create kept with must cover", max: 3, calls: []string{`{"pages":[` + page("") + `]}`}, want: []string{"refunds"}},
		{
			name:    "invalid items rejected, valid kept",
			max:     5,
			calls:   []string{`{"pages":[` + page("") + `,{"action":"create","slug":"Bad Slug","title":"x","why":"w"},{"action":"delete","slug":"x","why":"w"},{"action":"update","why":"w"},{"action":"update","slug":"limits"}]}`},
			wantErr: []string{"Kept 1 of 5 pages", "pages[1]: create needs a kebab-case slug", "pages[2]: action \"delete\"", "pages[3]: update needs the pageId or slug", "pages[4]: why is required"},
			want:    []string{"refunds"},
		},
		{
			name:    "page budget across calls",
			max:     2,
			calls:   []string{`{"pages":[{"action":"update","pageId":"pg_1","slug":"a","title":"A","why":"w"}]}`, `{"pages":[{"action":"update","pageId":"pg_2","slug":"b","title":"B","why":"w"},{"action":"deprecate","pageId":"pg_3","slug":"c","title":"C","why":"w"}]}`},
			wantErr: []string{"", "pages[1]: page budget reached (2)"},
			want:    []string{"a", "b"},
		},
		{
			name:    "duplicate slug",
			max:     5,
			calls:   []string{`{"pages":[` + page("") + `]}`, `{"pages":[` + page("") + `]}`},
			wantErr: []string{"", "page refunds is already in the plan"},
			want:    []string{"refunds"},
		},
		{
			name:    "must cover needs text",
			max:     5,
			calls:   []string{`{"pages":[{"action":"update","slug":"a","title":"A","why":"w","mustCover":[{"token":"GET /a"}]}]}`},
			wantErr: []string{"mustCover[0] needs a text"},
		},
		{name: "empty list", max: 5, calls: []string{`{"pages":[]}`}, wantErr: []string{"pages is empty"}},
		{name: "input wrapped in a string", max: 5, calls: []string{`"{\"pages\":[` + strings.ReplaceAll(page(""), `"`, `\"`) + `]}"`}, want: []string{"refunds"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := agent.NewPlanList(tc.max)
			add := p.AddTool()
			for i, in := range tc.calls {
				_, err := callTool(t, add, in)
				want := ""
				if i < len(tc.wantErr) {
					want = tc.wantErr[i]
				}
				if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
					t.Fatalf("call %d: err = %v, want %q", i, err, want)
				}
			}
			var slugs []string
			for _, a := range p.Pages() {
				slugs = append(slugs, a.Slug)
			}
			if strings.Join(slugs, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("pages = %v, want %v", slugs, tc.want)
			}
		})
	}
	p := agent.NewPlanList(1)
	if _, err := callTool(t, p.AddTool(), `{"pages":[`+page("")+`]}`); err != nil {
		t.Fatal(err)
	}
	if got := p.Pages()[0]; got.Why != "new unit" || got.MustCover[0].Token != "POST /v1/refunds" || got.Units[0] != "feature:refunds" {
		t.Fatalf("page = %+v", got)
	}
	finish := p.FinishTool()
	if err := finish.Validate(json.RawMessage(`{"summary":" Two pages "}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := callTool(t, finish, `{"summary":" Two pages "}`); err != nil || p.Summary() != "Two pages" {
		t.Fatalf("summary = %q err=%v", p.Summary(), err)
	}
}

func TestReleaseNotesGroupBySection(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
		want    string
	}{
		{name: "grouped in section order", input: `{"items":[{"section":"Fixed","text":"Retry on 503"},{"section":"added","text":"Refund reasons","commits":["a1"],"units":["api:post:/v1/refunds"]},{"section":"Added","text":"Webhooks"}]}`, want: "Added:Refund reasons|Webhooks;Fixed:Retry on 503"},
		{name: "unknown section", input: `{"items":[{"section":"Misc","text":"x"},{"section":"Removed","text":"Old endpoint"}]}`, wantErr: `items[0]: section "Misc" must be one of`, want: "Removed:Old endpoint"},
		{name: "blank text", input: `{"items":[{"section":"Added","text":"  "}]}`, wantErr: "items[0]: text is required"},
		{name: "not an array", input: `{"items":"Added things"}`, wantErr: "items must be an array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := agent.NewReleaseNotes()
			_, err := callTool(t, r.AddTool(), tc.input)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			var parts []string
			for _, s := range r.Sections() {
				var texts []string
				for _, it := range s.Items {
					texts = append(texts, it.Text)
				}
				parts = append(parts, s.Heading+":"+strings.Join(texts, "|"))
			}
			if got := strings.Join(parts, ";"); got != tc.want {
				t.Fatalf("sections = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAtomList(t *testing.T) {
	atom := func(title string) string { return `{"title":"` + title + `","body":"A fact."}` }
	cases := []struct {
		name    string
		max     int
		kinds   []string
		calls   []string
		wantErr []string
		want    int
	}{
		{name: "kept", max: 3, calls: []string{`{"atoms":[` + atom("A") + `,` + atom("B") + `]}`}, want: 2},
		{name: "budget across calls", max: 2, calls: []string{`{"atoms":[` + atom("A") + `]}`, `{"atoms":[` + atom("B") + `,` + atom("C") + `]}`}, wantErr: []string{"", "atoms[1]: atom budget reached (2)"}, want: 2},
		{name: "kind not allowed", max: 5, kinds: []string{"decision"}, calls: []string{`{"atoms":[{"title":"A","body":"b","kind":"fact"},{"title":"B","body":"b","kind":"Decision"}]}`}, wantErr: []string{`kind "fact" is not allowed`}, want: 1},
		{name: "limits", max: 5, calls: []string{`{"atoms":[{"title":"` + strings.Repeat("t", 201) + `","body":"b"},{"title":"","body":"b"},{"title":"x","body":"b","confidence":2}]}`}, wantErr: []string{"atoms[0]: title is longer than 200"}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := agent.NewAtomList(tc.max, tc.kinds)
			for i, in := range tc.calls {
				_, err := callTool(t, l.AddTool(), in)
				want := ""
				if i < len(tc.wantErr) {
					want = tc.wantErr[i]
				}
				if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
					t.Fatalf("call %d: err = %v, want %q", i, err, want)
				}
			}
			if got := len(l.Atoms()); got != tc.want {
				t.Fatalf("atoms = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReviewFindings(t *testing.T) {
	r := agent.NewReview()
	_, err := callTool(t, r.AddTool(), `{"findings":[{"verdict":"contradicted-here","title":"Refund window","pageId":"pg_1"},{"verdict":"maybe","title":"x"},{"verdict":"verified-here"}]}`)
	if err == nil || !strings.Contains(err.Error(), `findings[1]: verdict "maybe"`) || !strings.Contains(err.Error(), "findings[2]: needs a title or a claim") {
		t.Fatalf("err = %v", err)
	}
	if f := r.Findings(); len(f) != 1 || f[0].PageID != "pg_1" {
		t.Fatalf("findings = %+v", f)
	}
}

func TestUnitListReplacesAndCompletes(t *testing.T) {
	l := agent.NewUnitList()
	if _, err := callTool(t, l.AddTool(), `{"units":[{"key":"feature:billing","kind":"feature","title":"Billing","sourceRefs":["src/billing/**"]},{"key":"Bad Key","kind":"feature","title":"x","sourceRefs":["a"]},{"key":"feature:x","kind":"feature","title":" ","sourceRefs":["a"]}]}`); err == nil || !strings.Contains(err.Error(), "units[1]: key") || !strings.Contains(err.Error(), "units[2]: feature:x needs a title") {
		t.Fatalf("err = %v", err)
	}
	if _, err := callTool(t, l.AddTool(), `{"units":[{"key":"feature:billing","kind":"capability","title":"Billing v2","sourceRefs":["src/billing/**"]}]}`); err != nil {
		t.Fatal(err)
	}
	if u := l.Units(); len(u) != 1 || u[0].Title != "Billing v2" || u[0].Kind != "capability" {
		t.Fatalf("units = %+v", u)
	}
	if err := l.FinishTool().Validate(json.RawMessage(`{}`)); err == nil {
		t.Fatal("finish_units without complete accepted")
	}

	b := testBundle()
	b.Rendered.System[api.PhaseUnitsMap] = "units system"
	addUnit := use(agent.ToolAddUnits, `{"units":[{"key":"feature:billing","kind":"feature","title":"Billing","sourceRefs":["src/billing/**"]}]}`, 10, 5)
	cases := []struct {
		name     string
		maxTurns int
		replies  []api.MessagesResponse
		complete bool
		units    int
	}{
		{name: "finished complete", replies: []api.MessagesResponse{addUnit, use(agent.ToolFinishUnits, `{"complete":true}`, 10, 5)}, complete: true, units: 1},
		{name: "finished incomplete", replies: []api.MessagesResponse{addUnit, use(agent.ToolFinishUnits, `{"complete":false}`, 10, 5)}, units: 1},
		{name: "forced at the cap", maxTurns: 4, replies: []api.MessagesResponse{addUnit, use(agent.ToolFinishUnits, `{"complete":true}`, 10, 5)}, units: 1},
		{
			name: "truncated finish is discarded",
			replies: []api.MessagesResponse{
				{StopReason: api.StopMaxTokens, Usage: api.Usage{InputTokens: 10, OutputTokens: 5}, Content: []api.ContentPart{{Type: api.PartToolUse, ID: "x", Name: agent.ToolFinishUnits, Input: json.RawMessage(`{"complete":true}`)}}},
				addUnit, use(agent.ToolFinishUnits, `{"complete":false}`, 10, 5),
			},
			units: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bb := *b
			if tc.maxTurns > 0 {
				bb.Harness.MaxTurns = tc.maxTurns
			}
			list := agent.NewUnitList()
			h := &agent.Harness{LLM: &fakeLLM{replies: tc.replies}, Bundle: &bb}
			res, err := h.Run(context.Background(), agent.Task{Phase: api.PhaseUnitsMap, Kickoff: "<task/>", Tools: []agent.Tool{list.AddTool()}, Submit: list.FinishTool()})
			if err != nil {
				t.Fatal(err)
			}
			if got := list.Complete(res); got != tc.complete || len(list.Units()) != tc.units {
				t.Fatalf("complete=%v units=%d forced=%v", got, len(list.Units()), res.Forced)
			}
		})
	}
	if l.Complete(nil) {
		t.Fatal("no result is not complete")
	}
}
