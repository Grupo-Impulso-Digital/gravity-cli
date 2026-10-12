package docs_test

import (
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

func TestOperationsCarryTheirLineRange(t *testing.T) {
	ops, err := docs.Operations([]byte(specV3))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, op := range ops {
		got[op.Method+" "+op.Path] = [2]int{op.LineStart, op.LineEnd}
	}
	want := map[string][2]int{"GET /users": {7, 12}, "GET /users/{id}": {14, 31}}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s: got %v, want %v (all %v)", k, got[k], w, got)
		}
	}
}

func TestOperationLinesInJSON(t *testing.T) {
	spec := "{\n  \"openapi\": \"3.0.0\",\n  \"info\": {\"title\": \"t\", \"version\": \"1\"},\n  \"paths\": {\n    \"/a\": {\n      \"post\": {\n        \"responses\": {\"200\": {\"description\": \"ok\"}}\n      }\n    }\n  }\n}\n"
	ops, err := docs.Operations([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].LineStart != 6 || ops[0].LineEnd != 7 {
		t.Fatalf("got %+v", ops)
	}
}

func TestLocateFillsTheEndpointBinding(t *testing.T) {
	ops, err := docs.Operations([]byte(specV3))
	if err != nil {
		t.Fatal(err)
	}
	b, err := docs.APIBlock(ops[0], "gravity-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	hash := b.SourceBinding.Hash
	docs.Locate(&b, ops[0], "openapi.yaml", "0123456789abcdef0123456789abcdef01234567")
	sb := b.SourceBinding
	if sb.Path != "openapi.yaml" || sb.LineStart != ops[0].LineStart || sb.LineEnd != ops[0].LineEnd || sb.SHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("binding %+v", sb)
	}
	if sb.Hash != hash || sb.Ref != "GET /users" {
		t.Fatalf("identity changed: %+v", sb)
	}
	c, _ := docs.APIBlock(ops[0], "")
	docs.Locate(&c, ops[0], "openapi.yaml", "HEAD")
	if c.SourceBinding.SHA != "" {
		t.Fatalf("kept a non-sha commit: %+v", c.SourceBinding)
	}
}
