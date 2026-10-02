package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/llm/v1/prompts/docs-author" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "docs-author", "text": "You are a writer.", "version": "3",
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	text, err := c.Prompt(context.Background(), "docs-author")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if text != "You are a writer." {
		t.Errorf("text = %q", text)
	}
}
