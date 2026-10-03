package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type scriptedServer struct {
	responses []api.MessagesResponse
	requests  []api.MessagesRequest
	idx       int
}

func newMessagesServer(t *testing.T, s *scriptedServer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/llm/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		var req api.MessagesRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		s.requests = append(s.requests, req)

		if s.idx >= len(s.responses) {
			t.Fatalf("server received more requests (%d) than scripted responses (%d)", s.idx+1, len(s.responses))
		}
		resp := s.responses[s.idx]
		s.idx++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestRunner_DispatchesToolThenTerminates(t *testing.T) {
	rawInput := json.RawMessage(`{"from":"v1.0.0","to":"HEAD"}`)
	submitInput := json.RawMessage(`{"title":"v1.1.0","summary":"A release.","sections":[{"heading":"Added","items":["A new thing"]}]}`)

	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content: []api.ContentPart{
					{Type: api.PartText, Text: "Let me look at the log."},
					{Type: api.PartToolUse, ID: "tu_1", Name: "fake_log", Input: rawInput},
				},
			},
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content: []api.ContentPart{
					{Type: api.PartToolUse, ID: "tu_2", Name: agent.ToolSubmitReleaseNotes, Input: submitInput},
				},
			},
		},
	}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	client := api.New(srv.URL, "test-token")

	var toolRan bool
	fakeTool := agent.Tool{
		Def: api.Tool{Name: "fake_log", Description: "fake", InputSchema: map[string]any{"type": "object"}},
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			toolRan = true
			var in struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				t.Fatalf("tool input: %v", err)
			}
			if in.From != "v1.0.0" || in.To != "HEAD" {
				t.Errorf("unexpected tool input: %+v", in)
			}
			return "abc123 first commit\ndef456 second commit", nil
		},
	}

	runner := &agent.Runner{
		Client: client,
		System: "test",
		Tools:  []agent.Tool{fakeTool, agent.SubmitReleaseNotesTool()},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !toolRan {
		t.Error("expected the non-terminal tool to be dispatched")
	}
	if res.TerminalTool != agent.ToolSubmitReleaseNotes {
		t.Errorf("expected terminal tool %q, got %q", agent.ToolSubmitReleaseNotes, res.TerminalTool)
	}
	if res.Stopped {
		t.Errorf("expected clean termination, got stopped: %s", res.StopReason)
	}
	if res.ToolCalls != 1 {
		t.Errorf("expected 1 non-terminal tool call, got %d", res.ToolCalls)
	}
	if res.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", res.Iterations)
	}

	notes, err := agent.ParseReleaseNotes(res.TerminalInput)
	if err != nil {
		t.Fatalf("parse notes: %v", err)
	}
	if notes.Title != "v1.1.0" {
		t.Errorf("expected title v1.1.0, got %q", notes.Title)
	}
	if len(notes.Sections) != 1 || notes.Sections[0].Heading != "Added" {
		t.Errorf("unexpected sections: %+v", notes.Sections)
	}

	if len(script.requests) != 2 {
		t.Fatalf("expected 2 requests to the gateway, got %d", len(script.requests))
	}
	second := script.requests[1]
	if len(second.Messages) != 3 {
		t.Fatalf("expected 3 messages in second request, got %d", len(second.Messages))
	}
	last := second.Messages[2]
	if last.Role != api.RoleUser || len(last.Content) != 1 || last.Content[0].Type != api.PartToolResult {
		t.Errorf("expected last message to be a user tool_result, got %+v", last)
	}
	if last.Content[0].ToolUseID != "tu_1" {
		t.Errorf("tool_result must reference tu_1, got %q", last.Content[0].ToolUseID)
	}
	if last.Content[0].Content == "" {
		t.Error("tool_result content should carry the tool output")
	}
}

