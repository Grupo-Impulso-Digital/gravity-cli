package api

import (
	"context"
	"net/url"
)

// PromptResponse is the response of GET /api/llm/v1/prompts/:name.
type PromptResponse struct {
	Name    string `json:"name"`
	Text    string `json:"text"`
	Version string `json:"version,omitempty"`
}

// Prompt calls GET /api/llm/v1/prompts/:name.
func (c *Client) Prompt(ctx context.Context, name string) (string, error) {
	var out PromptResponse
	if err := c.Get(ctx, "/api/llm/v1/prompts/"+url.PathEscape(name), nil, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}
