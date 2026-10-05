package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Estimate is the plan's cost estimate of one pass.
type Estimate struct {
	AI                bool     `json:"ai"`
	FirstRun          bool     `json:"firstRun"`
	Commits           int      `json:"commits"`
	Files             int      `json:"files"`
	ApproxInputTokens int      `json:"approxInputTokens"`
	ApproxCostUSD     *float64 `json:"approxCostUsd"`
	Model             *string  `json:"model"`
}

// IsUnsupported reports a 404 or 405 from an endpoint an older server does not have.
func IsUnsupported(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	if e.StatusCode == http.StatusMethodNotAllowed {
		return true
	}
	return e.StatusCode == http.StatusNotFound && (e.Code == "" || e.Code == CodeNotFound)
}

// RejectsWriteManifest reports an older server refusing manifestHash on a write run.
func RejectsWriteManifest(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == http.StatusBadRequest && strings.Contains(e.Message, "manifestHash")
}

// ValidateVerbatim is one mapped verbatim file sent to validation.
type ValidateVerbatim struct {
	Pass           string   `json:"pass"`
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	CollectionPath []string `json:"collectionPath,omitempty"`
	Language       string   `json:"language,omitempty"`
	SourcePath     string   `json:"sourcePath"`
	Empty          bool     `json:"empty"`
}

// ValidateRequest is the body of POST /api/v1/repos/self/validate.
type ValidateRequest struct {
	ManifestHash string             `json:"manifestHash,omitempty"`
	Branch       string             `json:"branch"`
	Verbatim     []ValidateVerbatim `json:"verbatim,omitempty"`
	Structure    any                `json:"structure,omitempty"`
}

// ValidateIssue is one problem the platform found.
type ValidateIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Pass     string `json:"pass,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// I18nOutcome explains the languages a pass will actually produce.
type I18nOutcome struct {
	Pass      string   `json:"pass"`
	Languages []string `json:"languages"`
	Effective []string `json:"effective"`
	Reason    string   `json:"reason"`
}

// ValidatePass is the target verdict of one pass in a validation.
type ValidatePass struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Target           string   `json:"target"`
	Status           string   `json:"status"`
	RequiresApproval bool     `json:"requiresApproval"`
	Reasons          []string `json:"reasons"`
}

// ValidateResponse is the response of POST /api/v1/repos/self/validate.
type ValidateResponse struct {
	OK     bool            `json:"ok"`
	Issues []ValidateIssue `json:"issues"`
	I18n   []I18nOutcome   `json:"i18n"`
	Passes []ValidatePass  `json:"passes,omitempty"`
}

// Validate calls POST /api/v1/repos/self/validate.
func (c *Client) Validate(ctx context.Context, repo string, req ValidateRequest) (*ValidateResponse, error) {
	var out ValidateResponse
	if err := c.do(ctx, request{method: http.MethodPost, path: "/api/v1/repos/self/validate", query: repoQuery(repo), body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PendingApproval is a site/space the repository needs a grant for.
type PendingApproval struct {
	Target     string   `json:"target"`
	Site       string   `json:"site,omitempty"`
	Space      string   `json:"space,omitempty"`
	SpaceName  string   `json:"spaceName,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	Passes     []string `json:"passes"`
	Reasons    []string `json:"reasons"`
	Why        string   `json:"why,omitempty"`
	MayApprove bool     `json:"mayApprove"`
	Reason     string   `json:"reason,omitempty"`
	ApproveURL string   `json:"approveUrl,omitempty"`
}

// Grant is a site/space the repository may already write to.
type Grant struct {
	Target    string   `json:"target"`
	Site      string   `json:"site,omitempty"`
	Space     string   `json:"space,omitempty"`
	SpaceName string   `json:"spaceName,omitempty"`
	By        string   `json:"by,omitempty"`
	At        string   `json:"at,omitempty"`
	Source    string   `json:"source,omitempty"`
	Passes    []string `json:"passes"`
}

// ApprovalsRepo names the repository of an approvals answer.
type ApprovalsRepo struct {
	ID        string `json:"id"`
	RemoteKey string `json:"remoteKey"`
	Name      string `json:"name"`
}

// Approvals is the response of GET /api/v1/repos/self/approvals.
type Approvals struct {
	Repo       ApprovalsRepo     `json:"repo"`
	CanApprove bool              `json:"canApprove"`
	Pending    []PendingApproval `json:"pending"`
	Granted    []Grant           `json:"granted"`
}

