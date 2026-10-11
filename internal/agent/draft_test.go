package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func boolp(b bool) *bool { return &b }

func blockJSON(t *testing.T, s string) agent.Block {
	t.Helper()
	var b agent.Block
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return b
}

func TestBlockValidateAndContent(t *testing.T) {
	cases := []struct {
		name, in, want, err string
	}{
		{"heading", `{"type":"heading","text":"Limits","level":2}`, `{"level":2,"text":"Limits"}`, ""},
		{"heading level as string", `{"type":"heading","text":"Limits","level":"h3"}`, `{"level":3,"text":"Limits"}`, ""},
		{"heading without level", `{"type":"heading","text":"Limits"}`, "", "heading needs level 1, 2 or 3"},
		{"heading level 4", `{"type":"heading","text":"x","level":4}`, "", "level 1, 2 or 3"},
		{"prose", `{"type":"prose","text":"Pass a reason."}`, `{"text":"Pass a reason."}`, ""},
		{"prose without text", `{"type":"prose"}`, "", "prose needs text"},
		{"quote", `{"type":"quote","text":"Fast."}`, `{"text":"Fast."}`, ""},
		{"list alias", `{"type":"list","text":"One","variant":"ordered"}`, `{"text":"One","variant":"numbered"}`, ""},
		{"task list", `{"type":"list","text":"One","variant":"task"}`, `{"text":"One","variant":"bulleted"}`, ""},
		{"list without variant", `{"type":"list","text":"One"}`, "", "list needs variant bulleted|numbered"},
		{"list of many items", `{"type":"list","text":"One\nTwo","variant":"bulleted"}`, "", "one item per block"},
		{"code", `{"type":"code","text":"{}","language":"json"}`, `{"language":"json","text":"{}"}`, ""},
		{"code without language", `{"type":"code","text":"ls"}`, "", "code needs language"},
		{"callout", `{"type":"callout","text":"Careful.","variant":"warning"}`, `{"text":"Careful.","variant":"warning"}`, ""},
		{"callout alias", `{"type":"callout","text":"Tip.","variant":"tip"}`, `{"text":"Tip.","variant":"info"}`, ""},
		{"callout bad variant", `{"type":"callout","text":"x","variant":"purple"}`, "", "callout needs variant info|success|warning|danger"},
		{"table", `{"type":"table","rows":[["a","b"],["1","2"]]}`, `{"header":true,"rows":[["a","b"],["1","2"]]}`, ""},
		{"table header off", `{"type":"table","rows":[["1"]],"header":"false"}`, `{"header":false,"rows":[["1"]]}`, ""},
		{"table without rows", `{"type":"table"}`, "", "table needs rows"},
		{"divider", `{"type":"divider","text":"ignored"}`, `{}`, ""},
		{"unknown type", `{"type":"api","text":"x"}`, "", `type "api" must be one of`},
		{"audience", `{"type":"prose","text":"x","audiences":["developers"]}`, `{"text":"x"}`, ""},
		{"unknown audience", `{"type":"prose","text":"x","audiences":["admins"]}`, "", `audience "admins" must be one of developers`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := blockJSON(t, tc.in)
			err := b.Validate(api.BlockContract{}, []string{"developers"})
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Validate = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate = %v", err)
			}
			got, _ := json.Marshal(b.Content())
			if string(got) != tc.want {
				t.Fatalf("Content = %s, want %s", got, tc.want)
			}
		})
	}
}

func seedPage() *api.PageContent {
	return &api.PageContent{Page: api.PageInfo{ID: "pg_1", Slug: "refunds"}, Blocks: []api.PageBlock{
		{Key: "guide:refunds:1", Type: "heading", Ownership: api.OwnershipHybrid, Clean: boolp(true), Content: json.RawMessage(`{"text":"Refunds","level":2}`)},
		{Key: "guide:refunds:intro", Type: "prose", Ownership: api.OwnershipHybrid, Clean: boolp(true), Units: []string{"feature:refunds"}, Text: "Refunds take 5 days."},
		{Key: "guide:refunds:human", Type: "prose", Ownership: api.OwnershipHuman, Text: "Written by a person."},
		{Key: "guide:refunds:edited", Type: "prose", Ownership: api.OwnershipHybrid, Clean: boolp(false), Text: "Edited by a person."},
		{Key: "guide:refunds:old", Type: "prose", Ownership: api.OwnershipHybrid, Text: "No clean flag."},
		{Key: "api:POST:/v1/refunds", Type: "api", Ownership: api.OwnershipMachine, Content: json.RawMessage(`{"method":"post","path":"/v1/refunds"}`)},
		{Key: "doc:limits", Type: "prose", Ownership: api.OwnershipMachine, SourceBinding: &api.SourceBinding{Kind: "verbatim"}, Text: "Imported."},
		{Key: "guide:refunds:7", Type: "prose", Ownership: api.OwnershipMachine, SourceBinding: &api.SourceBinding{Kind: "ai"}, Text: "Machine AI text."},
		{Type: "prose", Ownership: api.OwnershipMachine, Text: "No key."},
	}}
}

