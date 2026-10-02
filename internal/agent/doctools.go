package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const (
	maxPageBytes    = 48 * 1024
	maxSearchHits   = 20
	maxRecallHits   = 20
	maxInventoryOut = 32 * 1024
	maxTreeBytes    = 32 * 1024
)

// DocsAPI is the content surface the doc tools read.
type DocsAPI interface {
	Page(ctx context.Context, pageID string, q api.PageQuery) (*api.PageContent, error)
	Search(ctx context.Context, req api.SearchRequest) ([]api.SearchHit, error)
	Recall(ctx context.Context, req api.RecallRequest) (*api.RecallResult, error)
	Inventory(ctx context.Context, q api.InventoryQuery) (*api.Inventory, error)
	SpaceTree(ctx context.Context, spaceID string) (*api.SpaceTree, error)
}

// DocScope bounds what the doc tools read: the target space, the product and its Nucleus namespace.
type DocScope struct {
	SpaceID   string
	SiteID    string
	Product   string
	Namespace string
}

// DocTools returns read_page, search_docs, recall_nucleus, product_inventory and list_target.
func DocTools(d DocsAPI, s DocScope) []Tool {
	tools := []Tool{
		{
			Def: api.Tool{
				Name:        "read_page",
				Description: "Read a documentation page as text: one header line per block (key, type, ownership, audiences, last writer, units), then its text. Human blocks are read-only.",
				InputSchema: object([]string{"pageId"}, map[string]any{
					"pageId": map[string]any{"type": "string"},
					"state":  enum("draft", "published"),
				}),
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					PageID string `json:"pageId"`
					State  string `json:"state"`
				}
				_ = json.Unmarshal(input, &in)
				if strings.TrimSpace(in.PageID) == "" {
					return "", errors.New("pageId is required")
				}
				text, err := PageText(ctx, d, in.PageID, in.State)
				if err != nil {
					return "", err
				}
				return truncate(text, maxPageBytes, "page"), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "search_docs",
				Description: "Search the documentation (published and draft) for pages and blocks matching a query.",
				InputSchema: object([]string{"query"}, map[string]any{
					"query":     map[string]any{"type": "string"},
					"limit":     map[string]any{"type": "integer"},
					"wholeSite": map[string]any{"type": "boolean", "description": "Search every space you can read instead of the target space."},
				}),
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Query     string `json:"query"`
					Limit     int    `json:"limit"`
					WholeSite bool   `json:"wholeSite"`
				}
				_ = json.Unmarshal(input, &in)
				if strings.TrimSpace(in.Query) == "" {
					return "", errors.New("query is required")
				}
				if in.Limit <= 0 || in.Limit > maxSearchHits {
					in.Limit = 10
				}
				req := api.SearchRequest{Query: in.Query, Limit: in.Limit, IncludeDrafts: true}
				if s.SpaceID != "" && !in.WholeSite {
					req.SpaceIDs = []string{s.SpaceID}
				}
				hits, err := d.Search(ctx, req)
				if err != nil {
					return "", err
				}
				return FormatHits(hits), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "recall_nucleus",
				Description: "Recall facts from the product's shared memory (Nucleus), including facts other repositories recorded.",
				InputSchema: object([]string{"query"}, map[string]any{
					"query": map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				}),
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				_ = json.Unmarshal(input, &in)
				if strings.TrimSpace(in.Query) == "" {
					return "", errors.New("query is required")
				}
				if in.Limit <= 0 || in.Limit > maxRecallHits {
					in.Limit = 8
				}
				shared := true
				res, err := d.Recall(ctx, api.RecallRequest{Query: in.Query, Limit: in.Limit, Namespace: s.Namespace, IncludeShared: &shared})
				if err != nil {
					return "", err
				}
				return FormatRecall(res.Hits), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "product_inventory",
				Description: "Look up the product's units: their contributing repositories and roles (declares, implements, documents), the pages bound to them and detected handoffs. Use it before minting a unit key or before contradicting another repository.",
				InputSchema: object(nil, map[string]any{
					"q":    map[string]any{"type": "string", "description": "Text to match in keys and titles."},
					"kind": enum(unitKinds...),
					"unit": map[string]any{"type": "string", "description": "An exact unit key (or alias)."},
				}),
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Q    string `json:"q"`
					Kind string `json:"kind"`
					Unit string `json:"unit"`
				}
				_ = json.Unmarshal(input, &in)
				inv, err := d.Inventory(ctx, api.InventoryQuery{Product: s.Product, Q: in.Q, Kind: in.Kind, Unit: in.Unit, Limit: 50})
				if err != nil {
					return "", err
				}
				return truncate(FormatUnits(inv.Units), maxInventoryOut, "inventory"), nil
			},
		},
	}
	if s.SpaceID != "" {
		tools = append(tools, Tool{
			Def: api.Tool{
				Name:        "list_target",
				Description: "List the target space: its collections and pages (id, slug, title, status, units, lock, last writer).",
				InputSchema: object(nil, map[string]any{}),
			},
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				tree, err := d.SpaceTree(ctx, s.SpaceID)
				if err != nil {
					return "", err
				}
				return truncate(FormatTree(tree), maxTreeBytes, "tree"), nil
			},
		})
	}
	return tools
}

