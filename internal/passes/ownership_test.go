package passes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func ownershipPlan() *api.Plan {
	yes, no := true, false
	me := api.RepoRef{Name: "billing-api", RemoteKey: "github.com/acme/billing-api"}
	gw := api.RepoRef{Name: "gateway", RemoteKey: "github.com/acme/gateway"}
	svc := api.RepoRef{Name: "refunds-svc", RemoteKey: "github.com/acme/refunds-svc"}
	return &api.Plan{Inventory: api.PlanInventory{Units: []api.Unit{
		{Key: "api:post:/v1/refunds", Kind: "api", Contributors: []api.Contributor{{Repo: me, Role: api.RoleImplements, Active: &yes}, {Repo: gw, Role: api.RoleDeclares, Active: &yes}}},
		{Key: "feature:webhooks", Kind: "feature", Contributors: []api.Contributor{{Repo: me, Role: api.RoleDeclares, Active: &yes}, {Repo: svc, Role: api.RoleImplements, Active: &yes}}},
		{Key: "feature:billing", Kind: "feature", Aliases: []string{"billing"}, Contributors: []api.Contributor{{Repo: gw, Role: api.RoleDeclares, Active: &yes}, {Repo: svc, Role: api.RoleImplements, Active: &no}}},
		{Key: "feature:solo", Kind: "feature", Contributors: []api.Contributor{{Repo: me, Role: api.RoleDeclares, Active: &yes}}},
		{Key: "feature:orphan", Kind: "feature"},
	}}}
}

func TestSettleAppliesTheClaimVerdictRules(t *testing.T) {
	own := passes.NewOwnership(ownershipPlan(), passes.RepoInfo{Name: "billing-api", RemoteKey: "github.com/acme/billing-api"})
	cases := []struct {
		name       string
		claim      agent.ClaimFinding
		lastWriter string
		verbatim   bool
		verdict    string
		elsewhere  string
		hintFor    string
	}{
		{"verified here stays", agent.ClaimFinding{Verdict: api.VerdictVerifiedHere, UnitKey: "api:post:/v1/refunds"}, "", false, api.VerdictVerifiedHere, "", ""},
		{"contradiction of an implemented unit is a finding", agent.ClaimFinding{Verdict: api.VerdictContradicted, UnitKey: "api:post:/v1/refunds"}, "", false, api.VerdictContradicted, "", ""},
		{"contradiction where this repo only declares becomes a hint for the implementer", agent.ClaimFinding{Verdict: api.VerdictContradicted, UnitKey: "feature:webhooks"}, "", false, api.VerdictContradicted, "", "github.com/acme/refunds-svc"},
		{"contradiction where this repo has no role goes to the declarer when nobody implements", agent.ClaimFinding{Verdict: api.VerdictContradicted, UnitKey: "billing"}, "", false, api.VerdictContradicted, "", "github.com/acme/gateway"},
		{"contradiction of this repo's own surface is a finding", agent.ClaimFinding{Verdict: api.VerdictContradicted, UnitKey: "feature:solo"}, "", false, api.VerdictContradicted, "", ""},
		{"contradiction on a locked verbatim page is always a finding", agent.ClaimFinding{Verdict: api.VerdictContradicted, UnitKey: "feature:webhooks"}, "", true, api.VerdictContradicted, "", ""},
		{"unverifiable with another contributor is true elsewhere", agent.ClaimFinding{Verdict: api.VerdictUnverifiable, UnitKey: "feature:webhooks"}, "", false, api.VerdictTrueElsewhere, "refunds-svc", ""},
		{"unverifiable whose block another repo wrote is true elsewhere", agent.ClaimFinding{Verdict: api.VerdictUnverifiable, UnitKey: "feature:orphan"}, "gateway", false, api.VerdictTrueElsewhere, "gateway", ""},
		{"unverifiable whose block this repo wrote stays a note", agent.ClaimFinding{Verdict: api.VerdictUnverifiable, UnitKey: "feature:orphan"}, "billing-api", false, api.VerdictUnverifiable, "", ""},
		{"true elsewhere naming a repo (Nucleus atom) is kept", agent.ClaimFinding{Verdict: api.VerdictTrueElsewhere, Repo: "github.com/acme/ledger"}, "", false, api.VerdictTrueElsewhere, "github.com/acme/ledger", ""},
		{"true elsewhere without any other contributor is downgraded", agent.ClaimFinding{Verdict: api.VerdictTrueElsewhere, UnitKey: "feature:solo"}, "", false, api.VerdictUnverifiable, "", ""},
		{"true elsewhere naming this repository is downgraded", agent.ClaimFinding{Verdict: api.VerdictTrueElsewhere, Repo: "billing-api"}, "", false, api.VerdictUnverifiable, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := own.Settle(tc.claim, tc.lastWriter, tc.verbatim)
			if v.Claim.Verdict != tc.verdict || v.Elsewhere != tc.elsewhere || strings.Join(v.HintFor, ",") != tc.hintFor {
				t.Fatalf("got verdict=%s elsewhere=%q hintFor=%v", v.Claim.Verdict, v.Elsewhere, v.HintFor)
			}
			if tc.verdict == api.VerdictTrueElsewhere && v.Claim.Repo != tc.elsewhere {
				t.Fatalf("repo evidence = %q", v.Claim.Repo)
			}
		})
	}
	if own.Role("billing") != "" || own.Role("feature:webhooks") != api.RoleDeclares || own.Role("api:post:/v1/refunds") != api.RoleImplements {
		t.Fatal("roles are resolved through keys and aliases")
	}
	if others := own.Others("api:post:/v1/refunds", api.RoleImplements, api.RoleDeclares); len(others) != 1 || others[0].Name != "gateway" {
		t.Fatalf("others = %+v", others)
	}
}