func lockedKeys(d *agent.Draft) map[string]string {
	out := map[string]string{}
	for _, b := range d.Blocks() {
		if b.Locked {
			out[b.Key] = b.LockReason
		}
	}
	return out
}

func TestNewDraftSeedsAndLocks(t *testing.T) {
	cases := []struct {
		name     string
		opts     agent.DraftOptions
		editable string
		locked   map[string]string
	}{
		{"update", agent.DraftOptions{Mode: agent.DraftUpdate, Slug: "refunds"}, "guide:refunds:1,guide:refunds:intro,guide:refunds:7", map[string]string{
			"guide:refunds:human": "a person owns it", "guide:refunds:edited": "a person edited it", "guide:refunds:old": "a person edited it",
			"api:POST:/v1/refunds": "its type api is not authored", "doc:limits": "it is generated from verbatim", "": "it has no key",
		}},
		{"reach", agent.DraftOptions{Mode: agent.DraftReach, Slug: "refunds", ReachUnits: []string{"feature:refunds"}}, "guide:refunds:intro", map[string]string{
			"guide:refunds:1": "it is not bound to feature:refunds", "guide:refunds:7": "it is not bound to feature:refunds",
		}},
		{"types narrowed by the bundle", agent.DraftOptions{Mode: agent.DraftUpdate, Slug: "refunds", Contract: api.BlockContract{Types: []string{"prose"}}}, "guide:refunds:intro,guide:refunds:7", map[string]string{
			"guide:refunds:1": "its type heading is not authored",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := agent.NewDraft(tc.opts, seedPage(), nil)
			if got := strings.Join(d.EditableKeys(), ","); got != tc.editable {
				t.Fatalf("editable = %s, want %s", got, tc.editable)
			}
			locked := lockedKeys(d)
			for k, reason := range tc.locked {
				if locked[k] != reason {
					t.Errorf("%s locked = %q, want %q", k, locked[k], reason)
				}
			}
			if !d.Diff().Empty() {
				t.Fatalf("an untouched draft has an empty diff: %+v", d.Diff())
			}
		})
	}
}

func TestDraftKeys(t *testing.T) {
	long := strings.Repeat("s", 140)
	cases := []struct {
		name  string
		opts  agent.DraftOptions
		page  *api.PageContent
		first string
	}{
		{"new page", agent.DraftOptions{Mode: agent.DraftCreate, Slug: "refunds"}, nil, "guide:refunds:1"},
		{"after the highest seed key", agent.DraftOptions{Mode: agent.DraftUpdate, Slug: "refunds"}, seedPage(), "guide:refunds:8"},
		{"reference prose", agent.DraftOptions{Mode: agent.DraftReferenceProse, Slug: "refunds", KeyPrefix: agent.KeyPrefixRef}, &api.PageContent{Blocks: []api.PageBlock{{Key: "ref:refunds:3", Type: "prose", Ownership: api.OwnershipMachine, Text: "x"}}}, "ref:refunds:4"},
		{"long slug", agent.DraftOptions{Mode: agent.DraftCreate, Slug: long}, nil, "guide:" + long[:114] + ":1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := agent.NewDraft(tc.opts, tc.page, nil)
			keys, err := d.Append("", nil, []agent.Block{{Type: "prose", Text: "One."}, {Type: "prose", Text: "Two."}})
			if err != nil {
				t.Fatal(err)
			}
			if keys[0] != tc.first || len(keys[0]) > 128 {
				t.Fatalf("keys = %v, want first %s", keys, tc.first)
			}
		})
	}
}

