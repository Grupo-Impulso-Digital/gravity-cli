package run_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func manualOpts(mode string) run.Options {
	return run.Options{Trigger: "manual", Branch: "feat/x", Mode: mode, Origin: "local", LeaseTimeout: time.Minute}
}

func TestOlderServersRunWriteRunsWithoutTheSnapshot(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	p.starts = append(p.starts, func(w http.ResponseWriter, _ map[string]any) {
		apiErr(w, 400, "bad_request", map[string]any{"message": "manifestHash is accepted on dry runs only."})
	})
	e := newEnv(t, p, r, manifest(t, ""))
	res, err := run.Execute(context.Background(), e.Env, manualOpts(api.ModeWrite))
	if err != nil {
		t.Fatal(err)
	}
	starts := p.find("POST", "/api/v1/runs")
	if len(starts) != 2 || starts[0].Body["manifestHash"] == nil || starts[1].Body["manifestHash"] != nil {
		t.Fatalf("starts = %+v", starts)
	}
	if res.Snapshot || !strings.Contains(strings.Join(e.log.lines, "\n"), "manifest_snapshot_unsupported") {
		t.Fatalf("snapshot = %v, log = %v", res.Snapshot, e.log.lines)
	}
}

func TestConfirmSeesThePlanAndCanDecline(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	est := 0.4
	p.plan.Passes[0].Estimate = &api.Estimate{AI: true, Commits: 1, ApproxCostUSD: &est}
	e := newEnv(t, p, r, manifest(t, ""))
	var seen run.PlanView
	e.Confirm = func(v run.PlanView) error {
		seen = v
		return run.ErrDeclined
	}
	_, err := run.Execute(context.Background(), e.Env, manualOpts(api.ModeDry))
	if !errors.Is(err, run.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if len(seen.Passes) != 1 || !seen.Passes[0].Run || !seen.Passes[0].AI || seen.AIPasses() != 1 || seen.Passes[0].Commits == 0 {
		t.Fatalf("view = %+v", seen)
	}
	if len(p.find("POST", "/api/v1/runs")) != 0 {
		t.Fatal("a declined plan starts nothing")
	}
}

func TestLeaseWaitsAreReportedWithTheHolder(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	p.starts = append(p.starts, func(w http.ResponseWriter, _ map[string]any) {
		apiErr(w, 409, "lease_held", map[string]any{"retryAfter": 7, "holder": map[string]any{"runId": "prun_9", "headSha": "abc", "expiresAt": "x"}})
	})
	e := newEnv(t, p, r, manifest(t, ""))
	var waits []run.LeaseWait
	e.OnLease = func(w run.LeaseWait) { waits = append(waits, w) }
	if _, err := run.Execute(context.Background(), e.Env, manualOpts(api.ModeWrite)); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0].Holder == nil || waits[0].Holder.RunID != "prun_9" || waits[0].Wait != 7*time.Second || waits[0].Timeout != time.Minute {
		t.Fatalf("waits = %+v", waits)
	}
}

func TestInterruptFinishesTheRunAsCanceled(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.routes["POST /api/v1/runs/prun_1/passes/ppr_1"] = func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		if body["status"] == "running" {
			cancel()
		}
		reply(w, 200, map[string]any{"passRun": map[string]any{"status": body["status"]}})
	}
	e := newEnv(t, p, r, manifest(t, ""))
	_, err := run.Execute(ctx, e.Env, manualOpts(api.ModeWrite))
	if !errors.Is(err, run.ErrInterrupted) {
		t.Fatalf("err = %v", err)
	}
	fin := p.find("POST", "/finish")
	if len(fin) != 1 || fin[0].Body["status"] != api.StatusCancelled {
		t.Fatalf("finish = %+v", fin)
	}
}

func TestADryRunIsRecordedAndReplayedAsAWriteRun(t *testing.T) {
	r := newRepo(t)
	wm := r.commit("init", map[string]string{"README.md": "x"})
	head := r.commit("feat: refunds api", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", wm)}
	m := manifest(t, "")
	e := newEnv(t, p, r, m)
	res, err := run.Execute(context.Background(), e.Env, manualOpts(api.ModeDry))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.find("POST", "/changes")) != 0 {
		t.Fatal("dry runs write nothing")
	}
	rec := run.NewRecording(res, "github.com/acme/billing-api", m.Hash, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	if rec.HeadSHA != head || rec.Changes() != 1 || rec.RunID != "prun_1" {
		t.Fatalf("recording = %+v", rec)
	}
	dir := t.TempDir()
	path, err := run.SaveRecording(dir, rec)
	if err != nil {
		t.Fatal(err)
	}
	if ignore, _ := os.ReadFile(filepath.Join(dir, ".gravity", ".gitignore")); string(ignore) != "*\n" {
		t.Fatalf("ignore = %q", ignore)
	}
	loaded, err := run.LoadRecording(dir, "latest")
	if err != nil || loaded.RunID != "prun_1" || filepath.Base(path) != "prun_1.json" {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	if _, err := run.LoadRecording(dir, "../etc"); err == nil {
		t.Fatal("run ids never escape the recording directory")
	}
	out, err := run.Replay(context.Background(), e.Env, manualOpts(api.ModeWrite), loaded)
	if err != nil {
		t.Fatal(err)
	}
	starts := p.find("POST", "/api/v1/runs")
	last := starts[len(starts)-1].Body
	if last["mode"] != "write" || last["headSha"] != head || last["manifestHash"] != m.Hash || !strings.Contains(last["note"].(string), "prun_1") {
		t.Fatalf("start = %+v", last)
	}
	if n := len(p.find("POST", "/changes")); n != 1 {
		t.Fatalf("changes = %d", n)
	}
	if len(p.find("POST", "/api/llm/v1/messages")) != 0 {
		t.Fatal("a replay calls no model")
	}
	if out.Finish == nil || len(out.Passes) != 1 || out.Passes[0].Status != api.StatusSucceeded {
		t.Fatalf("result = %+v", out)
	}
}
