package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// PageLock describes a repo lock on a page.
type PageLock struct {
	Kind     string   `json:"kind,omitempty"`
	Repo     *RepoRef `json:"repo,omitempty"`
	Pass     string   `json:"pass"`
	Path     string   `json:"path"`
	Branch   string   `json:"branch,omitempty"`
	URL      string   `json:"url,omitempty"`
	Hash     string   `json:"hash"`
	LockedAt string   `json:"lockedAt,omitempty"`
}

// OpenProposal is the open proposal of a page.
type OpenProposal struct {
	ID            string           `json:"id"`
	Status        string           `json:"status"`
	PipelineRunID string           `json:"pipelineRunId,omitempty"`
	CreatedBy     string           `json:"createdBy,omitempty"`
	Pending       *[]PendingChange `json:"pending,omitempty"`
}

// PendingChange is a pipeline change still waiting on an open proposal; nil Pending means the server predates the list.
type PendingChange struct {
	RepoID    string `json:"repoId"`
	RemoteKey string `json:"remoteKey"`
	Pass      string `json:"pass"`
	RunID     string `json:"runId"`
}

// PageInfo is the page section of a content read.
type PageInfo struct {
	ID               string        `json:"id"`
	Slug             string        `json:"slug"`
	Title            string        `json:"title"`
	Position         int           `json:"position"`
	Site             NamedRef      `json:"site"`
	Space            NamedRef      `json:"space"`
	CollectionPath   []string      `json:"collectionPath"`
	Status           string        `json:"status"`
	PublishedVersion *int          `json:"publishedVersion"`
	Lock             *PageLock     `json:"lock"`
	OpenProposal     *OpenProposal `json:"openProposal"`
	Units            []string      `json:"units"`
	UpdatedAt        string        `json:"updatedAt"`
}

// SourceBinding ties a block to the source it was generated from.
type SourceBinding struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Hash      string `json:"hash"`
	Generator string `json:"generator,omitempty"`
}

// PreviousWriter is the writer before the last one.
type PreviousWriter struct {
	Repo      string `json:"repo"`
	CommitSHA string `json:"commitSha"`
	At        string `json:"at"`
}

// BlockProvenance is the last writer of a block.
type BlockProvenance struct {
	Repo      string          `json:"repo"`
	Pass      string          `json:"pass"`
	CommitSHA string          `json:"commitSha"`
	RunID     string          `json:"runId"`
	At        string          `json:"at"`
	State     string          `json:"state"`
	Previous  *PreviousWriter `json:"previous,omitempty"`
}

// PageBlock is one block of a content read.
type PageBlock struct {
	Key           string           `json:"key"`
	Type          string           `json:"type"`
	Ownership     string           `json:"ownership"`
	Audiences     []string         `json:"audiences,omitempty"`
	Position      int              `json:"position"`
	Content       json.RawMessage  `json:"content"`
	SourceBinding *SourceBinding   `json:"sourceBinding,omitempty"`
	Units         []string         `json:"units,omitempty"`
	Provenance    *BlockProvenance `json:"provenance,omitempty"`
	Text          string           `json:"text,omitempty"`
}

// PageContent is the response of GET /api/v1/content/pages/{pageId}.
type PageContent struct {
	Page   PageInfo    `json:"page"`
	Blocks []PageBlock `json:"blocks"`
	Text   string      `json:"text,omitempty"`
}

// PageQuery selects the state, language and format of a content read.
type PageQuery struct {
	State    string
	Language string
	Format   string
}

func (q PageQuery) values() url.Values {
	v := url.Values{}
	if q.State != "" {
		v.Set("state", q.State)
	}
	if q.Language != "" {
		v.Set("language", q.Language)
	}
	if q.Format != "" {
		v.Set("format", q.Format)
	}
	return v
}

