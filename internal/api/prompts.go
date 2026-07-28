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

// Prompt calls GET /api/llm/v1/prompts/:name, returning the server-hosted system
// prompt for an agent. Hosting prompts server-side lets them improve without a
// CLI release; callers fall back to a baked-in default when this is unavailable
// (the endpoint may not be live yet — the error reports IsUnavailable()).
func (c *Client) Prompt(ctx context.Context, name string) (string, error) {
	var out PromptResponse
	if err := c.Get(ctx, "/api/llm/v1/prompts/"+url.PathEscape(name), nil, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}
