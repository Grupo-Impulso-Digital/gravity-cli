package passes_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func assertDataOnly(t *testing.T, kickoff, phase string) {
	t.Helper()
	if !strings.HasPrefix(kickoff, `<task phase="`+phase+`">`) || !strings.Contains(kickoff, "<change_summary>") {
		t.Fatalf("kickoff = %s", kickoff)
	}
	for _, imperative := range []string{"Decide", "Review the", "Write the", "Read pages", "reuse a title", "Call ", "use git_log"} {
		if strings.Contains(kickoff, imperative) {
			t.Fatalf("kickoff carries an instruction (%q): %s", imperative, kickoff)
		}
	}
}

func TestGuidesPlan(t *testing.T) {
	update := agent.PageAction{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Why: "reason added", MustCover: []agent.MustCover{{Token: "refunds", Text: "The refunds page"}}}
	create := agent.PageAction{Action: agent.ActionCreate, Slug: "refund-reasons", Title: "Refund reasons", Why: "new"}
	cases := []struct {
		name    string
		opts    map[string]any
		trigger string
		mode    string
		pages   []agent.PageAction
		check   func(t *testing.T, fake *fakeAPI, w *writes, rec *passes.Recorder, rep passes.Report)
	}{
		{
			name: "data-only kickoff and the page budget", opts: map[string]any{"maxPages": 1.0}, trigger: config.TriggerPR, mode: api.ModeDry, pages: []agent.PageAction{update, create},
			check: func(t *testing.T, fake *fakeAPI, _ *writes, rec *passes.Recorder, rep passes.Report) {
				kickoff := kickoffOf(fake.requests[0])
				assertDataOnly(t, kickoff, api.PhaseGuidesPlan)
				if !strings.Contains(kickoff, "Page budget: 1 · New pages: allowed") || !strings.Contains(kickoff, "<candidate_pages>\n- pg_1 refunds") {
					t.Fatalf("kickoff = %s", kickoff)
				}
				if !strings.Contains(toolErrors(fake.requests), "pages[1]: page budget reached (1)") {
					t.Fatalf("tool errors = %s", toolErrors(fake.requests))
				}
				if len(fake.requests) != 2 || len(rec.Recorded().Changes) != 0 || len(rep.Impact) != 1 || rep.Impact[0].Reason != "reason added" {
					t.Fatalf("impact mode stops after the plan: %d calls, impact %+v", len(fake.requests), rep.Impact)
				}
			},
		},
		{
			name: "deprecation without the model when deprecate is off", opts: map[string]any{"deprecate": false}, mode: api.ModeWrite,
			pages: []agent.PageAction{{Action: agent.ActionDeprecate, PageID: "pg_1", Slug: "refunds", Why: "the unit was removed"}},
			check: func(t *testing.T, fake *fakeAPI, w *writes, _ *passes.Recorder, rep passes.Report) {
				if !strings.Contains(kickoffOf(fake.requests[0]), "Deprecated pages: deleted") {
					t.Fatalf("kickoff = %s", kickoffOf(fake.requests[0]))
				}
				if len(fake.requests) != 2 || len(w.changes) != 1 || w.changes[0].Op != api.OpDelete || w.changes[0].Summary != "the unit was removed" || rep.Counts.Deleted != 1 {
					t.Fatalf("calls=%d changes=%+v", len(fake.requests), w.changes)
				}
			},
		},
		{
			name: "planner notes reach the writer", mode: api.ModeWrite, pages: []agent.PageAction{update},
			check: func(t *testing.T, fake *fakeAPI, _ *writes, _ *passes.Recorder, _ passes.Report) {
				writer := kickoffOf(fake.requests[2])
				if !strings.Contains(writer, "<planner_notes>reason added</planner_notes>") || !strings.Contains(writer, "<must_cover>\n- [token: refunds] The refunds page\n</must_cover>") {
					t.Fatalf("writer kickoff = %s", writer)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fake, base, head := guidesFixture(t)
			fake.llm = guidesLLM(t, pagePlan{Actions: tc.pages})
			in := input(t, r, fake, planPass(config.KindGuides, "guides", tc.opts), manifest(), base, head, tc.mode)
			if tc.trigger != "" {
				in.Trigger = tc.trigger
			}
			w, rec := &writes{}, &passes.Recorder{}
			var out passes.Sink = sink(w)
			if tc.mode == api.ModeDry {
				out = rec
			}
			rep, err := passes.Guides{}.Run(context.Background(), in, out)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, fake, w, rec, rep)
		})
	}
}

