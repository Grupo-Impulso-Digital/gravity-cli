package run_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func pushOpts() run.Options {
	return run.Options{Trigger: "push", Branch: "main", Mode: api.ModeWrite, Origin: "ci", LeaseTimeout: time.Minute}
}

func TestRunWritesInsideALeaseIngestsAndFinishes(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	head := r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	e := newEnv(t, p, r, manifest(t, "  units:\n    role: [declares, implements]\n"))
	res, err := run.Execute(context.Background(), e.Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if code := res.ExitCode(false); code != 0 {
		t.Fatalf("exit %d: %+v", code, res.Passes)
	}
	connect := p.find("POST", "/repos/connect")[0].Body
	if connect["dryRun"] != false || connect["context"].(map[string]any)["trigger"] != "push" {
		t.Fatalf("connect = %+v", connect)
	}
	start := p.find("POST", "/api/v1/runs")[0].Body
	entry := start["passes"].([]any)[0].(map[string]any)
	if start["headSha"] != head || start["mode"] != "write" || start["clientKey"] != "clientkey-0001" || entry["rangeKind"] != "watermark" || entry["baseSha"] != wm || entry["watermarkSeen"] != wm {
		t.Fatalf("start = %+v", start)
	}
	if _, has := start["manifestHash"]; has && start["manifestHash"] != nil {
		t.Fatalf("write runs never send manifestHash: %v", start["manifestHash"])
	}
	ingest := p.find("POST", "/products/self/inventory")
	if len(ingest) != 1 {
		t.Fatalf("ingest calls = %d", len(ingest))
	}
	unit := ingest[0].Body["units"].([]any)[0].(map[string]any)
	if ingest[0].Body["complete"] != true || unit["key"] != "api:post:/v1/refunds" || fmt.Sprint(unit["roles"]) != "[declares implements]" || ingest[0].Body["headSha"] != head {
		t.Fatalf("ingest = %+v", ingest[0].Body)
	}
	change := p.find("POST", "/changes")[0].Body
	units := change["blocks"].([]any)[0].(map[string]any)["units"]
	if change["op"] != "create" || change["runPassId"] != "ppr_1" || fmt.Sprint(units) != "[api:post:/v1/refunds]" {
		t.Fatalf("a unit the run ingested binds in the same run: %+v", change)
	}
	reports := p.find("POST", "/passes/ppr_1")
	if len(reports) != 2 || reports[0].Body["status"] != "running" || reports[1].Body["status"] != "succeeded" {
		t.Fatalf("pass reports = %+v", reports)
	}
	finish := p.find("POST", "/finish")
	if len(finish) != 1 || finish[0].Body["status"] != "succeeded" {
		t.Fatalf("finish = %+v", finish)
	}
	if res.Passes[0].CostUSD != 0.12 || res.Finish == nil {
		t.Fatalf("result = %+v", res.Passes[0])
	}
}

func TestNoRunRule(t *testing.T) {
	r := newRepo(t)
	first := r.commit("init", map[string]string{"api/openapi.yaml": spec})
	second := r.commit("docs only", map[string]string{"README.md": "readme"})

	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", first)}
	e := newEnv(t, p, r, manifest(t, ""))
	opts := pushOpts()
	opts.Mode = api.ModeDry
	res, err := run.Execute(context.Background(), e.Env, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.find("POST", "/api/v1/runs")) != 0 || res.Passes[0].SkipReason != api.SkipScopeUnchanged {
		t.Fatalf("a dry run whose passes are all unchanged creates no run: %+v", res.Passes)
	}

	p2 := newPlatform(t)
	p2.plan.Passes = []api.PlanPass{refPass("developer-api", first)}
	res, err = run.Execute(context.Background(), newEnv(t, p2, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	start := p2.find("POST", "/api/v1/runs")
	if len(start) != 1 || len(p2.find("POST", "/finish")) != 1 {
		t.Fatalf("a write run with a scope_unchanged pass is created so finish can advance it")
	}
	entry := start[0].Body["passes"].([]any)[0].(map[string]any)
	if entry["skip"] != api.SkipScopeUnchanged || entry["rangeKind"] != "watermark" || entry["watermarkSeen"] != first {
		t.Fatalf("entry = %+v", entry)
	}
	if start[0].Body["rangeKind"] != "watermark" || start[0].Body["baseSha"] != first {
		t.Fatalf("a run of skips still carries the run range: %+v", start[0].Body)
	}
	if len(p2.find("POST", "/changes")) != 0 || res.ExitCode(false) != 0 {
		t.Fatal("no writes for an unchanged scope")
	}

	p3 := newPlatform(t)
	p3.plan.Passes = []api.PlanPass{refPass("developer-api", second)}
	r.git("checkout", "-q", first)
	opts3 := pushOpts()
	res, err = run.Execute(context.Background(), newEnv(t, p3, r, manifest(t, "")).Env, opts3)
	if err != nil {
		t.Fatal(err)
	}
	if len(p3.find("POST", "/api/v1/runs")) != 0 || res.Passes[0].SkipReason != api.SkipStaleHead {
		t.Fatalf("the late job of two pushes skips stale_head without a run: %+v", res.Passes)
	}
	r.git("checkout", "-q", "main")

	p4 := newPlatform(t)
	p4.plan.Passes = []api.PlanPass{refPass("developer-api", second)}
	res, err = run.Execute(context.Background(), newEnv(t, p4, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passes[0].SkipReason != api.SkipNoChanges || len(p4.find("POST", "/api/v1/runs")) != 1 {
		t.Fatalf("head equal to the watermark is no_changes: %+v", res.Passes)
	}

	p5 := newPlatform(t)
	missing := refPass("developer-api", "")
	missing.Applies, missing.SkipReason = false, api.SkipTargetMissing
	p5.plan.Passes = []api.PlanPass{missing}
	res, err = run.Execute(context.Background(), newEnv(t, p5, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(p5.find("POST", "/api/v1/runs")) != 0 || res.ExitCode(false) != 2 {
		t.Fatalf("target_missing exits 2 without a run")
	}
}

func TestLeaseWaitReplansAndTimesOut(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	held := func(w http.ResponseWriter, _ map[string]any) {
		apiErr(w, 409, "lease_held", map[string]any{"retryAfter": 7, "holder": map[string]any{"runId": "prun_9", "headSha": "abcdef1234567"}})
	}
	p.starts = []func(http.ResponseWriter, map[string]any){held}
	e := newEnv(t, p, r, manifest(t, ""))
	if _, err := run.Execute(context.Background(), e.Env, pushOpts()); err != nil {
		t.Fatal(err)
	}
	if len(e.slept) != 1 || e.slept[0] != 7*time.Second || len(p.find("GET", "/self/plan")) != 2 {
		t.Fatalf("slept %v, plans %d", e.slept, len(p.find("GET", "/self/plan")))
	}

	p2 := newPlatform(t)
	p2.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	for range 20 {
		p2.starts = append(p2.starts, held)
	}
	e2 := newEnv(t, p2, r, manifest(t, ""))
	opts := pushOpts()
	opts.LeaseTimeout = 20 * time.Second
	now := time.Now()
	e2.Now = func() time.Time { return now.Add(time.Duration(len(e2.slept)) * 7 * time.Second) }
	_, err := run.Execute(context.Background(), e2.Env, opts)
	if !errors.Is(err, run.ErrLeaseTimeout) || !strings.Contains(err.Error(), "prun_9") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanStaleReplans(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	p.starts = []func(http.ResponseWriter, map[string]any){func(w http.ResponseWriter, _ map[string]any) { apiErr(w, 409, "plan_stale", nil) }}
	if _, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts()); err != nil {
		t.Fatal(err)
	}
	if len(p.find("GET", "/self/plan")) != 2 || len(p.find("POST", "/api/v1/runs")) != 2 {
		t.Fatal("plan_stale must re-plan and start again")
	}
}

func TestLeaseLostStopsWithoutFinish(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	p.routes["POST /api/v1/runs/prun_1/passes/ppr_1"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		apiErr(w, 409, "lease_lost", nil)
	}
	_, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
	if !errors.Is(err, run.ErrStopped) || !errors.Is(err, api.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if len(p.find("POST", "/finish")) != 0 || len(p.find("POST", "/changes")) != 0 {
		t.Fatal("a lost lease stops every call, finish included")
	}
}

func TestHeartbeatLeaseLostStopsTheRun(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	beat := make(chan struct{})
	p.routes["POST /api/v1/runs/prun_1/heartbeat"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		apiErr(w, 409, "lease_lost", nil)
		close(beat)
	}
	p.routes["POST /api/v1/runs/prun_1/passes/ppr_1"] = func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["status"] == "running" {
			<-beat
			time.Sleep(50 * time.Millisecond)
		}
		reply(w, 200, map[string]any{"passRun": map[string]any{}})
	}
	e := newEnv(t, p, r, manifest(t, ""))
	first := true
	e.Sleep = func(ctx context.Context, d time.Duration) error {
		if d == 60*time.Second && first {
			first = false
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := run.Execute(context.Background(), e.Env, pushOpts())
	if !errors.Is(err, run.ErrStopped) || len(p.find("POST", "/finish")) != 0 {
		t.Fatalf("err = %v, finish calls %d", err, len(p.find("POST", "/finish")))
	}
}

func TestPartialFailureAndFindingsExitCodes(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	guides := refPass("guides", "")
	guides.Kind = config.KindGuides
	p.plan.Passes = []api.PlanPass{refPass("developer-api", ""), guides}
	res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passes[1].Status != api.StatusFailed || !strings.Contains(res.Passes[1].Error, "no_provider_key") {
		t.Fatalf("guides = %+v", res.Passes[1])
	}
	if fin := p.find("POST", "/finish")[0].Body; fin["status"] != "partial" || res.ExitCode(false) != 2 {
		t.Fatalf("finish = %+v exit %d", fin, res.ExitCode(false))
	}
	failed := p.find("POST", "/passes/ppr_2")[1].Body
	if failed["status"] != "failed" || failed["error"] == nil {
		t.Fatalf("failed report = %+v", failed)
	}

	p2 := newPlatform(t)
	check := refPass("gate", "")
	check.Kind = config.KindCheck
	check.Options = map[string]any{"claims": false, "coverageMin": 1.0, "failOn": []any{"coverage"}}
	yes := true
	p2.plan.Inventory.Units = []api.Unit{{Key: "feature:x", Kind: "feature", Contributors: []api.Contributor{{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: "implements", Active: &yes}}}}
	p2.plan.Passes = []api.PlanPass{check}
	res, err = run.Execute(context.Background(), newEnv(t, p2, r, manifest(t, "")).Env, pushOpts())
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode(false) != 1 || len(res.Findings()) != 1 {
		t.Fatalf("findings exit 1: %d %+v", res.ExitCode(false), res.Findings())
	}
}

func TestPullRequestRunsAreDryWithTheManifestOverlay(t *testing.T) {
	r := newRepo(t)
	base := r.commit("init", map[string]string{"README.md": "x"})
	r.git("update-ref", "refs/remotes/origin/main", base)
	r.git("checkout", "-q", "-b", "feature")
	head := r.commit("feat: api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	m := manifest(t, "")
	opts := run.Options{Trigger: "pr", Branch: "feature", PR: &api.PRInfo{Number: 42, TargetBranch: "main"}, Origin: "ci", ConnectContext: "pr"}
	res, err := run.Execute(context.Background(), newEnv(t, p, r, m).Env, opts)
	if err != nil {
		t.Fatal(err)
	}
	plan := p.find("GET", "/self/plan")[0].Query
	if plan["mode"][0] != "dry" || plan["manifestHash"][0] != m.Hash || plan["branch"][0] != "main" || plan["trigger"][0] != "pr" {
		t.Fatalf("plan query = %v", plan)
	}
	start := p.find("POST", "/api/v1/runs")[0].Body
	if start["mode"] != "dry" || start["manifestHash"] != m.Hash || start["headSha"] != head || start["pr"].(map[string]any)["number"] != float64(42) {
		t.Fatalf("start = %+v", start)
	}
	entry := start["passes"].([]any)[0].(map[string]any)
	if entry["rangeKind"] != "pr" || entry["baseSha"] != base {
		t.Fatalf("entry = %+v", entry)
	}
	if p.find("POST", "/repos/connect")[0].Body["dryRun"] != true || len(p.find("POST", "/changes")) != 0 || len(p.find("POST", "/products/self/inventory")) != 0 {
		t.Fatal("a pr run never writes, ingests or persists configuration")
	}
	if rec := res.Passes[0].Report.Recorded; rec == nil || len(rec.Changes) != 1 || len(res.Passes[0].Report.Impact) != 1 {
		t.Fatalf("dry runs record the would-be changes: %+v", res.Passes[0].Report)
	}
}

func TestIngestBatchesWithAClosingEntriesCall(t *testing.T) {
	var b strings.Builder
	b.WriteString("openapi: 3.0.0\ninfo: {title: Big, version: 1.0.0}\npaths:\n")
	for i := range 2001 {
		fmt.Fprintf(&b, "  /v1/r%d:\n    get:\n      responses: {'200': {description: ok}}\n", i)
	}
	r := newRepo(t)
	r.commit("big spec", map[string]string{"api/openapi.yaml": b.String()})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	p.routes["POST /api/v1/runs/prun_1/changes"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		reply(w, 201, map[string]any{"change": map[string]any{"id": "chg"}})
	}
	if _, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts()); err != nil {
		t.Fatal(err)
	}
	calls := p.find("POST", "/products/self/inventory")
	if len(calls) != 3 {
		t.Fatalf("ingest calls = %d", len(calls))
	}
	if calls[0].Body["complete"] != false || len(calls[0].Body["units"].([]any)) != 2000 || calls[1].Body["complete"] != false || len(calls[1].Body["units"].([]any)) != 1 {
		t.Fatal("batches must be partial")
	}
	closing := calls[2].Body
	if closing["complete"] != true || closing["units"] != nil || len(closing["entries"].([]any)) != 2001 {
		t.Fatalf("closing call = complete %v units %v entries %d", closing["complete"], closing["units"], len(closing["entries"].([]any)))
	}
}

func TestUnitKeyCollisionFailsBeforeTheRun(t *testing.T) {
	r := newRepo(t)
	r.commit("spec", map[string]string{"api/openapi.yaml": "openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths:\n  /v1/A:\n    get:\n      responses: {'200': {description: ok}}\n  /v1/a:\n    get:\n      responses: {'200': {description: ok}}\n"})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	_, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, pushOpts())
	if err == nil || !strings.Contains(err.Error(), "unit_key_collision") || len(p.find("POST", "/api/v1/runs")) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestParallelPassesKeepPlanOrder(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	second := refPass("partner-api", "")
	second.Target.Space = &api.NamedRef{ID: "sp_2", Slug: "partner", Name: "Partner"}
	third := refPass("same-space", "")
	p.plan.Passes = []api.PlanPass{refPass("developer-api", ""), second, third}
	opts := pushOpts()
	opts.Parallel = 3
	res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, ps := range res.Passes {
		if ps.Status != api.StatusSucceeded {
			t.Fatalf("pass %s = %+v", ps.Name, ps)
		}
		names = append(names, ps.Name)
	}
	if strings.Join(names, ",") != "developer-api,partner-api,same-space" || len(p.find("POST", "/changes")) != 3 {
		t.Fatalf("names = %v", names)
	}
}

func TestSurveyDepthComesFromTheRepositorySetting(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	for i := 0; i < 4; i++ {
		r.commit(fmt.Sprintf("feat: change %d", i), map[string]string{"api/openapi.yaml": spec + fmt.Sprintf("# %d\n", i)})
	}
	for _, tc := range []struct {
		setting *api.RepoSurvey
		want    int
	}{{nil, 5}, {&api.RepoSurvey{MaxCommits: 2}, 2}} {
		p := newPlatform(t)
		p.plan.Repo.Survey = tc.setting
		p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
		opts := pushOpts()
		opts.Mode = api.ModeDry
		res, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts)
		if err != nil {
			t.Fatal(err)
		}
		if res.Range == nil || res.Range.Kind != api.RangeSurvey || res.Commits != tc.want {
			t.Fatalf("setting %+v: range %+v, %d commits, want %d", tc.setting, res.Range, res.Commits, tc.want)
		}
	}
}

func TestNamedPassesAreChecked(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	off := refPass("developer-api", "")
	off.Applies, off.SkipReason, off.Triggers = false, api.SkipTriggerMismatch, []string{"push", "pr"}
	p.plan.Passes = []api.PlanPass{off}
	opts := pushOpts()
	opts.Trigger, opts.Passes = "manual", []string{"developer-api"}
	_, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts)
	var se *run.SelectionError
	if !errors.As(err, &se) || se.Reason != api.SkipTriggerMismatch {
		t.Fatalf("err = %v", err)
	}
	opts.Trigger = "push"
	if _, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts); err != nil {
		t.Fatalf("a CI trigger never fails on a named pass that does not apply: %v", err)
	}
	opts.Passes = []string{"nope"}
	if _, err := run.Execute(context.Background(), newEnv(t, p, r, manifest(t, "")).Env, opts); !errors.As(err, &se) || se.Reason != run.SelectionUnknown {
		t.Fatalf("err = %v", err)
	}
}
