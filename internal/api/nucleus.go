package api

import (
	"context"
	"net/url"
)

// --- Nucleus memory service — GREENFIELD contract. ---
// Nucleus stores small "atoms" of memory that link to other atoms, forming a
// graph the AI can traverse instead of reading whole documents. The service
// exists but its API/MCP is not ready; the CLI integrates as a thin, best-effort
// client that degrades gracefully (see IsUnavailable) until it ships.

// AtomSource ties a code-distilled atom back to its source (reusing the doc
// SourceBinding shape so atoms are drift-traceable like blocks).
type AtomSource struct {
	Kind      string `json:"kind,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Hash      string `json:"hash,omitempty"`
	Generator string `json:"generator,omitempty"`
}

// AtomScope keys an atom to a product namespace and (optionally) a site/space.
type AtomScope struct {
	Namespace string `json:"namespace,omitempty"`
	Site      string `json:"site,omitempty"`
	Space     string `json:"space,omitempty"`
}

// Atom is one unit of memory. Links are atom-id edges to related atoms.
type Atom struct {
	ID        string      `json:"id,omitempty"`
	Content   string      `json:"content"`
	Links     []string    `json:"links,omitempty"`
	Tags      []string    `json:"tags,omitempty"`
	Source    *AtomSource `json:"source,omitempty"`
	Scope     *AtomScope  `json:"scope,omitempty"`
	Score     float64     `json:"score,omitempty"`
	UpdatedAt string      `json:"updatedAt,omitempty"`
}

// AtomQuery retrieves relevant atoms for a context.
type AtomQuery struct {
	Query string   `json:"query,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Site  string   `json:"site,omitempty"`
	Space string   `json:"space,omitempty"`
	Limit int      `json:"limit,omitempty"`
}

type atomQueryResponse struct {
	Atoms []Atom `json:"atoms"`
}

// AtomUpsertResponse is the response of upserting an atom.
type AtomUpsertResponse struct {
	Atom    Atom `json:"atom"`
	Created bool `json:"created"`
}

// QueryAtoms calls POST /api/v1/knowledge/:namespace/atoms/query.
func (c *Client) QueryAtoms(ctx context.Context, namespace string, q AtomQuery) ([]Atom, error) {
	var out atomQueryResponse
	if err := c.Post(ctx, "/api/v1/knowledge/"+url.PathEscape(namespace)+"/atoms/query", q, &out); err != nil {
		return nil, err
	}
	return out.Atoms, nil
}

// UpsertAtom calls POST /api/v1/knowledge/:namespace/atoms (idempotent on id, or
// on source ref+hash). The atom carries its links, so no separate link endpoint
// is needed.
func (c *Client) UpsertAtom(ctx context.Context, namespace string, a Atom) (*AtomUpsertResponse, error) {
	var out AtomUpsertResponse
	if err := c.Post(ctx, "/api/v1/knowledge/"+url.PathEscape(namespace)+"/atoms", a, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
