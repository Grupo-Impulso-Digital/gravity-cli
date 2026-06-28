package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// WhoAmI is the response of GET /api/v1/whoami.
type WhoAmI struct {
	OrganizationID   string  `json:"organizationId"`
	OrganizationName string  `json:"organizationName"`
	DefaultSiteSlug  *string `json:"defaultSiteSlug"`
	KeyHint          string  `json:"keyHint"`
	// Features advertises optional platform capabilities (e.g. "captures",
	// "nucleus"). Absent/nil means the platform predates feature reporting, so
	// every optional feature reads as "not yet available".
	Features map[string]bool `json:"features,omitempty"`
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

// Site describes a documentation site.
type Site struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

// Space is a top-level grouping within a site.
type Space struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Collection groups spaces/pages, possibly nested via ParentID.
type Collection struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	ParentID *string `json:"parentId"`
}

// PageRef is a lightweight page reference in the site tree.
type PageRef struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	SpaceSlug string `json:"spaceSlug"`
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

// SourceBinding ties a block to a source artifact in the repo or an upstream.
type SourceBinding struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Hash      string `json:"hash"`
	Generator string `json:"generator"`
}

// ContentBlock is a single block within a page snapshot.
type ContentBlock struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Type      string `json:"type"`
	Ownership string `json:"ownership"`
	// Audiences restricts which viewers see the block (public|users|developers);
	// empty renders to everyone. Absent on platforms predating block audiences.
	Audiences     []string        `json:"audiences,omitempty"`
	Content       json.RawMessage `json:"content"`
	SourceBinding *SourceBinding  `json:"sourceBinding"`
	Position      int             `json:"position"`
}

// Page is a full page snapshot from GET .../pages.
type Page struct {
	ID         string         `json:"id"`
	Slug       string         `json:"slug"`
	Title      string         `json:"title"`
	SpaceSlug  string         `json:"spaceSlug"`
	Version    *int           `json:"version"`
	ReleasedAt string         `json:"releasedAt"`
	Blocks     []ContentBlock `json:"blocks"`
}

// PagesResponse wraps the pages list.
type PagesResponse struct {
	Pages []Page `json:"pages"`
}

// Pages calls GET /api/v1/sites/:siteSlug/pages, optionally scoped to a space.
func (c *Client) Pages(ctx context.Context, siteSlug, spaceSlug string) ([]Page, error) {
	var q url.Values
	if spaceSlug != "" {
		q = url.Values{"space": []string{spaceSlug}}
	}
	var out PagesResponse
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/pages", q, &out); err != nil {
		return nil, err
	}
	return out.Pages, nil
}

// APIBlockContent is the documented operation captured in an api block.
type APIBlockContent struct {
	Method    string          `json:"method"`
	Path      string          `json:"path"`
	Summary   string          `json:"summary"`
	Params    json.RawMessage `json:"params"`
	Responses json.RawMessage `json:"responses"`
	// Extra preserves any additional fields without losing them.
	Extra map[string]json.RawMessage `json:"-"`
}

// APIBlock is one documented endpoint block.
type APIBlock struct {
	PageID        string          `json:"pageId"`
	PageSlug      string          `json:"pageSlug"`
	SpaceSlug     string          `json:"spaceSlug"`
	BlockID       string          `json:"blockId"`
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

// ReleaseNotesRequest is the body of POST .../release-notes. Provide either
// Sections or BodyMarkdown.
type ReleaseNotesRequest struct {
	SpaceSlug    string               `json:"spaceSlug"`
	Title        string               `json:"title"`
	Summary      string               `json:"summary,omitempty"`
	Sections     []ReleaseNoteSection `json:"sections,omitempty"`
	BodyMarkdown string               `json:"bodyMarkdown,omitempty"`
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

// SpaceUpsertRequest is the body of POST .../spaces. Upsert is idempotent on
// slug: an existing space is returned (200) rather than duplicated (201).
type SpaceUpsertRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// EnsureSpace calls POST /api/v1/sites/:siteSlug/spaces, creating the space if
// it does not exist and returning the existing one otherwise.
func (c *Client) EnsureSpace(ctx context.Context, siteSlug string, req SpaceUpsertRequest) (*Space, error) {
	var out Space
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/spaces", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Block audiences. A block with no audiences renders to every viewer; a block
// with one or more renders only to viewers in a listed audience. The platform
// must advertise support via whoami.features before the CLI emits the field.
const (
	AudiencePublic     = "public"
	AudienceUsers      = "users"
	AudienceDevelopers = "developers"
)

// BlockInput is a single block to author within a page upsert.
type BlockInput struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	Ownership string `json:"ownership"`
	// Audiences restricts which viewers see the block (public|users|developers);
	// empty renders to everyone.
	Audiences     []string       `json:"audiences,omitempty"`
	Content       any            `json:"content"`
	SourceBinding *SourceBinding `json:"sourceBinding,omitempty"`
	Position      int            `json:"position"`
}

// PageUpsertRequest is the body of POST .../pages. Upsert is keyed on
// (spaceSlug, slug); it produces a DRAFT plus an open proposal and never
// publishes directly.
type PageUpsertRequest struct {
	SpaceSlug string       `json:"spaceSlug"`
	Slug      string       `json:"slug"`
	Title     string       `json:"title"`
	Blocks    []BlockInput `json:"blocks"`
}

// UpsertPage calls POST /api/v1/sites/:siteSlug/pages. The response mirrors the
// release-notes shape (pageId, pageSlug, proposalId, status, reviewUrl).
func (c *Client) UpsertPage(ctx context.Context, siteSlug string, req PageUpsertRequest) (*ReleaseNotesResponse, error) {
	var out ReleaseNotesResponse
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/pages", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
