package plan

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func manifest(t *testing.T, src string) *config.Manifest {
	t.Helper()
	m, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func basePlan() *api.Plan {
	return &api.Plan{
		Repo:    api.PlanRepo{DefaultBranch: "main", AuthoritativeBranch: "main", ManifestHash: "sha256:stored"},
		Trigger: "push", Branch: "main",
		Passes: []api.PlanPass{
			{Name: "developer-api", Kind: "reference", Source: "manifest", Locked: true, Enabled: true, Applies: true, Triggers: []string{"push", "pr"}, Target: api.PassTarget{Ref: "dev-portal/api", Status: api.TargetOK}},
			{Name: "product-guides", Kind: "guides", Source: "app", Enabled: true, Applies: true, Triggers: []string{"push"}, Target: api.PassTarget{Ref: "product/guides", Status: api.TargetOK}},
			{Name: "old-manifest", Kind: "nucleus", Source: "manifest", Locked: true, Enabled: true, Applies: true, Triggers: []string{"push"}, Target: api.PassTarget{Status: api.TargetNone}},
		},
	}
}

func byName(e Effective) map[string]Pass {
	out := map[string]Pass{}
	for _, p := range e.Passes {
		out[p.Name] = p
	}
	return out
}

func TestMergeSyncedPlanIsUntouched(t *testing.T) {
	m := manifest(t, "version: 2\npasses:\n  - name: developer-api\n    kind: reference\n    target: dev-portal/api\n    triggers: [push, pr]\n")
	p := basePlan()
	p.Repo.ManifestHash = m.Hash
	e := Merge(p, m)
	got := byName(e)
	if len(e.Passes) != 3 || !got["developer-api"].Declared || got["developer-api"].Pending || got["old-manifest"].Archived || len(e.Warnings) != 0 {
		t.Fatalf("effective = %+v", e)
	}
}

func TestMergeRepoWinsOnDeclaredPasses(t *testing.T) {
	m := manifest(t, `version: 2
passes:
  - name: developer-api
    kind: reference
    target: dev-portal/api
    triggers: [pr]
    scope:
      paths: ["api/**"]
  - name: product-guides
    kind: guides
    target: product/user-guides
    triggers: [push]
  - name: memory
    kind: nucleus
    triggers: [push]
`)
	e := Merge(basePlan(), m)
	got := byName(e)
	dev := got["developer-api"]
	if !dev.Pending || dev.Applies || dev.SkipReason != api.SkipTriggerMismatch || dev.Scope.Paths[0] != "api/**" || dev.Target.Status != api.TargetOK {
		t.Fatalf("developer-api = %+v", dev)
	}
	guides := got["product-guides"]
	if guides.Source != "manifest" || !guides.Locked || guides.Target.Ref != "product/user-guides" || guides.Target.Status != api.TargetUnapproved || guides.SkipReason != api.SkipTargetUnapproved {
		t.Fatalf("product-guides = %+v", guides)
	}
	mem := got["memory"]
	if !mem.Overlay || !mem.Applies || mem.Target.Status != api.TargetNone {
		t.Fatalf("memory = %+v", mem)
	}
	old := got["old-manifest"]
	if !old.Archived || old.Applies {
		t.Fatalf("old-manifest = %+v", old)
	}
	codes := map[string]bool{}
	for _, w := range e.Warnings {
		codes[w.Code] = true
	}
	if !codes["pass_undeclared"] || !codes["manifest_not_authoritative"] {
		t.Fatalf("warnings = %+v", e.Warnings)
	}
}

func TestMergeAppPassesIgnore(t *testing.T) {
	m := manifest(t, "version: 2\nappPasses: ignore\n")
	p := basePlan()
	e := Merge(p, m)
	if g := byName(e)["product-guides"]; g.Applies || g.SkipReason != api.SkipNotSelected {
		t.Fatalf("app pass = %+v", g)
	}
}

func TestMergeWithoutManifest(t *testing.T) {
	e := Merge(basePlan(), nil)
	if len(e.Passes) != 3 || len(e.Warnings) != 0 {
		t.Fatalf("effective = %+v", e)
	}
}

func TestAppliesPrecedence(t *testing.T) {
	pp := api.PlanPass{Enabled: false, Triggers: []string{"pr"}, Target: api.PassTarget{Status: api.TargetMissing}}
	if ok, r := Applies(pp, "push", "main", "main"); ok || r != api.SkipDisabled {
		t.Fatalf("disabled first: %v %s", ok, r)
	}
	pp.Enabled = true
	if _, r := Applies(pp, "push", "main", "main"); r != api.SkipTriggerMismatch {
		t.Fatalf("trigger: %s", r)
	}
	pp.Triggers = []string{"push"}
	if _, r := Applies(pp, "push", "feat/x", "main"); r != api.SkipBranchMismatch {
		t.Fatalf("branch: %s", r)
	}
	if _, r := Applies(pp, "push", "main", "main"); r != api.SkipTargetMissing {
		t.Fatalf("target: %s", r)
	}
	pp.Branches = []string{"release/*"}
	pp.Target.Status = api.TargetOK
	if ok, _ := Applies(pp, "push", "release/2026", "main"); !ok {
		t.Fatal("glob branch should match")
	}
	pp.Triggers = []string{"release"}
	if ok, _ := Applies(pp, "release", "", "main"); !ok {
		t.Fatal("release ignores branches")
	}
}

func cs(trigger string, commits int, files ...string) *changeset.ChangeSet {
	c := &changeset.ChangeSet{Trigger: trigger}
	for range commits {
		c.Commits = append(c.Commits, changeset.Commit{SHA: "x"})
	}
	for _, f := range files {
		c.Files = append(c.Files, changeset.File{Path: f, Status: "M"})
	}
	return c
}

func TestDecideScopeMatrix(t *testing.T) {
	m := manifest(t, "version: 2\ncode:\n  openapi: [api/openapi.yaml]\n  exclude: [\"**/*.test.ts\"]\n")
	watermark := changeset.Range{Kind: api.RangeWatermark}
	guides := api.PlanPass{Name: "g", Kind: "guides", Applies: true, Scope: api.PassScope{Paths: []string{"src/ui/**"}}}
	ref := api.PlanPass{Name: "r", Kind: "reference", Applies: true}
	verb := api.PlanPass{Name: "v", Kind: "verbatim", Applies: true, Options: map[string]any{"files": []any{map[string]any{"include": "docs/handbook/**/*.md", "exclude": []any{"docs/handbook/drafts/**"}}}}}
	check := api.PlanPass{Name: "c", Kind: "check", Applies: true}
	changelog := api.PlanPass{Name: "cl", Kind: "changelog", Applies: true}
	unitsPass := api.PlanPass{Name: "u", Kind: "guides", Applies: true, Scope: api.PassScope{Paths: []string{"nothing/**"}, Units: []string{"api"}}}
	hinted := api.PlanPass{Name: "h", Kind: "guides", Applies: true, Scope: api.PassScope{Paths: []string{"nothing/**"}}, Hints: []api.PlanHint{{ID: "hint_1"}}}
	withUnits := cs("push", 1, "src/server/x.go")
	withUnits.Units.Touched = []changeset.TouchedUnit{{Key: "api:get:/x", Kind: "api"}}

	cases := []struct {
		name string
		pass api.PlanPass
		rng  changeset.Range
		cs   *changeset.ChangeSet
		run  bool
		skip string
	}{
		{"guides in scope", guides, watermark, cs("push", 1, "src/ui/page.tsx"), true, ""},
		{"guides out of scope", guides, watermark, cs("push", 1, "src/server/a.go"), false, api.SkipScopeUnchanged},
		{"guides excluded by code.exclude", guides, watermark, cs("push", 1, "src/ui/page.test.ts"), false, api.SkipScopeUnchanged},
		{"empty range", guides, watermark, cs("push", 0), false, api.SkipNoChanges},
		{"range skip wins", guides, changeset.Range{Kind: api.RangeWatermark, Skip: api.SkipStaleHead}, cs("push", 1, "src/ui/a.tsx"), false, api.SkipStaleHead},
		{"survey always runs", guides, changeset.Range{Kind: api.RangeSurvey}, cs("push", 3, "README.md"), true, ""},
		{"reference only its spec", ref, watermark, cs("push", 1, "src/ui/page.tsx"), false, api.SkipScopeUnchanged},
		{"reference spec changed", ref, watermark, cs("push", 1, "api/openapi.yaml"), true, ""},
		{"verbatim mapped file", verb, watermark, cs("push", 1, "docs/handbook/oncall.md"), true, ""},
		{"verbatim excluded file", verb, watermark, cs("push", 1, "docs/handbook/drafts/x.md"), false, api.SkipScopeUnchanged},
		{"check on pr always runs", check, changeset.Range{Kind: api.RangePR}, cs("pr", 0), true, ""},
		{"check on push follows scope", check, watermark, cs("push", 0), false, api.SkipNoChanges},
		{"changelog on release with commits", changelog, changeset.Range{Kind: api.RangeRelease}, cs("release", 2), true, ""},
		{"changelog on push with commits", changelog, watermark, cs("push", 1, "x"), true, ""},
		{"units hit", unitsPass, watermark, withUnits, true, ""},
		{"hints hit", hinted, watermark, cs("push", 1, "src/server/x.go"), true, ""},
		{"plan skip carried", api.PlanPass{Name: "x", Applies: false, SkipReason: api.SkipTargetUnapproved}, watermark, cs("push", 1, "a"), false, api.SkipTargetUnapproved},
		{"working tree runs", guides, changeset.Range{Kind: api.RangeWorkingTree}, cs("", 0, "src/ui/a.tsx"), true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.pass, tc.rng, tc.cs, m)
			if d.Run != tc.run || d.Skip != tc.skip {
				t.Fatalf("decision = %+v, want run=%v skip=%q", d, tc.run, tc.skip)
			}
		})
	}
}

