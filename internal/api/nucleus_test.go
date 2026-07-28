package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestQueryAtoms(t *testing.T) {
	var gotPath string
	var gotBody api.AtomQuery
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"atoms": []api.Atom{{ID: "a1", Content: "fact", Tags: []string{"t"}, Score: 0.9}},
		})
	}))
	defer srv.Close()

	atoms, err := api.New(srv.URL, "tok").QueryAtoms(context.Background(), "acme-platform", api.AtomQuery{Query: "webhooks", Limit: 5})
	if err != nil {
		t.Fatalf("QueryAtoms: %v", err)
	}
	if gotPath != "/api/v1/knowledge/acme-platform/atoms/query" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody.Query != "webhooks" || gotBody.Limit != 5 {
		t.Errorf("body = %+v", gotBody)
	}
	if len(atoms) != 1 || atoms[0].Content != "fact" {
		t.Errorf("atoms = %+v", atoms)
	}
}

func TestUpsertAtom(t *testing.T) {
	var gotPath string
	var gotBody api.Atom
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(api.AtomUpsertResponse{Atom: gotBody, Created: true})
	}))
	defer srv.Close()

	resp, err := api.New(srv.URL, "tok").UpsertAtom(context.Background(), "acme-platform", api.Atom{
		Content: "Retries use exponential backoff",
		Scope:   &api.AtomScope{Namespace: "acme-platform", Site: "acme"},
		Source:  &api.AtomSource{Kind: "code", Generator: "test"},
	})
	if err != nil {
		t.Fatalf("UpsertAtom: %v", err)
	}
	if gotPath != "/api/v1/knowledge/acme-platform/atoms" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody.Scope == nil || gotBody.Scope.Namespace != "acme-platform" {
		t.Errorf("scope not round-tripped: %+v", gotBody.Scope)
	}
	if !resp.Created {
		t.Error("expected created=true")
	}
}
