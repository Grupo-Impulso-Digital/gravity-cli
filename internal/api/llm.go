package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Roles in a conversation.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Content part types.
const (
	PartText       = "text"
	PartToolUse    = "tool_use"
	PartToolResult = "tool_result"
)

// Stop reasons.
const (
	StopEndTurn = "end_turn"
	StopToolUse = "tool_use"
)

// Tool-choice types.
const (
	ToolChoiceAuto = "auto"
)

// ContentPart is a single piece of message content.
type ContentPart struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Message is one turn in the conversation.
type Message struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

// UnmarshalJSON accepts either a string or an array for `content`.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	if len(raw.Content) == 0 {
		m.Content = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Content, &s); err == nil {
		m.Content = []ContentPart{{Type: PartText, Text: s}}
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(raw.Content, &parts); err != nil {
		return fmt.Errorf("decode message content: %w", err)
	}
	m.Content = parts
	return nil
}

// UserText is a convenience constructor for a plain-text user message.
func UserText(text string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{{Type: PartText, Text: text}}}
}

// ToolResult builds a user message carrying one or more tool results.
func ToolResult(results ...ContentPart) Message {
	return Message{Role: RoleUser, Content: results}
}

// Tool is a tool definition advertised to the model.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolChoice constrains how the model may use tools.
type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// MessagesContext lets the caller scope the platform gateway's RAG to a specific site/space.
type MessagesContext struct {
	Site      string `json:"site,omitempty"`
	Space     string `json:"space,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// MessagesRequest is the request body of POST /api/llm/v1/messages.
type MessagesRequest struct {
	Model       string           `json:"model,omitempty"`
	System      string           `json:"system,omitempty"`
	Messages    []Message        `json:"messages"`
	Tools       []Tool           `json:"tools,omitempty"`
	ToolChoice  *ToolChoice      `json:"tool_choice,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Context     *MessagesContext `json:"context,omitempty"`
}

// Usage reports token counts.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// MessagesResponse is the Anthropic Message returned by the gateway.
type MessagesResponse struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Role       string        `json:"role"`
	Model      string        `json:"model"`
	StopReason string        `json:"stop_reason"`
	Content    []ContentPart `json:"content"`
	Usage      Usage         `json:"usage"`
}

// ToolUses returns the tool_use parts of the response.
func (r *MessagesResponse) ToolUses() []ContentPart {
	var uses []ContentPart
	for _, p := range r.Content {
		if p.Type == PartToolUse {
			uses = append(uses, p)
		}
	}
	return uses
}

// TextContent concatenates all text parts of the response.
func (r *MessagesResponse) TextContent() string {
	var s string
	for _, p := range r.Content {
		if p.Type == PartText {
			s += p.Text
		}
	}
	return s
}

// Messages calls POST /api/llm/v1/messages.
func (c *Client) Messages(ctx context.Context, req MessagesRequest) (*MessagesResponse, error) {
	var out MessagesResponse
	if err := c.Post(ctx, "/api/llm/v1/messages", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
