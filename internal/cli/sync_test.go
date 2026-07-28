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

	plan, orphans := reconcilePageTargets(existing, targets, "")
	if len(plan) != 1 || !plan[0].update || plan[0].effectiveSlug != "guide" {
		t.Fatalf("expected exact-slug update on 'guide', got %+v", plan)
	}
	if len(orphans) != 0 {
		t.Errorf("no orphans expected, got %+v", orphans)
	}
}

func TestReconcilePageTargets_TitleFallbackRemapsMangledSlug(t *testing.T) {
	existing := []api.Page{{ID: "p1", SpaceSlug: "cli", Slug: "agents-md", Title: "AGENTS.md"}}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets, "")
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
	existing := []api.Page{
		{ID: "p1", SpaceSlug: "cli", Slug: "agents-md", Title: "AGENTS.md"},
		{ID: "p2", SpaceSlug: "cli", Slug: "agents-md-ed44", Title: "AGENTS.md"},
	}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets, "")
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
	dup := orphanPage{page: api.Page{Slug: "agents-md-ed44"}, ofSlug: "agents-md"}
	if !dup.duplicate() {
		t.Error("suffixed slug should be a prunable duplicate")
	}
	hand := orphanPage{page: api.Page{Slug: "agents-guide"}, ofSlug: "agents-md"}
	if hand.duplicate() {
		t.Error("unrelated slug must not be auto-pruned")
	}
}

func TestReconcilePageTargets_CreateWhenAbsent(t *testing.T) {
	existing := []api.Page{{ID: "p1", SpaceSlug: "cli", Slug: "other", Title: "Other"}}
	targets := []syncTarget{pageTarget("cli", "overview", "README")}

	plan, orphans := reconcilePageTargets(existing, targets, "")
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
	existing := []api.Page{{ID: "p1", SpaceSlug: "other", Slug: "agents-md", Title: "AGENTS.md"}}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets, "")
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
	_, err := buildSyncTargets(proj, dir, "test", "docs", "", nil)
	if err == nil {
		t.Fatal("expected a collision error for two docs targeting the same page slug")
	}
	if !strings.Contains(err.Error(), "same page") {
		t.Errorf("error should explain the page-slug collision; got %q", err)
	}
}

func repoPage(id, space, slug, title, remoteKey string) api.Page {
	p := api.Page{ID: id, SpaceSlug: space, Slug: slug, Title: title}
	if remoteKey != "" {
		id := "cr_" + remoteKey
		key := remoteKey
		p.RepoID = &id
		p.RepoRemoteKey = &key
	}
	return p
}

func TestReconcilePageTargets_RepoScope(t *testing.T) {
	const mine, sibling = "github.com/acme/api", "github.com/acme/web"

	cases := []struct {
		name        string
		existing    []api.Page
		myRemoteKey string
		wantUpdate  bool
		wantSlug    string
		wantOrphans int
	}{
		{
			name:        "own page is matched",
			existing:    []api.Page{repoPage("p1", "docs", "guide", "Guide", mine)},
			myRemoteKey: mine,
			wantUpdate:  true,
			wantSlug:    "guide",
		},
		{
			name:        "unattributed page is adopted",
			existing:    []api.Page{repoPage("p1", "docs", "guide", "Guide", "")},
			myRemoteKey: mine,
			wantUpdate:  true,
			wantSlug:    "guide",
		},
		{
			name:        "sibling page is never matched",
			existing:    []api.Page{repoPage("p1", "docs", "guide", "Guide", sibling)},
			myRemoteKey: mine,
			wantUpdate:  false,
			wantSlug:    "guide",
		},
		{
			name:        "no identity keeps the site-wide behavior",
			existing:    []api.Page{repoPage("p1", "docs", "guide", "Guide", sibling)},
			myRemoteKey: "",
			wantUpdate:  true,
			wantSlug:    "guide",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targets := []syncTarget{pageTarget("docs", "guide", "Guide")}
			plan, orphans := reconcilePageTargets(tc.existing, targets, tc.myRemoteKey)
			if len(plan) != 1 {
				t.Fatalf("plan = %+v, want one entry", plan)
			}
			if plan[0].update != tc.wantUpdate {
				t.Errorf("update = %v, want %v", plan[0].update, tc.wantUpdate)
			}
			if plan[0].effectiveSlug != tc.wantSlug {
				t.Errorf("effective slug = %q, want %q", plan[0].effectiveSlug, tc.wantSlug)
			}
			if len(orphans) != tc.wantOrphans {
				t.Errorf("orphans = %+v, want %d", orphans, tc.wantOrphans)
			}
		})
	}
}