// Approvals calls GET /api/v1/repos/self/approvals.
func (c *Client) Approvals(ctx context.Context, repo string) (*Approvals, error) {
	var out Approvals
	if err := c.Get(ctx, "/api/v1/repos/self/approvals", repoQuery(repo), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApproveRequest is the body of POST /api/v1/repos/self/approvals.
type ApproveRequest struct {
	Spaces []string `json:"spaces,omitempty"`
	Passes []string `json:"passes,omitempty"`
	All    bool     `json:"all,omitempty"`
}

// ApprovalOutcome is one grant made or refused by an approval answer.
type ApprovalOutcome struct {
	Target  string   `json:"target,omitempty"`
	Site    string   `json:"site,omitempty"`
	Space   string   `json:"space,omitempty"`
	Passes  []string `json:"passes,omitempty"`
	Pass    string   `json:"pass,omitempty"`
	Code    string   `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

// ApproveResult is the response of POST /api/v1/repos/self/approvals.
type ApproveResult struct {
	Approved []ApprovalOutcome `json:"approved"`
	Refused  []ApprovalOutcome `json:"refused"`
}

// Approve calls POST /api/v1/repos/self/approvals.
func (c *Client) Approve(ctx context.Context, repo string, req ApproveRequest) (*ApproveResult, error) {
	var out ApproveResult
	if err := c.do(ctx, request{method: http.MethodPost, path: "/api/v1/repos/self/approvals", query: repoQuery(repo), body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelledRun is the response of POST /api/v1/runs/{runId}/cancel.
type CancelledRun struct {
	Run FinishedRunInfo `json:"run"`
}

// CancelRun calls POST /api/v1/runs/{runId}/cancel.
func (c *Client) CancelRun(ctx context.Context, runID string) (*CancelledRun, error) {
	var out CancelledRun
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "cancel"), map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RepoRuns calls GET /api/v1/repos/self/runs, optionally filtered by status.
func (c *Client) RepoRuns(ctx context.Context, repo, status string, limit int) ([]RunSummary, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", itoa(limit))
	}
	var out struct {
		Runs []RunSummary `json:"runs"`
	}
	if err := c.Get(ctx, "/api/v1/repos/self/runs", q, &out); err != nil {
		return nil, err
	}
	return out.Runs, nil
}

// StructurePage is one page of a structure spec or tree.
type StructurePage struct {
	Slug         string `json:"slug" yaml:"slug"`
	Title        string `json:"title,omitempty" yaml:"title,omitempty"`
	Description  string `json:"description,omitempty" yaml:"description,omitempty"`
	Source       string `json:"source,omitempty" yaml:"source,omitempty"`
	Status       string `json:"status,omitempty" yaml:"-"`
	LockedByRepo string `json:"lockedByRepo,omitempty" yaml:"-"`
	SourceRepo   string `json:"sourceRepo,omitempty" yaml:"-"`
}

// StructureCollection is one collection of a structure spec or tree.
type StructureCollection struct {
	Slug        string                `json:"slug" yaml:"slug"`
	Title       string                `json:"title,omitempty" yaml:"title,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Collections []StructureCollection `json:"collections,omitempty" yaml:"collections,omitempty"`
	Pages       []StructurePage       `json:"pages,omitempty" yaml:"pages,omitempty"`
}

// StructureSpace is one space of a structure spec or tree.
type StructureSpace struct {
	Slug        string                `json:"slug" yaml:"slug"`
	Name        string                `json:"name,omitempty" yaml:"name,omitempty"`
	Type        string                `json:"type,omitempty" yaml:"type,omitempty"`
	Visibility  string                `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	Parent      string                `json:"parent,omitempty" yaml:"parent,omitempty"`
	Collections []StructureCollection `json:"collections,omitempty" yaml:"collections,omitempty"`
	Pages       []StructurePage       `json:"pages,omitempty" yaml:"pages,omitempty"`
}

// StructureSite names the site of a structure spec.
type StructureSite struct {
	Slug string `json:"slug" yaml:"slug"`
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

// Structure is the declared or live tree of a site.
type Structure struct {
	Site   StructureSite    `json:"site" yaml:"site"`
	Spaces []StructureSpace `json:"spaces" yaml:"spaces"`
}

// StructureTree calls GET /api/v1/structure?site=.
func (c *Client) StructureTree(ctx context.Context, site string) (*Structure, error) {
	var out Structure
	if err := c.Get(ctx, "/api/v1/structure", url.Values{"site": {site}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StructureItem is one node of a structure apply outcome.
type StructureItem struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// StructureApplyRequest is the body of POST /api/v1/structure/apply.
type StructureApplyRequest struct {
	Structure Structure `json:"structure"`
	DryRun    bool      `json:"dryRun"`
}

// StructureApplyResult is the response of POST /api/v1/structure/apply.
type StructureApplyResult struct {
	Created   []StructureItem `json:"created"`
	Updated   []StructureItem `json:"updated"`
	Unchanged []StructureItem `json:"unchanged"`
	Extra     []StructureItem `json:"extra"`
	Conflicts []StructureItem `json:"conflicts"`
	Deferred  []StructureItem `json:"deferred,omitempty"`
}

// ApplyStructure calls POST /api/v1/structure/apply.
func (c *Client) ApplyStructure(ctx context.Context, repo string, req StructureApplyRequest) (*StructureApplyResult, error) {
	var out StructureApplyResult
	if err := c.do(ctx, request{method: http.MethodPost, path: "/api/v1/structure/apply", query: repoQuery(repo), body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
