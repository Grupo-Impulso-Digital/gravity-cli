package api

import "context"

// Site describes a documentation site.
type Site struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Visibility string `json:"visibility,omitempty"`
	Position   int    `json:"position,omitempty"`
}

// SiteSpace is a space within a site tree.
type SiteSpace struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Name          string  `json:"name"`
	Type          string  `json:"type,omitempty"`
	ParentSpaceID *string `json:"parentSpaceId,omitempty"`
}

// SiteCollection is a collection within a site tree.
type SiteCollection struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parentId"`
	SpaceID   string  `json:"spaceId,omitempty"`
	SpaceSlug string  `json:"spaceSlug,omitempty"`
}

// SiteTree is the response of GET /api/v1/sites/{site}.
type SiteTree struct {
	Site        Site             `json:"site"`
	Spaces      []SiteSpace      `json:"spaces"`
	Collections []SiteCollection `json:"collections"`
}

// SiteTree calls GET /api/v1/sites/{site}.
func (c *Client) SiteTree(ctx context.Context, siteSlug string) (*SiteTree, error) {
	var out SiteTree
	if err := c.Get(ctx, "/api/v1/sites"+pathEscape(siteSlug), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Sites calls GET /api/v1/sites.
func (c *Client) Sites(ctx context.Context) ([]Site, error) {
	var out struct {
		Sites []Site `json:"sites"`
	}
	if err := c.Get(ctx, "/api/v1/sites", nil, &out); err != nil {
		return nil, err
	}
	return out.Sites, nil
}

// LLMConfig is the response of GET /api/llm/v1/config.
type LLMConfig struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	Tone               string   `json:"tone"`
	HasKey             bool     `json:"hasKey"`
	AvailableProviders []string `json:"availableProviders"`
}

// LLMConfig calls GET /api/llm/v1/config.
func (c *Client) LLMConfig(ctx context.Context) (*LLMConfig, error) {
	var out LLMConfig
	if err := c.Get(ctx, "/api/llm/v1/config", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