func TestReconcilePageTargets_SiblingDuplicateNotOrphaned(t *testing.T) {
	const mine, sibling = "github.com/acme/api", "github.com/acme/web"
	existing := []api.Page{
		repoPage("p1", "cli", "agents-md", "AGENTS.md", mine),
		repoPage("p2", "cli", "agents-md-ed44", "AGENTS.md", sibling),
	}
	targets := []syncTarget{pageTarget("cli", "agents", "AGENTS.md")}

	plan, orphans := reconcilePageTargets(existing, targets, mine)
	if len(plan) != 1 || plan[0].effectiveSlug != "agents-md" {
		t.Fatalf("expected to match this repo's page, got %+v", plan)
	}
	if len(orphans) != 0 {
		t.Errorf("a sibling's page must never be orphaned, got %+v", orphans)
	}
}

func TestPrunableDuplicate(t *testing.T) {
	const mine, sibling = "github.com/acme/api", "github.com/acme/web"
	cases := []struct {
		name        string
		orphan      orphanPage
		myRemoteKey string
		want        bool
	}{
		{
			name:        "own suffixed duplicate is prunable",
			orphan:      orphanPage{page: repoPage("p", "cli", "agents-md-ed44", "AGENTS.md", mine), ofSlug: "agents-md"},
			myRemoteKey: mine,
			want:        true,
		},
		{
			name:        "unattributed duplicate is reported, not pruned",
			orphan:      orphanPage{page: repoPage("p", "cli", "agents-md-ed44", "AGENTS.md", ""), ofSlug: "agents-md"},
			myRemoteKey: mine,
			want:        false,
		},
		{
			name:        "sibling duplicate is never pruned",
			orphan:      orphanPage{page: repoPage("p", "cli", "agents-md-ed44", "AGENTS.md", sibling), ofSlug: "agents-md"},
			myRemoteKey: mine,
			want:        false,
		},
		{
			name:        "without identity the pre-v2 suffix rule stands",
			orphan:      orphanPage{page: repoPage("p", "cli", "agents-md-ed44", "AGENTS.md", ""), ofSlug: "agents-md"},
			myRemoteKey: "",
			want:        true,
		},
		{
			name:        "unrelated slug is never prunable",
			orphan:      orphanPage{page: repoPage("p", "cli", "agents-guide", "AGENTS.md", mine), ofSlug: "agents-md"},
			myRemoteKey: mine,
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prunableDuplicate(tc.orphan, tc.myRemoteKey); got != tc.want {
				t.Errorf("prunableDuplicate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildSyncTargets_AttachesRepoAndLanguages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Readme\n\nbody\n")
	writeFile(t, dir, "CHANGELOG.md", "# v1.0\n\nshipped\n")
	proj := &config.Project{
		Spaces: config.Spaces{Default: "docs"},
		I18n:   config.I18n{Languages: []string{"fr", "es"}},
		Documents: []config.DocMap{
			{File: "README.md", Page: "overview", As: "page"},
			{File: "CHANGELOG.md", As: "release", Space: "changelog"},
		},
	}
	ref := &api.RepoRef{RemoteKey: "github.com/acme/api", Name: "api"}

	targets, err := buildSyncTargets(proj, dir, "test", "", "", ref)
	if err != nil {
		t.Fatalf("buildSyncTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	for _, tt := range targets {
		switch tt.kind {
		case "page":
			if tt.page.Repo == nil || tt.page.Repo.RemoteKey != ref.RemoteKey {
				t.Errorf("page target not attributed: %+v", tt.page.Repo)
			}
			if len(tt.page.Languages) != 2 {
				t.Errorf("page languages = %v, want fr,es", tt.page.Languages)
			}
		case "release":
			if tt.release.Repo == nil || tt.release.Repo.RemoteKey != ref.RemoteKey {
				t.Errorf("release target not attributed: %+v", tt.release.Repo)
			}
			if len(tt.release.Languages) != 2 {
				t.Errorf("release languages = %v, want fr,es", tt.release.Languages)
			}
		}
	}
}

func TestBuildSyncTargets_NoRepoIdentity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Readme\n\nbody\n")
	proj := &config.Project{
		Spaces:    config.Spaces{Default: "docs"},
		Documents: []config.DocMap{{File: "README.md", Page: "overview", As: "page"}},
	}
	targets, err := buildSyncTargets(proj, dir, "test", "", "", nil)
	if err != nil {
		t.Fatalf("buildSyncTargets: %v", err)
	}
	if targets[0].page.Repo != nil || targets[0].page.Languages != nil {
		t.Errorf("unattributed target should carry neither repo nor languages: %+v", targets[0].page)
	}
}