// PageText reads a page in the LLM-friendly text format, composing it from blocks when the server sends none.
func PageText(ctx context.Context, d DocsAPI, pageID, state string) (string, error) {
	pc, err := d.Page(ctx, pageID, api.PageQuery{State: state, Format: "text"})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(pc.Text) != "" {
		return pc.Text, nil
	}
	return ComposePageText(pc), nil
}

// ComposePageText renders page content as header lines plus block text.
func ComposePageText(pc *api.PageContent) string {
	var b strings.Builder
	locked := "no"
	if pc.Page.Lock != nil {
		locked = "yes (" + pc.Page.Lock.Path + ")"
	}
	fmt.Fprintf(&b, "# %s  (page %s · %s/%s/%s · %s · locked: %s)\n", pc.Page.Title, pc.Page.ID, pc.Page.Site.Slug, pc.Page.Space.Slug, pc.Page.Slug, pc.Page.Status, locked)
	for _, blk := range pc.Blocks {
		key := blk.Key
		if key == "" {
			key = fmt.Sprintf("#%d", blk.Position)
		}
		fmt.Fprintf(&b, "[[block key=%s type=%s own=%s", key, blk.Type, blk.Ownership)
		if len(blk.Audiences) > 0 {
			fmt.Fprintf(&b, " aud=%s", strings.Join(blk.Audiences, ","))
		}
		if blk.Provenance != nil && blk.Provenance.Repo != "" {
			fmt.Fprintf(&b, " by=%s@%s", blk.Provenance.Repo, shortRef(blk.Provenance.CommitSHA))
		}
		if len(blk.Units) > 0 {
			fmt.Fprintf(&b, " units=%s", strings.Join(blk.Units, ","))
		}
		b.WriteString("]]\n")
		text := blk.Text
		if text == "" {
			text = contentText(blk.Content)
		}
		b.WriteString(strings.TrimRight(text, "\n"))
		b.WriteString("\n\n")
	}
	return b.String()
}

func contentText(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	if t, ok := m["text"].(string); ok {
		return t
	}
	return string(raw)
}

func shortRef(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// FormatHits renders search hits one per line.
func FormatHits(hits []api.SearchHit) string {
	if len(hits) == 0 {
		return "(no matches)"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s (page %s · %s/%s/%s", h.Title, h.PageID, h.SiteSlug, h.SpaceSlug, h.PageSlug)
		if h.BlockKey != "" {
			fmt.Fprintf(&b, " · block %s", h.BlockKey)
		}
		fmt.Fprintf(&b, " · %s · %.2f): %s\n", h.Source, h.Score, strings.Join(strings.Fields(h.Snippet), " "))
	}
	return b.String()
}

// FormatRecall renders recalled atoms.
func FormatRecall(hits []api.RecallHit) string {
	if len(hits) == 0 {
		return "(nothing recalled)"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s [%s", h.Title, h.Kind)
		if h.Repo != nil && *h.Repo != "" {
			fmt.Fprintf(&b, " · repo %s", *h.Repo)
		}
		if h.Namespace != nil && *h.Namespace != "" {
			fmt.Fprintf(&b, " · %s", *h.Namespace)
		}
		fmt.Fprintf(&b, "]: %s\n", strings.Join(strings.Fields(h.Body), " "))
	}
	return b.String()
}

// FormatUnits renders product units with contributors, bindings and handoffs.
func FormatUnits(units []api.Unit) string {
	if len(units) == 0 {
		return "(no units)"
	}
	var b strings.Builder
	for _, u := range units {
		fmt.Fprintf(&b, "- %s (%s) %s", u.Key, u.Kind, u.Title)
		if u.Status != "" && u.Status != "active" {
			fmt.Fprintf(&b, " [%s]", u.Status)
		}
		b.WriteByte('\n')
		for _, c := range u.Contributors {
			state := ""
			if c.Active != nil && !*c.Active {
				state = " (inactive)"
			}
			fmt.Fprintf(&b, "    %s %s%s: %s\n", c.Repo.Label(), c.Role, state, strings.Join(c.SourceRefs, ", "))
		}
		for _, bd := range u.Bindings {
			fmt.Fprintf(&b, "    page %s %s/%s/%s\n", bd.PageID, bd.SiteSlug, bd.SpaceSlug, bd.PageSlug)
		}
		for _, h := range u.Handoffs {
			fmt.Fprintf(&b, "    handoff %s %s -> %s (%s)\n", h.Role, h.From, h.To, h.Status)
		}
	}
	return b.String()
}

// FormatTree renders a space tree.
func FormatTree(t *api.SpaceTree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Space %s (%s)\n", t.Space.Name, t.Space.Slug)
	for _, c := range t.Collections {
		fmt.Fprintf(&b, "collection %s %q\n", strings.Join(c.Path, "/"), c.Name)
	}
	for _, p := range t.Pages {
		where := strings.Join(p.CollectionPath, "/")
		if where != "" {
			where += "/"
		}
		fmt.Fprintf(&b, "page %s %s%s %q [%s]", p.ID, where, p.Slug, p.Title, p.Status)
		if len(p.Units) > 0 {
			fmt.Fprintf(&b, " units=%s", strings.Join(p.Units, ","))
		}
		if p.Lock != nil {
			fmt.Fprintf(&b, " locked=%s", p.Lock.Path)
		}
		if p.LastWriter != nil {
			fmt.Fprintf(&b, " by=%s", p.LastWriter.Repo)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
