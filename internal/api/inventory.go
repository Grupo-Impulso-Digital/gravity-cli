package api

import (
	"context"
	"net/url"
	"strconv"
)

// Unit kinds.
const (
	UnitKindFeature    = "feature"
	UnitKindService    = "service"
	UnitKindSystem     = "system"
	UnitKindAPI        = "api"
	UnitKindCapability = "capability"
)

// Contributor roles.
const (
	RoleDeclares   = "declares"
	RoleImplements = "implements"
	RoleDocuments  = "documents"
)

// InventoryQuery filters GET /api/v1/products/self/inventory.
type InventoryQuery struct {
	Product         string
	Kind            string
	Q               string
	Unit            string
	IncludeInactive bool
	Limit           int
	Cursor          string
}

func (q InventoryQuery) values() url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("product", q.Product)
	set("kind", q.Kind)
	set("q", q.Q)
	set("unit", q.Unit)
	set("cursor", q.Cursor)
	if q.IncludeInactive {
		v.Set("includeInactive", "true")
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

// Inventory is one page of GET /api/v1/products/self/inventory.
type Inventory struct {
	Product    Product `json:"product"`
	Units      []Unit  `json:"units"`
	NextCursor *string `json:"nextCursor"`
}

// Inventory calls GET /api/v1/products/self/inventory.
func (c *Client) Inventory(ctx context.Context, q InventoryQuery) (*Inventory, error) {
	var out Inventory
	if err := c.Get(ctx, "/api/v1/products/self/inventory", q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IngestUnit is one unit this repository reports.
type IngestUnit struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Audiences  []string `json:"audiences,omitempty"`
	Roles      []string `json:"roles"`
	SourceRefs []string `json:"sourceRefs"`
	SourceHash string   `json:"sourceHash,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
}

// IngestEntry is a (key, roles) pair of the closing ingest call.
type IngestEntry struct {
	Key   string   `json:"key"`
	Roles []string `json:"roles"`
}

// IngestRequest is the body of POST /api/v1/products/self/inventory.
type IngestRequest struct {
	Product  string        `json:"-"`
	RunID    string        `json:"runId"`
	HeadSHA  string        `json:"headSha"`
	Complete bool          `json:"complete"`
	Units    []IngestUnit  `json:"units,omitempty"`
	Entries  []IngestEntry `json:"entries,omitempty"`
}

// IngestCounts counts contributor rows touched by an ingest.
type IngestCounts struct {
	Created     int `json:"created"`
	Updated     int `json:"updated"`
	Unchanged   int `json:"unchanged"`
	Deactivated int `json:"deactivated"`
}

// IngestConflict is a unit the ingest could not apply.
type IngestConflict struct {
	Key          string `json:"key"`
	Reason       string `json:"reason"`
	ExistingKind string `json:"existingKind,omitempty"`
}

// IngestResult is the response of an inventory ingest.
type IngestResult struct {
	Counts    IngestCounts     `json:"counts"`
	Handoffs  []Handoff        `json:"handoffs"`
	Conflicts []IngestConflict `json:"conflicts"`
}

// IngestInventory calls POST /api/v1/products/self/inventory.
func (c *Client) IngestInventory(ctx context.Context, req IngestRequest) (*IngestResult, error) {
	var out IngestResult
	var q url.Values
	if req.Product != "" {
		q = url.Values{"product": {req.Product}}
	}
	if err := c.do(ctx, request{method: "POST", path: "/api/v1/products/self/inventory", query: q, body: req}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
