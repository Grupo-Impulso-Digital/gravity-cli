package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
)

func TestMessageUnmarshalStringContent(t *testing.T) {
	// The contract allows content to be a plain string; it must normalise to a
	// single text part.
	raw := `{"role":"user","content":"hello world"}`
	var m api.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Content) != 1 || m.Content[0].Type != api.PartText || m.Content[0].Text != "hello world" {
		t.Errorf("string content not normalised: %+v", m.Content)
	}
}

func TestMessageUnmarshalArrayContent(t *testing.T) {
	raw := `{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"foo","input":{"x":1}}]}`
	var m api.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Content) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(m.Content))
	}
	if m.Content[1].Type != api.PartToolUse || m.Content[1].Name != "foo" {
		t.Errorf("tool_use not parsed: %+v", m.Content[1])
	}
}

func TestMessagesResponseHelpers(t *testing.T) {
	resp := api.MessagesResponse{
		Content: []api.ContentPart{
			{Type: api.PartText, Text: "thinking "},
			{Type: api.PartToolUse, ID: "t1", Name: "submit", Input: json.RawMessage(`{}`)},
			{Type: api.PartText, Text: "more"},
		},
	}
	if got := resp.TextContent(); got != "thinking more" {
		t.Errorf("TextContent = %q", got)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].Name != "submit" {
		t.Errorf("ToolUses = %+v", uses)
	}
}

func TestMessagesEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/llm/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.MessagesResponse{
			ID: "msg_1", Type: "message", Role: "assistant", Model: "m",
			StopReason: api.StopEndTurn,
			Content:    []api.ContentPart{{Type: api.PartText, Text: "ok"}},
			Usage:      api.Usage{InputTokens: 10, OutputTokens: 2},
		})
	}))
	defer srv.Close()
	c := api.New(srv.URL, "t")
	resp, err := c.Messages(context.Background(), api.MessagesRequest{
		Messages: []api.Message{api.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if resp.StopReason != api.StopEndTurn || resp.TextContent() != "ok" {
		t.Errorf("unexpected response: %+v", resp)
	}
}