func TestNeedsRun(t *testing.T) {
	skipped := []Decision{{Skip: api.SkipTargetUnapproved}, {Skip: api.SkipStaleHead}}
	if NeedsRun(skipped, true) || NeedsRun(skipped, false) {
		t.Fatal("plan skips and stale heads never create a run")
	}
	advance := []Decision{{Skip: api.SkipScopeUnchanged}}
	if !NeedsRun(advance, true) || NeedsRun(advance, false) {
		t.Fatal("write runs are created to advance watermarks, dry runs are not")
	}
	if !NeedsRun([]Decision{{Run: true}}, false) {
		t.Fatal("work needs a run")
	}
}

func TestRequirePipelines(t *testing.T) {
	if err := RequirePipelines(map[string]bool{"pipelines": true}); err != nil {
		t.Fatal(err)
	}
	if err := RequirePipelines(nil); !errors.Is(err, ErrNoPipelines) {
		t.Fatal(err)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/self/plan" || r.URL.Query().Get("trigger") != "push" {
			t.Errorf("request %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"planHash":"sha256:x","passes":[{"name":"a","kind":"guides","applies":true}]}`)
	}))
	defer srv.Close()
	p, err := Fetch(context.Background(), api.New(srv.URL, "t"), api.PlanQuery{Trigger: "push"})
	if err != nil || p.PlanHash != "sha256:x" || len(p.Passes) != 1 {
		t.Fatalf("plan = %+v %v", p, err)
	}
}
