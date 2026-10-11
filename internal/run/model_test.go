package run_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func withModel(pp api.PlanPass, m api.PlanModel) api.PlanPass {
	pp.Model = &m
	pp.Language = &api.Language{Code: "en", Name: "English"}
	return pp
}

func proseRef(name string) api.PlanPass {
	pp := refPass(name, "")
	pp.Options = map[string]any{"prose": true}
	return pp
}

func TestUnavailableModelStopsBeforeTheConfirm(t *testing.T) {
	unavailable := api.PlanModel{ID: "openai/gpt-x", Label: "GPT X", Unavailable: &api.ModelUnavailable{Reason: api.ModelReasonNoKey}}
	skipped := withModel(guidesPass("guides"), unavailable)
	skipped.Applies, skipped.SkipReason = false, api.SkipDisabled
	cases := []struct {
		name   string
		passes []api.PlanPass
		stops  bool
		want   string
	}{
		{name: "running AI pass", passes: []api.PlanPass{withModel(guidesPass("guides"), unavailable)}, stops: true, want: "pass guides: model GPT X has no provider key in Gravity"},
		{name: "reference with prose", passes: []api.PlanPass{refPass("developer-api", ""), withModel(proseRef("prose-api"), api.PlanModel{ID: "m", Unavailable: &api.ModelUnavailable{Reason: api.ModelReasonNotEnabled}})}, stops: true, want: "pass prose-api: model m is not enabled"},
		{name: "pass without a model call", passes: []api.PlanPass{withModel(refPass("developer-api", ""), unavailable)}},
		{name: "skipped pass", passes: []api.PlanPass{refPass("developer-api", ""), skipped}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.commit("init", map[string]string{"api/openapi.yaml": spec})
			p := newPlatform(t)
			p.plan.Passes = tc.passes
			e := newEnv(t, p, r, manifest(t, ""))
			confirmed := false
			e.Confirm = func(run.PlanView) error {
				confirmed = true
				return nil
			}
			_, err := run.Execute(context.Background(), e.Env, pushOpts())
			var mu *run.ModelUnavailableError
			if got := errors.As(err, &mu); got != tc.stops {
				t.Fatalf("err = %v", err)
			}
			if !tc.stops {
				if err != nil || !confirmed {
					t.Fatalf("err = %v confirmed = %v", err, confirmed)
				}
				return
			}
			if confirmed || len(p.find("POST", "/api/v1/runs")) != 0 {
				t.Fatal("an unusable model stops the run before the confirm and the start")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--model") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestModelFlagReachesThePlanTheStartAndTheBundle(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		maxOutput int
		quality   string
		confirm   bool
		warned    bool
	}{
		{name: "configured model", quality: api.QualityRecommended, confirm: true},
		{name: "model flag with a lower output limit", model: "openai/gpt-x", maxOutput: 4096, quality: api.QualitySupported, confirm: true},
		{name: "not recommended without a plan view", model: "openai/gpt-x", quality: api.QualityNotRecommended, warned: true},
		{name: "not recommended after the plan view", model: "openai/gpt-x", quality: api.QualityNotRecommended, confirm: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.commit("init", map[string]string{"api/openapi.yaml": spec})
			p := newPlatform(t)
			p.plan.Passes = []api.PlanPass{withModel(guidesPass("guides"), api.PlanModel{ID: "anthropic/claude-test", Label: "Claude Test", Quality: tc.quality})}
			p.llm = func(map[string]any) any { return toolReply("finish_plan", map[string]any{"summary": "nothing"}) }
			p.routes["GET /api/llm/v1/instructions"] = func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				b := testBundle()
				b["model"].(map[string]any)["quality"] = tc.quality
				b["model"].(map[string]any)["qualityNote"] = "loses tool calls"
				reply(w, 200, b)
			}
			e := newEnv(t, p, r, manifest(t, ""))
			var view *run.PlanView
			if tc.confirm {
				e.Confirm = func(v run.PlanView) error {
					view = &v
					return nil
				}
			}
			opts := pushOpts()
			opts.Model, opts.MaxOutput = tc.model, tc.maxOutput
			if _, err := run.Execute(context.Background(), e.Env, opts); err != nil {
				t.Fatal(err)
			}
			for _, c := range p.find("GET", "/repos/self/plan") {
				if got := strings.Join(c.Query["model"], ","); got != tc.model {
					t.Fatalf("plan model = %q", got)
				}
			}
			starts := p.find("POST", "/api/v1/runs")
			if got, _ := starts[0].Body["model"].(string); got != tc.model {
				t.Fatalf("start model = %q", got)
			}
			bundle := p.find("GET", "/api/llm/v1/instructions")[0].Query
			if got := strings.Join(bundle["model"], ","); got != tc.model {
				t.Fatalf("bundle model = %q", got)
			}
			wantMax := ""
			if tc.maxOutput > 0 {
				wantMax = "4096"
			}
			if got := strings.Join(bundle["maxOutput"], ","); got != wantMax {
				t.Fatalf("bundle maxOutput = %q", got)
			}
			if tc.confirm {
				pv := view.Passes[0]
				if pv.Model == nil || pv.Model.Label != "Claude Test" || pv.Language == nil || pv.Language.Code != "en" {
					t.Fatalf("plan view = %+v", pv)
				}
			}
			logged := strings.Join(e.log.lines, "\n")
			if got := strings.Contains(logged, "warn model_not_recommended: guides: Claude Test is rated not recommended for documentation runs (loses tool calls)"); got != tc.warned {
				t.Fatalf("log = %s", logged)
			}
		})
	}
}

