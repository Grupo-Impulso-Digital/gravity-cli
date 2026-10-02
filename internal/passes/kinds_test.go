package passes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func verbatimPass() api.PlanPass {
	return planPass(config.KindVerbatim, "handbook", map[string]any{"files": []any{map[string]any{"include": "docs/handbook/**"}}})
}

func TestVerbatimImportsChangedFilesUploadsImagesAndProposesDeletions(t *testing.T) {
	r := newRepo(t)
	head := r.commit("docs", map[string]string{
		"docs/handbook/oncall/rotation.md": "---\ntitle: Rotation\n---\nSee ![chart](chart.png) and [outage](../outage.md).\n",
		"docs/handbook/oncall/chart.png":   "PNG",
		"docs/handbook/outage.md":          "# Outage\n\nRestart it.\n",
	})
	fake := newFakeAPI()
	outageHash := docs.HashBytes([]byte("# Outage\n\nRestart it.\n"))
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_out", Slug: "outage", Title: "Outage", Lock: &api.PageLock{Pass: "handbook", Path: "docs/handbook/outage.md", Hash: outageHash}}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_old", Slug: "old", Title: "Old", Lock: &api.PageLock{Pass: "handbook", Path: "docs/handbook/old.md", Hash: "sha256:x"}}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_other", Slug: "other", Title: "Other", Lock: &api.PageLock{Pass: "another-pass", Path: "docs/x.md", Hash: "sha256:y"}}})
	w := &writes{}
	rep, err := passes.Verbatim{}.Run(context.Background(), input(t, r, fake, verbatimPass(), manifest(), "", head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.verbatim) != 1 || w.verbatim[0].File.Path != "docs/handbook/oncall/rotation.md" {
		t.Fatalf("only the changed file is imported: %+v", w.verbatim)
	}
	req := w.verbatim[0]
	if req.Page.Slug != "rotation" || req.Page.Title != "Rotation" || strings.Join(req.Page.CollectionPath, "/") != "oncall" || req.File.CommitSHA != head || !strings.HasPrefix(req.File.URL, "https://github.com/acme/billing-api/blob/main/") {
		t.Fatalf("request = %+v", req)
	}
	text := req.Blocks[0].Content.(map[string]any)["text"].(string)
	if !strings.Contains(text, "https://media.test/") || !strings.Contains(text, "[outage](../api/outage)") {
		t.Fatalf("image and link not rewritten: %q", text)
	}
	if len(w.assets) != 1 || w.assets[0] != "docs/handbook/oncall/chart.png" {
		t.Fatalf("assets = %v", w.assets)
	}
	if len(w.deletions) != 1 || w.deletions[0].Path != "docs/handbook/old.md" {
		t.Fatalf("deletions = %+v", w.deletions)
	}
	if rep.Counts.Imported != 1 || rep.Counts.Unchanged != 1 || rep.Counts.Deleted != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
}

func guidesLLM(t *testing.T, plan agent.PagePlan, changes agent.PageChanges) func(api.MessagesRequest) api.MessagesResponse {
	t.Helper()
	return func(req api.MessagesRequest) api.MessagesResponse {
		if req.Context == nil || req.Context.RunPassID != "ppr_1" {
			t.Errorf("gateway call without the run context: %+v", req.Context)
		}
		switch lastTool(req) {
		case agent.ToolSubmitPagePlan:
			return submit(agent.ToolSubmitPagePlan, plan)
		case agent.ToolSubmitPageChanges:
			return submit(agent.ToolSubmitPageChanges, changes)
		}
		t.Fatalf("unexpected tools %v", req.Tools)
		return api.MessagesResponse{}
	}
}

func guidesFixture(t *testing.T) (*repoT, *fakeAPI, string, string) {
	t.Helper()
	r := newRepo(t)
	base := r.commit("init", map[string]string{"src/refunds.ts": "export const limit = 5\n"})
	head := r.commit("feat(refunds): add reason", map[string]string{"src/refunds.ts": "export const limit = 5\nexport const reason = true\n"})
	fake := newFakeAPI()
	fake.inventory = []api.Unit{{
		Key: "feature:refunds", Kind: "feature", Title: "Refunds",
		Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: api.RoleImplements, SourceRefs: []string{"src/refunds.ts"}}},
		Bindings:     []api.UnitBinding{{PageID: "pg_1", PageSlug: "refunds", SiteSlug: "dev", SpaceSlug: "api"}},
	}}
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_1", Slug: "refunds", Title: "Refunds"}, Blocks: []api.PageBlock{
		{Key: "guide:refunds:intro", Type: "prose", Ownership: api.OwnershipHybrid, Text: "Refunds."},
		{Key: "guide:refunds:human", Type: "prose", Ownership: api.OwnershipHuman, Text: "Written by a person."},
	}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_lock", Slug: "locked", Title: "Locked", Lock: &api.PageLock{Path: "docs/x.md"}}})
	return r, fake, base, head
}