func TestRunner_RejectsInvalidTerminalInput(t *testing.T) {
	badInput := json.RawMessage(`{"summary":"s","upserts":"totally not an array"}`)
	goodInput := json.RawMessage(`{"summary":"s","upserts":[{"key":"h1","type":"heading","content":{"text":"Overview"},"rationale":{"summary":"new section","commits":["a1b2c3d"]}}]}`)

	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content:    []api.ContentPart{{Type: api.PartToolUse, ID: "tu_1", Name: agent.ToolSubmitPageChanges, Input: badInput}},
			},
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content:    []api.ContentPart{{Type: api.PartToolUse, ID: "tu_2", Name: agent.ToolSubmitPageChanges, Input: goodInput}},
			},
		},
	}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client: api.New(srv.URL, "test-token"),
		Tools:  []agent.Tool{agent.SubmitPageChangesTool()},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TerminalTool != agent.ToolSubmitPageChanges {
		t.Fatalf("expected terminal tool after correction, got %q (stopped=%v %s)", res.TerminalTool, res.Stopped, res.StopReason)
	}
	if res.Iterations != 2 {
		t.Errorf("expected 2 iterations (reject + accept), got %d", res.Iterations)
	}
	var page agent.PageChanges
	if err := agent.Decode(res.TerminalInput, &page); err != nil {
		t.Fatalf("parse accepted input: %v", err)
	}
	if len(page.Upserts) != 1 || page.Upserts[0].Key != "h1" {
		t.Errorf("unexpected accepted page: %+v", page)
	}

	if len(script.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(script.requests))
	}
	msgs := script.requests[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != api.RoleUser || len(last.Content) != 1 || last.Content[0].Type != api.PartToolResult {
		t.Fatalf("expected trailing user tool_result, got %+v", last)
	}
	if !last.Content[0].IsError || last.Content[0].ToolUseID != "tu_1" {
		t.Errorf("expected error tool_result for tu_1, got %+v", last.Content[0])
	}
	if !strings.Contains(last.Content[0].Content, "upserts") {
		t.Errorf("rejection should explain the upserts problem, got %q", last.Content[0].Content)
	}
}

func TestRunner_EndTurnStopsLoopWithoutTerminalTool(t *testing.T) {
	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopEndTurn,
				Content:    []api.ContentPart{{Type: api.PartText, Text: "All done, nothing to do."}},
			},
		},
	}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client: api.New(srv.URL, "test-token"),
		Tools: []agent.Tool{{
			Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}},
			Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
		}},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TerminalTool != "" {
		t.Errorf("expected no terminal tool, got %q", res.TerminalTool)
	}
	if res.FinalText == "" {
		t.Error("expected final text to be captured")
	}
}

func findingsUse(id string) api.MessagesResponse {
	return api.MessagesResponse{
		Type: "message", Role: "assistant", StopReason: api.StopToolUse,
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: id, Name: agent.ToolReportFindings, Input: json.RawMessage(`{"findings":[]}`)}},
	}
}

func TestRunner_EndTurnWithoutSubmitIsForcedOnce(t *testing.T) {
	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{Type: "message", Role: "assistant", StopReason: api.StopEndTurn, Content: []api.ContentPart{{Type: api.PartText, Text: "Looks fine."}}},
			findingsUse("tu_f"),
		},
	}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{Client: api.New(srv.URL, "test-token"), Tools: []agent.Tool{agent.ReportFindingsTool()}}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TerminalTool != agent.ToolReportFindings {
		t.Fatalf("expected the forced submit, got %q", res.TerminalTool)
	}
	first, second := script.requests[0].ToolChoice, script.requests[1].ToolChoice
	if first == nil || first.Type != api.ToolChoiceAuto {
		t.Errorf("first turn tool_choice = %+v, want auto", first)
	}
	if second == nil || second.Type != api.ToolChoiceTool || second.Name != agent.ToolReportFindings {
		t.Errorf("follow-up tool_choice = %+v, want tool %s", second, agent.ToolReportFindings)
	}
}

func TestRunner_FinalTurnForcesTerminalTool(t *testing.T) {
	spin := api.MessagesResponse{
		Type: "message", Role: "assistant", StopReason: api.StopToolUse,
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: "tu_x", Name: "spin", Input: json.RawMessage(`{}`)}},
	}
	script := &scriptedServer{responses: []api.MessagesResponse{spin, spin, findingsUse("tu_f")}}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client:        api.New(srv.URL, "test-token"),
		MaxIterations: 3,
		Tools: []agent.Tool{
			{
				Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}},
				Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "spun", nil },
			},
			agent.ReportFindingsTool(),
		},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stopped || res.TerminalTool != agent.ToolReportFindings {
		t.Fatalf("expected a forced submit on the last turn, got stopped=%v terminal=%q", res.Stopped, res.TerminalTool)
	}
	for i, req := range script.requests {
		want := api.ToolChoiceAuto
		if i == 2 {
			want = api.ToolChoiceTool
		}
		if req.ToolChoice == nil || req.ToolChoice.Type != want {
			t.Errorf("request %d tool_choice = %+v, want %s", i, req.ToolChoice, want)
		}
	}
	if script.requests[2].ToolChoice.Name != agent.ToolReportFindings {
		t.Errorf("final turn must name the terminal tool, got %+v", script.requests[2].ToolChoice)
	}
}

