package docs_test

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

const specV3 = `openapi: 3.0.0
info:
  title: Test
  version: 1.0.0
paths:
  /users:
    get:
      summary: List users
      tags: [Users]
      responses:
        '200':
          description: ok
  /users/{id}:
    get:
      summary: Get a user
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: session
          in: cookie
          schema:
            type: string
      responses:
        '200':
          description: ok
        '404':
          description: missing
`

func TestAPIBlocksCarryEndpointBindingsAndUnits(t *testing.T) {
	blocks, err := docs.APIBlocks([]byte(specV3), "gravity-cli/test")
	if err != nil {
		t.Fatalf("APIBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 api blocks, got %d", len(blocks))
	}
	want := map[string]string{
		"api:GET:/users":      "api:get:/users",
		"api:GET:/users/{id}": "api:get:/users/:id",
	}
	for _, b := range blocks {
		if b.Type != "api" || b.Ownership != "machine" {
			t.Errorf("block %s: type/ownership = %s/%s", b.Key, b.Type, b.Ownership)
		}
		unit, ok := want[b.Key]
		if !ok || len(b.Units) != 1 || b.Units[0] != unit {
			t.Errorf("block %s units = %v", b.Key, b.Units)
		}
		if b.SourceBinding == nil || b.SourceBinding.Kind != "endpoint" || !strings.HasPrefix(b.SourceBinding.Hash, "sha256:") {
			t.Fatalf("block %s binding = %+v", b.Key, b.SourceBinding)
		}
	}
	again, err := docs.APIBlocks([]byte(specV3), "gravity-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].SourceBinding.Hash != blocks[0].SourceBinding.Hash {
		t.Error("endpoint hashes are not deterministic")
	}
}

func TestAPIContentMatchesThePlatformBlockSchema(t *testing.T) {
	ops, err := docs.Operations([]byte(specV3))
	if err != nil {
		t.Fatal(err)
	}
	var get docs.APIContent
	for _, op := range ops {
		if op.OperationID == "getUser" {
			get = docs.Content(op)
		}
	}
	if get.Method != "GET" || len(get.Params) != 1 || get.Params[0].In != "path" || get.Params[0].Type != "string" {
		t.Fatalf("params = %+v", get.Params)
	}
	if len(get.Responses) != 2 || get.Responses[0].Status != "200" || get.Responses[1].Status != "404" {
		t.Fatalf("responses = %+v", get.Responses)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"On-call Rotation!": "on-call-rotation", "  Ünïcode  ": "n-code", "v1.4.0": "v1-4-0"} {
		if got := docs.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
