package api

import (
	"context"
	"net/url"
)

// Unit kinds an inventory unit is classified as.
const (
	UnitKindFeature    = "feature"
	UnitKindService    = "service"
	UnitKindSystem     = "system"
	UnitKindAPI        = "api"
	UnitKindCapability = "capability"
)

// Coverage states a unit can be in.
const (
	UnitStateDocumented   = "documented"
	UnitStateStale        = "stale"
	UnitStateUndocumented = "undocumented"
)

// InventoryUnit is one documentable thing this repo contains.
type InventoryUnit struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary,omitempty"`
	SourceRefs []string `json:"sourceRefs,omitempty"`
	SourceHash string   `json:"sourceHash,omitempty"`
	Audiences  []string `json:"audiences,omitempty"`
	PageSlugs  []string `json:"pageSlugs,omitempty"`
}

// InventoryRequest is the body of POST /api/v1/sites/:siteSlug/inventory.
type InventoryRequest struct {
	Repo        RepoRef         `json:"repo"`
	GeneratedAt string          `json:"generatedAt,omitempty"`
	Replace     bool            `json:"replace"`
	Units       []InventoryUnit `json:"units"`
}

// InventoryCounts reports what the platform did with the submitted units.
type InventoryCounts struct {
	Received  int `json:"received"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
}

// InventoryResponse is the response of POST /api/v1/sites/:siteSlug/inventory.
type InventoryResponse struct {
	RepoID      string          `json:"repoId"`
	Units       InventoryCounts `json:"units"`
	CoverageURL string          `json:"coverageUrl"`
}

// PublishInventory calls POST /api/v1/sites/:siteSlug/inventory.
func (c *Client) PublishInventory(ctx context.Context, siteSlug string, req InventoryRequest) (*InventoryResponse, error) {
	var out InventoryResponse
	if err := c.Post(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/inventory", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CoverageTotals aggregates a repo's unit states.
type CoverageTotals struct {
	Units        int     `json:"units"`
	Documented   int     `json:"documented"`
	Stale        int     `json:"stale"`
	Undocumented int     `json:"undocumented"`
	Ratio        float64 `json:"ratio"`
}

// CoverageKind is CoverageTotals for one unit kind.
type CoverageKind struct {
	Kind         string  `json:"kind"`
	Units        int     `json:"units"`
	Documented   int     `json:"documented"`
	Stale        int     `json:"stale"`
	Undocumented int     `json:"undocumented"`
	Ratio        float64 `json:"ratio"`
}

// CoverageUnit is one inventory unit with its resolved state.
type CoverageUnit struct {
	Key          string   `json:"key"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	State        string   `json:"state"`
	PageSlugs    []string `json:"pageSlugs,omitempty"`
	SourceRefs   []string `json:"sourceRefs,omitempty"`
	FirstSeenAt  string   `json:"firstSeenAt,omitempty"`
	LastSeenAt   string   `json:"lastSeenAt,omitempty"`
	DocumentedAt string   `json:"documentedAt,omitempty"`
}

// CoverageUncoveredPage is a page this repo wrote that its current inventory no longer claims.
type CoverageUncoveredPage struct {
	PageID     string `json:"pageId"`
	Slug       string `json:"slug"`
	SpaceSlug  string `json:"spaceSlug"`
	Title      string `json:"title"`
	ReleasedAt string `json:"releasedAt,omitempty"`
}

// RepoCoverage is one repo's coverage report.
type RepoCoverage struct {
	RepoID         string                  `json:"repoId"`
	RemoteKey      string                  `json:"remoteKey"`
	Name           string                  `json:"name"`
	ProductSlug    string                  `json:"productSlug,omitempty"`
	RepoRole       string                  `json:"repoRole,omitempty"`
	LastPingAt     string                  `json:"lastPingAt,omitempty"`
	LastWriteAt    string                  `json:"lastWriteAt,omitempty"`
	Totals         CoverageTotals          `json:"totals"`
	ByKind         []CoverageKind          `json:"byKind,omitempty"`
	Units          []CoverageUnit          `json:"units,omitempty"`
	UncoveredPages []CoverageUncoveredPage `json:"uncoveredPages,omitempty"`
}

// CoverageResponse is the response of GET /api/v1/sites/:siteSlug/coverage.
type CoverageResponse struct {
	SiteSlug    string         `json:"siteSlug"`
	GeneratedAt string         `json:"generatedAt"`
	Repos       []RepoCoverage `json:"repos"`
}

// Coverage calls GET /api/v1/sites/:siteSlug/coverage.
func (c *Client) Coverage(ctx context.Context, siteSlug, remoteKey, kind string) (*CoverageResponse, error) {
	q := url.Values{}
	if remoteKey != "" {
		q.Set("repo", remoteKey)
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	var out CoverageResponse
	if err := c.Get(ctx, "/api/v1/sites/"+url.PathEscape(siteSlug)+"/coverage", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
