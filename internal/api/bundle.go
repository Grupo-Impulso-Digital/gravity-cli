package api

import (
	"context"
	"net/http"
)

// Change operations.
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
	OpImport = "import"
)

// Block ownerships.
const (
	OwnershipMachine = "machine"
	OwnershipHybrid  = "hybrid"
	OwnershipHuman   = "human"
)

// Rationale explains why a block changed.
type Rationale struct {
	Summary    string   `json:"summary"`
	Commits    []string `json:"commits,omitempty"`
	SourceRefs []string `json:"sourceRefs,omitempty"`
}

// ChangeBlock is one block in a page change or a verbatim import.
type ChangeBlock struct {
	Key           string         `json:"key"`
	Type          string         `json:"type"`
	Ownership     string         `json:"ownership"`
	Content       any            `json:"content"`
	SourceBinding *SourceBinding `json:"sourceBinding,omitempty"`
	Audiences     []string       `json:"audiences,omitempty"`
	After         *string        `json:"after,omitempty"`
	Units         []string       `json:"units,omitempty"`
	Rationale     *Rationale     `json:"rationale,omitempty"`
}

// ChangeTarget addresses the page a change applies to.
type ChangeTarget struct {
	PageID         string   `json:"pageId,omitempty"`
	SpaceID        string   `json:"spaceId,omitempty"`
	Slug           string   `json:"slug,omitempty"`
	CollectionPath []string `json:"collectionPath,omitempty"`
}

// ChangeRequest is the body of POST /api/v1/runs/{runId}/changes.
type ChangeRequest struct {
	RunPassID       string        `json:"runPassId"`
	Op              string        `json:"op"`
	Target          ChangeTarget  `json:"target"`
	Title           string        `json:"title,omitempty"`
	Summary         string        `json:"summary"`
	Blocks          []ChangeBlock `json:"blocks,omitempty"`
	RemoveBlockKeys []string      `json:"removeBlockKeys,omitempty"`
	Units           []string      `json:"units,omitempty"`
	Languages       []string      `json:"languages,omitempty"`
}

// CompetingChange is another run's change on the same blocks.
type CompetingChange struct {
	ChangeID  string   `json:"changeId"`
	RunID     string   `json:"runId"`
	Repo      string   `json:"repo"`
	BlockKeys []string `json:"blockKeys"`
}

