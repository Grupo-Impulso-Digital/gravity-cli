package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type scriptLLM struct {
	replies  []func(req api.MessagesRequest) (*api.MessagesResponse, error)
	requests []api.MessagesRequest
}

func (s *scriptLLM) Messages(_ context.Context, req api.MessagesRequest) (*api.MessagesResponse, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) > len(s.replies) {
		return nil, fmt.Errorf("unscripted request %d", len(s.requests))
	}
	return s.replies[len(s.requests)-1](req)
}

func script(replies ...any) *scriptLLM {
	s := &scriptLLM{}
	for _, r := range replies {
		switch v := r.(type) {
		case api.MessagesResponse:
			s.replies = append(s.replies, func(api.MessagesRequest) (*api.MessagesResponse, error) { return &v, nil })
		case error:
			s.replies = append(s.replies, func(api.MessagesRequest) (*api.MessagesResponse, error) { return nil, v })
		case func(api.MessagesRequest) (*api.MessagesResponse, error):
			s.replies = append(s.replies, v)
		default:
			panic(fmt.Sprintf("unsupported reply %T", r))
		}
	}
	return s
}

type toolUse struct{ id, name, input string }

func uses(calls ...toolUse) api.MessagesResponse {
	r := api.MessagesResponse{StopReason: api.StopToolUse, Usage: api.Usage{InputTokens: 10, OutputTokens: 5}}
	for _, c := range calls {
		r.Content = append(r.Content, api.ContentPart{Type: api.PartToolUse, ID: c.id, Name: c.name, Input: json.RawMessage(c.input)})
	}
	return r
}

func text(stop, s string) api.MessagesResponse {
	r := api.MessagesResponse{StopReason: stop, Usage: api.Usage{InputTokens: 10, OutputTokens: 5}}
	if s != "" {
		r.Content = []api.ContentPart{{Type: api.PartText, Text: s}}
	}
	return r
}

func spin(id string) toolUse { return toolUse{id, "spin", `{}`} }
func submitFindings(id string) toolUse {
	return toolUse{id, agent.ToolReportFindings, `{"findings":[]}`}
}

func spinTool(ran *[]string) agent.Tool {
	return agent.Tool{
		Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			if ran != nil {
				*ran = append(*ran, "spin")
			}
			return "spun", nil
		},
	}
}

func failTool() agent.Tool {
	return agent.Tool{
		Def: api.Tool{Name: "boom", InputSchema: map[string]any{"type": "object"}},
		Run: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("exploded") },
	}
}