func reachFixture(t *testing.T) (*repoT, *fakeAPI, string, string) {
	t.Helper()
	r, fake, base, head := guidesFixture(t)
	fake.inventory[0].Bindings = append(fake.inventory[0].Bindings, api.UnitBinding{PageID: "pg_ext", PageSlug: "refund-flows", SiteSlug: "product", SpaceSlug: "guides"})
	fake.addPage("sp_2", &api.PageContent{Page: api.PageInfo{ID: "pg_ext", Slug: "refund-flows", Title: "Refund flows"}, Blocks: []api.PageBlock{
		{Key: "guide:refund-flows:limits", Type: "prose", Ownership: api.OwnershipHybrid, Units: []string{"feature:refunds"}, Text: "At most 5 refunds.", Provenance: &api.BlockProvenance{Repo: "gateway", CommitSHA: "9f8e7d6"}},
		{Key: "guide:refund-flows:other", Type: "prose", Ownership: api.OwnershipHybrid, Text: "Unrelated."},
	}})
	return r, fake, base, head
}

func TestGuidesUpdatesBlocksAnotherRepositoryWroteThroughUnitReach(t *testing.T) {
	r, fake, base, head := reachFixture(t)
	fake.llm = guidesLLM(t,
		agent.PagePlan{Actions: []agent.PageAction{
			{Action: agent.ActionUpdate, PageID: "pg_ext", Slug: "refund-flows", Reason: "reason added", Units: []string{"feature:refunds"}},
			{Action: agent.ActionDeprecate, PageID: "pg_ext", Slug: "refund-flows", Reason: "no"},
		}},
		agent.PageChanges{Summary: "Mentions the reason", Upserts: []agent.BlockEdit{
			{Key: "guide:refund-flows:limits", Type: "prose", Content: []byte(`{"text":"At most 5 refunds, each with a reason."}`), Rationale: agent.Rationale{Summary: "reason field"}},
			{Key: "guide:refund-flows:new", Type: "prose", Content: []byte(`{"text":"new"}`), Rationale: agent.Rationale{Summary: "x"}},
		}, RemoveKeys: []string{"guide:refund-flows:other"}},
	)
	w := &writes{}
	rep, err := passes.Guides{}.Run(context.Background(), input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 1 {
		t.Fatalf("changes = %+v", w.changes)
	}
	c := w.changes[0]
	if c.Op != api.OpUpdate || c.Target.PageID != "pg_ext" || len(c.Blocks) != 1 || c.Blocks[0].Key != "guide:refund-flows:limits" || len(c.RemoveBlockKeys) != 0 {
		t.Fatalf("only the bound block may change: %+v", c)
	}
	b := c.Blocks[0]
	if strings.Join(b.Units, ",") != "feature:refunds" || b.Rationale == nil || len(b.Rationale.Commits) == 0 || b.Rationale.Commits[0] != head || strings.Join(b.Rationale.SourceRefs, ",") != "src/refunds.ts" {
		t.Fatalf("provenance = %+v units=%v", b.Rationale, b.Units)
	}
	if rep.Counts.ViaUnit != 1 || rep.Counts.Updated != 1 || len(rep.Impact) != 1 || !strings.Contains(rep.Impact[0].Reason, "unit reach (implements feature:refunds)") {
		t.Fatalf("counts=%+v impact=%+v", rep.Counts, rep.Impact)
	}
	warnings := strings.Join(rep.Warnings, "\n")
	if !strings.Contains(warnings, "only its bound blocks") || !strings.Contains(warnings, "guide:refund-flows:new") || !strings.Contains(warnings, "guide:refund-flows:other") {
		t.Fatalf("warnings = %v", rep.Warnings)
	}
}

func TestGuidesDoesNotReachUnitsThisRepositoryOnlyDocuments(t *testing.T) {
	r, fake, base, head := reachFixture(t)
	fake.inventory[0].Contributors[0].Role = api.RoleDocuments
	fake.llm = guidesLLM(t, agent.PagePlan{Actions: []agent.PageAction{{Action: agent.ActionUpdate, PageID: "pg_ext", Slug: "refund-flows", Reason: "x"}}}, agent.PageChanges{})
	w := &writes{}
	rep, err := passes.Guides{}.Run(context.Background(), input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 0 || !strings.Contains(strings.Join(rep.Warnings, "\n"), "unknown page pg_ext") {
		t.Fatalf("changes=%+v warnings=%v", w.changes, rep.Warnings)
	}
}

func TestGuidesPreviewFlagsPagesWithAnotherRunsOpenProposal(t *testing.T) {
	r, fake, base, head := reachFixture(t)
	fake.trees["sp_1"].Pages[0].OpenProposal = &api.OpenProposal{ID: "prop_9", Status: "open", PipelineRunID: "prun_gateway"}
	fake.llm = guidesLLM(t, agent.PagePlan{Actions: []agent.PageAction{
		{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Reason: "reason added"},
		{Action: agent.ActionUpdate, PageID: "pg_ext", Slug: "refund-flows", Reason: "reason added"},
	}}, agent.PageChanges{})
	in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeDry)
	in.Trigger = config.TriggerPR
	rep, err := passes.Guides{}.Run(context.Background(), in, &passes.Recorder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Competing) != 1 || !rep.Competing[0].Pending || rep.Competing[0].Page.ID != "pg_1" || rep.Competing[0].With[0].RunID != "prun_gateway" {
		t.Fatalf("competing = %+v", rep.Competing)
	}
	if len(rep.Impact) != 2 || !strings.Contains(rep.Impact[1].Reason, "unit reach") {
		t.Fatalf("impact = %+v", rep.Impact)
	}
}

func TestGuidesPreviewSupersedesItsOwnPendingChange(t *testing.T) {
	r, fake, base, head := reachFixture(t)
	fake.trees["sp_1"].Pages[0].OpenProposal = &api.OpenProposal{ID: "prop_9", Status: "open", PipelineRunID: "prun_earlier"}
	for i := range fake.pages["pg_1"].Blocks {
		fake.pages["pg_1"].Blocks[i].Provenance = &api.BlockProvenance{Repo: "billing-api", Pass: "guides", RunID: "prun_earlier"}
	}
	fake.llm = guidesLLM(t, agent.PagePlan{Actions: []agent.PageAction{{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Reason: "reason added"}}}, agent.PageChanges{})
	in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeDry)
	in.Trigger = config.TriggerPR
	rep, err := passes.Guides{}.Run(context.Background(), in, &passes.Recorder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Competing) != 0 || !strings.Contains(strings.Join(rep.Warnings, "\n"), "pending change from prun_earlier, which the platform supersedes") {
		t.Fatalf("competing=%+v warnings=%v", rep.Competing, rep.Warnings)
	}
}

