package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// WhoAmI is the response of GET /api/v1/whoami.
type WhoAmI struct {
	OrganizationID   string          `json:"organizationId"`
	OrganizationName string          `json:"organizationName"`
	DefaultSiteSlug  *string         `json:"defaultSiteSlug"`
	KeyHint          string          `json:"keyHint"`
	Features         map[string]bool `json:"features,omitempty"`
}

// WhoAmI calls GET /api/v1/whoami.
func (c *Client) WhoAmI(ctx context.Context) (*WhoAmI, error) {
	var out WhoAmI
	if err := c.Get(ctx, "/api/v1/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// SetupPingRequest is the body of POST /api/v1/setup/ping.
type SetupPingRequest struct {
	CLI    PingCLI    `json:"cli"`
	Config PingConfig `json:"config"`
	Repo   PingRepo   `json:"repo"`

	ConfigFull map[string]any  `json:"configFull,omitempty"`
	ConfigYAML string          `json:"configYaml,omitempty"`
	DocSources *PingDocSources `json:"docSources,omitempty"`
}

// PingCLI reports the running binary's version and build platform.
type PingCLI struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// PingConfig is the resolved (flag/env/file) connection config the CLI would use.
type PingConfig struct {
	APIURL string `json:"apiUrl"`
	Site   string `json:"site,omitempty"`
	Space  string `json:"space,omitempty"`
}

// PingRepo identifies the local repo.
type PingRepo struct {
	Name            string `json:"name,omitempty"`
	Remote          string `json:"remote,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Commit          string `json:"commit,omitempty"`
	RemoteKeySource string `json:"remoteKeySource,omitempty"`
}

// Repo-identity sources reported by PingRepo.RemoteKeySource.
const (
	RemoteKeySourceRemote = "remote"
	RemoteKeySourceConfig = "config"
)

// PingDocSources summarizes the manifest's doc surface for the setup wizard.
type PingDocSources struct {
	Sources   int      `json:"sources"`
	Documents int      `json:"documents"`
	Spaces    []string `json:"spaces,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
	Languages []string `json:"languages,omitempty"`
	Units     string   `json:"units,omitempty"`
}

// SetupPingResponse is the response of POST /api/v1/setup/ping.
type SetupPingResponse struct {
	OK               bool   `json:"ok"`
	OrganizationName string `json:"organizationName"`
	KeyHint          string `json:"keyHint"`
	DefaultSiteSlug  string `json:"defaultSiteSlug"`

	Repo           *PingRepoRef    `json:"repo,omitempty"`
	Siblings       []SiblingRepo   `json:"siblings,omitempty"`
	ServerFeatures map[string]bool `json:"serverFeatures,omitempty"`
}

// PingRepoRef is the platform's registration of the pinging repo.
type PingRepoRef struct {
	ID          string `json:"id"`
	FirstSeenAt string `json:"firstSeenAt"`
}

// SiblingRepo is another repo in the org writing to the same site.
type SiblingRepo struct {
	Name        string   `json:"name"`
	ProductSlug string   `json:"productSlug"`
	RemoteKey   string   `json:"remoteKey"`
	Spaces      []string `json:"spaces,omitempty"`
	Collections []string `json:"collections,omitempty"`
	LastPingAt  string   `json:"lastPingAt"`
	LastWriteAt string   `json:"lastWriteAt"`
	CLIVersion  string   `json:"cliVersion"`
}

// RepoRef attributes a write to a connected repo.
type RepoRef struct {
	RemoteKey string `json:"remoteKey"`
	Name      string `json:"name,omitempty"`
}

// SetupPing calls POST /api/v1/setup/ping.
func (c *Client) SetupPing(ctx context.Context, req SetupPingRequest) (*SetupPingResponse, error) {
	var out SetupPingResponse
	if err := c.Post(ctx, "/api/v1/setup/ping", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Site describes a documentation site.
type Site struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

// Space is a page container within a site.
type Space struct {
	ID             string  `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	ParentSpaceID  *string `json:"parentSpaceId,omitempty"`
	OverviewPageID *string `json:"overviewPageId,omitempty"`
}

// Collection is a folder of pages inside a space.
type Collection struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parentId"`
	SpaceID   string  `json:"spaceId,omitempty"`
	SpaceSlug string  `json:"spaceSlug,omitempty"`
}

// PageRef is a lightweight page reference in the site tree.
type PageRef struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	SpaceID       string  `json:"spaceId"`
	SpaceSlug     string  `json:"spaceSlug"`
	CollectionID  *string `json:"collectionId,omitempty"`
	RepoID        *string `json:"repoId,omitempty"`
	RepoRemoteKey *string `json:"repoRemoteKey,omitempty"`
}

// SiteTree is the response of GET /api/v1/sites/:siteSlug.
type SiteTree struct {
	Site        Site         `json:"site"`
	Spaces      []Space      `json:"spaces"`
	Collections []Collection `json:"collections"`
	Pages       []PageRef    `json:"pages"`
}

// SiteTree calls GET /api/v1/sites/:siteSlug.
func (c *Client) SiteTree(ctx context.Context, siteSlug string) (*SiteTree, error) {
	var out SiteTree
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SiteSummary is one entry in the GET /api/v1/sites list.
type SiteSummary struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type sitesResponse struct {
	Sites []SiteSummary `json:"sites"`
}

// Sites calls GET /api/v1/sites.
func (c *Client) Sites(ctx context.Context) ([]SiteSummary, error) {
	var out sitesResponse
	if err := c.Get(ctx, "/api/v1/sites", nil, &out); err != nil {
		return nil, err
	}
	return out.Sites, nil
}

// SourceBinding ties a block to a source artifact in the repo or an upstream.
type SourceBinding struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Hash      string `json:"hash"`
	Generator string `json:"generator"`
}

// ContentBlock is a single block within a page snapshot.
type ContentBlock struct {
	ID            string          `json:"id"`
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	Ownership     string          `json:"ownership"`
	Audiences     []string        `json:"audiences,omitempty"`
	Content       json.RawMessage `json:"content"`
	SourceBinding *SourceBinding  `json:"sourceBinding"`
	Position      int             `json:"position"`
}

// PageLanguage is one language version's freshness, from the platform's page locales.
type PageLanguage struct {
	Language  string `json:"language"`
	Status    string `json:"status"`
	Outdated  bool   `json:"outdated"`
	UpdatedAt string `json:"updatedAt"`
}

// Page statuses reported by GET .../pages; an older server omits the field.
const (
	PageStatusReleased = "released"
	PageStatusDraft    = "draft"
)

// Page is a full page snapshot from GET .../pages.
type Page struct {
	ID            string         `json:"id"`
	Slug          string         `json:"slug"`
	Title         string         `json:"title"`
	SpaceSlug     string         `json:"spaceSlug"`
	Status        string         `json:"status,omitempty"`
	Version       *int           `json:"version"`
	ReleasedAt    string         `json:"releasedAt"`
	Blocks        []ContentBlock `json:"blocks"`
	RepoID        *string        `json:"repoId,omitempty"`
	RepoRemoteKey *string        `json:"repoRemoteKey,omitempty"`
	Languages     []PageLanguage `json:"languages,omitempty"`
}

// IsDraft reports whether this page exists only as a draft or open proposal.
func (p Page) IsDraft() bool {
	return p.Status == PageStatusDraft
}

// PagesResponse wraps the pages list.
type PagesResponse struct {
	Pages []Page `json:"pages"`
}

// PageListOptions filters GET /api/v1/sites/:siteSlug/pages.
type PageListOptions struct {
	SpaceSlug    string
	Repo         string
	Languages    bool
	IncludeDraft bool
}

// ListPages calls GET /api/v1/sites/:siteSlug/pages with the given filters.
func (c *Client) ListPages(ctx context.Context, siteSlug string, opts PageListOptions) ([]Page, error) {
	q := url.Values{}
	if opts.SpaceSlug != "" {
		q.Set("space", opts.SpaceSlug)
	}
	if opts.Repo != "" {
		q.Set("repo", opts.Repo)
	}
	if opts.Languages {
		q.Set("languages", "1")
	}
	if opts.IncludeDraft {
		q.Set("include", "draft")
	}
	var out PagesResponse
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/pages", q, &out); err != nil {
		return nil, err
	}
	return out.Pages, nil
}

// Pages calls GET /api/v1/sites/:siteSlug/pages, optionally scoped to a space.
func (c *Client) Pages(ctx context.Context, siteSlug, spaceSlug string) ([]Page, error) {
	return c.ListPages(ctx, siteSlug, PageListOptions{SpaceSlug: spaceSlug})
}

// APIBlockContent is the documented operation captured in an api block.
type APIBlockContent struct {
	Method    string          `json:"method"`
	Path      string          `json:"path"`
	Summary   string          `json:"summary"`
	Params    json.RawMessage `json:"params"`
	Responses json.RawMessage `json:"responses"`

	Extra map[string]json.RawMessage `json:"-"`
}

// APIBlock is one documented endpoint block.
type APIBlock struct {
	PageID        string          `json:"pageId"`
	PageSlug      string          `json:"pageSlug"`
	SpaceSlug     string          `json:"spaceSlug"`
	BlockID       string          `json:"blockId"`
	Key           string          `json:"key,omitempty"`
	Ownership     string          `json:"ownership"`
	SourceBinding *SourceBinding  `json:"sourceBinding"`
	Content       APIBlockContent `json:"content"`
}

// APIBlocksResponse wraps the api-blocks list.
type APIBlocksResponse struct {
	Blocks []APIBlock `json:"blocks"`
}

// APIBlocks calls GET /api/v1/sites/:siteSlug/api-blocks.
func (c *Client) APIBlocks(ctx context.Context, siteSlug string) ([]APIBlock, error) {
	var out APIBlocksResponse
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/api-blocks", nil, &out); err != nil {
		return nil, err
	}
	return out.Blocks, nil
}

// ReleaseNoteSection is one grouped section of a release-notes proposal.
type ReleaseNoteSection struct {
	Heading string   `json:"heading"`
	Items   []string `json:"items"`
}

// ReleaseNotesRequest is the body of POST .../release-notes.
type ReleaseNotesRequest struct {
	SpaceSlug    string               `json:"spaceSlug"`
	Title        string               `json:"title"`
	Summary      string               `json:"summary,omitempty"`
	Sections     []ReleaseNoteSection `json:"sections,omitempty"`
	BodyMarkdown string               `json:"bodyMarkdown,omitempty"`
	Repo         *RepoRef             `json:"repo,omitempty"`
	Languages    []string             `json:"languages,omitempty"`
}

// ReleaseNotesResponse is the response of POST .../release-notes.
type ReleaseNotesResponse struct {
	PageID     string `json:"pageId"`
	PageSlug   string `json:"pageSlug"`
	ProposalID string `json:"proposalId"`
	Status     string `json:"status"`
	ReviewURL  string `json:"reviewUrl"`
}

// CreateReleaseNotes calls POST /api/v1/sites/:siteSlug/release-notes.
func (c *Client) CreateReleaseNotes(ctx context.Context, siteSlug string, req ReleaseNotesRequest) (*ReleaseNotesResponse, error) {
	var out ReleaseNotesResponse
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/release-notes", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceUpsertRequest is the body of POST .../spaces.
type SpaceUpsertRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Type        string `json:"type,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
}

type spaceEnvelope struct {
	Space Space `json:"space"`
}

// EnsureSpace calls POST /api/v1/sites/:siteSlug/spaces.
func (c *Client) EnsureSpace(ctx context.Context, siteSlug string, req SpaceUpsertRequest) (*Space, error) {
	var out spaceEnvelope
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/spaces", req, &out); err != nil {
		return nil, err
	}
	return &out.Space, nil
}

// SpacePatchRequest is the body of PATCH .../spaces/:spaceSlug.
type SpacePatchRequest struct {
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Parent      *string `json:"parent,omitempty"`
	HomePage    *string `json:"homePage,omitempty"`
}

// MarshalJSON maps empty-string Parent/HomePage pointers to JSON null.
func (r SpacePatchRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if r.Name != "" {
		m["name"] = r.Name
	}
	if r.Description != nil {
		m["description"] = nullableString(*r.Description)
	}
	if r.Parent != nil {
		m["parent"] = nullableString(*r.Parent)
	}
	if r.HomePage != nil {
		m["homePage"] = nullableString(*r.HomePage)
	}
	return json.Marshal(m)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// UpdateSpace calls PATCH /api/v1/sites/:siteSlug/spaces/:spaceSlug.
func (c *Client) UpdateSpace(ctx context.Context, siteSlug, spaceSlug string, req SpacePatchRequest) (*Space, error) {
	var out spaceEnvelope
	path := "/api/v1/sites/" + url.PathEscape(siteSlug) + "/spaces/" + url.PathEscape(spaceSlug)
	if err := c.Patch(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out.Space, nil
}

// Block audiences.
const (
	AudiencePublic     = "public"
	AudienceUsers      = "users"
	AudienceDevelopers = "developers"
)

// BlockInput is a single block to author within a page upsert.
type BlockInput struct {
	Key           string         `json:"key"`
	Type          string         `json:"type"`
	Ownership     string         `json:"ownership"`
	Audiences     []string       `json:"audiences,omitempty"`
	Content       any            `json:"content"`
	SourceBinding *SourceBinding `json:"sourceBinding,omitempty"`
	Position      int            `json:"position"`
}

// PageUpsertRequest is the body of POST .../pages.
type PageUpsertRequest struct {
	SpaceSlug  string       `json:"spaceSlug"`
	Slug       string       `json:"slug"`
	Title      string       `json:"title"`
	Collection string       `json:"collection,omitempty"`
	Blocks     []BlockInput `json:"blocks"`
	Repo       *RepoRef     `json:"repo,omitempty"`
	Languages  []string     `json:"languages,omitempty"`
}

// UpsertPage calls POST /api/v1/sites/:siteSlug/pages.
func (c *Client) UpsertPage(ctx context.Context, siteSlug string, req PageUpsertRequest) (*ReleaseNotesResponse, error) {
	var out ReleaseNotesResponse
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/pages", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePage calls DELETE /api/v1/sites/:siteSlug/spaces/:spaceSlug/pages/:pageSlug.
func (c *Client) DeletePage(ctx context.Context, siteSlug, spaceSlug, pageSlug string) (*ReleaseNotesResponse, error) {
	var out ReleaseNotesResponse
	path := "/api/v1/sites/" + url.PathEscape(siteSlug) +
		"/spaces/" + url.PathEscape(spaceSlug) +
		"/pages/" + url.PathEscape(pageSlug)
	if err := c.Delete(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
