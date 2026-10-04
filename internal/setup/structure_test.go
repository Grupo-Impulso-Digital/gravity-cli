package setup

import (
	"sort"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
)

func layoutOf(files map[string]string) Layout {
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	return Layout{
		Name: "polaris", Files: names, Primary: true,
		Read: func(p string) ([]byte, bool, error) {
			s, ok := files[p]
			return []byte(s), ok, nil
		},
		Detect: &detect.Result{OpenAPI: []detect.OpenAPIDoc{{Path: "api/openapi.yaml"}}, ReleaseTags: 3},
	}
}

func TestDraftStructureFromAMonorepo(t *testing.T) {
	l := layoutOf(map[string]string{
		"README.md":                        "# Polaris\n\nFleet software.\n",
		"docs/guides/start.md":             "# Start\n\nGo.\n",
		"packages/admin/README.md":         "# @acme/polaris-admin\n\nAdmin.\n",
		"packages/admin/docs/install.md":   "# Install\n\nSteps.\n",
		"packages/empty/README.md":         "# polaris-empty\n",
		"packages/admin/node_modules/x.md": "# vendored\n\nx\n",
	})
	live := &api.Structure{Site: api.StructureSite{Slug: "polaris"}, Spaces: []api.StructureSpace{{Slug: "handbook", Name: "Handbook", Type: "knowledge-base"}}}
	d := DraftStructure(DraftInput{Site: api.StructureSite{Slug: "polaris", Name: "Polaris"}, Live: live, Layouts: []Layout{l}})
	if d.Pass == nil || d.Pass.Target != "polaris/handbook" {
		t.Fatalf("pass = %+v", d.Pass)
	}
	files := d.Pass.Options["files"].([]config.VerbatimFile)
	var includes []string
	for _, f := range files {
		includes = append(includes, f.Include)
	}
	if strings.Join(includes, ",") != "README.md,docs/**/*.md,packages/admin/README.md,packages/admin/docs/**/*.md" {
		t.Fatalf("includes = %v", includes)
	}
	var spaces []string
	for _, sp := range d.Structure.Spaces {
		spaces = append(spaces, sp.Slug)
	}
	if strings.Join(spaces, ",") != "handbook,api,changelog" {
		t.Fatalf("spaces = %v", spaces)
	}
	h := d.Structure.Spaces[0]
	if len(h.Pages) != 1 || h.Pages[0].Slug != "overview" || h.Pages[0].Source != "README.md" {
		t.Fatalf("root pages = %+v", h.Pages)
	}
	var admin api.StructureCollection
	for _, c := range h.Collections {
		if c.Slug == "admin" {
			admin = c
		}
	}
	if admin.Title != "Polaris Admin" || len(admin.Pages) != 2 || admin.Pages[0].Slug != "admin-overview" {
		t.Fatalf("admin = %+v", admin)
	}
	again := DraftStructure(DraftInput{Site: api.StructureSite{Slug: "polaris", Name: "Polaris"}, Declared: &d.Structure, Live: live, Layouts: []Layout{l}})
	if len(again.Structure.Spaces) != 3 || len(again.Structure.Spaces[0].Collections) != len(h.Collections) {
		t.Fatalf("drafting over a declared structure is idempotent: %+v", again.Structure)
	}
}

func TestDiffStructureMarksImportsAndExtras(t *testing.T) {
	declared := api.Structure{Site: api.StructureSite{Slug: "p"}, Spaces: []api.StructureSpace{{Slug: "docs", Pages: []api.StructurePage{{Slug: "overview", Source: "README.md"}, {Slug: "faq"}}}}}
	live := &api.Structure{Site: api.StructureSite{Slug: "p"}, Spaces: []api.StructureSpace{{Slug: "docs", Pages: []api.StructurePage{{Slug: "legacy"}}}}}
	ops := map[string]string{}
	for _, it := range DiffStructure(declared, live) {
		ops[it.Path] = it.Op
	}
	if ops["docs"] != DiffExists || ops["docs#overview"] != DiffImport || ops["docs#faq"] != DiffCreate || ops["docs#legacy"] != DiffExtra {
		t.Fatalf("ops = %v", ops)
	}
}
