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

// SetupPingRequest is the body of POST /api/v1/setup/ping — the one-shot setup
// handshake the web app polls after guiding a user through install + `gravity
// init`. It carries no secret beyond the bearer token; the server associates the
// ping with the acting key so the browser can confirm the token works and render
// this repo's resolved configuration.
type SetupPingRequest struct {
	CLI    PingCLI    `json:"cli"`
	Config PingConfig `json:"config"`
	Repo   PingRepo   `json:"repo"`
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

// PingRepo identifies the local repo: its manifest/directory name, a normalized
// remote (scheme + credentials stripped), and the current branch.
type PingRepo struct {
	Name   string `json:"name,omitempty"`
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// SetupPingResponse is the response of POST /api/v1/setup/ping.
type SetupPingResponse struct {
	OK               bool   `json:"ok"`
	OrganizationName string `json:"organizationName"`
	KeyHint          string `json:"keyHint"`
	DefaultSiteSlug  string `json:"defaultSiteSlug"`
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

// Space is a page container within a site. A non-nil ParentSpaceID marks it as
// a subspace (one level of nesting); OverviewPageID is the page rendered when a
// reader lands on the space itself (its home page). Both are nil on platforms
// predating the space hierarchy.
type Space struct {
	ID             string  `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	ParentSpaceID  *string `json:"parentSpaceId,omitempty"`
	OverviewPageID *string `json:"overviewPageId,omitempty"`
}

// Collection is a folder of pages INSIDE a space (space-scoped since the
// hierarchy inversion), self-nesting via ParentID.
type Collection struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parentId"`
	SpaceID   string  `json:"spaceId,omitempty"`
	SpaceSlug string  `json:"spaceSlug,omitempty"`
}

// PageRef is a lightweight page reference in the site tree. A non-nil
// CollectionID files the page under one of its space's collections.
type PageRef struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Title        string  `json:"title"`
	SpaceID      string  `json:"spaceId"`
	SpaceSlug    string  `json:"spaceSlug"`
	CollectionID *string `json:"collectionId,omitempty"`
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

// SiteSummary is one entry in the GET /api/v1/sites list: the org's sites the
// acting key may see (scope-aware). It carries the platform's Description, which
// the site-tree Site does not, so it is kept distinct from Site.
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

// Sites calls GET /api/v1/sites, returning the sites the token may target. Used
// by `gravity init` to let the user pick a site rather than type its slug.
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
// Parent (a space slug) creates the space as a subspace, one level deep; on
// the existing-space path a differing parent reparents it (declarative), while
// an omitted parent leaves the hierarchy untouched. Requires the platform's
// space-hierarchy feature — older servers reject unknown fields, so callers
// must omit Parent unless whoami advertises it.
type SpaceUpsertRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parent      string `json:"parent,omitempty"`
}

// spaceEnvelope matches the { space: {...} } body the space endpoints return.
type spaceEnvelope struct {
	Space Space `json:"space"`
}

// EnsureSpace calls POST /api/v1/sites/:siteSlug/spaces, creating the space if
// it does not exist and returning the existing one otherwise.
func (c *Client) EnsureSpace(ctx context.Context, siteSlug string, req SpaceUpsertRequest) (*Space, error) {
	var out spaceEnvelope
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/spaces", req, &out); err != nil {
		return nil, err
	}
	return &out.Space, nil
}

// SpacePatchRequest is the body of PATCH .../spaces/:spaceSlug. Pointer fields
// distinguish "leave unchanged" (nil) from "set" / "clear" (pointer to a value
// or to the empty string — the client maps an empty string to JSON null).
type SpacePatchRequest struct {
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	// Parent reparents the space under the named top-level space; a pointer to
	// "" promotes it back to the top level (sent as null).
	Parent *string `json:"parent,omitempty"`
	// HomePage pins the named page (a slug within this space) as the space's
	// overview/home page; a pointer to "" unsets it (sent as null).
	HomePage *string `json:"homePage,omitempty"`
}

// MarshalJSON maps empty-string Parent/HomePage pointers to JSON null (the
// server's "clear" sentinel) while keeping omitted fields absent.
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

// UpdateSpace calls PATCH /api/v1/sites/:siteSlug/spaces/:spaceSlug (partial
// update: name / description / parent / home page). Requires the platform's
// space-hierarchy feature for the Parent/HomePage fields.
func (c *Client) UpdateSpace(ctx context.Context, siteSlug, spaceSlug string, req SpacePatchRequest) (*Space, error) {
	var out spaceEnvelope
	path := "/api/v1/sites/" + url.PathEscape(siteSlug) + "/spaces/" + url.PathEscape(spaceSlug)
	if err := c.Patch(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out.Space, nil
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
// publishes directly. Collection (a slug within the space) files the page
// under that collection, get-or-creating it — how a shared space's pages are
// grouped per repo. Omitted = the page's current placement is left alone.
// Requires the platform's space-hierarchy feature; callers must omit it unless
// whoami advertises support.
type PageUpsertRequest struct {
	SpaceSlug  string       `json:"spaceSlug"`
	Slug       string       `json:"slug"`
	Title      string       `json:"title"`
	Collection string       `json:"collection,omitempty"`
	Blocks     []BlockInput `json:"blocks"`
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

// DeletePage calls DELETE /api/v1/sites/:siteSlug/spaces/:spaceSlug/pages/:pageSlug.
// Deletion is governed: the server opens a `delete` proposal (a change request a
// reviewer approves) rather than removing the page immediately, and the call is
// idempotent (re-requesting reuses the open proposal). The response mirrors the
// upsert shape (pageId, pageSlug, proposalId, status, reviewUrl).
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