func TestGuidesHaltingErrorEndsThePass(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	fake.llm = guidesLLM(t, pagePlan{Actions: []agent.PageAction{
		{Action: agent.ActionCreate, Slug: "one", Title: "One", Why: "x"},
		{Action: agent.ActionCreate, Slug: "two", Title: "Two", Why: "x"},
	}})
	fake.llmErr = func(req api.MessagesRequest) error {
		if lastTool(req) == agent.ToolSubmitPage {
			return &api.APIError{StatusCode: http.StatusPaymentRequired, Code: api.CodeBudgetExceeded, Message: "budget exceeded"}
		}
		return nil
	}
	w := &writes{}
	_, err := passes.Guides{}.Run(context.Background(), input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite), sink(w))
	if !api.HaltsRun(err) || len(w.changes) != 0 || len(fake.requests) != 3 {
		t.Fatalf("err=%v changes=%d calls=%d", err, len(w.changes), len(fake.requests))
	}
}

func TestChangelogGroupsItemsBySection(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"a.txt": "1"})
	r.git("tag", "v1.0.0")
	base := r.git("rev-parse", "HEAD")
	head := r.commit("feat: refunds", map[string]string{"a.txt": "2"})
	fake := newFakeAPI()
	fake.llm = func(req api.MessagesRequest) api.MessagesResponse {
		return listTurn(req, agent.ToolAddReleaseItems, "items", []agent.ReleaseItem{
			{Section: "Fixed", Text: "Retries stop at 3"},
			{Section: "Added", Text: "Refund reasons"},
			{Section: "Misc", Text: "dropped"},
			{Section: "added", Text: "Webhooks"},
		}, agent.ToolFinishReleaseNotes, map[string]any{"summary": "Refunds and webhooks."})
	}
	in := input(t, r, fake, planPass(config.KindChangelog, "changelog", map[string]any{"unreleased": false}), manifest(), base, head, api.ModeWrite)
	in.Trigger = config.TriggerRelease
	in.Range.Tag, in.Range.PreviousTag, in.Range.Kind = "v1.1.0", "v1.0.0", api.RangeRelease
	w := &writes{}
	if _, err := (passes.Changelog{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	kickoff := kickoffOf(fake.requests[0])
	assertDataOnly(t, kickoff, api.PhaseChangelogWrite)
	if !strings.Contains(kickoff, "Release notes: release v1.1.0\nReaders: customer\nSections: Added, Changed, Fixed") {
		t.Fatalf("kickoff = %s", kickoff)
	}
	if !strings.Contains(toolErrors(fake.requests), `items[2]: section "Misc"`) {
		t.Fatalf("tool errors = %s", toolErrors(fake.requests))
	}
	var got []string
	for _, b := range w.changes[0].Blocks {
		got = append(got, b.Key+"="+b.Content.(map[string]any)["text"].(string))
	}
	want := "changelog:v1-1-0:summary=Refunds and webhooks.,changelog:v1-1-0:added=Added,changelog:v1-1-0:added:1=Refund reasons,changelog:v1-1-0:added:2=Webhooks,changelog:v1-1-0:fixed=Fixed,changelog:v1-1-0:fixed:1=Retries stop at 3"
	if strings.Join(got, ",") != want {
		t.Fatalf("blocks = %v", got)
	}
}

func TestNucleusAtomBudgetAndKickoff(t *testing.T) {
	r := newRepo(t)
	base := r.commit("init", map[string]string{"a.txt": "1"})
	head := r.commit("feat: caps retries at 3", map[string]string{"a.txt": "2"})
	fake := newFakeAPI()
	fake.recall = []api.RecallHit{{Memory: api.Memory{Title: "Retries", Body: "Webhooks retry 5 times."}}}
	fake.llm = func(req api.MessagesRequest) api.MessagesResponse {
		return listTurn(req, agent.ToolAddAtoms, "atoms", []agent.Atom{{Title: "Retries", Body: "Webhooks retry 3 times."}, {Title: "Backoff", Body: "Exponential."}}, agent.ToolFinishAtoms, map[string]any{})
	}
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindNucleus, "memory", map[string]any{"maxAtoms": 1.0}), manifest(), base, head, api.ModeWrite)
	if _, err := (passes.Nucleus{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	kickoff := kickoffOf(fake.requests[0])
	assertDataOnly(t, kickoff, api.PhaseNucleusDistill)
	if !strings.Contains(kickoff, "Namespace: product:acme\nAtom budget: 1\n") || !strings.Contains(kickoff, "<known_atoms>\n") || !strings.Contains(kickoff, "Webhooks retry 5 times.") {
		t.Fatalf("kickoff = %s", kickoff)
	}
	if len(w.memories) != 1 || w.memories[0].Title != "Retries" || !strings.Contains(toolErrors(fake.requests), "atoms[1]: atom budget reached (1)") {
		t.Fatalf("memories=%+v errors=%s", w.memories, toolErrors(fake.requests))
	}
}

func TestCheckReviewKickoffIsData(t *testing.T) {
	r := newRepo(t)
	base := r.commit("init", map[string]string{"src/limits.ts": "export const retries = 3\n"})
	head := r.commit("fix: retries", map[string]string{"src/limits.ts": "export const retries = 4\n"})
	fake := newFakeAPI()
	fake.inventory = []api.Unit{{Key: "feature:limits", Kind: "feature", Title: "Limits", Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: api.RoleImplements, SourceRefs: []string{"src/limits.ts"}}}, Bindings: []api.UnitBinding{{PageID: "pg_lim", PageSlug: "limits"}}}}
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_lim", Slug: "limits", Title: "Limits"}, Blocks: []api.PageBlock{{Key: "guide:limits:1", Type: "prose", Ownership: api.OwnershipHybrid, Text: "Retries: 3."}}})
	fake.llm = func(req api.MessagesRequest) api.MessagesResponse {
		return listTurn(req, agent.ToolAddFindings, "findings", []agent.ClaimFinding{
			{Verdict: api.VerdictContradicted, Title: "Retries are 3", PageID: "pg_lim", BlockKey: "guide:limits:1", UnitKey: "feature:limits", File: "src/limits.ts", Line: 1},
		}, agent.ToolFinishReview, map[string]any{"summary": "one contradiction"})
	}
	in := input(t, r, fake, planPass(config.KindCheck, "gate", nil), manifest(), base, head, api.ModeDry)
	rep, err := passes.Check{}.Run(context.Background(), in, &passes.Recorder{})
	if err != nil {
		t.Fatal(err)
	}
	kickoff := kickoffOf(fake.requests[0])
	assertDataOnly(t, kickoff, api.PhaseCheckReview)
	if !strings.Contains(kickoff, "<pages>\n<page id=\"pg_lim\" slug=\"limits\" category=\"claims\">\n") || !strings.Contains(kickoff, "Retries: 3.\n</page>\n</pages>") {
		t.Fatalf("kickoff = %s", kickoff)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Code != passes.CodeClaimContradicted {
		t.Fatalf("findings = %+v", rep.Findings)
	}
}