func TestGuidesAuthorsBlockEditsAndNeverTouchesHumanBlocks(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	fake.llm = guidesLLM(t,
		agent.PagePlan{Actions: []agent.PageAction{
			{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Reason: "reason added in " + head[:7], Units: []string{"feature:refunds"}},
			{Action: agent.ActionUpdate, PageID: "pg_lock", Slug: "locked", Reason: "nope"},
		}},
		agent.PageChanges{Summary: "Documents the reason", Upserts: []agent.BlockEdit{
			{Key: "guide:refunds:reason", Type: "prose", Content: []byte(`{"text":"Pass a reason."}`), Rationale: agent.Rationale{Summary: "new field", Commits: []string{head}}},
			{Key: "guide:refunds:human", Type: "prose", Content: []byte(`{"text":"overwrite"}`), Rationale: agent.Rationale{Summary: "x"}},
		}, RemoveKeys: []string{"guide:refunds:human"}, Hints: []agent.HintDraft{{Kind: "contradiction", Claim: "Gateway retries 5 times", UnitKey: "feature:refunds"}}},
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
	if c.Op != api.OpUpdate || c.Target.PageID != "pg_1" || len(c.Blocks) != 1 || c.Blocks[0].Key != "guide:refunds:reason" || len(c.RemoveBlockKeys) != 0 {
		t.Fatalf("change = %+v", c)
	}
	b := c.Blocks[0]
	if b.Ownership != api.OwnershipHybrid || b.SourceBinding.Kind != "ai" || b.SourceBinding.Ref != "guides" || b.Rationale.Commits[0] != head {
		t.Fatalf("block = %+v", b)
	}
	if len(c.Units) != 1 || c.Units[0] != "feature:refunds" {
		t.Fatalf("units = %v", c.Units)
	}
	if len(w.hints) != 1 || w.hints[0].PageID != "pg_1" || rep.Counts.Hints != 1 {
		t.Fatalf("hints = %+v", w.hints)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "locked") {
		t.Fatalf("locked page must be skipped with a warning: %v", rep.Warnings)
	}
	for _, req := range fake.requests {
		if strings.Contains(req.System, "Organization voice") {
			t.Fatal("the CLI must not embed instruction layers")
		}
	}
}

func TestGuidesOnPullRequestsReportsImpactWithoutAuthoring(t *testing.T) {
	r, fake, base, head := guidesFixture(t)
	fake.llm = guidesLLM(t, agent.PagePlan{Actions: []agent.PageAction{{Action: agent.ActionUpdate, PageID: "pg_1", Slug: "refunds", Reason: "reason added"}}}, agent.PageChanges{})
	in := input(t, r, fake, planPass(config.KindGuides, "guides", nil), manifest(), base, head, api.ModeDry)
	in.Trigger = config.TriggerPR
	rec := &passes.Recorder{}
	rep, err := passes.Guides{}.Run(context.Background(), in, rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 1 || len(rec.Recorded().Changes) != 0 {
		t.Fatalf("impact mode runs only the plan: %d calls, %d changes", len(fake.requests), len(rec.Recorded().Changes))
	}
	if len(rep.Impact) != 1 || rep.Impact[0].Page.Slug != "refunds" || rep.Impact[0].Action != "update" {
		t.Fatalf("impact = %+v", rep.Impact)
	}
}

func TestChangelogReleaseAndUnreleasedPages(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"a.txt": "1"})
	r.git("tag", "v1.3.0")
	base := r.git("rev-parse", "HEAD")
	head := r.commit("feat: refunds reason (#42)", map[string]string{"a.txt": "2"})
	r.git("tag", "v1.4.0")
	fake := newFakeAPI()
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_unrel", Slug: "unreleased", Title: "Unreleased"}, Blocks: []api.PageBlock{{Key: "changelog:unreleased:added:1", Type: "list", Ownership: api.OwnershipHybrid}}})
	fake.llm = func(req api.MessagesRequest) api.MessagesResponse {
		return submit(agent.ToolSubmitReleaseNotes, agent.ReleaseNotesInput{Summary: "Refund reasons.", Sections: []agent.ReleaseSection{{Heading: "Added", Items: []agent.ReleaseItem{{Text: "Refund reasons", Commits: []string{head}}}}}})
	}
	pp := planPass(config.KindChangelog, "changelog", map[string]any{"audience": "internal"})
	in := input(t, r, fake, pp, manifest(), base, head, api.ModeWrite)
	in.Trigger = config.TriggerRelease
	in.Range.Tag, in.Range.PreviousTag, in.Range.Kind = "v1.4.0", "v1.3.0", api.RangeRelease
	w := &writes{}
	if _, err := (passes.Changelog{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 2 {
		t.Fatalf("changes = %+v", w.changes)
	}
	release, unreleased := w.changes[0], w.changes[1]
	if release.Op != api.OpCreate || release.Target.Slug != "v1-4-0" || release.Title != "Release v1.4.0" {
		t.Fatalf("release page = %+v", release)
	}
	keys := []string{}
	for _, b := range release.Blocks {
		keys = append(keys, b.Key)
	}
	if strings.Join(keys, ",") != "changelog:v1-4-0:summary,changelog:v1-4-0:added,changelog:v1-4-0:added:1" {
		t.Fatalf("keys = %v", keys)
	}
	item := release.Blocks[2].Content.(map[string]any)["text"].(string)
	if !strings.Contains(item, head[:7]) {
		t.Fatalf("internal audience lists commits: %q", item)
	}
	if unreleased.Target.PageID != "pg_unrel" || unreleased.Blocks[0].Content.(map[string]any)["text"] != passes.NothingUnreleased || unreleased.RemoveBlockKeys[0] != "changelog:unreleased:added:1" {
		t.Fatalf("unreleased page = %+v", unreleased)
	}
	if got := passes.ReleaseSlug("release-{major}-{minor}", "v2.7.1", time.Now()); got != "release-2-7" {
		t.Fatalf("slug = %s", got)
	}
}

func TestNucleusWritesNamespacedRepoTaggedAtoms(t *testing.T) {
	r := newRepo(t)
	base := r.commit("init", map[string]string{"a.txt": "1"})
	head := r.commit("feat: caps retries at 3", map[string]string{"a.txt": "2"})
	fake := newFakeAPI()
	fake.inventory = []api.Unit{{Key: "feature:retries", Kind: "feature"}}
	fake.llm = func(api.MessagesRequest) api.MessagesResponse {
		return submit(agent.ToolSubmitAtoms, agent.AtomsInput{Atoms: []agent.Atom{
			{Title: "Retries", Body: "Webhooks retry 3 times.", Commits: []string{head}, Units: []string{"feature:retries", "feature:unknown"}},
			{Title: "Decision", Body: "x", Kind: "decision"},
		}})
	}
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindNucleus, "memory", map[string]any{"kinds": []any{"fact"}}), manifest(), base, head, api.ModeWrite)
	rep, err := passes.Nucleus{}.Run(context.Background(), in, sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.memories) != 1 {
		t.Fatalf("kinds filter: %+v", w.memories)
	}
	m := w.memories[0]
	if m.Namespace != "product:acme" || m.RunID != "prun_1" || len(m.Sources) != 3 || m.Sources[0].Type != api.SourceRepo || m.Sources[2].ID != "feature:retries" {
		t.Fatalf("memory = %+v", m)
	}
	if rep.Counts.Queued != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
}

