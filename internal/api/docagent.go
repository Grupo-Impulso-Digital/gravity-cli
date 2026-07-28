package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// Doc Agent run statuses.
const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	//nolint:misspell // "cancelled" is the platform's wire value, not prose.
	RunStatusCancelled = "cancelled"
)

// DocAgentRunRequest is the body of POST /api/v1/sites/:siteSlug/doc-agent/runs.
type DocAgentRunRequest struct {
	SpaceSlug       string   `json:"spaceSlug"`
	ConnectionLabel string   `json:"connectionLabel,omitempty"`
	Brief           string   `json:"brief,omitempty"`
	Async           bool     `json:"async"`
	Repo            *RepoRef `json:"repo,omitempty"`
}

// DocAgentArtifact is one artifact produced by a run.
type DocAgentArtifact struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	URL       string          `json:"url,omitempty"`
	CreatedAt string          `json:"createdAt"`
	Meta      json.RawMessage `json:"meta,omitempty"`
}

// DocAgentStats summarizes a run's output.
type DocAgentStats struct {
	Artifacts   int `json:"artifacts"`
	Screenshots int `json:"screenshots"`
	Errors      int `json:"errors"`
}

// DocAgentRun is the response of launching (202) and of polling (200) a run.
type DocAgentRun struct {
	RunID           string             `json:"runId"`
	Status          string             `json:"status"`
	StatusURL       string             `json:"statusUrl,omitempty"`
	SpaceSlug       string             `json:"spaceSlug,omitempty"`
	ConnectionLabel string             `json:"connectionLabel,omitempty"`
	Trigger         string             `json:"trigger,omitempty"`
	ProposalID      string             `json:"proposalId,omitempty"`
	ReviewURL       string             `json:"reviewUrl,omitempty"`
	Error           string             `json:"error,omitempty"`
	CreatedAt       string             `json:"createdAt,omitempty"`
	StartedAt       string             `json:"startedAt,omitempty"`
	FinishedAt      string             `json:"finishedAt,omitempty"`
	Stats           *DocAgentStats     `json:"stats,omitempty"`
	Artifacts       []DocAgentArtifact `json:"artifacts,omitempty"`
}

// Done reports whether the run has reached a terminal state.
func (r *DocAgentRun) Done() bool {
	switch r.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled:
		return true
	}
	return false
}

// StartDocAgentRun calls POST /api/v1/sites/:siteSlug/doc-agent/runs.
func (c *Client) StartDocAgentRun(ctx context.Context, siteSlug string, req DocAgentRunRequest) (*DocAgentRun, error) {
	var out DocAgentRun
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/doc-agent/runs", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DocAgentRunStatus calls GET /api/v1/sites/:siteSlug/doc-agent/runs/:runId.
func (c *Client) DocAgentRunStatus(ctx context.Context, siteSlug, runID string) (*DocAgentRun, error) {
	var out DocAgentRun
	path := "/api/v1/sites/" + url.PathEscape(siteSlug) + "/doc-agent/runs/" + url.PathEscape(runID)
	if err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