func TestReferenceProseFailureFailsThePage(t *testing.T) {
	r := newRepo(t)
	head := r.commit("spec", map[string]string{"api/openapi.yaml": specV1})
	fake := newFakeAPI()
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_ref", Slug: "refunds", Title: "Refunds"}})
	fake.llm = func(api.MessagesRequest) api.MessagesResponse {
		return api.MessagesResponse{StopReason: api.StopRefusal, Usage: api.Usage{InputTokens: 10, OutputTokens: 1}, Content: []api.ContentPart{{Type: api.PartText, Text: "No."}}}
	}
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindReference, "api-ref", map[string]any{"prose": true}), manifest(), "", head, api.ModeWrite)
	rep, err := (passes.Reference{}).Run(context.Background(), in, sink(w))
	if err == nil || !strings.Contains(err.Error(), "page(s) failed") || len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "prose: refusal: No. (model anthropic/claude-test)") {
		t.Fatalf("err=%v errors=%v", err, rep.Errors)
	}
	for _, c := range w.changes {
		for _, b := range c.Blocks {
			if !strings.HasPrefix(b.Key, "api:") {
				t.Fatalf("a failed prose page sends no prose: %+v", c)
			}
		}
	}
	if len(w.changes) == 0 {
		t.Fatal("the api blocks are still written")
	}
}
