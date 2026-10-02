package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
)

// Product identifies a product.
type Product struct {
	ID               string `json:"id,omitempty"`
	Slug             string `json:"slug"`
	Name             string `json:"name,omitempty"`
	NucleusNamespace string `json:"nucleusNamespace,omitempty"`
}

// RepoRef identifies a repository; the platform sends it as an object or as a bare remote key.
type RepoRef struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	RemoteKey string `json:"remoteKey,omitempty"`
}

// UnmarshalJSON accepts a remote-key string or a repository object.
func (r *RepoRef) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*r = RepoRef{RemoteKey: s}
		return nil
	}
	type plain RepoRef
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*r = RepoRef(p)
	return nil
}

// Label returns the most readable identifier of the repository.
func (r RepoRef) Label() string {
	switch {
	case r.Name != "":
		return r.Name
	case r.RemoteKey != "":
		return r.RemoteKey
	}
	return r.ID
}

// Warning is a non-fatal notice from the platform.
type Warning struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Sibling is another repository of the same product.
type Sibling struct {
	Name      string `json:"name"`
	RemoteKey string `json:"remoteKey"`
	LastRunAt string `json:"lastRunAt,omitempty"`
	Passes    int    `json:"passes,omitempty"`
	Health    string `json:"health,omitempty"`
}

// ProductTarget summarizes where a product's passes write.
type ProductTarget struct {
	SiteSlug  string `json:"siteSlug"`
	SpaceSlug string `json:"spaceSlug"`
	Passes    int    `json:"passes"`
}

// ProductSummary is one entry of GET /api/v1/products.
type ProductSummary struct {
	Product
	Repos   []RepoRef       `json:"repos"`
	Targets []ProductTarget `json:"targets"`
}

// Products calls GET /api/v1/products.
func (c *Client) Products(ctx context.Context) ([]ProductSummary, error) {
	var out struct {
		Products []ProductSummary `json:"products"`
	}
	if err := c.Get(ctx, "/api/v1/products", nil, &out); err != nil {
		return nil, err
	}
	return out.Products, nil
}

// Connect contexts: the trigger is what the CLI is about to do.
const (
	ContextInit    = "init"
	ContextStatus  = "status"
	ContextPreview = "preview"
	OriginCI       = "ci"
	OriginLocal    = "local"
)

