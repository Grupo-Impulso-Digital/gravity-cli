package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type fakeDocs struct {
	search api.SearchRequest
	recall api.RecallRequest
	inv    api.InventoryQuery
}

func (f *fakeDocs) Page(_ context.Context, id string, q api.PageQuery) (*api.PageContent, error) {
	return &api.PageContent{
		Page: api.PageInfo{ID: id, Slug: "refunds", Title: "Refunds", Status: "changed", Site: api.NamedRef{Slug: "dev"}, Space: api.NamedRef{Slug: "api"}},
		Blocks: []api.PageBlock{
			{Key: "api:POST:/v1/refunds", Type: "api", Ownership: "machine", Units: []string{"api:post:/v1/refunds"}, Provenance: &api.BlockProvenance{Repo: "billing-api", CommitSHA: "a1b2c3d4e5"}, Text: "POST /v1/refunds"},
			{Type: "prose", Ownership: "human", Position: 1, Content: json.RawMessage(`{"text":"Refunds take 5 days."}`)},
		},
	}, nil
}

func (f *fakeDocs) Search(_ context.Context, req api.SearchRequest) ([]api.SearchHit, error) {
	f.search = req
	return []api.SearchHit{{PageID: "pg_1", PageSlug: "webhooks", Title: "Webhooks", SiteSlug: "dev", SpaceSlug: "api", Snippet: "retries 5 times", Score: 0.8, Source: "published"}}, nil
}

func (f *fakeDocs) Recall(_ context.Context, req api.RecallRequest) (*api.RecallResult, error) {
	f.recall = req
	repo := "github.com/acme/gateway"
	return &api.RecallResult{Hits: []api.RecallHit{{Memory: api.Memory{Title: "Retries", Kind: "fact", Body: "Gateway caps retries at 3.", Repo: &repo}}}}, nil
}

func (f *fakeDocs) Inventory(_ context.Context, q api.InventoryQuery) (*api.Inventory, error) {
	f.inv = q
	return &api.Inventory{Units: []api.Unit{{Key: "api:post:/v1/refunds", Kind: "api", Title: "Create a refund", Contributors: []api.Contributor{{Repo: api.RepoRef{Name: "gateway"}, Role: "declares", SourceRefs: []string{"routes/billing.ts"}}}}}}, nil
}

func (f *fakeDocs) SpaceTree(context.Context, string) (*api.SpaceTree, error) {
	return &api.SpaceTree{Space: api.NamedRef{Slug: "api", Name: "API"}, Pages: []api.TreePage{{ID: "pg_1", Slug: "rotation", Title: "Rotation", Status: "published", Lock: &api.PageLock{Path: "docs/rotation.md"}}}}, nil
}

func tool(t *testing.T, tools []agent.Tool, name string) agent.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Def.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return agent.Tool{}
}

func TestDocTools(t *testing.T) {
	d := &fakeDocs{}
	tools := agent.DocTools(d, agent.DocScope{SpaceID: "sp_1", Product: "acme", Namespace: "product:acme"})
	ctx := context.Background()

	out, err := tool(t, tools, "read_page").Run(ctx, json.RawMessage(`{"pageId":"pg_9"}`))
	if err != nil || !strings.Contains(out, "[[block key=api:POST:/v1/refunds type=api own=machine by=billing-api@a1b2c3d units=api:post:/v1/refunds]]") || !strings.Contains(out, "[[block key=#1 type=prose own=human]]\nRefunds take 5 days.") {
		t.Fatalf("read_page = %q, %v", out, err)
	}
	out, err = tool(t, tools, "search_docs").Run(ctx, json.RawMessage(`{"query":"retries"}`))
	if err != nil || !strings.Contains(out, "Webhooks") || len(d.search.SpaceIDs) != 1 || !d.search.IncludeDrafts {
		t.Fatalf("search_docs = %q, %v, %+v", out, err, d.search)
	}
	out, err = tool(t, tools, "recall_nucleus").Run(ctx, json.RawMessage(`{"query":"retries"}`))
	if err != nil || !strings.Contains(out, "repo github.com/acme/gateway") || d.recall.Namespace != "product:acme" || d.recall.IncludeShared == nil || !*d.recall.IncludeShared {
		t.Fatalf("recall_nucleus = %q, %v, %+v", out, err, d.recall)
	}
	out, err = tool(t, tools, "product_inventory").Run(ctx, json.RawMessage(`{"unit":"api:post:/v1/refunds"}`))
	if err != nil || !strings.Contains(out, "gateway declares: routes/billing.ts") || d.inv.Product != "acme" {
		t.Fatalf("product_inventory = %q, %v", out, err)
	}
	out, err = tool(t, tools, "list_target").Run(ctx, json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, "locked=docs/rotation.md") {
		t.Fatalf("list_target = %q, %v", out, err)
	}
	if _, err := tool(t, tools, "read_page").Run(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("read_page without pageId succeeded")
	}
}