func TestRunner_ToolBudgetExhaustionForcesSubmit(t *testing.T) {
	twoSpins := api.MessagesResponse{
		Type: "message", Role: "assistant", StopReason: api.StopToolUse,
		Content: []api.ContentPart{
			{Type: api.PartToolUse, ID: "tu_a", Name: "spin", Input: json.RawMessage(`{}`)},
			{Type: api.PartToolUse, ID: "tu_b", Name: "spin", Input: json.RawMessage(`{}`)},
		},
	}
	script := &scriptedServer{responses: []api.MessagesResponse{twoSpins, findingsUse("tu_f")}}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client:       api.New(srv.URL, "test-token"),
		MaxToolCalls: 1,
		Tools: []agent.Tool{
			{
				Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}},
				Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "spun", nil },
			},
			agent.ReportFindingsTool(),
		},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stopped || res.TerminalTool != agent.ToolReportFindings {
		t.Fatalf("expected a forced submit after the budget ran out, got stopped=%v terminal=%q", res.Stopped, res.TerminalTool)
	}
	results := script.requests[1].Messages[2].Content
	if len(results) != 2 || !results[1].IsError || results[1].ToolUseID != "tu_b" {
		t.Errorf("every tool_use must get a tool_result, the over-budget one an error: %+v", results)
	}
	if tc := script.requests[1].ToolChoice; tc == nil || tc.Type != api.ToolChoiceTool {
		t.Errorf("the turn after the budget ran out must force the submit, got %+v", tc)
	}
}

func TestRunner_StripsEmptyTextBlocks(t *testing.T) {
	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content: []api.ContentPart{
					{Type: api.PartText, Text: "   "},
					{Type: api.PartToolUse, ID: "tu_1", Name: "empty_tool"},
				},
			},
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopEndTurn,
				Content:    []api.ContentPart{{Type: api.PartText, Text: "done"}},
			},
		},
	}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client: api.New(srv.URL, "test-token"),
		Tools: []agent.Tool{{
			Def: api.Tool{Name: "empty_tool", InputSchema: map[string]any{"type": "object"}},
			Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
		}},
	}
	if _, err := runner.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(script.requests) != 2 {
		t.Fatalf("expected 2 gateway requests, got %d", len(script.requests))
	}
	assistant := script.requests[1].Messages[1]
	for _, p := range assistant.Content {
		if p.Type == api.PartText && strings.TrimSpace(p.Text) == "" {
			t.Errorf("empty text block was echoed back to the provider: %+v", assistant.Content)
		}
	}
	if len(assistant.Content) != 1 || assistant.Content[0].Type != api.PartToolUse {
		t.Errorf("expected only the tool_use to survive sanitization, got %+v", assistant.Content)
	}
	if got := strings.TrimSpace(string(assistant.Content[0].Input)); got != "{}" {
		t.Errorf("empty tool_use input must default to {}, got %q", got)
	}
	toolResult := script.requests[1].Messages[2]
	if c := toolResult.Content[0].Content; strings.TrimSpace(c) == "" {
		t.Errorf("empty tool output must be replaced with a placeholder, got %q", c)
	}
}

func TestRunner_RetriesTransientGatewayError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "provider_error", "message": "flaky"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.MessagesResponse{
			Type: "message", Role: "assistant", StopReason: api.StopEndTurn,
			Content: []api.ContentPart{{Type: api.PartText, Text: "ok"}},
		})
	}))
	defer srv.Close()

	client := api.New(srv.URL, "test-token")
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	runner := &agent.Runner{Client: client}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run should succeed after retries: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts (2 transient failures + success), got %d", calls)
	}
	if res.FinalText != "ok" {
		t.Errorf("expected final text from the successful retry, got %q", res.FinalText)
	}
}

func TestRunner_IterationCap(t *testing.T) {
	toolUse := api.MessagesResponse{
		Type:       "message",
		Role:       "assistant",
		StopReason: api.StopToolUse,
		Content: []api.ContentPart{
			{Type: api.PartToolUse, ID: "tu_x", Name: "spin", Input: json.RawMessage(`{}`)},
		},
	}
	var responses []api.MessagesResponse
	for i := 0; i < 5; i++ {
		responses = append(responses, toolUse)
	}
	script := &scriptedServer{responses: responses}
	srv := newMessagesServer(t, script)
	defer srv.Close()

	runner := &agent.Runner{
		Client:        api.New(srv.URL, "test-token"),
		MaxIterations: 3,
		Tools: []agent.Tool{{
			Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}},
			Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "spun", nil },
		}},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Stopped {
		t.Error("expected loop to stop on the iteration cap")
	}
	if res.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", res.Iterations)
	}
}

