package api

import (
	"context"
	"net/url"
	"strconv"
)

// Range kinds.
const (
	RangePR          = "pr"
	RangeWatermark   = "watermark"
	RangeSurvey      = "survey"
	RangeRelease     = "release"
	RangeExplicit    = "explicit"
	RangeWorkingTree = "working_tree"
)

// Run and pass-run statuses.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
	StatusPartial   = "partial"
	StatusCancelled = "cancelled" //nolint:misspell // platform status value
	StatusAbandoned = "abandoned"
)

// PRInfo describes the pull request of a pr run.
type PRInfo struct {
	Number       int    `json:"number"`
	URL          string `json:"url,omitempty"`
	TargetBranch string `json:"targetBranch"`
}

// ReleaseInfo describes the release of a release run.
type ReleaseInfo struct {
	Tag         string `json:"tag"`
	PreviousTag string `json:"previousTag,omitempty"`
}

// CIInfo describes the CI job running the CLI.
type CIInfo struct {
	Provider string `json:"provider"`
	RunURL   string `json:"runUrl,omitempty"`
	Event    string `json:"event,omitempty"`
}

// RunPassStart is one pass entry of a run start: a resolved range or a skip reason.
type RunPassStart struct {
	Name          string  `json:"name"`
	RangeKind     string  `json:"rangeKind,omitempty"`
	BaseSHA       string  `json:"baseSha,omitempty"`
	WatermarkSeen *string `json:"watermarkSeen,omitempty"`
	Skip          string  `json:"skip,omitempty"`
}

// StartRunRequest is the body of POST /api/v1/runs.
type StartRunRequest struct {
	ClientKey    string         `json:"clientKey"`
	Trigger      string         `json:"trigger"`
	Mode         string         `json:"mode"`
	Origin       string         `json:"origin"`
	Branch       string         `json:"branch,omitempty"`
	HeadSHA      string         `json:"headSha"`
	BaseSHA      string         `json:"baseSha,omitempty"`
	RangeKind    string         `json:"rangeKind,omitempty"`
	PR           *PRInfo        `json:"pr"`
	Release      *ReleaseInfo   `json:"release"`
	CI           *CIInfo        `json:"ci,omitempty"`
	CLI          CLIInfo        `json:"cli"`
	Note         string         `json:"note,omitempty"`
	PlanHash     string         `json:"planHash"`
	ManifestHash *string        `json:"manifestHash"`
	Passes       []RunPassStart `json:"passes"`
}

// PassRange is the resolved range of a pass run.
type PassRange struct {
	Kind    string `json:"kind"`
	BaseSHA string `json:"baseSha"`
	HeadSHA string `json:"headSha"`
}

// RunInfo is the run section of a run start.
type RunInfo struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	Mode             string `json:"mode"`
	Trigger          string `json:"trigger"`
	LeaseKey         string `json:"leaseKey,omitempty"`
	Authoritative    bool   `json:"authoritative"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	HeartbeatSeconds int    `json:"heartbeatSeconds,omitempty"`
	AppURL           string `json:"appUrl,omitempty"`
}

// RunPass is one pass run of a started run.
type RunPass struct {
	RunPassID        string     `json:"runPassId"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	SkipReason       string     `json:"skipReason,omitempty"`
	Range            *PassRange `json:"range,omitempty"`
	InstructionsHash string     `json:"instructionsHash,omitempty"`
}

// StartedRun is the response of POST /api/v1/runs.
type StartedRun struct {
	Run    RunInfo   `json:"run"`
	Passes []RunPass `json:"passes"`
}