func TestDraftEditsAndDiff(t *testing.T) {
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftUpdate, Slug: "refunds", Audiences: []string{"developers"}}, seedPage(), nil)
	if _, err := d.Append("nope", nil, []agent.Block{{Type: "prose", Text: "x"}}); err == nil || !strings.Contains(err.Error(), "no block \"nope\"") {
		t.Fatalf("unknown after: %v", err)
	}
	if _, err := d.Append("", nil, []agent.Block{{Type: "prose", Text: "ok"}, {Type: "heading", Text: "x"}}); err == nil || !strings.Contains(err.Error(), "blocks[1] (heading)") {
		t.Fatalf("invalid blocks are named: %v", err)
	}
	if len(d.Blocks()) != 9 {
		t.Fatal("a failed append adds nothing")
	}
	top, err := d.Append(agent.AfterFirst, &agent.Rationale{Summary: "shared"}, []agent.Block{{Type: "callout", Text: "New.", Variant: "info"}, {Type: "prose", Text: "Second.", Rationale: &agent.Rationale{Summary: "own"}}})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := d.Append("guide:refunds:intro", nil, []agent.Block{{Type: "prose", Text: "Pass a reason."}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Replace("guide:refunds:human", agent.Block{Type: "prose", Text: "x"}); err == nil || !strings.Contains(err.Error(), "a person owns it") {
		t.Fatalf("locked replace: %v", err)
	}
	if err := d.Replace("guide:refunds:intro", agent.Block{Type: "prose", Text: "Refunds take 3 days."}); err != nil {
		t.Fatal(err)
	}
	if err := d.Replace("guide:refunds:1", agent.Block{Type: "heading", Text: "Refunds", Level: 2, Rationale: &agent.Rationale{Summary: "rationale only"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete([]string{"guide:refunds:7", "guide:refunds:human"}); err == nil {
		t.Fatal("a delete with a locked key must fail")
	}
	if err := d.Delete([]string{"guide:refunds:7", mid[0]}); err != nil {
		t.Fatal(err)
	}
	more, _ := d.Append("", nil, []agent.Block{{Type: "prose", Text: "Last."}})
	if more[0] != "guide:refunds:11" {
		t.Fatalf("deleted keys are never reused: %v", more)
	}
	diff := d.Diff()
	var got []string
	for _, u := range diff.Upserts {
		got = append(got, u.Block.Key+"@"+u.After)
	}
	want := []string{top[0] + "@^", top[1] + "@" + top[0], "guide:refunds:intro@guide:refunds:1", "guide:refunds:11@doc:limits"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("upserts = %v, want %v", got, want)
	}
	if strings.Join(diff.Remove, ",") != "guide:refunds:7" {
		t.Fatalf("remove = %v (new keys deleted before submit are not removed)", diff.Remove)
	}
	if r := diff.Upserts[0].Block.Rationale; r == nil || r.Summary != "shared" {
		t.Fatalf("the call rationale applies to blocks without one: %+v", r)
	}
	if r := diff.Upserts[1].Block.Rationale; r == nil || r.Summary != "own" {
		t.Fatalf("a block rationale wins: %+v", r)
	}
	if u := diff.Upserts[2].Block.Units; strings.Join(u, ",") != "feature:refunds" {
		t.Fatalf("replace keeps units it is not given: %v", u)
	}
}

func TestDraftReachIsReplaceOnly(t *testing.T) {
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftReach, Slug: "refunds", ReachUnits: []string{"feature:refunds"}}, seedPage(), nil)
	if _, err := d.Append("", nil, []agent.Block{{Type: "prose", Text: "x"}}); err == nil {
		t.Fatal("reach drafts cannot add blocks")
	}
	if err := d.Delete([]string{"guide:refunds:intro"}); err == nil {
		t.Fatal("reach drafts cannot delete blocks")
	}
	if err := d.Replace("guide:refunds:1", agent.Block{Type: "heading", Text: "x", Level: 2}); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound replace: %v", err)
	}
	if err := d.Replace("guide:refunds:intro", agent.Block{Type: "prose", Text: "Refunds take 3 days."}); err != nil {
		t.Fatal(err)
	}
	diff := d.Diff()
	if len(diff.Upserts) != 1 || diff.Upserts[0].Block.Key != "guide:refunds:intro" || len(diff.Remove) != 0 {
		t.Fatalf("diff = %+v", diff)
	}
}

func TestDraftMergesComputedBlocks(t *testing.T) {
	page := &api.PageContent{Blocks: []api.PageBlock{
		{Key: "ref:refunds:1", Type: "prose", Ownership: api.OwnershipMachine, Text: "Intro."},
		{Key: "api:POST:/v1/refunds", Type: "api", Ownership: api.OwnershipMachine, Content: json.RawMessage(`{"method":"post","path":"/v1/refunds","summary":"old"}`)},
	}}
	computed := []api.ChangeBlock{
		{Key: "api:POST:/v1/refunds", Type: "api", Content: map[string]any{"method": "post", "path": "/v1/refunds", "summary": "Create a refund"}},
		{Key: "api:GET:/v1/refunds/{id}", Type: "api", Content: map[string]any{"method": "get", "path": "/v1/refunds/{id}"}},
	}
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftReferenceProse, Slug: "refunds", KeyPrefix: agent.KeyPrefixRef}, page, computed)
	blocks := d.Blocks()
	if len(blocks) != 3 || blocks[1].Text() != "POST /v1/refunds Create a refund" || blocks[2].Key != "api:GET:/v1/refunds/{id}" || !blocks[2].Locked {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !strings.Contains(d.View(), "[api:GET:/v1/refunds/{id}] (locked) api: GET /v1/refunds/{id}") {
		t.Fatalf("view = %s", d.View())
	}
}

func TestDraftPersistsOnlyWithAPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "runs", "prun_1", "ppr_1", "new-refunds.json")
	d := agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "refunds", Path: p}, nil, nil)
	if _, err := d.Append("", nil, []agent.Block{{Type: "prose", Text: "Saved."}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), `"key": "guide:refunds:1"`) || !strings.Contains(string(data), `"origin": "new"`) {
		t.Fatalf("draft file = %s, %v", data, err)
	}
	agent.NewDraft(agent.DraftOptions{Mode: agent.DraftCreate, Slug: "other"}, nil, nil)
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("a draft without a path writes nothing: %v", entries)
	}
}
