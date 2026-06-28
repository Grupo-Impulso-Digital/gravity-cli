package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
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

func TestPromptUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "unknown_route", "message": "no such route"},
		})
	}))
	defer srv.Close()

	c := api.New(srv.URL, "t")
	_, err := c.Prompt(context.Background(), "docs-author")
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if !apiErr.IsUnavailable() {
		t.Errorf("expected IsUnavailable() for a 404/unknown_route prompt, got %+v", apiErr)
	}
}
