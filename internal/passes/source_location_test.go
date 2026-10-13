package passes_test

import (
	"context"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func TestVerbatimEmitsSourceLocationsOnlyWhenTheServerAcceptsThem(t *testing.T) {
	r := newRepo(t)
	head := r.commit("docs", map[string]string{
		"docs/handbook/guide.md": "---\ntitle: Guide\n---\n\nRead me.\n\nAnd this.\n",
	})
	w := &writes{}
	in := input(t, r, newFakeAPI(), verbatimPass(), manifest(), "", head, api.ModeWrite)
	if _, err := (passes.Verbatim{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	if len(w.verbatim) != 1 {
		t.Fatalf("imports: %+v", w.verbatim)
	}
	for _, b := range w.verbatim[0].Blocks {
		if sb := b.SourceBinding; sb.Path != "" || sb.LineStart != 0 || sb.SHA != "" {
			t.Fatalf("an older server gets no location: %+v", sb)
		}
	}
	w = &writes{}
	in = input(t, r, newFakeAPI(), verbatimPass(), manifest(), "", head, api.ModeWrite)
	in.Plan.Capabilities.Features = map[string]bool{passes.FeatureSourceLocation: true}
	if _, err := (passes.Verbatim{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	blocks := w.verbatim[0].Blocks
	if len(blocks) != 2 {
		t.Fatalf("blocks: %+v", blocks)
	}
	for i, want := range [][2]int{{5, 5}, {7, 7}} {
		sb := blocks[i].SourceBinding
		if sb.Path != "docs/handbook/guide.md" || sb.LineStart != want[0] || sb.LineEnd != want[1] || sb.SHA != head {
			t.Fatalf("block %d binding %+v, want lines %v at %s", i, sb, want, head)
		}
	}
}
