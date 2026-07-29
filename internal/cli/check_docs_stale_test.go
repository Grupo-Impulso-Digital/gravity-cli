package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func stalePages(t *testing.T, root, ref string) []api.Page {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ref), []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}
	binding := &api.SourceBinding{Kind: "cli", Ref: ref, Hash: "sha256:" + strings.Repeat("0", 64)}
	return []api.Page{{
		Slug: "contract",
		Blocks: []api.ContentBlock{
			{Type: "code", Ownership: "machine", SourceBinding: binding},
			{Type: "prose", Ownership: "hybrid", SourceBinding: binding},
		},
	}}
}

func TestVerifyDocsBindings_MappedStaleIsNoteNotFinding(t *testing.T) {
	root := t.TempDir()
	pages := stalePages(t, root, "docs/contract.md")
	mapped := mappedRefs(&config.Project{Documents: []config.DocMap{{File: "docs/contract.md", Page: "contract"}}})

	res := verifyDocsBindings(root, pages, "acme", mapped)
	if len(res.Findings) != 0 {
		t.Fatalf("mapped stale ref must not produce findings; got %+v", res.Findings)
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "stale but mapped") && strings.Contains(n, "2 block(s)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a deduped stale-but-mapped note; notes = %v", res.Notes)
	}
}

func TestVerifyDocsBindings_UnmappedStaleIsOneDedupedFinding(t *testing.T) {
	root := t.TempDir()
	pages := stalePages(t, root, "docs/contract.md")

	res := verifyDocsBindings(root, pages, "acme", nil)
	if len(res.Findings) != 1 {
		t.Fatalf("expected exactly 1 deduped finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.Kind != "stale" || !strings.Contains(f.Title, "2 affected") {
		t.Errorf("finding = %+v", f)
	}
	if _, err := checks.HashRepoFile(root, "docs/contract.md"); err != nil {
		t.Fatalf("sanity: %v", err)
	}
}
