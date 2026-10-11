package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const bundleJSON = `{
  "schema": "gravity.instructions/1",
  "version": "sha256:0123456789abcdef",
  "toolContract": "gravity.tools/1",
  "model": {
    "provider": "origin", "id": "anthropic/claude-sonnet-4-6", "label": "Claude Sonnet 4.6",
    "requested": null, "fallback": {"from": "gpt-5", "reason": "no key"},
    "limits": {"maxOutputTokens": 64000, "contextWindow": 200000, "known": true},
    "features": {"forcedToolChoice": true}, "quality": "recommended", "qualityNote": null
  },
  "language": {"code": "fr", "name": "French"},
  "audiences": [{"key": "developers", "label": "Developers", "description": "Builders", "builtin": true}],
  "harness": {"maxTurns": 80, "maxToolCalls": 200, "stepTokens": 4000, "maxContinueNudges": 4},
  "blocks": {"types": ["heading","prose"], "calloutVariants": ["info"], "listVariants": ["bulleted","numbered"]},
  "checks": {"outputMarkers": ["TODO:"]},
  "phases": {"guides.write": "guides-write@abcd1234", "units.map": "units-map@abcd1234"},
  "rendered": {"system": {"guides.write": "You write guides.", "units.map": "Map units."}},
  "tools": {"draft_view": "Shows the draft."},
  "warnings": ["w1"]
}`

func TestInstructionsBundle(t *testing.T) {
	c, s := fixtureServer(t, 200, bundleJSON)
	b, err := c.InstructionsBundle(context.Background(), api.InstructionsQuery{RunPassID: "rp_1", Model: "claude-sonnet-4-6", MaxOutput: 4096})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "GET", "/api/llm/v1/instructions")
	if s.query.Get("runPassId") != "rp_1" || s.query.Get("model") != "claude-sonnet-4-6" || s.query.Get("maxOutput") != "4096" {
		t.Fatalf("query = %v", s.query)
	}
	if b.Model.Limits.MaxOutputTokens != 64000 || !b.Model.Features.ForcedToolChoice || b.Model.Fallback == nil || b.Model.Fallback.From != "gpt-5" {
		t.Fatalf("model = %+v", b.Model)
	}
	if b.Language.Code != "fr" || b.Harness.StepTokens != 4000 || len(b.Audiences) != 1 || b.Checks.OutputMarkers[0] != "TODO:" {
		t.Fatalf("bundle = %+v", b)
	}
	if b.ShortVersion() != "01234567" {
		t.Errorf("ShortVersion = %q", b.ShortVersion())
	}
	if sys, err := b.System(api.PhaseGuidesWrite); err != nil || sys != "You write guides." {
		t.Errorf("System = %q, %v", sys, err)
	}
	if _, err := b.System(api.PhaseGuidesPlan); err == nil {
		t.Error("missing phase accepted")
	}
	if d, ok := b.ToolDescription("draft_view"); !ok || d != "Shows the draft." {
		t.Errorf("ToolDescription = %q %v", d, ok)
	}
}

func TestInstructionsBundleQueryOmitsDefaults(t *testing.T) {
	c, s := fixtureServer(t, 200, bundleJSON)
	if _, err := c.InstructionsBundle(context.Background(), api.InstructionsQuery{RunPassID: "rp_1"}); err != nil {
		t.Fatal(err)
	}
	if s.query.Has("model") || s.query.Has("maxOutput") {
		t.Fatalf("query = %v", s.query)
	}
	if _, err := c.InstructionsBundle(context.Background(), api.InstructionsQuery{}); err == nil {
		t.Fatal("empty run pass id accepted")
	}
}