func runner(llm agent.LLM, tools ...agent.Tool) *agent.Runner {
	return &agent.Runner{
		Client:           llm,
		System:           "system text",
		Tools:            tools,
		Model:            "anthropic/claude-test",
		Limits:           api.ModelLimits{MaxOutputTokens: 8000, ContextWindow: 200000, Known: true},
		Caps:             api.HarnessLimits{MaxTurns: 20, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
		ForcedToolChoice: true,
		Context:          &api.MessagesContext{RunID: "prun_1", RunPassID: "ppr_1", InstructionsVersion: "sha256:abc", Phase: api.PhaseCheckReview},
	}
}

func lastText(req api.MessagesRequest) string {
	m := req.Messages[len(req.Messages)-1]
	p := m.Content[len(m.Content)-1]
	if p.Type != api.PartText {
		return ""
	}
	return p.Text
}

func TestRunnerStopReasons(t *testing.T) {
	cases := []struct {
		name     string
		llm      *scriptLLM
		wantErr  error
		wantTerm string
		check    func(t *testing.T, s *scriptLLM, res *agent.Result, err error)
	}{
		{
			name:     "tool use then terminal",
			llm:      script(uses(spin("a")), uses(submitFindings("b"))),
			wantTerm: agent.ToolReportFindings,
			check: func(t *testing.T, s *scriptLLM, res *agent.Result, _ error) {
				if res.ToolCalls != 1 || res.Turns != 2 {
					t.Fatalf("toolCalls=%d turns=%d", res.ToolCalls, res.Turns)
				}
				results := s.requests[1].Messages[2].Content
				if len(results) != 1 || results[0].ToolUseID != "a" || results[0].Content != "spun" {
					t.Fatalf("tool results = %+v", results)
				}
			},
		},
		{
			name:     "max tokens notice then recovery",
			llm:      script(api.MessagesResponse{StopReason: api.StopMaxTokens, Content: []api.ContentPart{{Type: api.PartToolUse, ID: "x", Name: "spin", Input: json.RawMessage(`{}`)}}}, uses(submitFindings("b"))),
			wantTerm: agent.ToolReportFindings,
			check: func(t *testing.T, s *scriptLLM, _ *agent.Result, _ error) {
				req := s.requests[1]
				assistant := req.Messages[1]
				if len(assistant.Content) != 1 || assistant.Content[0].Text != "(output truncated)" {
					t.Fatalf("truncated assistant = %+v", assistant.Content)
				}
				want := "Your last reply hit the output limit (~8000 tokens) and its tool calls were discarded. Work in smaller steps: at most ~4000 tokens per tool call."
				if got := lastText(req); got != want {
					t.Fatalf("notice = %q", got)
				}
			},
		},
		{
			name:    "three truncated replies fail with output_limit",
			llm:     script(text(api.StopMaxTokens, "part"), text(api.StopMaxTokens, "part"), text(api.StopMaxTokens, "part")),
			wantErr: agent.ErrOutputLimit,
		},
		{
			name:     "truncation streak resets",
			llm:      script(text(api.StopMaxTokens, "p"), text(api.StopMaxTokens, "p"), uses(spin("a")), text(api.StopMaxTokens, "p"), text(api.StopMaxTokens, "p"), uses(submitFindings("b"))),
			wantTerm: agent.ToolReportFindings,
		},
		{
			name:     "end turn nudges then submit",
			llm:      script(text(api.StopEndTurn, "thinking"), text(api.StopEndTurn, ""), uses(submitFindings("b"))),
			wantTerm: agent.ToolReportFindings,
			check: func(t *testing.T, s *scriptLLM, _ *agent.Result, _ error) {
				if got := lastText(s.requests[1]); got != "Continue. Call report_findings when the work is complete." {
					t.Fatalf("nudge = %q", got)
				}
				if a := s.requests[2].Messages[3]; a.Role != api.RoleAssistant || a.Content[0].Text != "(no output)" {
					t.Fatalf("empty assistant = %+v", a)
				}
				for i, req := range s.requests {
					if req.ToolChoice.Type != api.ToolChoiceAuto {
						t.Fatalf("request %d tool_choice = %+v", i, req.ToolChoice)
					}
				}
			},
		},
		{
			name:    "nudges exhausted fail with no_submit",
			llm:     script(text(api.StopEndTurn, "a"), text(api.StopEndTurn, "b"), text(api.StopEndTurn, "c")),
			wantErr: agent.ErrNoSubmit,
		},
		{
			name:    "refusal fails with its text",
			llm:     script(text(api.StopRefusal, "I can't help with that.")),
			wantErr: agent.ErrRefusal,
			check: func(t *testing.T, _ *scriptLLM, _ *agent.Result, err error) {
				if !strings.Contains(err.Error(), "I can't help with that.") || !strings.Contains(err.Error(), "anthropic/claude-test") {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name:    "gateway error is returned",
			llm:     script(&api.APIError{StatusCode: 402, Code: api.CodeBudgetExceeded}),
			wantErr: nil,
			check: func(t *testing.T, _ *scriptLLM, _ *agent.Result, err error) {
				if !api.HaltsRun(err) {
					t.Fatalf("err = %v, want a run-halting error", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := runner(tc.llm, spinTool(nil), agent.ReportFindingsTool()).Run(context.Background(), "kickoff")
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				var te *agent.TaskError
				if !errors.As(err, &te) || te.Model != "anthropic/claude-test" {
					t.Fatalf("err %v is not a TaskError with the model", err)
				}
			case tc.wantTerm != "":
				if err != nil || res.TerminalTool != tc.wantTerm {
					t.Fatalf("err=%v terminal=%q", err, res.TerminalTool)
				}
			}
			if tc.check != nil {
				tc.check(t, tc.llm, res, err)
			}
		})
	}
}

func TestRunnerToolOrdering(t *testing.T) {
	cases := []struct {
		name      string
		calls     []toolUse
		wantTerm  bool
		wantRan   []string
		wantError map[string]string
	}{
		{name: "terminal listed first runs last", calls: []toolUse{submitFindings("t"), spin("a")}, wantTerm: true, wantRan: []string{"spin"}},
		{name: "earlier failure blocks terminal", calls: []toolUse{{"b", "boom", `{}`}, submitFindings("t")}, wantRan: nil, wantError: map[string]string{"b": "exploded", "t": "was not run"}},
		{name: "unknown tool blocks terminal", calls: []toolUse{{"u", "nope", `{}`}, submitFindings("t")}, wantError: map[string]string{"u": "unknown tool", "t": "was not run"}},
		{name: "second terminal refused", calls: []toolUse{submitFindings("t1"), submitFindings("t2")}, wantError: map[string]string{"t2": "once per turn", "t1": "was not run"}},
		{name: "invalid terminal input is a tool error", calls: []toolUse{{"t", agent.ToolReportFindings, `{"findings":"no"}`}}, wantError: map[string]string{"t": "invalid report_findings input"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			llm := script(uses(tc.calls...), uses(submitFindings("final")))
			res, err := runner(llm, spinTool(&ran), failTool(), agent.ReportFindingsTool()).Run(context.Background(), "kickoff")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(ran, ",") != strings.Join(tc.wantRan, ",") {
				t.Fatalf("ran = %v, want %v", ran, tc.wantRan)
			}
			if tc.wantTerm {
				if res.Turns != 1 {
					t.Fatalf("turns = %d, want the terminal on the first turn", res.Turns)
				}
				return
			}
			results := llm.requests[1].Messages[2].Content
			if len(results) != len(tc.calls) {
				t.Fatalf("results = %+v", results)
			}
			for i, r := range results {
				if r.ToolUseID != tc.calls[i].id {
					t.Fatalf("result %d is for %s, want %s", i, r.ToolUseID, tc.calls[i].id)
				}
				if want, ok := tc.wantError[r.ToolUseID]; ok && (!r.IsError || !strings.Contains(r.Content, want)) {
					t.Fatalf("result %s = %+v, want an error containing %q", r.ToolUseID, r, want)
				}
			}
		})
	}
}

func finishUnitsTool() agent.Tool {
	return agent.Tool{
		Def:      api.Tool{Name: "finish_units", InputSchema: map[string]any{"type": "object"}},
		Terminal: true,
	}
}

func TestRunnerCaps(t *testing.T) {
	spins := func(n int) []any {
		out := make([]any, 0, n)
		for i := range n {
			out = append(out, uses(spin(fmt.Sprintf("s%d", i))))
		}
		return out
	}
	cases := []struct {
		name    string
		caps    api.HarnessLimits
		forced  bool
		replies []any
		submit  agent.Tool
		check   func(t *testing.T, s *scriptLLM, res *agent.Result, err error)
	}{
		{
			name:    "forced tool choice from cap minus three",
			caps:    api.HarnessLimits{MaxTurns: 5, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
			forced:  true,
			replies: append(spins(3), uses(submitFindings("f"))),
			submit:  agent.ReportFindingsTool(),
			check: func(t *testing.T, s *scriptLLM, res *agent.Result, err error) {
				if err != nil || !res.Forced || res.Partial {
					t.Fatalf("err=%v forced=%v partial=%v", err, res.Forced, res.Partial)
				}
				for i, req := range s.requests {
					want := api.ToolChoiceAuto
					if i >= 2 {
						want = api.ToolChoiceTool
					}
					if req.ToolChoice.Type != want {
						t.Fatalf("request %d tool_choice = %+v, want %s", i, req.ToolChoice, want)
					}
				}
				if r := s.requests[3].Messages[6].Content[0]; !r.IsError || !strings.Contains(r.Content, "only report_findings") {
					t.Fatalf("forced turn spin result = %+v", r)
				}
			},
		},
		{
			name:    "user text when forced tool choice is unsupported",
			caps:    api.HarnessLimits{MaxTurns: 4, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
			replies: append(spins(1), uses(submitFindings("f"))),
			submit:  agent.ReportFindingsTool(),
			check: func(t *testing.T, s *scriptLLM, _ *agent.Result, err error) {
				if err != nil {
					t.Fatal(err)
				}
				req := s.requests[1]
				if req.ToolChoice.Type != api.ToolChoiceAuto || !strings.Contains(lastText(req), "Call report_findings now") {
					t.Fatalf("tool_choice=%+v last=%q", req.ToolChoice, lastText(req))
				}
				if n := len(req.Messages[2].Content); n != 2 {
					t.Fatalf("the instruction joins the tool results message, got %d parts", n)
				}
			},
		},
		{
			name:    "turn cap exhausted",
			caps:    api.HarnessLimits{MaxTurns: 3, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 9},
			forced:  true,
			replies: []any{text(api.StopEndTurn, "a"), text(api.StopEndTurn, "b"), text(api.StopEndTurn, "c")},
			submit:  agent.ReportFindingsTool(),
			check: func(t *testing.T, _ *scriptLLM, _ *agent.Result, err error) {
				if !errors.Is(err, agent.ErrCapReached) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name:    "tool-call cap forces the terminal",
			caps:    api.HarnessLimits{MaxTurns: 20, MaxToolCalls: 1, StepTokens: 4000, MaxContinueNudges: 2},
			forced:  true,
			replies: []any{uses(spin("a"), spin("b")), uses(submitFindings("f"))},
			submit:  agent.ReportFindingsTool(),
			check: func(t *testing.T, s *scriptLLM, res *agent.Result, err error) {
				if err != nil || res.ToolCalls != 1 || !res.Forced {
					t.Fatalf("err=%v calls=%d forced=%v", err, res.ToolCalls, res.Forced)
				}
				results := s.requests[1].Messages[2].Content
				if results[0].IsError || !results[1].IsError || !strings.Contains(results[1].Content, "tool-call cap of 1") {
					t.Fatalf("results = %+v", results)
				}
				if s.requests[1].ToolChoice.Type != api.ToolChoiceTool {
					t.Fatalf("tool_choice = %+v", s.requests[1].ToolChoice)
				}
			},
		},
		{
			name:    "forced list terminal is partial",
			caps:    api.HarnessLimits{MaxTurns: 3, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
			forced:  true,
			replies: []any{uses(toolUse{"f", "finish_units", `{"complete":true}`})},
			submit:  finishUnitsTool(),
			check: func(t *testing.T, _ *scriptLLM, res *agent.Result, err error) {
				if err != nil || !res.Partial || string(res.TerminalInput) != `{"complete":false}` {
					t.Fatalf("err=%v partial=%v input=%s", err, res.Partial, res.TerminalInput)
				}
			},
		},
		{
			name:    "unforced list terminal is complete",
			caps:    api.HarnessLimits{MaxTurns: 10, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
			forced:  true,
			replies: []any{uses(toolUse{"f", "finish_units", `{"complete":true}`})},
			submit:  finishUnitsTool(),
			check: func(t *testing.T, _ *scriptLLM, res *agent.Result, err error) {
				if err != nil || res.Partial || string(res.TerminalInput) != `{"complete":true}` {
					t.Fatalf("err=%v partial=%v input=%s", err, res.Partial, res.TerminalInput)
				}
			},
		},
		{
			name:    "forced terminal with errors fails the task",
			caps:    api.HarnessLimits{MaxTurns: 3, MaxToolCalls: 50, StepTokens: 4000, MaxContinueNudges: 2},
			forced:  true,
			replies: []any{uses(toolUse{"f", agent.ToolReportFindings, `{"findings":"bad"}`})},
			submit:  agent.ReportFindingsTool(),
			check: func(t *testing.T, _ *scriptLLM, _ *agent.Result, err error) {
				if !errors.Is(err, agent.ErrForcedSubmitRejected) {
					t.Fatalf("err = %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := script(tc.replies...)
			r := runner(llm, spinTool(nil), tc.submit)
			r.Caps = tc.caps
			r.ForcedToolChoice = tc.forced
			res, err := r.Run(context.Background(), "kickoff")
			tc.check(t, llm, res, err)
		})
	}
}

func TestRunnerForcedToolChoiceRejectedFallsBackToUserText(t *testing.T) {
	llm := script(
		&api.APIError{StatusCode: 400, Code: api.CodeProviderBadRequest, Message: "tool_choice not supported"},
		uses(submitFindings("f")),
	)
	r := runner(llm, agent.ReportFindingsTool())
	r.Caps.MaxTurns = 1
	res, err := r.Run(context.Background(), "kickoff")
	if err != nil || res.TerminalTool != agent.ToolReportFindings {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	retry := llm.requests[1]
	if retry.ToolChoice.Type != api.ToolChoiceAuto || !strings.Contains(lastText(retry), "Call report_findings now") {
		t.Fatalf("retry tool_choice=%+v last=%q", retry.ToolChoice, lastText(retry))
	}
}

func TestRunnerMaxTokens(t *testing.T) {
	cases := []struct {
		name   string
		limits api.ModelLimits
		want   func(firstEstimate int) int
	}{
		{name: "model max output", limits: api.ModelLimits{MaxOutputTokens: 8000, ContextWindow: 200000}, want: func(int) int { return 8000 }},
		{name: "bounded by the context window", limits: api.ModelLimits{MaxOutputTokens: 64000, ContextWindow: 20000}, want: func(est int) int { return 20000 - est - 2000 }},
		{name: "defaults when unknown", limits: api.ModelLimits{}, want: func(int) int { return agent.DefaultMaxOutputTokens }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := script(uses(submitFindings("f")))
			r := runner(llm, agent.ReportFindingsTool())
			r.Limits = tc.limits
			if _, err := r.Run(context.Background(), "kickoff"); err != nil {
				t.Fatal(err)
			}
			req := llm.requests[0]
			defs, _ := json.Marshal(req.Tools)
			msgs, _ := json.Marshal(api.UserText("kickoff"))
			est := len(msgs)/3 + (len(r.System)+len(defs))/3
			if got, want := req.MaxTokens, tc.want(est); got != want {
				t.Fatalf("max_tokens = %d, want ~%d", got, want)
			}
		})
	}
}

func TestRunnerSendsCachedSystemAndBreakpoint(t *testing.T) {
	llm := script(uses(spin("a")), uses(submitFindings("f")))
	if _, err := runner(llm, spinTool(nil), agent.ReportFindingsTool()).Run(context.Background(), "kickoff"); err != nil {
		t.Fatal(err)
	}
	for i, req := range llm.requests {
		if req.System != "" || len(req.SystemBlocks) != 1 || req.SystemBlocks[0].Text != "system text" || req.SystemBlocks[0].CacheControl == nil {
			t.Fatalf("request %d system = %q %+v", i, req.System, req.SystemBlocks)
		}
		marks := 0
		for mi, m := range req.Messages {
			for pi, p := range m.Content {
				if p.CacheControl != nil {
					marks++
					if mi != len(req.Messages)-1 || pi != len(m.Content)-1 {
						t.Fatalf("request %d breakpoint on message %d part %d", i, mi, pi)
					}
				}
			}
		}
		if marks != 1 {
			t.Fatalf("request %d has %d breakpoints", i, marks)
		}
		if req.Context.InstructionsVersion != "sha256:abc" || req.Model != "anthropic/claude-test" {
			t.Fatalf("request %d context=%+v model=%q", i, req.Context, req.Model)
		}
	}
}

func bigTool(name string, size int) agent.Tool {
	return agent.Tool{
		Def: api.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
		Run: func(context.Context, json.RawMessage) (string, error) { return strings.Repeat("x", size), nil },
	}
}

func TestRunnerElidesOldToolResults(t *testing.T) {
	llm := script(
		uses(toolUse{"r1", "read_file", `{"path":"a.go"}`}),
		uses(toolUse{"d1", "draft_view", `{}`}),
		uses(toolUse{"r2", "read_file", `{"path":"b.go"}`}),
		uses(toolUse{"r3", "read_file", `{"path":"c.go"}`}),
		uses(submitFindings("f")),
	)
	r := runner(llm, bigTool("read_file", 9000), bigTool("draft_view", 9000), agent.ReportFindingsTool())
	r.Limits = api.ModelLimits{MaxOutputTokens: 1000, ContextWindow: 20000}
	r.Caps.StepTokens = 500
	for i := range llm.replies {
		reply := llm.replies[i]
		llm.replies[i] = func(req api.MessagesRequest) (*api.MessagesResponse, error) {
			resp, err := reply(req)
			if resp != nil {
				b, _ := json.Marshal(req.Messages)
				resp.Usage = api.Usage{InputTokens: len(b) / 3}
			}
			return resp, err
		}
	}
	if _, err := r.Run(context.Background(), "kickoff"); err != nil {
		t.Fatal(err)
	}
	last := llm.requests[4]
	got := map[string]string{}
	for _, m := range last.Messages {
		for _, p := range m.Content {
			if p.Type == api.PartToolResult {
				got[p.ToolUseID] = p.Content
			}
		}
	}
	if !strings.HasPrefix(got["r1"], "[elided: read_file {\"path\":\"a.go\"}; call it again if needed]") {
		t.Fatalf("oldest result not elided: %.60q", got["r1"])
	}
	if strings.HasPrefix(got["d1"], "[elided") {
		t.Fatal("the latest draft_view must never be elided")
	}
	if strings.HasPrefix(got["r3"], "[elided") {
		t.Fatal("the newest result should survive")
	}
	if k := last.Messages[0].Content[0].Text; k != "kickoff" {
		t.Fatalf("kickoff changed: %q", k)
	}
}

func TestRunnerContextTooLarge(t *testing.T) {
	tooLarge := &api.APIError{StatusCode: 413, Code: api.CodeContextTooLarge}
	cases := []struct {
		name    string
		replies []any
		wantErr error
	}{
		{name: "elide once and retry", replies: []any{uses(spin("a")), uses(spin("b")), tooLarge, uses(submitFindings("f"))}},
		{name: "second 413 fails with context_limit", replies: []any{uses(spin("a")), uses(spin("b")), tooLarge, tooLarge}, wantErr: agent.ErrContextLimit},
		{name: "nothing to elide fails with context_limit", replies: []any{tooLarge}, wantErr: agent.ErrContextLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := script(tc.replies...)
			res, err := runner(llm, bigTool("spin", 3000), agent.ReportFindingsTool()).Run(context.Background(), "kickoff")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || res.TerminalTool != agent.ToolReportFindings || res.Turns != 3 {
				t.Fatalf("err=%v res=%+v", err, res)
			}
			retry := llm.requests[3]
			if c := retry.Messages[2].Content[0].Content; !strings.HasPrefix(c, "[elided: spin") {
				t.Fatalf("retry did not elide: %.40q", c)
			}
		})
	}
}

func TestRunnerUsage(t *testing.T) {
	first := uses(spin("a"))
	first.Usage = api.Usage{InputTokens: 100, OutputTokens: 20, CacheCreationInputTokens: 1000}
	first.Attribution = api.Attribution{Model: "anthropic/claude-served"}
	second := uses(submitFindings("f"))
	second.Usage = api.Usage{InputTokens: 50, OutputTokens: 10, CacheReadInputTokens: 1000}
	res, err := runner(script(first, second), spinTool(nil), agent.ReportFindingsTool()).Run(context.Background(), "kickoff")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 || res.InputTokens != 2150 || res.OutputTokens != 30 || res.CacheReadTokens != 1000 || res.CacheWriteTokens != 1000 || res.Model != "anthropic/claude-served" {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunnerOverHTTP(t *testing.T) {
	var bodies []map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "provider_error", "message": "flaky"}})
			return
		}
		w.Header().Set(api.HeaderModel, "anthropic/claude-test")
		_ = json.NewEncoder(w).Encode(uses(submitFindings("f")))
	}))
	defer srv.Close()
	client := api.New(srv.URL, "test-token")
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	res, err := runner(client, agent.ReportFindingsTool()).Run(context.Background(), "kickoff")
	if err != nil || res.TerminalTool != agent.ToolReportFindings || res.Model != "anthropic/claude-test" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	system, ok := bodies[1]["system"].([]any)
	if !ok || len(system) != 1 || system[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("system on the wire = %#v", bodies[1]["system"])
	}
}