// StartRun calls POST /api/v1/runs; repo is required for user and org principals.
func (c *Client) StartRun(ctx context.Context, repo string, req StartRunRequest) (*StartedRun, error) {
	var out StartedRun
	if err := c.do(ctx, request{method: "POST", path: "/api/v1/runs", query: repoQuery(repo), body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Heartbeat calls POST /api/v1/runs/{runId}/heartbeat and returns the new expiry.
func (c *Client) Heartbeat(ctx context.Context, runID string) (string, error) {
	var out struct {
		ExpiresAt string `json:"expiresAt"`
	}
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "heartbeat"), map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.ExpiresAt, nil
}

// RunDetail is the run section of GET /api/v1/runs/{runId}.
type RunDetail struct {
	ID            string       `json:"id"`
	Status        string       `json:"status"`
	Mode          string       `json:"mode"`
	Trigger       string       `json:"trigger"`
	Origin        string       `json:"origin"`
	Branch        *string      `json:"branch"`
	Authoritative bool         `json:"authoritative"`
	LeaseKey      *string      `json:"leaseKey"`
	HeadSHA       string       `json:"headSha"`
	BaseSHA       *string      `json:"baseSha"`
	RangeKind     *string      `json:"rangeKind"`
	PR            *PRInfo      `json:"pr"`
	Release       *ReleaseInfo `json:"release"`
	Note          *string      `json:"note"`
	ExpiresAt     *string      `json:"expiresAt"`
	StartedAt     string       `json:"startedAt"`
	FinishedAt    *string      `json:"finishedAt"`
	ChangesCount  int          `json:"changesCount"`
	FindingsCount int          `json:"findingsCount"`
	LLMCalls      int          `json:"llmCalls"`
	InputTokens   int          `json:"inputTokens"`
	OutputTokens  int          `json:"outputTokens"`
	CostUSD       float64      `json:"costUsd"`
	Error         *string      `json:"error"`
	AppURL        string       `json:"appUrl"`
}

// RunPassDetail is one pass run of GET /api/v1/runs/{runId}.
type RunPassDetail struct {
	RunPassID         string     `json:"runPassId"`
	Name              string     `json:"name"`
	Kind              string     `json:"kind"`
	Source            string     `json:"source"`
	Overlay           bool       `json:"overlay"`
	Status            string     `json:"status"`
	SkipReason        *string    `json:"skipReason"`
	Range             *PassRange `json:"range"`
	WatermarkSeen     *string    `json:"watermarkSeen"`
	WatermarkAdvanced bool       `json:"watermarkAdvanced"`
	WatermarkNote     *string    `json:"watermarkNote"`
	InstructionsHash  string     `json:"instructionsHash"`
	ChangesCount      int        `json:"changesCount"`
	FindingsCount     int        `json:"findingsCount"`
	LLMCalls          int        `json:"llmCalls"`
	InputTokens       int        `json:"inputTokens"`
	OutputTokens      int        `json:"outputTokens"`
	CostUSD           float64    `json:"costUsd"`
	Error             *string    `json:"error"`
	StartedAt         *string    `json:"startedAt"`
	FinishedAt        *string    `json:"finishedAt"`
}

// BundleSummary counts the changes of a run bundle.
type BundleSummary struct {
	Changes      int      `json:"changes"`
	Applied      int      `json:"applied"`
	Held         int      `json:"held"`
	Competing    int      `json:"competing"`
	Accepted     int      `json:"accepted"`
	Declined     int      `json:"declined"`
	AutoAccepted int      `json:"autoAccepted"`
	Proposals    []string `json:"proposals,omitempty"`
	AppURL       string   `json:"appUrl"`
}

// CaptureRun links a capture pass run to its Doc Agent run.
type CaptureRun struct {
	RunPassID     string `json:"runPassId"`
	DocAgentRunID string `json:"docAgentRunId"`
	Status        string `json:"status"`
}

// Run is the response of GET /api/v1/runs/{runId}.
type Run struct {
	Run         RunDetail       `json:"run"`
	Passes      []RunPassDetail `json:"passes"`
	Bundle      BundleSummary   `json:"bundle"`
	CaptureRuns []CaptureRun    `json:"captureRuns"`
}

// GetRun calls GET /api/v1/runs/{runId}.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var out Run
	if err := c.Get(ctx, "/api/v1/runs"+pathEscape(runID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Finding severities and claim verdicts.
const (
	SeverityError        = "error"
	SeverityWarning      = "warning"
	SeverityInfo         = "info"
	VerdictVerifiedHere  = "verified-here"
	VerdictTrueElsewhere = "true-elsewhere"
	VerdictUnverifiable  = "unverifiable"
	VerdictContradicted  = "contradicted-here"
)

// Evidence supports a finding.
type Evidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// Finding is one check finding of a pass report.
type Finding struct {
	Severity string     `json:"severity"`
	Code     string     `json:"code"`
	Title    string     `json:"title"`
	Detail   string     `json:"detail,omitempty"`
	Verdict  string     `json:"verdict,omitempty"`
	File     string     `json:"file,omitempty"`
	Line     int        `json:"line,omitempty"`
	Page     *PageRef   `json:"page,omitempty"`
	BlockKey string     `json:"blockKey,omitempty"`
	UnitKey  string     `json:"unitKey,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

// Note is a soft claim note of a pass report.
type Note struct {
	Verdict string   `json:"verdict"`
	Title   string   `json:"title"`
	Page    *PageRef `json:"page,omitempty"`
}

// Impact is how a dry run describes a page it would write.
type Impact struct {
	Page   PageRef `json:"page"`
	Action string  `json:"action"`
	Reason string  `json:"reason"`
}

// PassReport is the report body of a pass run.
type PassReport struct {
	Summary         string    `json:"summary"`
	ChangeIDs       []string  `json:"changeIds,omitempty"`
	Findings        []Finding `json:"findings,omitempty"`
	Notes           []Note    `json:"notes,omitempty"`
	Impact          []Impact  `json:"impact,omitempty"`
	ConsumedHintIDs []string  `json:"consumedHintIds,omitempty"`
	UnitsTouched    []string  `json:"unitsTouched,omitempty"`
	DurationMs      int64     `json:"durationMs,omitempty"`
}

// PassReportRequest is the body of POST /api/v1/runs/{runId}/passes/{runPassId}.
type PassReportRequest struct {
	Status     string      `json:"status"`
	SkipReason *string     `json:"skipReason"`
	Error      *string     `json:"error"`
	Report     *PassReport `json:"report,omitempty"`
}

// PassRunResult is the passRun section of a pass report response.
type PassRunResult struct {
	RunPassID     string  `json:"runPassId"`
	Name          string  `json:"name"`
	Status        string  `json:"status"`
	SkipReason    *string `json:"skipReason"`
	ChangesCount  int     `json:"changesCount"`
	FindingsCount int     `json:"findingsCount"`
	LLMCalls      int     `json:"llmCalls"`
	CostUSD       float64 `json:"costUsd"`
	StartedAt     string  `json:"startedAt,omitempty"`
	FinishedAt    string  `json:"finishedAt,omitempty"`
}

// ReportPass calls POST /api/v1/runs/{runId}/passes/{runPassId}.
func (c *Client) ReportPass(ctx context.Context, runID, runPassID string, req PassReportRequest) (*PassRunResult, error) {
	var out struct {
		PassRun PassRunResult `json:"passRun"`
	}
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "passes", runPassID), req, &out); err != nil {
		return nil, err
	}
	return &out.PassRun, nil
}

// FinishReport is the report body of a run finish.
type FinishReport struct {
	Summary   string `json:"summary"`
	PRComment string `json:"prComment,omitempty"`
}

// FinishRunRequest is the body of POST /api/v1/runs/{runId}/finish.
type FinishRunRequest struct {
	Status string        `json:"status"`
	Error  *string       `json:"error"`
	Report *FinishReport `json:"report,omitempty"`
}

// WatermarkResult reports whether finish advanced a pass watermark.
type WatermarkResult struct {
	Pass      string  `json:"pass"`
	Branch    string  `json:"branch"`
	CommitSHA string  `json:"commitSha"`
	Advanced  bool    `json:"advanced"`
	Reason    *string `json:"reason"`
}

// FinishedRunInfo is the run section of a finish response.
type FinishedRunInfo struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	FinishedAt string  `json:"finishedAt"`
	CostUSD    float64 `json:"costUsd"`
	AppURL     string  `json:"appUrl"`
}

// FinishedRun is the response of POST /api/v1/runs/{runId}/finish.
type FinishedRun struct {
	Run        FinishedRunInfo   `json:"run"`
	Watermarks []WatermarkResult `json:"watermarks"`
	Bundle     BundleSummary     `json:"bundle"`
}

// FinishRun calls POST /api/v1/runs/{runId}/finish.
func (c *Client) FinishRun(ctx context.Context, runID string, req FinishRunRequest) (*FinishedRun, error) {
	var out FinishedRun
	if err := c.Post(ctx, "/api/v1/runs"+pathEscape(runID, "finish"), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func repoQuery(repo string) url.Values {
	if repo == "" {
		return nil
	}
	return url.Values{"repo": {repo}}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
