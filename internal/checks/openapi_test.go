package checks_test

import (
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
)

const fixtureSpec = `
openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /users:
    get:
      summary: List users
    post:
      summary: Create a user
  /users/{id}:
    get:
      summary: Get a user
`

func TestParseOpenAPI(t *testing.T) {
	ops, err := checks.ParseOpenAPI([]byte(fixtureSpec))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("expected 3 operations, got %d: %+v", len(ops), ops)
	}
	if op, ok := ops["GET /users"]; !ok || op.Summary != "List users" {
		t.Errorf("GET /users not parsed correctly: %+v", op)
	}
	if _, ok := ops["POST /users"]; !ok {
		t.Error("POST /users missing")
	}
}

func TestDiffOperations(t *testing.T) {
	spec, err := checks.ParseOpenAPI([]byte(fixtureSpec))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	docs := []checks.DocumentedOp{
		// Matches spec exactly.
		{Method: "GET", Path: "/users", Summary: "List users", PageSlug: "users"},
		// Same op but summary drifted -> changed.
		{Method: "GET", Path: "/users/{id}", Summary: "Fetch a single user", PageSlug: "users"},
		// Documented but not in spec -> orphaned.
		{Method: "DELETE", Path: "/users/{id}", Summary: "Delete a user", PageSlug: "users"},
		// POST /users is in spec but NOT here -> undocumented.
	}

	findings := checks.DiffOperations(spec, docs)

	byKind := map[string][]checks.APIDiffFinding{}
	for _, f := range findings {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}

	if len(byKind["undocumented"]) != 1 || byKind["undocumented"][0].Path != "/users" {
		t.Errorf("expected 1 undocumented (POST /users), got %+v", byKind["undocumented"])
	}
	if len(byKind["orphaned"]) != 1 || byKind["orphaned"][0].Method != "DELETE" {
		t.Errorf("expected 1 orphaned (DELETE /users/{id}), got %+v", byKind["orphaned"])
	}
	if len(byKind["changed"]) != 1 || byKind["changed"][0].Path != "/users/{id}" {
		t.Errorf("expected 1 changed (GET /users/{id}), got %+v", byKind["changed"])
	}
}

func TestDiffOperationsNoFindingsWhenAligned(t *testing.T) {
	spec, _ := checks.ParseOpenAPI([]byte(fixtureSpec))
	docs := []checks.DocumentedOp{
		{Method: "GET", Path: "/users", Summary: "List users"},
		{Method: "POST", Path: "/users", Summary: "Create a user"},
		{Method: "GET", Path: "/users/{id}", Summary: "Get a user"},
	}
	findings := checks.DiffOperations(spec, docs)
	if len(findings) != 0 {
		t.Errorf("expected no findings when aligned, got %+v", findings)
	}
}
