package passes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func TestVerbatimSkipsEmptyFilesUnlessAllowed(t *testing.T) {
	r := newRepo(t)
	head := r.commit("docs", map[string]string{
		"docs/handbook/guide.md":        "# Guide\n\nRead me.\n",
		"docs/handbook/todo.md":         "# Todo\n",
		"docs/handbook/blank/README.md": "<!-- soon -->\n",
	})
	fake := newFakeAPI()
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_blank", Slug: "overview", Title: "Blank", Lock: &api.PageLock{Pass: "handbook", Path: "docs/handbook/blank/README.md", Hash: "sha256:old"}}})
	w := &writes{}
	rep, err := passes.Verbatim{}.Run(context.Background(), input(t, r, fake, verbatimPass(), manifest(), "", head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.verbatim) != 1 || w.verbatim[0].File.Path != "docs/handbook/guide.md" {
		t.Fatalf("only the non-empty file is imported: %+v", w.verbatim)
	}
	if len(w.deletions) != 0 {
		t.Fatalf("the page of an empty file is left alone: %+v", w.deletions)
	}
	if blocks := w.verbatim[0].Blocks; len(blocks) == 0 || blocks[0].Type == "heading" {
		t.Fatalf("the leading H1 equal to the title is dropped: %+v", blocks)
	}
	warnings := strings.Join(rep.Warnings, "\n")
	for _, want := range []string{"docs/handbook/todo.md: empty, skipped (set options.allowEmpty: true to import it)", "docs/handbook/blank/README.md: empty, skipped"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q in\n%s", want, warnings)
		}
	}
	allow := planPass(config.KindVerbatim, "handbook", map[string]any{"allowEmpty": true, "files": []any{map[string]any{"include": "docs/handbook/**"}}})
	w = &writes{}
	if _, err := (passes.Verbatim{}).Run(context.Background(), input(t, r, newFakeAPI(), allow, manifest(), "", head, api.ModeWrite), sink(w)); err != nil {
		t.Fatal(err)
	}
	if len(w.verbatim) != 3 {
		t.Fatalf("allowEmpty imports every file: %d", len(w.verbatim))
	}
}
