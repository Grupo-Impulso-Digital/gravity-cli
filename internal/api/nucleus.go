package api

import (
	"context"
	"net/url"
)

// Memory scope types.
const (
	MemoryScopeOrg   = "org"
	MemoryScopeSite  = "site"
	MemoryScopeSpace = "space"
)

// MemoryRef is a reference to a platform object.
type MemoryRef struct {
	RefType string `json:"refType"`
	RefID   string `json:"refId"`
}

// MemoryScope identifies where an atom lives.
type MemoryScope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Memory is one nucleus atom as the platform serializes it.
type Memory struct {
	ID         string      `json:"id"`
	Scope      MemoryScope `json:"scope"`
	Kind       string      `json:"kind"`
	Status     string      `json:"status"`
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	Tags       []string    `json:"tags,omitempty"`
	Confidence float64     `json:"confidence,omitempty"`
	Sources    []MemoryRef `json:"sources,omitempty"`
}

// RecallHit is a Memory plus its ranking.
type RecallHit struct {
	Memory
	Score     float64  `json:"score"`
	MatchedBy []string `json:"matchedBy"`
}

// RecallRequest is the body of both recall endpoints.
type RecallRequest struct {
	Query         string   `json:"query"`
	Limit         int      `json:"limit,omitempty"`
	SpaceID       string   `json:"spaceId,omitempty"`
	Kinds         []string `json:"kinds,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	MinConfidence float64  `json:"minConfidence,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
}

// RecallResponse is the response of both recall endpoints.
type RecallResponse struct {
	Scope MemoryScope `json:"scope"`
	Hits  []RecallHit `json:"hits"`
}

// MemoryUpsertRequest is the body of POST /api/v1/nucleus/memories.
type MemoryUpsertRequest struct {
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	Kind       string      `json:"kind,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	Confidence float64     `json:"confidence,omitempty"`
	Sources    []MemoryRef `json:"sources,omitempty"`
	Scope      string      `json:"scope,omitempty"`
	SiteSlug   string      `json:"siteSlug,omitempty"`
	SpaceID    string      `json:"spaceId,omitempty"`
}

// MemoryUpsertResponse is the response of POST /api/v1/nucleus/memories.
type MemoryUpsertResponse struct {
	Memory  Memory `json:"memory"`
	Outcome string `json:"outcome"`
}

// Recall calls POST /api/v1/sites/:siteSlug/nucleus/recall.
func (c *Client) Recall(ctx context.Context, siteSlug string, req RecallRequest) (*RecallResponse, error) {
	path := "/api/v1/nucleus/recall"
	if siteSlug != "" {
		path = "/api/v1/sites/" + url.PathEscape(siteSlug) + "/nucleus/recall"
	}
	var out RecallResponse
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpsertMemory calls POST /api/v1/nucleus/memories.
func (c *Client) UpsertMemory(ctx context.Context, req MemoryUpsertRequest) (*MemoryUpsertResponse, error) {
	var out MemoryUpsertResponse
	if err := c.Post(ctx, "/api/v1/nucleus/memories", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