// Page calls GET /api/v1/content/pages/{pageId}.
func (c *Client) Page(ctx context.Context, pageID string, q PageQuery) (*PageContent, error) {
	var out PageContent
	if err := c.Get(ctx, "/api/v1/content/pages"+pathEscape(pageID), q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PageBySlug calls GET /api/v1/content/pages?spaceId=&slug=.
func (c *Client) PageBySlug(ctx context.Context, spaceID, slug string, q PageQuery) (*PageContent, error) {
	v := q.values()
	v.Set("spaceId", spaceID)
	v.Set("slug", slug)
	var out PageContent
	if err := c.Get(ctx, "/api/v1/content/pages", v, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResolvedPage is the response of GET /api/v1/content/resolve.
type ResolvedPage struct {
	PageID    string `json:"pageId"`
	SiteSlug  string `json:"siteSlug"`
	SpaceSlug string `json:"spaceSlug"`
	PageSlug  string `json:"pageSlug"`
}

// ResolvePage calls GET /api/v1/content/resolve?ref=.
func (c *Client) ResolvePage(ctx context.Context, ref string) (*ResolvedPage, error) {
	var out ResolvedPage
	if err := c.Get(ctx, "/api/v1/content/resolve", url.Values{"ref": {ref}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TreeCollection is a collection of a space tree.
type TreeCollection struct {
	ID       string   `json:"id"`
	Slug     string   `json:"slug"`
	Path     []string `json:"path"`
	Name     string   `json:"name"`
	ParentID *string  `json:"parentId"`
}

// LastWriter is the last repository that wrote a page.
type LastWriter struct {
	Repo string `json:"repo"`
	At   string `json:"at"`
}

// TreePage is a page of a space tree.
type TreePage struct {
	ID             string        `json:"id"`
	Slug           string        `json:"slug"`
	Title          string        `json:"title"`
	CollectionPath []string      `json:"collectionPath"`
	Position       int           `json:"position"`
	Status         string        `json:"status"`
	Units          []string      `json:"units"`
	Lock           *PageLock     `json:"lock"`
	LastWriter     *LastWriter   `json:"lastWriter"`
	OpenProposal   *OpenProposal `json:"openProposal"`
}

// SpaceTree is one page of GET /api/v1/content/spaces/{spaceId}/tree.
type SpaceTree struct {
	Space       NamedRef         `json:"space"`
	Collections []TreeCollection `json:"collections"`
	Pages       []TreePage       `json:"pages"`
	NextCursor  *string          `json:"nextCursor"`
}

// SpaceTreePage calls GET /api/v1/content/spaces/{spaceId}/tree for one page of results.
func (c *Client) SpaceTreePage(ctx context.Context, spaceID, cursor string, limit int) (*SpaceTree, error) {
	v := url.Values{}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	if limit > 0 {
		v.Set("limit", itoa(limit))
	}
	var out SpaceTree
	if err := c.Get(ctx, "/api/v1/content/spaces"+pathEscape(spaceID, "tree"), v, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceTree follows nextCursor until the whole tree of a space is read.
func (c *Client) SpaceTree(ctx context.Context, spaceID string) (*SpaceTree, error) {
	first, err := c.SpaceTreePage(ctx, spaceID, "", 0)
	if err != nil {
		return nil, err
	}
	all := *first
	cursor := first.NextCursor
	for cursor != nil && *cursor != "" {
		next, err := c.SpaceTreePage(ctx, spaceID, *cursor, 0)
		if err != nil {
			return nil, err
		}
		all.Pages = append(all.Pages, next.Pages...)
		cursor = next.NextCursor
	}
	all.NextCursor = nil
	return &all, nil
}

// SearchRequest is the body of POST /api/v1/content/search.
type SearchRequest struct {
	Query         string   `json:"query"`
	SiteIDs       []string `json:"siteIds"`
	SpaceIDs      []string `json:"spaceIds"`
	Limit         int      `json:"limit,omitempty"`
	IncludeDrafts bool     `json:"includeDrafts"`
}

// SearchHit is one search result.
type SearchHit struct {
	PageID    string  `json:"pageId"`
	PageSlug  string  `json:"pageSlug"`
	Title     string  `json:"title"`
	SiteSlug  string  `json:"siteSlug"`
	SpaceSlug string  `json:"spaceSlug"`
	BlockKey  string  `json:"blockKey,omitempty"`
	Anchor    string  `json:"anchor,omitempty"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
}

// Search calls POST /api/v1/content/search.
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	if req.SiteIDs == nil {
		req.SiteIDs = []string{}
	}
	if req.SpaceIDs == nil {
		req.SpaceIDs = []string{}
	}
	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	if err := c.Post(ctx, "/api/v1/content/search", req, &out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}

// ProvenanceEntry is one write in a block's history.
type ProvenanceEntry struct {
	Action     string   `json:"action"`
	State      string   `json:"state"`
	Repo       string   `json:"repo"`
	Pass       string   `json:"pass"`
	RunID      string   `json:"runId"`
	CommitSHA  string   `json:"commitSha"`
	SourceRefs []string `json:"sourceRefs"`
	Units      []string `json:"units"`
	At         string   `json:"at"`
}

// BlockHistory is the provenance history of one block, newest first.
type BlockHistory struct {
	Key     string            `json:"key"`
	History []ProvenanceEntry `json:"history"`
}

// PageProvenance is the response of GET /api/v1/content/pages/{pageId}/provenance.
type PageProvenance struct {
	Page   PageInfo       `json:"page"`
	Blocks []BlockHistory `json:"blocks"`
}

// Provenance calls GET /api/v1/content/pages/{pageId}/provenance.
func (c *Client) Provenance(ctx context.Context, pageID string) (*PageProvenance, error) {
	var out PageProvenance
	if err := c.Get(ctx, "/api/v1/content/pages"+pathEscape(pageID, "provenance"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
