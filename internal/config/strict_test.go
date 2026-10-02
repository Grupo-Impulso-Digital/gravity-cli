package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadErr(t *testing.T, body string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProject(dir)
	return err
}

func TestStrictParsingSuggestsTheIntendedKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"top level", "site: acme\nrelaseNotes:\n  space: news\n", `line 2: unknown key "relaseNotes" in the top level — did you mean "releaseNotes"?`},
		{"nested", "site: acme\nspaces:\n  defalt: guides\n", `line 3: unknown key "spaces.defalt" in spaces — did you mean "default"?`},
		{"in a list", "site: acme\ndocuments:\n  - file: README.md\n    page: overview\n    ownrship: human\n", `unknown key "documents[0].ownrship" in documents[0] — did you mean "ownership"?`},
		{"far off", "site: acme\nwibble: 1\n", `unknown key "wibble" in the top level (valid keys:`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadErr(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant it to contain %q", err, tc.want)
			}
		})
	}
}

func TestRemovedKeysFailClearly(t *testing.T) {
	err := loadErr(t, "site: acme\nsources:\n  - source: openapi.yaml\n    page: api\n    generator: x\nknowledge:\n  scope: acme-api\n")
	if err == nil {
		t.Fatal("removed keys must fail")
	}
	for _, want := range []string{
		"line 5: sources[0].generator was removed in v0.3",
		"line 7: knowledge.scope was removed in v0.3",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	}
}

func TestStrictParsingAcceptsEveryDocumentedKey(t *testing.T) {
	body := `version: 1
site: acme
apiUrl: https://api.gravitydocs.io
product: {slug: acme, repo: api, role: api}
spaces:
  default: guides
  shared: [platform]
  parent: platform
  home: overview
  declare:
    - {slug: platform, name: Platform, type: product-docs, visibility: public, audiences: [public]}
sources:
  - {source: openapi.yaml, kind: openapi, space: guides, page: api, title: API, collection: rest}
documents:
  - {file: README.md, space: guides, page: overview, title: Hi, ownership: human, as: page, version: "1", collection: intro}
releaseNotes: {space: news, changelog: CHANGELOG.md}
knowledge: {namespace: acme}
discovery:
  units: service
  include: [src/**]
  exclude: [src/x/**]
  entrypoints: [src/main.go]
  audiences: {default: [users]}
i18n: {languages: [en, fr]}
coverage: {min: 0.5, require: [overview]}
`
	if err := loadErr(t, body); err != nil {
		t.Errorf("a manifest using every documented key must load: %v", err)
	}
}

func TestParseProjectForMigrationDropsRemovedKeys(t *testing.T) {
	p, dropped, err := ParseProjectForMigration([]byte("site: acme\nknowledge:\n  namespace: n\n  scope: s\n"), ProjectFileName)
	if err != nil {
		t.Fatal(err)
	}
	if p.Knowledge.Namespace != "n" || len(dropped) != 1 || !strings.Contains(dropped[0], "knowledge.scope") {
		t.Errorf("p = %+v dropped = %v", p, dropped)
	}
	if _, _, err := ParseProjectForMigration([]byte("site: acme\nsitee: x\n"), ProjectFileName); err == nil {
		t.Error("a typo must still fail a migration rather than be silently dropped")
	}
}
