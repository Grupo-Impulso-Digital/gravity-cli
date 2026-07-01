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

	"github.com/impulso/gravity-cli/internal/agent"
	"github.com/impulso/gravity-cli/internal/api"
)

// scriptedServer returns canned /api/llm/v1/messages responses in sequence and
// records the requests it received.
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
			// Turn 1: model calls a (non-terminal) tool.
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content: []api.ContentPart{
					{Type: api.PartText, Text: "Let me look at the log."},
					{Type: api.PartToolUse, ID: "tu_1", Name: "fake_log", Input: rawInput},
				},
			},
			// Turn 2: model calls the terminal submit tool.
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
			// Assert the model's input reached the tool.
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

	// The second request must echo the assistant turn AND the tool_result.
	if len(script.requests) != 2 {
		t.Fatalf("expected 2 requests to the gateway, got %d", len(script.requests))
	}
	second := script.requests[1]
	// messages: [user kickoff, assistant tool_use, user tool_result]
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

func TestRunner_EndTurnStopsLoop(t *testing.T) {
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
		Tools:  []agent.Tool{agent.ReportFindingsTool()},
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

// TestRunner_StripsEmptyTextBlocks guards the intermittent mid-run 400: a model
// turn that carries an empty text part alongside tool_use must NOT be echoed back
// verbatim (an empty text block draws a provider 400), and a tool returning "" must
// still produce a non-empty tool_result.
func TestRunner_StripsEmptyTextBlocks(t *testing.T) {
	script := &scriptedServer{
		responses: []api.MessagesResponse{
			{
				Type:       "message",
				Role:       "assistant",
				StopReason: api.StopToolUse,
				Content: []api.ContentPart{
					{Type: api.PartText, Text: "   "},                       // empty/whitespace text the provider would reject
					{Type: api.PartToolUse, ID: "tu_1", Name: "empty_tool"}, // no input → must default to {}
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
			Run: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }, // empty output
		}},
	}
	if _, err := runner.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The second request echoes the assistant turn (index 1) and the tool_result (index 2).
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

// TestRunner_RetriesTransientGatewayError guards resilience to the flaky LLM
// gateway: a 502 (provider_error) is retried rather than aborting the run.
func TestRunner_RetriesTransientGatewayError(t *testing.T) {
	old := agent.RetryBackoff
	agent.RetryBackoff = time.Millisecond
	defer func() { agent.RetryBackoff = old }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 { // first two attempts flake with a transient gateway 502
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

	runner := &agent.Runner{Client: api.New(srv.URL, "test-token"), Tools: []agent.Tool{agent.ReportFindingsTool()}}
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
	// Always return a tool_use for the same fake tool so the loop never
	// terminates on its own; assert the cap kicks in.
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
