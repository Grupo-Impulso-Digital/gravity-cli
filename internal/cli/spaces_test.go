package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func strPtr(s string) *string { return &s }

func TestBuildSpacesView(t *testing.T) {
	tree := &api.SiteTree{
		Site: api.Site{Slug: "dimonoff", Name: "Dimonoff Docs"},
		Spaces: []api.Space{
			{ID: "s1", Slug: "fundamentum", Name: "Fundamentum"},
			{ID: "s2", Slug: "connect", Name: "Fundamentum Connect", ParentSpaceID: strPtr("s1"), OverviewPageID: strPtr("p1")},
			{ID: "s3", Slug: "changelog", Name: "Changelog"},
		},
		Collections: []api.Collection{
			{ID: "c1", Slug: "device-api", Name: "Device Api", SpaceID: "s2", SpaceSlug: "connect"},
		},
		Pages: []api.PageRef{
			{ID: "p1", Slug: "overview", SpaceID: "s2", SpaceSlug: "connect"},
			{ID: "p2", Slug: "device-api/guide", SpaceID: "s2", SpaceSlug: "connect", CollectionID: strPtr("c1")},
			{ID: "p3", Slug: "device-api/reference", SpaceID: "s2", SpaceSlug: "connect", CollectionID: strPtr("c1")},
		},
	}
	proj := &config.Project{
		Spaces:       config.Spaces{Default: "connect", Shared: []string{"connect"}},
		ReleaseNotes: config.ReleaseNotes{Space: "changelog"},
	}

	view := buildSpacesView(tree, proj)
	if len(view.Spaces) != 2 {
		t.Fatalf("top-level spaces = %d, want 2", len(view.Spaces))
	}
	fund := view.Spaces[0]
	if fund.Slug != "fundamentum" || len(fund.Subspaces) != 1 {
		t.Fatalf("fundamentum should hold one subspace; got %+v", fund)
	}
	connect := fund.Subspaces[0]
	if connect.HomePage != "overview" {
		t.Errorf("home page = %q, want overview", connect.HomePage)
	}
	if !connect.ThisRepo || !connect.Shared {
		t.Errorf("connect should be annotated as this repo's shared space; got %+v", connect)
	}
	if connect.Pages != 1 {
		t.Errorf("flat pages = %d, want 1 (the overview)", connect.Pages)
	}
	if len(connect.Colls) != 1 || connect.Colls[0].Pages != 2 {
		t.Errorf("device-api collection should count 2 pages; got %+v", connect.Colls)
	}
	if !view.Spaces[1].Releases {
		t.Errorf("changelog should be annotated as the release-notes space; got %+v", view.Spaces[1])
	}

	var out bytes.Buffer
	printSpacesView(&out, tree.Site, view)
	s := out.String()
	for _, want := range []string{"└─ connect", "⌂ overview", "← this repo", "device-api  (collection, 2 pages)", "release notes"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered tree missing %q:\n%s", want, s)
		}
	}
}
