package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func textBlock(typ, key, text string) api.ContentBlock {
	raw, _ := json.Marshal(map[string]any{"text": text})
	return api.ContentBlock{Key: key, Type: typ, Ownership: "human", Content: raw}
}

func TestPageBlockDigestCarriesText(t *testing.T) {
	got := pageBlockDigest([]api.ContentBlock{
		textBlock("heading", "h1", "Billing"),
		textBlock("prose", "p1", "Invoices are issued on the first of the month."),
	})
	for _, want := range []string{"key=h1 type=heading", "text: ## Billing", "text: Invoices are issued on the first of the month."} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}
}

func TestPageBlockDigestIsBounded(t *testing.T) {
	var blocks []api.ContentBlock
	for i := 0; i < 200; i++ {
		blocks = append(blocks, textBlock("prose", "p", strings.Repeat("word ", 400)))
	}
	got := pageBlockDigest(blocks)
	if len(got) > authorDigestLimit+200 {
		t.Errorf("digest is %d bytes, want <= ~%d", len(got), authorDigestLimit)
	}
	if !strings.Contains(got, "more block(s) omitted") {
		t.Error("expected an omission marker")
	}
}

func TestDocsDigestCarriesPageTextWithinBudget(t *testing.T) {
	pages := []api.Page{{Title: "Billing", Slug: "billing", SpaceSlug: "guides", Blocks: []api.ContentBlock{
		textBlock("prose", "p", "Refunds take five business days."),
	}}}
	got := docsDigest(pages)
	if !strings.Contains(got, "Refunds take five business days.") || !strings.Contains(got, `page "Billing" (space guides, slug billing)`) {
		t.Errorf("digest = %s", got)
	}
	for i := 0; i < 100; i++ {
		pages = append(pages, api.Page{Title: "Big", Slug: "big", SpaceSlug: "guides", Blocks: []api.ContentBlock{
			textBlock("prose", "p", strings.Repeat("lorem ipsum ", 1000)),
		}})
	}
	got = docsDigest(pages)
	if len(got) > gapDigestTotal+8*1024 {
		t.Errorf("digest is %d bytes, want bounded near %d", len(got), gapDigestTotal)
	}
	if !strings.Contains(got, "more page(s) omitted") {
		t.Error("expected an omission marker naming the remaining pages")
	}
}

func TestDocsPlanUsesTheLargerOutputBudget(t *testing.T) {
	dir := newGitRepo(t, nil)
	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var maxTokens []int
	fp := &fakePlatform{llm: func(body []byte) any {
		var req api.MessagesRequest
		_ = json.Unmarshal(body, &req)
		maxTokens = append(maxTokens, req.MaxTokens)
		return toolUseResponse(agent.ToolSubmitDocPlan, map[string]any{
			"pages": []any{map[string]any{"slug": "overview", "title": "Overview", "summary": "x"}},
			"units": []any{},
		})
	}}
	srv := fp.serve(t)
	if _, err := runDocsPlan(context.Background(), api.New(srv.URL, "t"), repo, "sys", planScope{audiences: []string{"users"}}, io.Discard, nil); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(maxTokens) == 0 || maxTokens[0] != agent.DefaultPlanMaxTokens || agent.DefaultPlanMaxTokens < 16000 {
		t.Errorf("plan max_tokens = %v, want %d", maxTokens, agent.DefaultPlanMaxTokens)
	}
}

func TestDocsAuthorSeesExistingPageText(t *testing.T) {
	dir := newGitRepo(t, nil)
	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var kickoff string
	fp := &fakePlatform{llm: func(body []byte) any {
		var req api.MessagesRequest
		_ = json.Unmarshal(body, &req)
		kickoff = req.Messages[0].Content[0].Text
		return toolUseResponse(agent.ToolSubmitPageDoc, map[string]any{
			"title":  "Billing",
			"blocks": []any{map[string]any{"key": "p1", "type": "prose", "content": map[string]any{"text": "x"}}},
		})
	}}
	srv := fp.serve(t)
	existing := []api.ContentBlock{textBlock("prose", "p1", "Refunds take five business days.")}
	if _, _, err := runDocsAuthor(context.Background(), api.New(srv.URL, "t"), repo, "sys",
		agent.DocPlanPage{Slug: "billing", Title: "Billing"}, existing, io.Discard, nil); err != nil {
		t.Fatalf("author: %v", err)
	}
	if !strings.Contains(kickoff, "Refunds take five business days.") {
		t.Errorf("the author must see the page's current text:\n%s", kickoff)
	}
}
