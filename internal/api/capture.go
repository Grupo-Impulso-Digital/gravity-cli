package api

import (
	"context"
	"net/url"
)

// --- Gravity runner ("capture") — GREENFIELD contract. ---
// These endpoints are not yet implemented by the platform; the CLI degrades
// gracefully (see IsUnavailable) until they ship. The shapes below define the
// contract for the platform team.

// Viewport is the browser viewport for a capture run.
type Viewport struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// CaptureTarget describes the app to navigate.
type CaptureTarget struct {
	URL      string    `json:"url"`
	Paths    []string  `json:"paths,omitempty"`
	Viewport *Viewport `json:"viewport,omitempty"`
	// AuthSecretRef names a platform-stored login recipe/secret — never a raw
	// credential in CI.
	AuthSecretRef string `json:"authSecretRef,omitempty"`
}

// CaptureScope bounds a crawl.
type CaptureScope struct {
	MaxPages int      `json:"maxPages,omitempty"`
	Include  []string `json:"include,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
}

// CaptureAttach controls how artifacts are attached to the docs site.
type CaptureAttach struct {
	Mode              string `json:"mode,omitempty"` // proposal | none
	ReleaseProposalID string `json:"releaseProposalId,omitempty"`
}

// CaptureRequest is the body of POST /api/v1/sites/:site/captures.
type CaptureRequest struct {
	SpaceSlug string         `json:"spaceSlug,omitempty"`
	Label     string         `json:"label,omitempty"`
	Target    CaptureTarget  `json:"target"`
	Capture   []string       `json:"capture,omitempty"` // screenshots | pages | dom | console
	Scope     *CaptureScope  `json:"scope,omitempty"`
	Attach    *CaptureAttach `json:"attach,omitempty"`
	Async     bool           `json:"async,omitempty"`
}

// CaptureArtifact is one captured page/screenshot.
type CaptureArtifact struct {
	Type    string `json:"type"` // screenshot | page | dom | console
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"` // ok | error
	Detail  string `json:"detail,omitempty"`
	BlockID string `json:"blockId,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

// CaptureStats summarizes a run.
type CaptureStats struct {
	PagesVisited int `json:"pagesVisited"`
	Screenshots  int `json:"screenshots"`
	Errors       int `json:"errors"`
}

// CaptureRun is the response of launching / polling a capture run. It is a
// superset of ReleaseNotesResponse so attached proposals surface a reviewUrl.
type CaptureRun struct {
	RunID       string            `json:"runId"`
	Status      string            `json:"status"` // queued | running | succeeded | partial | failed
	StatusURL   string            `json:"statusUrl,omitempty"`
	PageSlug    string            `json:"pageSlug,omitempty"`
	ProposalID  string            `json:"proposalId,omitempty"`
	ReviewURL   string            `json:"reviewUrl,omitempty"`
	Stats       CaptureStats      `json:"stats"`
	Artifacts   []CaptureArtifact `json:"artifacts,omitempty"`
	StartedAt   string            `json:"startedAt,omitempty"`
	CompletedAt string            `json:"completedAt,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// Done reports whether the run has reached a terminal state.
func (r *CaptureRun) Done() bool {
	switch r.Status {
	case "succeeded", "partial", "failed":
		return true
	}
	return false
}

// LaunchCapture calls POST /api/v1/sites/:site/captures.
func (c *Client) LaunchCapture(ctx context.Context, siteSlug string, req CaptureRequest) (*CaptureRun, error) {
	var out CaptureRun
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/captures", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CaptureStatus calls GET /api/v1/sites/:site/captures/:runId.
func (c *Client) CaptureStatus(ctx context.Context, siteSlug, runID string) (*CaptureRun, error) {
	var out CaptureRun
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/captures/"+url.PathEscape(runID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