func TestRecordingKeepsGenerationAndUsageAndReplayForwardsIt(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{guidesPass("guides")}
	p.llm = func(map[string]any) any {
		return map[string]any{"stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 120, "output_tokens": 30}, "content": []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": "finish_plan", "input": map[string]any{"summary": "nothing"}}}}
	}
	m := manifest(t, "")
	e := newEnv(t, p, r, m)
	res, err := run.Execute(context.Background(), e.Env, manualOpts(api.ModeDry))
	if err != nil {
		t.Fatal(err)
	}
	if g := res.Passes[0].Generation; g == nil || g.InstructionsVersion != "sha256:test" {
		t.Fatalf("pass result = %+v", res.Passes[0])
	}
	rec := run.NewRecording(res, "github.com/acme/billing-api", m.Hash, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	if _, err := run.SaveRecording(dir, rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := run.LoadRecording(dir, rec.RunID)
	if err != nil {
		t.Fatal(err)
	}
	rp := loaded.Passes[0]
	if rp.Generation == nil || rp.Generation.Model != "anthropic/claude-test" || rp.Generation.Provider != "anthropic" || rp.Generation.InstructionsVersion != "sha256:test" {
		t.Fatalf("generation = %+v", rp.Generation)
	}
	if rp.Usage == nil || *rp.Usage != (run.RecordedUsage{Turns: 1, InputTokens: 120, OutputTokens: 30}) {
		t.Fatalf("usage = %+v", rp.Usage)
	}
	calls := len(p.find("POST", "/api/llm/v1/messages"))
	before := len(p.find("POST", "/passes/ppr_1"))
	out, err := run.Replay(context.Background(), e.Env, manualOpts(api.ModeWrite), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.find("POST", "/api/llm/v1/messages")) != calls {
		t.Fatal("a replay calls no model")
	}
	reports := p.find("POST", "/passes/ppr_1")[before:]
	gen, _ := reports[len(reports)-1].Body["generation"].(map[string]any)
	if gen["model"] != "anthropic/claude-test" || gen["instructionsVersion"] != "sha256:test" || gen["provider"] != "anthropic" {
		t.Fatalf("replayed report = %+v", reports[len(reports)-1].Body)
	}
	if out.Passes[0].Generation == nil {
		t.Fatalf("replayed pass = %+v", out.Passes[0])
	}
}

func TestRecordingWithoutModelCallsHasNoGeneration(t *testing.T) {
	r := newRepo(t)
	r.commit("init", map[string]string{"api/openapi.yaml": spec})
	p := newPlatform(t)
	p.plan.Passes = []api.PlanPass{refPass("developer-api", "")}
	m := manifest(t, "")
	res, err := run.Execute(context.Background(), newEnv(t, p, r, m).Env, manualOpts(api.ModeDry))
	if err != nil {
		t.Fatal(err)
	}
	rec := run.NewRecording(res, "github.com/acme/billing-api", m.Hash, time.Now())
	if len(rec.Passes) != 1 || rec.Passes[0].Generation != nil || rec.Passes[0].Usage != nil {
		t.Fatalf("recording = %+v", rec.Passes)
	}
}