// ChangeWarning is a non-fatal note on a change.
type ChangeWarning struct {
	Code     string `json:"code"`
	BlockKey string `json:"blockKey,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Change is a recorded pipeline change.
type Change struct {
	ID            string            `json:"id"`
	Op            string            `json:"op"`
	Status        string            `json:"status"`
	Reach         string            `json:"reach,omitempty"`
	Page          PageRef           `json:"page"`
	ProposalID    string            `json:"proposalId,omitempty"`
	Competing     bool              `json:"competing"`
	CompetingWith []CompetingChange `json:"competingWith,omitempty"`
	HeldReason    *string           `json:"heldReason"`
	Warnings      []ChangeWarning   `json:"warnings,omitempty"`
	ReviewURL     string            `json:"reviewUrl,omitempty"`
	Adopted       bool              `json:"adopted,omitempty"`
	Lock          *PageLock         `json:"lock,omitempty"`
	PendingLock   *PageLock         `json:"pendingLock,omitempty"`
}

// ProposeChange calls POST /api/v1/runs/{runId}/changes.
func (c *Client) ProposeChange(ctx context.Context, runID string, req ChangeRequest) (*Change, error) {
	var out struct {
		Change Change `json:"change"`
	}
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "changes"), req, &out); err != nil {
		return nil, err
	}
	return &out.Change, nil
}

// VerbatimFile identifies the repository file of a verbatim import.
type VerbatimFile struct {
	Path      string `json:"path"`
	Hash      string `json:"hash"`
	Branch    string `json:"branch"`
	CommitSHA string `json:"commitSha"`
	URL       string `json:"url,omitempty"`
}

// VerbatimPage sets the identity and placement of an imported page.
type VerbatimPage struct {
	Slug             string            `json:"slug"`
	Title            string            `json:"title"`
	Description      string            `json:"description,omitempty"`
	Position         int               `json:"position"`
	CollectionPath   []string          `json:"collectionPath"`
	CollectionTitles map[string]string `json:"collectionTitles,omitempty"`
}

// VerbatimRequest is the body of POST /api/v1/runs/{runId}/verbatim.
type VerbatimRequest struct {
	RunPassID string        `json:"runPassId"`
	File      VerbatimFile  `json:"file"`
	Page      VerbatimPage  `json:"page"`
	Blocks    []ChangeBlock `json:"blocks"`
	Languages []string      `json:"languages"`
}

// VerbatimResult is the response of a verbatim import or deletion.
type VerbatimResult struct {
	Change *Change  `json:"change"`
	Status string   `json:"status,omitempty"`
	Page   *PageRef `json:"page,omitempty"`
}

// Unchanged reports whether the platform skipped the write.
func (r *VerbatimResult) Unchanged() bool {
	return r != nil && r.Status == "unchanged"
}

// ImportVerbatim calls POST /api/v1/runs/{runId}/verbatim.
func (c *Client) ImportVerbatim(ctx context.Context, runID string, req VerbatimRequest) (*VerbatimResult, error) {
	if req.Languages == nil {
		req.Languages = []string{}
	}
	var out VerbatimResult
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "verbatim"), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VerbatimDeleteRequest is the body of POST /api/v1/runs/{runId}/verbatim/delete.
type VerbatimDeleteRequest struct {
	RunPassID string `json:"runPassId"`
	Path      string `json:"path"`
	Reason    string `json:"reason"`
}

// DeleteVerbatim calls POST /api/v1/runs/{runId}/verbatim/delete.
func (c *Client) DeleteVerbatim(ctx context.Context, runID string, req VerbatimDeleteRequest) (*VerbatimResult, error) {
	var out VerbatimResult
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "verbatim", "delete"), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Asset is an uploaded image.
type Asset struct {
	Key          string `json:"key"`
	URL          string `json:"url"`
	Deduplicated bool   `json:"deduplicated"`
}

// UploadAsset calls POST /api/v1/runs/{runId}/assets with the raw image bytes.
func (c *Client) UploadAsset(ctx context.Context, runID, runPassID, repoPath, contentType, sha256Hex string, data []byte) (*Asset, error) {
	headers := map[string]string{
		"X-Gravity-Asset-Path":   repoPath,
		"X-Gravity-Asset-Sha256": sha256Hex,
	}
	if runPassID != "" {
		headers["X-Gravity-Run-Pass-Id"] = runPassID
	}
	var out Asset
	err := c.do(ctx, request{
		method:      http.MethodPost,
		path:        "/api/v1/runs" + pathEscape(runID, "assets"),
		raw:         data,
		contentType: contentType,
		headers:     headers,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// HintEvidence supports a cross-repo hint.
type HintEvidence struct {
	Commits []string `json:"commits,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// HintInput is one hint to raise.
type HintInput struct {
	Kind     string       `json:"kind"`
	UnitKey  string       `json:"unitKey,omitempty"`
	PageID   string       `json:"pageId,omitempty"`
	BlockKey string       `json:"blockKey,omitempty"`
	Claim    string       `json:"claim"`
	Detail   string       `json:"detail,omitempty"`
	Evidence HintEvidence `json:"evidence"`
	ForRepos []string     `json:"forRepos"`
}

// HintsResult is the response of POST /api/v1/runs/{runId}/hints.
type HintsResult struct {
	Created      []string `json:"created"`
	Deduplicated int      `json:"deduplicated"`
}

// RaiseHints calls POST /api/v1/runs/{runId}/hints.
func (c *Client) RaiseHints(ctx context.Context, runID, runPassID string, hints []HintInput) (*HintsResult, error) {
	for i := range hints {
		if hints[i].ForRepos == nil {
			hints[i].ForRepos = []string{}
		}
	}
	var out HintsResult
	body := map[string]any{"runPassId": runPassID, "hints": hints}
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "hints"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