func TestGuidesPreviewReadsThePendingList(t *testing.T) {
	cases := []struct {
		name      string
		pending   *[]api.PendingChange
		competing string
		warning   string
	}{
		{"another repository's pending change", &[]api.PendingChange{{RepoID: "cr_gw", RemoteKey: "github.com/acme/gateway", Pass: "gateway-guides", RunID: "prun_gw"}, {RepoID: "cr_1", Pass: "guides", RunID: "prun_old"}}, "github.com/acme/gateway (pass gateway-guides)", ""},
		{"another pass of this repository", &[]api.PendingChange{{RepoID: "cr_1", RemoteKey: "github.com/acme/billing-api", Pass: "reference", RunID: "prun_ref"}}, "github.com/acme/billing-api (pass reference)", ""},
		{"only this repository and pass", &[]api.PendingChange{{RepoID: "cr_1", RemoteKey: "github.com/acme/billing-api", Pass: "guides", RunID: "prun_old"}}, "", "replaces this pass's pending change from prun_old"},
		{"nothing pending", &[]api.PendingChange{}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fake, base, head := reachFixture(t)
			fake.trees["sp_1"].Pages[0].OpenProposal = &api.OpenProposal{ID: "prop_9", Status: "open", PipelineRunID: "prun_old", Pending: tc.pending}
			fake.llm = guidesLLM(t, agent.PagePlan{Actions: []agent.PageAction{{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Reason: "reason added"}}}, agent.PageChanges{})
			in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeDry)
			in.Trigger = config.TriggerPR
			rep, err := passes.Guides{}.Run(context.Background(), in, &passes.Recorder{})
			if err != nil {
				t.Fatal(err)
			}
			warnings := strings.Join(rep.Warnings, "\n")
			switch {
			case tc.competing != "":
				if len(rep.Competing) != 1 || rep.Competing[0].With[0].Repo != tc.competing || !rep.Competing[0].Pending {
					t.Fatalf("competing = %+v", rep.Competing)
				}
			case len(rep.Competing) != 0:
				t.Fatalf("competing = %+v", rep.Competing)
			}
			if tc.warning != "" && !strings.Contains(warnings, tc.warning) || tc.warning == "" && strings.Contains(warnings, "pending change") {
				t.Fatalf("warnings = %s", warnings)
			}
		})
	}
}