func TestCheckFindsDriftCoverageAndContradictions(t *testing.T) {
	r := newRepo(t)
	base := r.commit("spec v1", map[string]string{"api/openapi.yaml": specV1})
	head := r.commit("spec v2", map[string]string{"api/openapi.yaml": specV2, "src/webhooks.ts": "retries = 3"})
	fake := newFakeAPI()
	v1 := apiBlocks(t, specV1)
	stale := pageBlock(v1["api:POST:/v1/refunds"])
	removed := pageBlock(v1["api:GET:/v1/refunds"])
	removed.Provenance = &api.BlockProvenance{Repo: "billing-api"}
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_ref", Slug: "refunds", Title: "Refunds", Units: []string{"api:post:/v1/refunds"}}, Blocks: []api.PageBlock{stale, removed}})
	yes := true
	fake.inventory = []api.Unit{
		{Key: "api:post:/v1/refunds", Kind: "api", Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: api.RoleImplements, Active: &yes}}, Bindings: []api.UnitBinding{{PageID: "pg_ref", PageSlug: "refunds"}}},
		{Key: "feature:webhooks", Kind: "feature", Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: api.RoleImplements}}},
		{Key: "feature:gateway", Kind: "feature", Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: api.RoleDeclares}, {Repo: api.RepoRef{RemoteKey: "github.com/acme/gateway"}, Role: api.RoleImplements}}},
	}
	fake.llm = func(api.MessagesRequest) api.MessagesResponse {
		return submit(agent.ToolReportFindings, agent.FindingsInput{Findings: []agent.ClaimFinding{
			{Verdict: api.VerdictContradicted, Title: "Refunds says 5 retries", UnitKey: "api:post:/v1/refunds", PageID: "pg_ref", File: "src/webhooks.ts", Line: 1},
			{Verdict: api.VerdictContradicted, Title: "Gateway timeout", UnitKey: "feature:gateway", PageID: "pg_ref"},
			{Verdict: api.VerdictUnverifiable, Title: "Daily limit", PageID: "pg_ref"},
			{Verdict: api.VerdictTrueElsewhere, Title: "Auth", Repo: "github.com/acme/gateway"},
		}})
	}
	pp := planPass(config.KindCheck, "gate", map[string]any{"coverageMin": 1.0, "require": []any{"feature"}})
	in := input(t, r, fake, pp, manifest(), base, head, api.ModeDry)
	in.Plan.Passes = []api.PlanPass{pp, planPass(config.KindReference, "developer-api", nil)}
	rec := &passes.Recorder{}
	rep, err := passes.Check{}.Run(context.Background(), in, rec)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{}
	for _, f := range rep.Findings {
		codes[f.Code]++
	}
	if codes[passes.CodeDrift] != 1 || codes[passes.CodeDriftRemoved] != 1 || codes[passes.CodeCoverage] != 1 || codes[passes.CodeCoverageRequired] != 1 || codes[passes.CodeClaimContradicted] != 1 {
		t.Fatalf("findings = %+v", codes)
	}
	if len(rec.Recorded().Hints) != 1 || rec.Recorded().Hints[0].ForRepos[0] != "github.com/acme/gateway" {
		t.Fatalf("a contradiction about another repository's unit becomes a hint: %+v", rec.Recorded().Hints)
	}
	if len(rep.Notes) != 1 || len(rep.Claims) != 1 || !rep.Failing {
		t.Fatalf("notes=%d claims=%d failing=%v", len(rep.Notes), len(rep.Claims), rep.Failing)
	}
	in.FailOn = []string{"coverage"}
	if passes.Failing(rep.Findings, passes.FailOn(in)) != true {
		t.Fatal("coverage findings fail with --fail-on coverage")
	}
	if passes.Failing([]api.Finding{{Severity: api.SeverityError, Code: passes.CodeDrift}}, []string{"coverage"}) {
		t.Fatal("drift must not fail when only coverage is in failOn")
	}
}

