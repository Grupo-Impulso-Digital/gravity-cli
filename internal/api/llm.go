package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
)

// CacheEphemeral is the only prompt-cache type the gateway forwards.
const CacheEphemeral = "ephemeral"

// CacheControl marks a prompt-cache breakpoint.
type CacheControl struct {
	Type string `json:"type"`
}

// EphemeralCache returns a new ephemeral cache breakpoint.
func EphemeralCache() *CacheControl {
	return &CacheControl{Type: CacheEphemeral}
}

// TextBlock is one block of a block-shaped system prompt.
type TextBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CachedSystem is a system prompt sent as one text block with an ephemeral cache breakpoint.
func CachedSystem(text string) []TextBlock {
	return []TextBlock{{Type: PartText, Text: text, CacheControl: EphemeralCache()}}
}

// Tool-choice types.
const (
	ToolChoiceAuto = "auto"
	ToolChoiceTool = "tool"
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

	CacheControl *CacheControl `json:"cache_control,omitempty"`
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

// WithCacheBreakpoint returns a copy of messages whose last content part of the last message carries an ephemeral cache breakpoint, and no other part does.
func WithCacheBreakpoint(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, m := range messages {
		parts := make([]ContentPart, len(m.Content))
		for j, p := range m.Content {
			p.CacheControl = nil
			parts[j] = p
		}
		out[i] = Message{Role: m.Role, Content: parts}
	}
	if n := len(out); n > 0 {
		if k := len(out[n-1].Content); k > 0 {
			out[n-1].Content[k-1].CacheControl = EphemeralCache()
		}
	}
	return out
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

// Gateway call purposes recorded with a run context.
const (
	PurposePlan     = "plan"
	PurposeAuthor   = "author"
	PurposeCheck    = "check"
	PurposeDistill  = "distill"
	PurposeImpact   = "impact"
	PurposeMapUnits = "map-units"
)

// MessagesContext scopes a gateway call to a site/space or to a pass run.
type MessagesContext struct {
	Site                string `json:"site,omitempty"`
	Space               string `json:"space,omitempty"`
	Namespace           string `json:"namespace,omitempty"`
	RunID               string `json:"runId,omitempty"`
	RunPassID           string `json:"runPassId,omitempty"`
	Purpose             string `json:"purpose,omitempty"`
	InstructionsVersion string `json:"instructionsVersion,omitempty"`
	Phase               string `json:"phase,omitempty"`
}

// MessagesRequest is the request body of POST /api/llm/v1/messages.
type MessagesRequest struct {
	Model        string           `json:"model,omitempty"`
	System       string           `json:"system,omitempty"`
	SystemBlocks []TextBlock      `json:"-"`
	Messages     []Message        `json:"messages"`
	Tools        []Tool           `json:"tools,omitempty"`
	ToolChoice   *ToolChoice      `json:"tool_choice,omitempty"`
	MaxTokens    int              `json:"max_tokens,omitempty"`
	Temperature  *float64         `json:"temperature,omitempty"`
	Context      *MessagesContext `json:"context,omitempty"`
}

type messagesRequestJSON MessagesRequest

// MarshalJSON sends `system` as text blocks when SystemBlocks is set.
func (r MessagesRequest) MarshalJSON() ([]byte, error) {
	if len(r.SystemBlocks) == 0 {
		return json.Marshal(messagesRequestJSON(r))
	}
	return json.Marshal(struct {
		messagesRequestJSON
		System []TextBlock `json:"system"`
	}{messagesRequestJSON(r), r.SystemBlocks})
}

// UnmarshalJSON accepts `system` as a string or as text blocks.
func (r *MessagesRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		messagesRequestJSON
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = MessagesRequest(raw.messagesRequestJSON)
	r.System, r.SystemBlocks = "", nil
	if len(raw.System) == 0 || string(raw.System) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw.System, &r.System); err == nil {
		return nil
	}
	if err := json.Unmarshal(raw.System, &r.SystemBlocks); err != nil {
		return fmt.Errorf("decode system: %w", err)
	}
	return nil
}

// SystemText is the system prompt as one string, text blocks joined with blank lines.
func (r *MessagesRequest) SystemText() string {
	if len(r.SystemBlocks) == 0 {
		return r.System
	}
	texts := make([]string, 0, len(r.SystemBlocks))
	for _, b := range r.SystemBlocks {
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, "\n\n")
}

// Usage reports token counts.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// PromptTokens is the whole prompt size: uncached input plus cache reads and writes.
func (u Usage) PromptTokens() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// Gateway attribution headers.
const (
	HeaderModel    = "X-Gravity-Model"
	HeaderProvider = "X-Gravity-Provider"
	HeaderFallback = "X-Gravity-Fallback"
)

// Attribution is the model the gateway actually used, from its response headers.
type Attribution struct {
	Model    string
	Provider string
	Fallback bool
}

// MessagesResponse is the Anthropic Message returned by the gateway.
type MessagesResponse struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Role        string        `json:"role"`
	Model       string        `json:"model"`
	StopReason  string        `json:"stop_reason"`
	Content     []ContentPart `json:"content"`
	Usage       Usage         `json:"usage"`
	Attribution Attribution   `json:"-"`
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
	var header http.Header
	r := request{method: http.MethodPost, path: "/api/llm/v1/messages", body: req, idempotent: true, llm: true, respHeader: &header}
	if err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	out.Attribution = Attribution{
		Model:    header.Get(HeaderModel),
		Provider: header.Get(HeaderProvider),
		Fallback: strings.TrimSpace(header.Get(HeaderFallback)) == "1",
	}
	return &out, nil
}
