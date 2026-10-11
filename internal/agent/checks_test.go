package agent_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func words(n int, text string) string {
	parts := strings.Fields(text)
	out := make([]string, 0, n)
	for len(out) < n {
		out = append(out, parts[len(out)%len(parts)])
	}
	return strings.Join(out, " ") + "."
}

const (
	english = "The refund is sent to the card that paid for the order and you can track it in the dashboard with the reference"
	french  = "Le remboursement est envoyé sur la carte qui a payé la commande et vous pouvez le suivre dans le tableau avec la référence"
)

func findingCodes(findings []agent.Finding) string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Code+":"+f.Severity)
	}
	return strings.Join(out, ",")
}

func TestCheckFindings(t *testing.T) {
	long := strings.Repeat("word ", 130)
	cases := []struct {
		name      string
		mode      string
		language  string
		mustCover []agent.MustCover
		blocks    []agent.Block
		uncovered []agent.Uncovered
		want      string
	}{
		{"clean english page", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: words(160, english)}}, nil, ""},
		{"french page on an english site", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: words(160, french)}}, nil, "E1:error"},
		{"short french text is a warning", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: words(60, french)}}, nil, "E1:warning"},
		{"french site reads french", agent.DraftUpdate, "fr", nil, []agent.Block{{Type: "prose", Text: words(160, french)}}, nil, ""},
		{"code is not language-checked", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "code", Text: words(200, french), Language: "text"}}, nil, ""},
		{"fenced code in prose", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "Run:\n```\nls\n```"}}, nil, "E2:error"},
		{"json in a callout", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "callout", Variant: "info", Text: `Send {"amount": 100, "reason": "dup"} to the API.`}}, nil, "E2:error"},
		{"short json is fine", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: `Send {"a":1} please.`}}, nil, ""},
		{"indented lines", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "Example:\n    a\n    b\n    c"}}, nil, "E2:error"},
		{"markdown heading", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "## Limits\nAt most five."}}, nil, "E3:error"},
		{"bold only", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "**Limits**"}}, nil, "E3:error"},
		{"short line without period", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "Refund limits"}}, nil, "E3:warning"},
		{"planning voice", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "This section should describe the refund limits."}}, nil, "E4:error"},
		{"french planning voice", agent.DraftUpdate, "fr", nil, []agent.Block{{Type: "prose", Text: "Cette page doit décrire les remboursements."}}, nil, "E4:error"},
		{"mentions the planner", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "heading", Level: 2, Text: "What the planner wants"}}, nil, "E4:error"},
		{"output marker", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "code", Language: "ts", Text: "// TODO: remove"}}, nil, "E5:error"},
		{"missing token", agent.DraftUpdate, "en", []agent.MustCover{{Token: "GET /topics/{id}", Text: "Read a topic."}}, []agent.Block{{Type: "prose", Text: "Topics are listed."}}, nil, "E7:error"},
		{"token covered with :param and backticks", agent.DraftUpdate, "en", []agent.MustCover{{Token: "GET /topics/{id}", Text: "x"}}, []agent.Block{{Type: "prose", Text: "Call `get /topics/:id` to read one."}}, nil, ""},
		{"token declared uncovered", agent.DraftUpdate, "en", []agent.MustCover{{Token: "DELETE /topics/{id}", Text: "x"}}, []agent.Block{{Type: "prose", Text: "Topics are listed."}}, []agent.Uncovered{{Token: "delete /topics/{id}", Reason: "internal"}}, ""},
		{"text-only must cover is advisory", agent.DraftUpdate, "en", []agent.MustCover{{Text: "Explain pagination."}}, []agent.Block{{Type: "prose", Text: "Topics are listed."}}, nil, ""},
		{"short new page", agent.DraftCreate, "en", nil, []agent.Block{{Type: "prose", Text: "Topics are listed."}}, nil, "W1:warning"},
		{"long new page", agent.DraftCreate, "en", nil, []agent.Block{{Type: "prose", Text: long + "end."}}, nil, ""},
		{"duplicate adjacent blocks", agent.DraftUpdate, "en", nil, []agent.Block{{Type: "prose", Text: "Same text."}, {Type: "prose", Text: "same  text."}}, nil, "W2:warning"},
		{"reach ignores coverage", agent.DraftReach, "en", []agent.MustCover{{Token: "GET /x"}}, nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := agent.NewDraft(agent.DraftOptions{Mode: tc.mode, Slug: "topics", Language: tc.language, MustCover: tc.mustCover}, nil, nil)
			if len(tc.blocks) > 0 {
				if _, err := d.Append("", nil, tc.blocks); err != nil {
					t.Fatal(err)
				}
			}
			if got := findingCodes(d.Check(tc.uncovered)); got != tc.want {
				t.Fatalf("findings = %s, want %s (%v)", got, tc.want, d.Check(tc.uncovered))
			}
		})
	}
}

func TestChecksSkipUntouchedSeedBlocks(t *testing.T) {
	page := &api.PageContent{Blocks: []api.PageBlock{
		{Key: "guide:x:1", Type: "prose", Ownership: api.OwnershipHybrid, Clean: boolp(true), Content: json.RawMessage(`{"text":"This page will describe TODO: things."}`)},
		{Key: "guide:x:2", Type: "prose", Ownership: api.OwnershipHuman, Text: "Covers GET /x."},
	}}
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftUpdate, Slug: "x", Language: "en", MustCover: []agent.MustCover{{Token: "GET /x"}}}, page, nil)
	if f := d.Check(nil); len(f) != 0 {
		t.Fatalf("untouched blocks are not checked, and existing text covers tokens: %v", f)
	}
	if err := d.Replace("guide:x:1", agent.Block{Type: "prose", Text: "This page will describe TODO: more things."}); err != nil {
		t.Fatal(err)
	}
	if got := findingCodes(d.Check(nil)); got != "E4:error,E5:error" {
		t.Fatalf("a changed block is checked: %s", got)
	}
}

func TestCheckBlocks(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		blocks []api.ChangeBlock
		want   []string
	}{
		{name: "clean create page is short", mode: agent.DraftCreate, blocks: []api.ChangeBlock{{Key: "k1", Type: agent.BlockHeading, Content: map[string]any{"text": "Topics", "level": 2}}, {Key: "k2", Type: agent.BlockProse, Content: map[string]any{"text": "Topics group posts."}}}, want: []string{agent.CheckShortPage}},
		{name: "update skips the short page warning", mode: agent.DraftUpdate, blocks: []api.ChangeBlock{{Key: "k1", Type: agent.BlockProse, Content: map[string]any{"text": "Topics group posts."}}}},
		{name: "errors on change blocks", mode: agent.DraftUpdate, blocks: []api.ChangeBlock{
			{Key: "k1", Type: agent.BlockProse, Content: map[string]any{"text": "## Topics"}},
			{Key: "k2", Type: agent.BlockProse, Content: map[string]any{"text": "TODO: finish."}},
			{Key: "k3", Type: agent.BlockCode, Content: map[string]any{"text": `{"a": 1}`, "language": "json"}},
		}, want: []string{agent.CheckMarkdownHead, agent.CheckOutputMarker}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, f := range agent.CheckBlocks(agent.DraftOptions{Mode: tt.mode, Language: "en", Path: filepath.Join(t.TempDir(), "never.json")}, tt.blocks) {
				got = append(got, f.Code)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("codes = %v, want %v", got, tt.want)
			}
		})
	}
}