func TestCapturePollsTheDocAgentRun(t *testing.T) {
	r := newRepo(t)
	head := r.commit("init", map[string]string{"a.txt": "1"})
	fake := newFakeAPI()
	fake.runs = []*api.Run{
		{},
		{CaptureRuns: []api.CaptureRun{{RunPassID: "ppr_1", DocAgentRunID: "dar_1", Status: "running"}}},
		{CaptureRuns: []api.CaptureRun{{RunPassID: "ppr_1", DocAgentRunID: "dar_1", Status: "succeeded"}}},
	}
	in := input(t, r, fake, planPass(config.KindCapture, "capture", nil), manifest(), "", head, api.ModeWrite)
	slept := 0
	in.Sleep = func(context.Context, time.Duration) error { slept++; return nil }
	rep, err := passes.Capture{}.Run(context.Background(), in, sink(&writes{}))
	if err != nil || rep.Summary != "Doc Agent run dar_1 succeeded" || slept != 2 {
		t.Fatalf("rep=%+v err=%v slept=%d", rep.Summary, err, slept)
	}
	fake.runs = []*api.Run{{}}
	_, err = passes.Capture{}.Run(context.Background(), in, sink(&writes{}))
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (passes.Capture{}).Run(context.Background(), in, &passes.Recorder{}); err != nil {
		t.Fatal(err)
	}
}
