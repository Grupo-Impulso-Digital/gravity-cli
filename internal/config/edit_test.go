package config

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestSetKeyKeepsTheRestOfTheFile(t *testing.T) {
	src := []byte("# docs for billing\nversion: 2\nproduct: billing\npasses:\n  - name: api\n    kind: reference\n    target: dev/api\n")
	out, err := SetKey(src, "structure", api.Structure{Site: api.StructureSite{Slug: "dev", Name: "Dev"}, Spaces: []api.StructureSpace{{Slug: "api", Name: "API", Type: "api-reference"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "# docs for billing\nversion: 2\n") || !strings.Contains(s, "structure:\n  site:\n    slug: dev\n") || !strings.Contains(s, "  - name: api\n") {
		t.Fatalf("out =\n%s", s)
	}
	if _, err := Parse(out); err != nil {
		t.Fatal(err)
	}
	again, err := SetKey(out, "structure", api.Structure{Site: api.StructureSite{Slug: "docs"}})
	if err != nil || strings.Count(string(again), "structure:") != 1 || !strings.Contains(string(again), "slug: docs") {
		t.Fatalf("replace =\n%s", again)
	}
}

func TestUpsertPassAddsOrReplacesByName(t *testing.T) {
	src := []byte("version: 2\npasses:\n  - name: api\n    kind: reference\n    target: dev/api\n")
	out, err := UpsertPass(src, Pass{Name: "docs", Kind: KindVerbatim, Target: "dev/docs", Options: map[string]any{"files": []VerbatimFile{{Include: "README.md", Slug: "overview"}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err = UpsertPass(out, Pass{Name: "api", Kind: KindReference, Target: "dev/reference"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(m.Passes) != 2 || m.Passes[0].Target != "dev/reference" || m.Passes[1].Name != "docs" {
		t.Fatalf("passes = %+v", m.Passes)
	}
	fresh, err := UpsertPass(nil, Pass{Name: "docs", Kind: KindNucleus})
	if err != nil || !strings.Contains(string(fresh), "passes:\n  - name: docs\n") {
		t.Fatalf("fresh =\n%s", fresh)
	}
}
