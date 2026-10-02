package api

import (
	"context"
	"encoding/json"
)

// Memory scope types.
const (
	MemoryScopeOrg   = "org"
	MemoryScopeSite  = "site"
	MemoryScopeSpace = "space"
)

// Memory source types added for pipelines.
const (
	SourceRepo        = "repo"
	SourceCommit      = "commit"
	SourceUnit        = "unit"
	SourcePipelineRun = "pipeline_run"
)

// MemoryScope identifies where an atom lives.
type MemoryScope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// MemorySource cites where a memory came from.
type MemorySource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// UnmarshalJSON accepts the request shape and the stored refType/refId shape.
func (s *MemorySource) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		RefType string `json:"refType"`
		RefID   string `json:"refId"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Type, s.ID = raw.Type, raw.ID
	if s.Type == "" {
		s.Type = raw.RefType
	}
	if s.ID == "" {
		s.ID = raw.RefID
	}
	return nil
}

// Memory is one Nucleus atom.
type Memory struct {
	ID         string         `json:"id"`
	Scope      MemoryScope    `json:"scope"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Namespace  *string        `json:"namespace,omitempty"`
	Repo       *string        `json:"repo,omitempty"`
	Sources    []MemorySource `json:"sources,omitempty"`
}

// RecallHit is a recalled atom with its ranking.
type RecallHit struct {
	Memory
	Score     float64  `json:"score"`
	MatchedBy []string `json:"matchedBy,omitempty"`
}

// RecallRequest is the body of POST /api/v1/nucleus/recall.
type RecallRequest struct {
	Query         string   `json:"query"`
	Limit         int      `json:"limit,omitempty"`
	SpaceID       string   `json:"spaceId,omitempty"`
	Kinds         []string `json:"kinds,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	MinConfidence float64  `json:"minConfidence,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
	IncludeShared *bool    `json:"includeShared,omitempty"`
	Repo          string   `json:"repo,omitempty"`
}

// RecallResult is the response of POST /api/v1/nucleus/recall.
type RecallResult struct {
	Scope MemoryScope `json:"scope"`
	Hits  []RecallHit `json:"hits"`
}

// Recall calls POST /api/v1/nucleus/recall.
func (c *Client) Recall(ctx context.Context, req RecallRequest) (*RecallResult, error) {
	var out RecallResult
	if err := c.Post(ctx, "/api/v1/nucleus/recall", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MemoryWrite is the body of POST /api/v1/nucleus/memories.
type MemoryWrite struct {
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Kind       string         `json:"kind,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	SiteSlug   string         `json:"siteSlug,omitempty"`
	SpaceID    string         `json:"spaceId,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	RunID      string         `json:"runId,omitempty"`
	Sources    []MemorySource `json:"sources,omitempty"`
}

// Memory write outcomes.
const (
	OutcomeCreated         = "created"
	OutcomeUpdated         = "updated"
	OutcomeUnchanged       = "unchanged"
	OutcomeQueuedForReview = "queued_for_review"
)

// MemoryResult is the response of POST /api/v1/nucleus/memories.
type MemoryResult struct {
	Outcome    string  `json:"outcome"`
	Memory     *Memory `json:"memory,omitempty"`
	Atom       *Memory `json:"atom,omitempty"`
	RevisionID string  `json:"revisionId,omitempty"`
}

// WriteMemory calls POST /api/v1/nucleus/memories.
func (c *Client) WriteMemory(ctx context.Context, req MemoryWrite) (*MemoryResult, error) {
	var out MemoryResult
	if err := c.Post(ctx, "/api/v1/nucleus/memories", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