func TestRunner_ForcedToolChoiceRejectedFallsBackToInstruction(t *testing.T) {
	var reqs []api.MessagesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.MessagesRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		reqs = append(reqs, req)
		w.Header().Set("Content-Type", "application/json")
		if req.ToolChoice != nil && req.ToolChoice.Type == api.ToolChoiceTool {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "invalid_request", "message": "tool_choice forcing is not supported by this model"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(findingsUse("tu_f"))
	}))
	defer srv.Close()

	runner := &agent.Runner{
		Client:        api.New(srv.URL, "test-token"),
		MaxIterations: 1,
		System:        "base",
		Tools:         []agent.Tool{agent.ReportFindingsTool()},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("a rejected forced tool_choice must fall back, got %v", err)
	}
	if res.TerminalTool != agent.ToolReportFindings {
		t.Fatalf("terminal = %q", res.TerminalTool)
	}
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want the forced attempt plus one fallback", len(reqs))
	}
	retry := reqs[1]
	if retry.ToolChoice == nil || retry.ToolChoice.Type != api.ToolChoiceAuto {
		t.Errorf("fallback tool_choice = %+v, want auto", retry.ToolChoice)
	}
	if !strings.HasPrefix(retry.System, "base") || !strings.Contains(retry.System, agent.ToolReportFindings) {
		t.Errorf("fallback system prompt must name the terminal tool, got %q", retry.System)
	}
}

func TestRunner_TokenBudgetForcesSubmitThenStops(t *testing.T) {
	spin := api.MessagesResponse{
		Type: "message", Role: "assistant", StopReason: api.StopToolUse,
		Usage:   api.Usage{InputTokens: 600, OutputTokens: 100},
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: "tu_x", Name: "spin", Input: json.RawMessage(`{}`)}},
	}
	script := &scriptedServer{responses: []api.MessagesResponse{spin, findingsUse("tu_f")}}
	srv := newMessagesServer(t, script)
	defer srv.Close()
	runner := &agent.Runner{
		Client:      api.New(srv.URL, "test-token"),
		TokenBudget: 500,
		Tools: []agent.Tool{
			{Def: api.Tool{Name: "spin", InputSchema: map[string]any{"type": "object"}}, Run: func(context.Context, json.RawMessage) (string, error) { return "spun", nil }},
			agent.ReportFindingsTool(),
		},
	}
	res, err := runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalTool != agent.ToolReportFindings || res.Tokens() != 700 {
		t.Fatalf("terminal=%q tokens=%d", res.TerminalTool, res.Tokens())
	}
	if tc := script.requests[1].ToolChoice; tc == nil || tc.Type != api.ToolChoiceTool {
		t.Fatalf("the turn after the budget ran out must force the submit, got %+v", tc)
	}

	stubborn := &scriptedServer{responses: []api.MessagesResponse{spin, spin}}
	srv2 := newMessagesServer(t, stubborn)
	defer srv2.Close()
	runner.Client = api.New(srv2.URL, "test-token")
	res, err = runner.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stopped || !strings.Contains(res.StopReason, "token budget") {
		t.Fatalf("a forced turn past the budget must stop: %+v", res)
	}
}

func TestRunner_ForcedToolChoiceRejectedUpstreamFallsBack(t *testing.T) {
	var reqs []api.MessagesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.MessagesRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		reqs = append(reqs, req)
		w.Header().Set("Content-Type", "application/json")
		if req.ToolChoice != nil && req.ToolChoice.Type == api.ToolChoiceTool {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "provider_error", "message": "Upstream provider rejected the request (HTTP 400)"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(findingsUse("tu_f"))
	}))
	defer srv.Close()
	client := api.New(srv.URL, "test-token")
	client.Retry = api.RetryPolicy{Attempts: 1}
	runner := &agent.Runner{Client: client, MaxIterations: 1, System: "base", Tools: []agent.Tool{agent.ReportFindingsTool()}}
	res, err := runner.Run(context.Background(), "go")
	if err != nil || res.TerminalTool != agent.ToolReportFindings || len(reqs) != 2 {
		t.Fatalf("a gateway provider_error on a forced turn falls back once: err=%v requests=%d", err, len(reqs))
	}
}
