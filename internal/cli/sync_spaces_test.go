package cli

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func declaringProject() *config.Project {
	return &config.Project{
		Spaces: config.Spaces{
			Default: "product",
			Declare: []config.SpaceDecl{
				{
					Slug: "developers", Name: "Developers", Parent: "product",
					Type: config.SpaceTypeAPIReference, Visibility: config.SpaceVisibilityUnlisted,
					Audiences: []string{api.AudienceDevelopers},
				},
				{
					Slug: "product", Name: "Product",
					Type: config.SpaceTypeProductDocs, Visibility: config.SpaceVisibilityPublic,
					Audiences: []string{api.AudiencePublic, api.AudienceUsers},
				},
			},
		},
	}
}

func declaredSyncTargets() []syncTarget {
	return []syncTarget{
		{
			kind: "page", space: "developers", label: "api",
			page: api.PageUpsertRequest{SpaceSlug: "developers", Slug: "api", Title: "API"},
		},
		{
			kind: "page", space: "guides", label: "guide",
			page: api.PageUpsertRequest{SpaceSlug: "guides", Slug: "guide", Title: "Guide"},
		},
	}
}

func TestRunSyncEnsuresDeclaredSpacesFirst(t *testing.T) {
	tests := []struct {
		name        string
		features    map[string]bool
		wantParent  bool
		wantMeta    bool
		wantNotices []string
		denyNotices []string
	}{
		{
			name:        "platform supports hierarchy and metadata",
			features:    map[string]bool{featureSpaceHierarchy: true, featureSpaceMetadata: true},
			wantParent:  true,
			wantMeta:    true,
			denyNotices: []string{"does not type spaces yet", "predates subspaces/collections"},
		},
		{
			name:        "platform lacks space-metadata",
			features:    map[string]bool{featureSpaceHierarchy: true},
			wantParent:  true,
			wantNotices: []string{"does not type spaces yet"},
			denyNotices: []string{"predates subspaces/collections"},
		},
		{
			name:        "platform lacks both flags",
			features:    map[string]bool{},
			wantNotices: []string{"does not type spaces yet", "predates subspaces/collections"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &hierarchyServer{features: tc.features}
			srv := httptest.NewServer(h.handler(t))
			defer srv.Close()

			var out bytes.Buffer
			err := runSync(context.Background(), api.New(srv.URL, "sk_live_test"), "site",
				declaredSyncTargets(), true, hierarchyFromProject(declaringProject()), io.Discard, &out)
			if err != nil {
				t.Fatalf("runSync: %v", err)
			}

			if len(h.spaceEnsures) != 3 {
				t.Fatalf("space ensures = %d (%v), want 3 (product, developers, guides)", len(h.spaceEnsures), h.spaceEnsures)
			}
			gotOrder := []string{
				h.spaceEnsures[0]["slug"].(string),
				h.spaceEnsures[1]["slug"].(string),
				h.spaceEnsures[2]["slug"].(string),
			}
			wantOrder := []string{"product", "developers", "guides"}
			for i := range wantOrder {
				if gotOrder[i] != wantOrder[i] {
					t.Fatalf("ensure order = %v, want %v (declared spaces first, parents before children)", gotOrder, wantOrder)
				}
			}
			if h.spaceEnsures[0]["name"] != "Product" {
				t.Errorf("declared name not sent: %v", h.spaceEnsures[0])
			}
			if _, hasDesc := h.spaceEnsures[0]["description"]; hasDesc {
				t.Errorf("a declared space must not be overwritten with the CLI's boilerplate description: %v", h.spaceEnsures[0])
			}
			if h.spaceEnsures[2]["description"] != "Maintained by the gravity CLI." {
				t.Errorf("an implicit target space keeps its description: %v", h.spaceEnsures[2])
			}

			child := h.spaceEnsures[1]
			if gotParent := child["parent"]; tc.wantParent != (gotParent == "product") {
				t.Errorf("child parent = %v, want sent = %v", gotParent, tc.wantParent)
			}
			for _, field := range []string{"type", "visibility"} {
				_, present := child[field]
				if present != tc.wantMeta {
					t.Errorf("child %s present = %v, want %v (%v)", field, present, tc.wantMeta, child)
				}
			}
			if tc.wantMeta && (child["type"] != config.SpaceTypeAPIReference || child["visibility"] != config.SpaceVisibilityUnlisted) {
				t.Errorf("declared metadata not sent verbatim: %v", child)
			}

			for _, want := range tc.wantNotices {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing notice %q:\n%s", want, out.String())
				}
			}
			for _, deny := range tc.denyNotices {
				if strings.Contains(out.String(), deny) {
					t.Errorf("unexpected notice %q:\n%s", deny, out.String())
				}
			}
		})
	}
}

func TestRunSyncWithoutDeclarationsKeepsLegacyEnsure(t *testing.T) {
	h := &hierarchyServer{features: map[string]bool{featureSpaceHierarchy: true, featureSpaceMetadata: true}}
	srv := httptest.NewServer(h.handler(t))
	defer srv.Close()

	targets := []syncTarget{{
		kind: "page", space: "connect", label: "guide",
		page: api.PageUpsertRequest{SpaceSlug: "connect", Slug: "guide", Title: "Guide"},
	}}
	hier := hierarchyFromProject(&config.Project{Spaces: config.Spaces{Default: "connect", Parent: "fundamentum"}})
	var out bytes.Buffer
	if err := runSync(context.Background(), api.New(srv.URL, "sk_live_test"), "site", targets, true, hier, io.Discard, &out); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	if len(h.spaceEnsures) != 2 {
		t.Fatalf("space ensures = %v, want the parent then the default space", h.spaceEnsures)
	}
	if h.spaceEnsures[0]["slug"] != "fundamentum" || h.spaceEnsures[1]["parent"] != "fundamentum" {
		t.Errorf("legacy spaces.parent path changed: %v", h.spaceEnsures)
	}
}