func TestInstructionsBundleRefusals(t *testing.T) {
	withSchema := func(schema, tools string) string {
		return strings.Replace(strings.Replace(bundleJSON, `"gravity.instructions/1"`, schema, 1), `"gravity.tools/1"`, tools, 1)
	}
	cases := []struct {
		name     string
		status   int
		body     string
		tooOld   bool
		contract bool
		code     string
	}{
		{"route missing", 404, `<html><title>Not Found</title></html>`, true, false, ""},
		{"method not allowed", 405, ``, true, false, ""},
		{"run pass missing", 404, `{"error":{"code":"not_found","message":"Run pass not found"}}`, false, false, api.CodeNotFound},
		{"schema major 2", 200, withSchema(`"gravity.instructions/2"`, `"gravity.tools/1"`), true, false, ""},
		{"schema minor", 200, withSchema(`"gravity.instructions/1.3"`, `"gravity.tools/1.1"`), false, false, ""},
		{"schema missing", 200, withSchema(`""`, `"gravity.tools/1"`), true, false, ""},
		{"other schema", 200, withSchema(`"acme.instructions/1"`, `"gravity.tools/1"`), true, false, ""},
		{"tool major 2", 200, withSchema(`"gravity.instructions/1"`, `"gravity.tools/2"`), false, true, ""},
		{"model conflict", 409, `{"error":{"code":"model_conflict","message":"Model differs"}}`, false, false, api.CodeModelConflict},
		{"run not running", 409, `{"error":{"code":"run_not_running","message":"x"}}`, false, false, api.CodeRunNotRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fixtureServer(t, tc.status, tc.body)
			b, err := c.InstructionsBundle(context.Background(), api.InstructionsQuery{RunPassID: "rp_1"})
			if errors.Is(err, api.ErrServerTooOld) != tc.tooOld || errors.Is(err, api.ErrToolContract) != tc.contract {
				t.Fatalf("err = %v", err)
			}
			if tc.code != "" && !api.HasCode(err, tc.code) {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if !tc.tooOld && !tc.contract && tc.code == "" && (err != nil || b == nil) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestServerTooOldMessage(t *testing.T) {
	want := "this Gravity server is too old for gravity 1.2 (no instructions bundle); upgrade the server or use gravity 1.1"
	if api.ErrServerTooOld.Error() != want {
		t.Fatalf("message = %q", api.ErrServerTooOld.Error())
	}
}

func TestModelUnavailableDetails(t *testing.T) {
	cases := []struct {
		reason string
		hint   string
	}{
		{api.ModelReasonNoKey, "add the provider key"},
		{api.ModelReasonNotEnabled, "enable the model"},
		{api.ModelReasonCatalogUnavailable, "catalog is unreachable"},
		{"", "check Gravity's AI settings"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			body := fmt.Sprintf(`{"error":{"code":"model_unavailable","message":"Model unavailable","details":{"reason":%q,"requested":"gpt-5"}}}`, tc.reason)
			c, _ := fixtureServer(t, 409, body)
			_, err := c.InstructionsBundle(context.Background(), api.InstructionsQuery{RunPassID: "rp_1"})
			var ae *api.APIError
			if !errors.As(err, &ae) || ae.Code != api.CodeModelUnavailable || ae.Reason() != tc.reason || ae.Requested() != "gpt-5" {
				t.Fatalf("err = %#v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.hint) || !strings.Contains(msg, "model gpt-5") || !strings.Contains(msg, "409 model_unavailable") {
				t.Errorf("message = %q", msg)
			}
			if tc.reason != "" && !strings.Contains(msg, "reason: "+tc.reason) {
				t.Errorf("message lacks reason: %q", msg)
			}
		})
	}
}

func TestErrorDetailsShapes(t *testing.T) {
	cases := []struct {
		name    string
		details string
		list    int
		param   string
	}{
		{"list", `[{"path":"x","message":"bad"}]`, 1, ""},
		{"object", `{"reason":"context_too_large","param":"max_tokens","limit":4096}`, 0, "max_tokens"},
		{"absent", `null`, 0, ""},
		{"scalar", `"weird"`, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fixtureServer(t, 400, `{"error":{"code":"provider_bad_request","message":"Bad","details":`+tc.details+`}}`)
			_, err := c.Messages(context.Background(), api.MessagesRequest{})
			var ae *api.APIError
			if !errors.As(err, &ae) || ae.Code != api.CodeProviderBadRequest {
				t.Fatalf("err = %v", err)
			}
			if len(ae.Details) != tc.list || ae.Param() != tc.param {
				t.Fatalf("details = %+v fields = %+v", ae.Details, ae.Fields)
			}
			if tc.name == "object" && (ae.Field("limit") != "4096" || !strings.Contains(err.Error(), "param: max_tokens")) {
				t.Errorf("fields = %+v, message = %q", ae.Fields, err.Error())
			}
		})
	}
}

func TestHaltsRun(t *testing.T) {
	cases := []struct {
		err   error
		halts bool
		stops bool
	}{
		{&api.APIError{StatusCode: 402, Code: api.CodeBudgetExceeded}, true, false},
		{&api.APIError{StatusCode: 402, Code: api.CodeNoProviderKey}, true, false},
		{fmt.Errorf("page x: %w", &api.APIError{StatusCode: 402, Code: api.CodeBudgetExceeded}), true, false},
		{&api.APIError{StatusCode: 409, Code: api.CodeModelUnavailable}, false, false},
		{&api.APIError{StatusCode: 413, Code: api.CodeContextTooLarge}, false, false},
		{&api.APIError{StatusCode: 409, Code: api.CodeLeaseLost}, false, true},
		{&api.APIError{StatusCode: 401}, false, true},
		{errors.New("x"), false, false},
		{nil, false, false},
	}
	for i, tc := range cases {
		if got := api.HaltsRun(tc.err); got != tc.halts {
			t.Errorf("%d: HaltsRun(%v) = %v", i, tc.err, got)
		}
		if got := api.StopsRun(tc.err); got != tc.stops {
			t.Errorf("%d: StopsRun(%v) = %v", i, tc.err, got)
		}
	}
}

func TestMessagesRequestSystemShapes(t *testing.T) {
	cases := []struct {
		name string
		req  api.MessagesRequest
		want string
	}{
		{"string", api.MessagesRequest{System: "be brief"}, `"be brief"`},
		{"absent", api.MessagesRequest{}, ``},
		{"blocks", api.MessagesRequest{System: "ignored", SystemBlocks: api.CachedSystem("be brief")}, `[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Messages = []api.Message{api.UserText("hi")}
			buf, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(buf, &raw); err != nil {
				t.Fatal(err)
			}
			if string(raw["system"]) != tc.want {
				t.Fatalf("system = %s, want %s", raw["system"], tc.want)
			}
			var back api.MessagesRequest
			if err := json.Unmarshal(buf, &back); err != nil {
				t.Fatal(err)
			}
			if back.SystemText() != tc.req.SystemText() || len(back.Messages) != 1 {
				t.Fatalf("round trip = %+v", back)
			}
			if tc.name == "blocks" && (len(back.SystemBlocks) != 1 || back.SystemBlocks[0].CacheControl == nil || back.System != "") {
				t.Fatalf("blocks = %+v", back.SystemBlocks)
			}
		})
	}
}

func TestSystemTextJoinsBlocks(t *testing.T) {
	r := api.MessagesRequest{SystemBlocks: []api.TextBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}}
	if r.SystemText() != "a\n\nb" {
		t.Fatalf("SystemText = %q", r.SystemText())
	}
}

func TestMessagesRequestV2Body(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"content":[]}`)
	req := api.MessagesRequest{
		Model:        "anthropic/claude-sonnet-4-6",
		SystemBlocks: api.CachedSystem("sys"),
		Messages:     api.WithCacheBreakpoint([]api.Message{api.UserText("kickoff")}),
		Context:      &api.MessagesContext{RunID: "r", RunPassID: "rp", Purpose: api.PurposeAuthor, InstructionsVersion: "sha256:ab", Phase: api.PhaseGuidesWrite},
	}
	if _, err := c.Messages(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model    string          `json:"model"`
		System   []api.TextBlock `json:"system"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
		Context map[string]string `json:"context"`
	}
	if err := json.Unmarshal(s.raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "anthropic/claude-sonnet-4-6" || len(body.System) != 1 || body.System[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("body = %s", s.raw)
	}
	if body.Context["instructionsVersion"] != "sha256:ab" || body.Context["phase"] != "guides.write" || body.Context["purpose"] != "author" {
		t.Fatalf("context = %v", body.Context)
	}
	cc, ok := body.Messages[0].Content[0]["cache_control"].(map[string]any)
	if !ok || cc["type"] != "ephemeral" {
		t.Fatalf("content = %v", body.Messages[0].Content)
	}
}

func TestLegacyMessagesBodyHasNoV2Fields(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"content":[]}`)
	if _, err := c.Messages(context.Background(), api.MessagesRequest{System: "s", Messages: []api.Message{api.UserText("x")}, Context: &api.MessagesContext{RunID: "r"}}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"instructionsVersion", "phase", "cache_control", `"model"`} {
		if strings.Contains(string(s.raw), field) {
			t.Errorf("legacy body carries %s: %s", field, s.raw)
		}
	}
}

func TestWithCacheBreakpoint(t *testing.T) {
	cases := []struct {
		name     string
		messages []api.Message
		marked   [][2]int
	}{
		{"empty", nil, nil},
		{"one", []api.Message{api.UserText("a")}, [][2]int{{0, 0}}},
		{"moves to last part", []api.Message{
			{Role: api.RoleUser, Content: []api.ContentPart{{Type: api.PartText, Text: "a", CacheControl: api.EphemeralCache()}}},
			{Role: api.RoleAssistant, Content: []api.ContentPart{{Type: api.PartText, Text: "b"}}},
			api.ToolResult(api.ContentPart{Type: api.PartToolResult, ToolUseID: "t1"}, api.ContentPart{Type: api.PartToolResult, ToolUseID: "t2"}),
		}, [][2]int{{2, 1}}},
		{"empty last message", []api.Message{api.UserText("a"), {Role: api.RoleAssistant}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.messages)
			out := api.WithCacheBreakpoint(tc.messages)
			after, _ := json.Marshal(tc.messages)
			if string(before) != string(after) {
				t.Fatal("input mutated")
			}
			var got [][2]int
			for i, m := range out {
				for j, p := range m.Content {
					if p.CacheControl != nil {
						got = append(got, [2]int{i, j})
					}
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.marked) {
				t.Fatalf("marked = %v, want %v", got, tc.marked)
			}
		})
	}
}

func TestMessagesUsageAndAttribution(t *testing.T) {
	cases := []struct {
		name     string
		headers  map[string]string
		want     api.Attribution
		usage    string
		prompt   int
		cacheHit int
	}{
		{"attributed", map[string]string{"x-gravity-model": "anthropic/claude-sonnet-4-6", "x-gravity-provider": "origin", "x-gravity-fallback": "1"},
			api.Attribution{Model: "anthropic/claude-sonnet-4-6", Provider: "origin", Fallback: true},
			`{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":900,"cache_creation_input_tokens":100}`, 1010, 900},
		{"no fallback", map[string]string{"x-gravity-model": "gpt-4o", "x-gravity-provider": "openai", "x-gravity-fallback": "0"},
			api.Attribution{Model: "gpt-4o", Provider: "openai"}, `{"input_tokens":10,"output_tokens":5}`, 10, 0},
		{"legacy server", nil, api.Attribution{}, `{"input_tokens":3,"output_tokens":1}`, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				_, _ = io.WriteString(w, `{"stop_reason":"max_tokens","content":[{"type":"text","text":"x"}],"usage":`+tc.usage+`}`)
			}))
			defer srv.Close()
			c, _ := newTestClient(srv.URL)
			resp, err := c.Messages(context.Background(), api.MessagesRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Attribution != tc.want || resp.StopReason != api.StopMaxTokens {
				t.Fatalf("resp = %+v", resp)
			}
			if resp.Usage.PromptTokens() != tc.prompt || resp.Usage.CacheReadInputTokens != tc.cacheHit {
				t.Fatalf("usage = %+v", resp.Usage)
			}
		})
	}
}

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.sleeps = append(f.sleeps, d)
	f.now = f.now.Add(d)
	return nil
}

