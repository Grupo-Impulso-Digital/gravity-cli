package legacy

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseV1(t *testing.T) {
	src := `site: docs
product:
  slug: acme
  role: api
spaces:
  default: guides
  declare:
    - slug: guides
      audiences: [developers]
sources:
  - source: api/openapi.yaml
    kind: openapi
    space: api
    page: reference
    generator: x
documents:
  - file: docs/a.md
    page: a
releaseNotes:
  space: changelog
  changelog: CHANGELOG.md
knowledge:
  namespace: acme
  scope: org
`
	p, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if p.Site != "docs" || p.Product.Role != "api" || p.Spaces.Declare[0].Audiences[0] != "developers" {
		t.Fatalf("unexpected decode: %+v", p)
	}
	want := []string{"sources[0].generator", "releaseNotes.changelog", "knowledge.scope"}
	if got := p.DeadKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DeadKeys = %v, want %v", got, want)
	}
}

func TestParseV1UnknownKey(t *testing.T) {
	_, err := Parse([]byte("site: docs\nsorces: []\n"))
	if err == nil || !strings.Contains(err.Error(), "sorces") {
		t.Fatalf("want unknown key error, got %v", err)
	}
}