// CLIInfo identifies the CLI build.
type CLIInfo struct {
	Version string `json:"version"`
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

// ConnectRepo describes the local repository in a connect.
type ConnectRepo struct {
	Remote        string `json:"remote"`
	Name          string `json:"name"`
	Provider      string `json:"provider,omitempty"`
	WebURL        string `json:"webUrl,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Commit        string `json:"commit,omitempty"`
}

// ConnectContext says what the CLI is about to do and where it runs.
type ConnectContext struct {
	Trigger string `json:"trigger"`
	Origin  string `json:"origin"`
}

// DetectedOpenAPI is one detected OpenAPI document.
type DetectedOpenAPI struct {
	Path       string `json:"path"`
	Operations int    `json:"operations"`
}

// Detected carries local detection results.
type Detected struct {
	Languages     []string          `json:"languages,omitempty"`
	OpenAPI       []DetectedOpenAPI `json:"openapi,omitempty"`
	UIRoutes      int               `json:"uiRoutes,omitempty"`
	MarkdownFiles int               `json:"markdownFiles,omitempty"`
	CI            string            `json:"ci,omitempty"`
}

// CreateTarget asks connect to create a missing space.
type CreateTarget struct {
	Site       string  `json:"site"`
	Space      string  `json:"space"`
	Name       string  `json:"name"`
	Type       string  `json:"type,omitempty"`
	Visibility string  `json:"visibility,omitempty"`
	Parent     *string `json:"parent"`
}

// ConnectRequest is the body of POST /api/v1/repos/connect.
type ConnectRequest struct {
	CLI           CLIInfo         `json:"cli"`
	Repo          ConnectRepo     `json:"repo"`
	Context       ConnectContext  `json:"context"`
	Product       string          `json:"product,omitempty"`
	Manifest      json.RawMessage `json:"manifest"`
	ManifestYAML  string          `json:"manifestYaml,omitempty"`
	ManifestHash  string          `json:"manifestHash,omitempty"`
	Detected      *Detected       `json:"detected,omitempty"`
	CreateTargets []CreateTarget  `json:"createTargets,omitempty"`
	DryRun        bool            `json:"dryRun"`
}

// ConnectedRepo is the repository as registered.
type ConnectedRepo struct {
	ID         string  `json:"id"`
	RemoteKey  string  `json:"remoteKey"`
	Name       string  `json:"name"`
	Created    bool    `json:"created"`
	CreatedVia string  `json:"createdVia"`
	Product    Product `json:"product"`
	AppURL     string  `json:"appUrl"`
}

// ManifestOutcome reports what connect did with the submitted manifest.
type ManifestOutcome struct {
	Version             int       `json:"version"`
	Hash                string    `json:"hash"`
	Accepted            bool      `json:"accepted"`
	Persisted           bool      `json:"persisted"`
	Reason              *string   `json:"reason"`
	AuthoritativeBranch string    `json:"authoritativeBranch"`
	PassesUpserted      []string  `json:"passesUpserted"`
	PassesConverted     []string  `json:"passesConverted"`
	PassesArchived      []string  `json:"passesArchived"`
	Warnings            []Warning `json:"warnings"`
}

// Effective is the effective pass configuration connect computed.
type Effective struct {
	AppPasses string     `json:"appPasses"`
	Overlay   bool       `json:"overlay"`
	Passes    []PlanPass `json:"passes"`
}

// CreatedTarget is a space connect created.
type CreatedTarget struct {
	Site  string `json:"site"`
	Space string `json:"space"`
	ID    string `json:"id"`
}

// ConnectResponse is the response of POST /api/v1/repos/connect.
type ConnectResponse struct {
	Repo           ConnectedRepo   `json:"repo"`
	Manifest       ManifestOutcome `json:"manifest"`
	Effective      Effective       `json:"effective"`
	CreatedTargets []CreatedTarget `json:"createdTargets"`
	Siblings       []Sibling       `json:"siblings"`
	ServerFeatures map[string]bool `json:"serverFeatures"`
}

// Connect calls POST /api/v1/repos/connect.
func (c *Client) Connect(ctx context.Context, req ConnectRequest) (*ConnectResponse, error) {
	if req.Manifest == nil {
		req.Manifest = json.RawMessage("null")
	}
	var out ConnectResponse
	if err := c.Post(ctx, "/api/v1/repos/connect", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PassSpec is one pass object in the manifest shape, used by the app pass upsert.
type PassSpec struct {
	Kind         string         `json:"kind"`
	Title        string         `json:"title,omitempty"`
	Template     string         `json:"template,omitempty"`
	Target       string         `json:"target,omitempty"`
	Triggers     []string       `json:"triggers,omitempty"`
	Branches     []string       `json:"branches,omitempty"`
	Scope        *PassScope     `json:"scope,omitempty"`
	Audiences    []string       `json:"audiences,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Publish      string         `json:"publish,omitempty"`
	Enabled      *bool          `json:"enabled,omitempty"`
	Options      map[string]any `json:"options,omitempty"`
}

// UpsertPass calls PUT /api/v1/repos/{repoId}/passes/{passName}.
func (c *Client) UpsertPass(ctx context.Context, repoID, name string, spec PassSpec) (*PlanPass, error) {
	var out PlanPass
	if err := c.Put(ctx, "/api/v1/repos"+pathEscape(repoID, "passes", name), spec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NamedRef is an id, slug and name triple.
type NamedRef struct {
	ID   string `json:"id,omitempty"`
	Slug string `json:"slug"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

// CollectionRef identifies a collection inside a target space.
type CollectionRef struct {
	ID   string   `json:"id,omitempty"`
	Slug string   `json:"slug,omitempty"`
	Name string   `json:"name,omitempty"`
	Path []string `json:"path,omitempty"`
}

// Approval records who approved a pass target.
type Approval struct {
	By string `json:"by"`
	At string `json:"at"`
}

// Target statuses.
const (
	TargetOK         = "ok"
	TargetMissing    = "missing"
	TargetUnapproved = "unapproved"
	TargetNone       = "none"
)

// PassTarget is where a pass writes.
type PassTarget struct {
	Status         string         `json:"status"`
	Ref            string         `json:"ref"`
	Site           *NamedRef      `json:"site,omitempty"`
	Space          *NamedRef      `json:"space,omitempty"`
	Collection     *CollectionRef `json:"collection,omitempty"`
	ViewerURL      string         `json:"viewerUrl,omitempty"`
	ApproveURL     string         `json:"approveUrl,omitempty"`
	Approval       *Approval      `json:"approval,omitempty"`
	SiteSlug       string         `json:"siteSlug,omitempty"`
	SpaceSlug      string         `json:"spaceSlug,omitempty"`
	CollectionPath []string       `json:"collectionPath,omitempty"`
}

// PassScope is the scope of a pass.
type PassScope struct {
	Paths   []string `json:"paths,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Units   []string `json:"units,omitempty"`
}

// InstructionLayer is one ordered editorial prompt layer.
type InstructionLayer struct {
	Source string `json:"source"`
	ID     string `json:"id,omitempty"`
	Label  string `json:"label"`
	Text   string `json:"text"`
}

// Instructions are the composed layers of a pass.
type Instructions struct {
	Hash   string             `json:"hash"`
	Layers []InstructionLayer `json:"layers"`
}

// PromptRef names the hosted kind prompt of a pass.
type PromptRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Watermark is the last commit a pass processed on a branch.
type Watermark struct {
	Branch    string `json:"branch"`
	CommitSHA string `json:"commitSha"`
	RunID     string `json:"runId,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// PageRef identifies a page.
type PageRef struct {
	ID    string `json:"id,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Title string `json:"title,omitempty"`
}

// HintSource names the run that raised a hint.
type HintSource struct {
	Repo  string `json:"repo"`
	RunID string `json:"runId"`
}

// PlanHint is an open cross-repo hint delivered with a pass.
type PlanHint struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`
	Claim    string      `json:"claim"`
	UnitKey  string      `json:"unitKey"`
	Page     *PageRef    `json:"page,omitempty"`
	BlockKey string      `json:"blockKey,omitempty"`
	RaisedBy *HintSource `json:"raisedBy,omitempty"`
}

// Skip reasons a plan or the CLI can assign to a pass.
const (
	SkipDisabled         = "disabled"
	SkipNotSelected      = "not_selected"
	SkipTriggerMismatch  = "trigger_mismatch"
	SkipBranchMismatch   = "branch_mismatch"
	SkipModuleDisabled   = "module_disabled"
	SkipTargetMissing    = "target_missing"
	SkipTargetUnapproved = "target_unapproved"
	SkipScopeMissing     = "scope_missing"
	SkipScopeUnchanged   = "scope_unchanged"
	SkipNoChanges        = "no_changes"
	SkipStaleHead        = "stale_head"
	SkipBudgetExceeded   = "budget_exceeded"
)

// PlanPass is one pass of a plan (also the effective pass shape of connect and pass upsert).
type PlanPass struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Title         string         `json:"title,omitempty"`
	Kind          string         `json:"kind"`
	Template      string         `json:"template,omitempty"`
	Source        string         `json:"source,omitempty"`
	Locked        bool           `json:"locked"`
	Enabled       bool           `json:"enabled"`
	Overlay       bool           `json:"overlay,omitempty"`
	Applies       bool           `json:"applies"`
	SkipReason    string         `json:"skipReason,omitempty"`
	MissingScopes []string       `json:"missingScopes,omitempty"`
	Triggers      []string       `json:"triggers,omitempty"`
	Branches      []string       `json:"branches,omitempty"`
	Target        PassTarget     `json:"target"`
	Scope         PassScope      `json:"scope"`
	Audiences     []string       `json:"audiences,omitempty"`
	PublishMode   string         `json:"publishMode,omitempty"`
	Trusted       bool           `json:"trusted,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
	Instructions  Instructions   `json:"instructions"`
	Prompt        *PromptRef     `json:"prompt,omitempty"`
	Watermark     *Watermark     `json:"watermark,omitempty"`
	Hints         []PlanHint     `json:"hints,omitempty"`
}

// PlanRepo is the repository section of a plan.
type PlanRepo struct {
	ID                  string `json:"id"`
	RemoteKey           string `json:"remoteKey"`
	Name                string `json:"name"`
	DefaultBranch       string `json:"defaultBranch"`
	AuthoritativeBranch string `json:"authoritativeBranch"`
	WebURL              string `json:"webUrl,omitempty"`
	ManifestHash        string `json:"manifestHash,omitempty"`
	AppURL              string `json:"appUrl,omitempty"`
}

// Contributor is a repository's role on a unit.
type Contributor struct {
	Repo        RepoRef  `json:"repo"`
	Role        string   `json:"role"`
	Active      *bool    `json:"active,omitempty"`
	SourceRefs  []string `json:"sourceRefs"`
	LastSeenAt  string   `json:"lastSeenAt,omitempty"`
	LastSeenSHA string   `json:"lastSeenSha,omitempty"`
}

// UnitBinding is a page (or block) bound to a unit.
type UnitBinding struct {
	PageID    string  `json:"pageId"`
	PageSlug  string  `json:"pageSlug"`
	SiteSlug  string  `json:"siteSlug"`
	SpaceSlug string  `json:"spaceSlug"`
	BlockKey  *string `json:"blockKey"`
}

// Handoff is a detected move of a unit role between repositories.
type Handoff struct {
	ID         string `json:"id"`
	UnitKey    string `json:"unitKey,omitempty"`
	Role       string `json:"role"`
	From       string `json:"from"`
	To         string `json:"to"`
	Status     string `json:"status,omitempty"`
	DetectedAt string `json:"detectedAt,omitempty"`
}

// Unit is a product unit.
type Unit struct {
	Key          string        `json:"key"`
	Kind         string        `json:"kind"`
	Title        string        `json:"title,omitempty"`
	Summary      string        `json:"summary,omitempty"`
	Status       string        `json:"status,omitempty"`
	Audiences    []string      `json:"audiences,omitempty"`
	Aliases      []string      `json:"aliases,omitempty"`
	PrimaryRepo  *RepoRef      `json:"primaryRepo,omitempty"`
	Contributors []Contributor `json:"contributors,omitempty"`
	Bindings     []UnitBinding `json:"bindings,omitempty"`
	Handoffs     []Handoff     `json:"handoffs,omitempty"`
}

// PlanInventory is the product inventory relevant to this repository.
type PlanInventory struct {
	Units     []Unit `json:"units"`
	Truncated bool   `json:"truncated"`
}

// LLMCapability describes the organization's LLM configuration.
type LLMCapability struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

// Limits are the platform limits a run must respect.
type Limits struct {
	MaxChangesPerRun   int     `json:"maxChangesPerRun"`
	MaxBlocksPerChange int     `json:"maxBlocksPerChange"`
	MaxAssetBytes      int64   `json:"maxAssetBytes"`
	LeaseTTLSeconds    int     `json:"leaseTtlSeconds"`
	HeartbeatSeconds   int     `json:"heartbeatSeconds"`
	MaxCostUSDPerRun   float64 `json:"maxCostUsdPerRun"`
}

// Capabilities are the per-organization capabilities of a plan.
type Capabilities struct {
	Features map[string]bool `json:"features"`
	Modules  map[string]bool `json:"modules"`
	LLM      LLMCapability   `json:"llm"`
	Limits   Limits          `json:"limits"`
}

// Plan is the response of GET /api/v1/repos/self/plan.
type Plan struct {
	PlanHash     string        `json:"planHash"`
	Overlay      bool          `json:"overlay"`
	Repo         PlanRepo      `json:"repo"`
	Product      Product       `json:"product"`
	Trigger      string        `json:"trigger"`
	Branch       string        `json:"branch"`
	Passes       []PlanPass    `json:"passes"`
	Inventory    PlanInventory `json:"inventory"`
	Capabilities Capabilities  `json:"capabilities"`
	Siblings     []Sibling     `json:"siblings"`
	Warnings     []Warning     `json:"warnings"`
}

// PassByName returns the plan pass with that name.
func (p *Plan) PassByName(name string) (*PlanPass, bool) {
	if p == nil {
		return nil, false
	}
	for i := range p.Passes {
		if p.Passes[i].Name == name {
			return &p.Passes[i], true
		}
	}
	return nil, false
}

// Run modes.
const (
	ModeWrite = "write"
	ModeDry   = "dry"
)

// PlanQuery selects a plan.
type PlanQuery struct {
	Repo         string
	Trigger      string
	Branch       string
	Passes       []string
	Mode         string
	ManifestHash string
}

func (q PlanQuery) values() url.Values {
	v := url.Values{}
	if q.Repo != "" {
		v.Set("repo", q.Repo)
	}
	v.Set("trigger", q.Trigger)
	if q.Branch != "" {
		v.Set("branch", q.Branch)
	}
	for _, p := range q.Passes {
		v.Add("pass", p)
	}
	if q.Mode != "" {
		v.Set("mode", q.Mode)
	}
	if q.ManifestHash != "" {
		v.Set("manifestHash", q.ManifestHash)
	}
	return v
}

// Plan calls GET /api/v1/repos/self/plan.
func (c *Client) Plan(ctx context.Context, q PlanQuery) (*Plan, error) {
	var out Plan
	if err := c.Get(ctx, "/api/v1/repos/self/plan", q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StatusRepo is the repository section of a status.
type StatusRepo struct {
	ID            string `json:"id"`
	RemoteKey     string `json:"remoteKey"`
	Name          string `json:"name"`
	Health        string `json:"health"`
	LastConnectAt string `json:"lastConnectAt,omitempty"`
	LastRunAt     string `json:"lastRunAt,omitempty"`
	CLIVersion    string `json:"cliVersion,omitempty"`
	AppURL        string `json:"appUrl,omitempty"`
}

// LastRun is a pass's most recent run.
type LastRun struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
	At     string `json:"at"`
}

// StatusPass is one pass in a status.
type StatusPass struct {
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	Source     string      `json:"source"`
	Enabled    bool        `json:"enabled"`
	Target     PassTarget  `json:"target"`
	LastRun    *LastRun    `json:"lastRun,omitempty"`
	Watermarks []Watermark `json:"watermarks,omitempty"`
}

// RunSummary is one recent run in a status.
type RunSummary struct {
	ID         string  `json:"id"`
	Trigger    string  `json:"trigger"`
	Mode       string  `json:"mode"`
	Status     string  `json:"status"`
	Branch     string  `json:"branch,omitempty"`
	HeadSHA    string  `json:"headSha,omitempty"`
	StartedAt  string  `json:"startedAt,omitempty"`
	FinishedAt string  `json:"finishedAt,omitempty"`
	Changes    int     `json:"changes"`
	Findings   int     `json:"findings"`
	CostUSD    float64 `json:"costUsd"`
	AppURL     string  `json:"appUrl,omitempty"`
}

// OpenBundle is a run bundle awaiting review.
type OpenBundle struct {
	RunID   string `json:"runId"`
	Pending int    `json:"pending"`
	AppURL  string `json:"appUrl,omitempty"`
}

// TokenSummary describes a repository token in a status.
type TokenSummary struct {
	KeyHint    string  `json:"keyHint"`
	Kind       string  `json:"kind"`
	LastUsedAt string  `json:"lastUsedAt,omitempty"`
	ExpiresAt  *string `json:"expiresAt"`
}

// Health statuses.
const (
	HealthNeverRun = "never_run"
	HealthLive     = "live"
	HealthStale    = "stale"
	HealthFailing  = "failing"
	HealthBlocked  = "blocked"
	HealthBroken   = "broken"
)

// Health summarizes a repository's pipeline health.
type Health struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

// Status is the response of GET /api/v1/repos/self/status.
type Status struct {
	Repo        StatusRepo     `json:"repo"`
	Product     Product        `json:"product"`
	Passes      []StatusPass   `json:"passes"`
	Runs        []RunSummary   `json:"runs"`
	OpenBundles []OpenBundle   `json:"openBundles"`
	Tokens      []TokenSummary `json:"tokens"`
	Health      Health         `json:"health"`
}

// Status calls GET /api/v1/repos/self/status.
func (c *Client) Status(ctx context.Context, repo string, runs int) (*Status, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	if runs > 0 {
		q.Set("runs", itoa(runs))
	}
	var out Status
	if err := c.Get(ctx, "/api/v1/repos/self/status", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