func TestLLMRetriesUntilTotalCap(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		cap        time.Duration
		failFirst  int32
		wantCalls  int32
		wantErr    bool
		firstSleep time.Duration
	}{
		{"recovers after more than three attempts", 503, "", 30 * time.Minute, 5, 6, false, time.Second},
		{"gives up at the cap", 503, "", 2 * time.Minute, 1000, 7, true, time.Second},
		{"honours long retry-after", 429, "120", 30 * time.Minute, 2, 3, false, 120 * time.Second},
		{"retry-after beyond the cap", 429, "600", 5 * time.Minute, 1000, 1, true, 0},
		{"client errors are final", 413, "", 30 * time.Minute, 1000, 1, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) <= tc.failFirst {
					if tc.retryAfter != "" {
						w.Header().Set("Retry-After", tc.retryAfter)
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, `{"error":{"code":"x","message":"y"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"stop_reason":"end_turn","content":[]}`)
			}))
			defer srv.Close()
			c := api.New(srv.URL, "tok")
			clock := &fakeClock{now: time.Unix(0, 0)}
			c.Now, c.Sleep, c.LLMRetryCap = clock.Now, clock.Sleep, tc.cap
			_, err := c.Messages(context.Background(), api.MessagesRequest{})
			if (err != nil) != tc.wantErr || calls.Load() != tc.wantCalls {
				t.Fatalf("err = %v calls = %d sleeps = %v", err, calls.Load(), clock.sleeps)
			}
			var total time.Duration
			for _, d := range clock.sleeps {
				total += d
			}
			if total >= tc.cap {
				t.Fatalf("waited %s, cap %s", total, tc.cap)
			}
			if tc.firstSleep > 0 && (len(clock.sleeps) == 0 || clock.sleeps[0] != tc.firstSleep) {
				t.Fatalf("sleeps = %v", clock.sleeps)
			}
		})
	}
}

func TestLLMRetriesNetworkErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 4 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, `{"content":[]}`)
	}))
	defer srv.Close()
	c := api.New(srv.URL, "tok")
	clock := &fakeClock{now: time.Unix(0, 0)}
	c.Now, c.Sleep = clock.Now, clock.Sleep
	if _, err := c.Messages(context.Background(), api.MessagesRequest{}); err != nil || calls.Load() != 5 {
		t.Fatalf("err = %v calls = %d", err, calls.Load())
	}
}

func TestLLMDeadlineIsLongerThanOtherCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, `{"content":[]}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv.URL)
	c.HTTPClient.Timeout = 50 * time.Millisecond
	c.LLMTimeout = 5 * time.Second
	if _, err := c.Messages(context.Background(), api.MessagesRequest{}); err != nil {
		t.Fatalf("model call: %v", err)
	}
	if err := c.Get(context.Background(), "/x", nil, nil); err == nil {
		t.Fatal("non-model call outlived the client timeout")
	}
	if c.HTTPClient.Timeout != 50*time.Millisecond {
		t.Fatal("client timeout mutated")
	}
}

func TestLLMDefaults(t *testing.T) {
	c := api.New("http://x", "")
	if c.HTTPClient.Timeout != 120*time.Second || c.LLMTimeout != 15*time.Minute || c.LLMRetryCap != 30*time.Minute {
		t.Fatalf("defaults = %s %s %s", c.HTTPClient.Timeout, c.LLMTimeout, c.LLMRetryCap)
	}
}

func TestPlanModelAndLanguage(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"planHash":"h","passes":[
	  {"name":"guides","kind":"guides","model":{"id":"anthropic/claude-sonnet-4-6","label":"Claude Sonnet 4.6","quality":"recommended","limits":{"maxOutputTokens":64000,"contextWindow":200000,"known":true},"fallback":null},"language":{"code":"en","name":"English"}},
	  {"name":"changelog","kind":"changelog","model":{"id":"gpt-5","label":"GPT-5","quality":"untested","limits":{"maxOutputTokens":8192,"contextWindow":128000,"known":false},"unavailable":{"reason":"no_key"}}},
	  {"name":"reference","kind":"reference"}]}`)
	p, err := c.Plan(context.Background(), api.PlanQuery{Trigger: "manual", Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if s.query.Get("model") != "gpt-5" {
		t.Fatalf("query = %v", s.query)
	}
	g, _ := p.PassByName("guides")
	if g.Model == nil || g.Model.Limits.MaxOutputTokens != 64000 || g.Model.Unavailable != nil || g.Language == nil || g.Language.Name != "English" {
		t.Fatalf("guides = %+v", g)
	}
	un := p.UnavailableModels()
	if len(un) != 1 || un[0].Name != "changelog" || un[0].Model.Unavailable.Reason != api.ModelReasonNoKey {
		t.Fatalf("unavailable = %+v", un)
	}
	if (*api.Plan)(nil).UnavailableModels() != nil {
		t.Fatal("nil plan")
	}
	if _, err := c.Plan(context.Background(), api.PlanQuery{Trigger: "manual"}); err != nil || s.query.Has("model") {
		t.Fatalf("query = %v, %v", s.query, err)
	}
}

func TestStartRunModelAndReportGeneration(t *testing.T) {
	cases := []struct {
		name  string
		model string
		gen   *api.Generation
	}{
		{"with model and generation", "claude-sonnet-4-6", &api.Generation{InstructionsVersion: "sha256:ab", Model: "anthropic/claude-sonnet-4-6", Provider: "origin"}},
		{"without", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := fixtureServer(t, 201, `{"run":{"id":"r"},"passes":[],"passRun":{"runPassId":"rp"}}`)
			if _, err := c.StartRun(context.Background(), "", api.StartRunRequest{Trigger: "manual", Model: tc.model}); err != nil {
				t.Fatal(err)
			}
			if got, ok := s.body["model"]; (tc.model == "" && ok) || (tc.model != "" && got != tc.model) {
				t.Fatalf("start body model = %v", s.body["model"])
			}
			if _, err := c.ReportPass(context.Background(), "r", "rp", api.PassReportRequest{Status: api.StatusSucceeded, Generation: tc.gen}); err != nil {
				t.Fatal(err)
			}
			gen, ok := s.body["generation"].(map[string]any)
			if tc.gen == nil {
				if ok {
					t.Fatalf("generation sent: %v", s.body)
				}
				return
			}
			if gen["instructionsVersion"] != "sha256:ab" || gen["model"] != "anthropic/claude-sonnet-4-6" || gen["provider"] != "origin" {
				t.Fatalf("generation = %v", gen)
			}
		})
	}
}
