package cli

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func pageTarget(space, slug, title string) syncTarget {
	return syncTarget{
		kind:  "page",
		space: space,
		label: "doc -> " + space + "/" + slug,
		page:  api.PageUpsertRequest{SpaceSlug: space, Slug: slug, Title: title},
	}
}

func TestReconcilePageTargets_ExactSlugUpdate(t *testing.T) {
	existing := []api.Page{{ID: "p1", SpaceSlug: "docs", Slug: "guide", Title: "Guide"}}
	targets := []syncTarget{pageTarget("docs", "guide", "Guide")}

	plan, orphans := reconcilePageTargets(existing, targets)
	if len(plan) != 1 || !plan[0].update || plan[0].effectiveSlug != "guide" {
		t.Fatalf("expected exact-slug update on 'guide', got %+v", plan)
	}
	if len(orphans) != 0 {
		t.Errorf("no orphans expected, got %+v", orphans)
	}
}

func TestReconcilePageTargets_TitleFallbackRemapsMangledSlug(t *testing.T) {
	// The server slugged the page from its title ("AGENTS.md" -> "agents-md"),
	// so the configured slug "agents" no longer matches. Reconcile must remap
	// onto the existing page by title and send its actual slug.
	existing := []api.Page{{ID: "p1", SpaceSlug: "cli", Slug: "agents-md", Title: "AGENTS.md"}}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets)
	if len(plan) != 1 || !plan[0].update {
		t.Fatalf("expected an update via title fallback, got %+v", plan)
	}
	if plan[0].effectiveSlug != "agents-md" {
		t.Errorf("effective slug = %q, want the existing page's slug agents-md", plan[0].effectiveSlug)
	}
	if plan[0].configSlug != "agents" {
		t.Errorf("configSlug = %q, want agents", plan[0].configSlug)
	}
	if len(orphans) != 0 {
		t.Errorf("no orphans expected, got %+v", orphans)
	}
}

func TestReconcilePageTargets_DuplicatePicksCanonicalFlagsOrphan(t *testing.T) {
	// Two pages share the title (the duplication bug): the canonical one and a
	// random-suffixed duplicate. Reconcile updates the canonical and flags the
	// suffixed one as an orphan, never modifying it.
	existing := []api.Page{
		{ID: "p1", SpaceSlug: "cli", Slug: "agents-md", Title: "AGENTS.md"},
		{ID: "p2", SpaceSlug: "cli", Slug: "agents-md-ed44", Title: "AGENTS.md"},
	}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets)
	if len(plan) != 1 || plan[0].effectiveSlug != "agents-md" {
		t.Fatalf("expected to pick canonical agents-md, got %+v", plan)
	}
	if len(orphans) != 1 || orphans[0].page.Slug != "agents-md-ed44" {
		t.Fatalf("expected the suffixed duplicate as the sole orphan, got %+v", orphans)
	}
	if orphans[0].ofSlug != "agents-md" || !orphans[0].duplicate() {
		t.Errorf("orphan should be tagged a duplicate of agents-md and prunable; got %+v", orphans[0])
	}
}

func TestOrphanPage_DuplicateOnlyForSuffixedSlug(t *testing.T) {
	// A suffixed collision slug is a prunable duplicate...
	dup := orphanPage{page: api.Page{Slug: "agents-md-ed44"}, ofSlug: "agents-md"}
	if !dup.duplicate() {
		t.Error("suffixed slug should be a prunable duplicate")
	}
	// ...but a same-title page with an unrelated slug is not (manual review only).
	hand := orphanPage{page: api.Page{Slug: "agents-guide"}, ofSlug: "agents-md"}
	if hand.duplicate() {
		t.Error("unrelated slug must not be auto-pruned")
	}
}

func TestReconcilePageTargets_CreateWhenAbsent(t *testing.T) {
	existing := []api.Page{{ID: "p1", SpaceSlug: "cli", Slug: "other", Title: "Other"}}
	targets := []syncTarget{pageTarget("cli", "overview", "README")}

	plan, orphans := reconcilePageTargets(existing, targets)
	if len(plan) != 1 || plan[0].update {
		t.Fatalf("expected a CREATE (no match), got %+v", plan)
	}
	if plan[0].effectiveSlug != "overview" {
		t.Errorf("effective slug = %q, want the configured overview", plan[0].effectiveSlug)
	}
	if len(orphans) != 0 {
		t.Errorf("unrelated pages must not be orphans, got %+v", orphans)
	}
}

func TestReconcilePageTargets_TitleMatchScopedToSpace(t *testing.T) {
	// A same-title page in a different space must not be matched.
	existing := []api.Page{{ID: "p1", SpaceSlug: "other", Slug: "agents-md", Title: "AGENTS.md"}}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets)
	if len(plan) != 1 || plan[0].update {
		t.Fatalf("cross-space title must not match; want CREATE, got %+v", plan)
	}
	if len(orphans) != 0 {
		t.Errorf("page in an unmanaged space is not an orphan, got %+v", orphans)
	}
}

func TestBuildSyncTargets_DuplicateSlugCollision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Readme\n\nbody\n")
	writeFile(t, dir, "ci/README.md", "# CI Readme\n\nbody\n")
	proj := &config.Project{
		Spaces: config.Spaces{Default: "gravity-cli"},
		Documents: []config.DocMap{
			{File: "README.md", Page: "overview", As: "page"},
			{File: "ci/README.md", Page: "overview", As: "page"},
		},
	}
	_, err := buildSyncTargets(proj, dir, "test", "docs", "")
	if err == nil {
		t.Fatal("expected a collision error for two docs targeting the same page slug")
	}
	if !strings.Contains(err.Error(), "same page") {
		t.Errorf("error should explain the page-slug collision; got %q", err)
	}
}
