package setup

import (
	"reflect"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
)

var products = []api.ProductSummary{
	{Product: api.Product{Slug: "zeta", Name: "Zeta"}},
	{Product: api.Product{Slug: "acme-platform", Name: "Acme Platform"}, Repos: []api.RepoRef{{Name: "gateway", RemoteKey: "github.com/acme/gateway"}}, Targets: []api.ProductTarget{{SiteSlug: "dev", SpaceSlug: "api", Passes: 1}, {SiteSlug: "product", SpaceSlug: "guides", Passes: 3}}},
}

var sites = []api.Site{{Slug: "dev", Name: "Developer Portal", Position: 2}, {Slug: "handbook", Name: "Handbook", Position: 0}, {Slug: "product", Name: "Product", Position: 1}}

func TestRankProductsPrefersSiblings(t *testing.T) {
	got := RankProducts(products, "github.com/acme/billing-api", "billing-api")
	var slugs []string
	for _, o := range got {
		slugs = append(slugs, o.Slug)
	}
	if !reflect.DeepEqual(slugs, []string{"acme-platform", "billing-api", "zeta"}) || !got[1].New {
		t.Fatalf("ranked = %+v", got)
	}
	if got[0].Label() != "Acme Platform (gateway connected)" || got[1].Label() != "New product: billing-api" {
		t.Fatalf("labels = %q %q", got[0].Label(), got[1].Label())
	}
	none := RankProducts(products, "gitlab.com/other/tool", "tool")
	if none[0].Slug != "tool" || !none[0].New {
		t.Fatalf("without evidence the new product comes first: %+v", none)
	}
	same := RankProducts([]api.ProductSummary{{Product: api.Product{Slug: "tool", Name: "Tool"}}}, "gitlab.com/other/tool", "tool")
	if len(same) != 1 || same[0].New {
		t.Fatalf("an existing product with the repository's slug wins: %+v", same)
	}
	only := RankProducts([]api.ProductSummary{{Product: api.Product{Slug: "acme", Name: "Acme"}}}, "gitlab.com/other/tool", "tool")
	if len(only) != 2 || only[0].Slug != "acme" || only[0].New || !only[1].New || only[1].Slug != "tool" {
		t.Fatalf("an org's only product comes first without sibling evidence: %+v", only)
	}
}

func TestDefaultSite(t *testing.T) {
	if s := DefaultSite(ProductOption{Slug: "acme-platform"}, products, sites); s.Slug != "product" {
		t.Fatalf("the product's most targeted site wins: %+v", s)
	}
	if s := DefaultSite(ProductOption{Slug: "zeta"}, products, sites); s.Slug != "handbook" {
		t.Fatalf("else the first site by position: %+v", s)
	}
	if s := DefaultSite(ProductOption{Slug: "zeta", Name: "Zeta"}, products, nil); !s.New || s.Slug != "zeta" || s.Name != "Zeta" {
		t.Fatalf("else a new site named after the product: %+v", s)
	}
}

func TestSuggestFromDetection(t *testing.T) {
	det := &detect.Result{
		OpenAPI:     []detect.OpenAPIDoc{{Path: "api/openapi.yaml", Operations: 42, Version: "3.1.0"}},
		UIFramework: "Next.js", UIRoutes: 18, UIPaths: []string{"app/**"},
		ServerRoutes: 4, ServerPaths: []string{"server/**"},
		DocsFolders: []detect.Folder{{Path: "docs/handbook", Files: 9}},
		Runbooks:    []detect.Folder{{Path: "runbooks", Files: 3}},
		ReleaseTags: 12,
	}
	tree := &api.SiteTree{Spaces: []api.SiteSpace{{Slug: "reference", Name: "API", Type: "api-reference"}, {Slug: "guides", Name: "Guides", Type: "product-docs"}}}
	got := Suggest(Inputs{Detect: det, Site: Site{Slug: "dev", Name: "Developer Portal"}, Tree: tree, MemoryModule: true})
	type row struct {
		name, template, target string
		selected, create       bool
	}
	var rows []row
	for _, s := range got {
		rows = append(rows, row{s.Pass.Name, s.Pass.Template, s.Pass.Target, s.Selected, s.Create != nil})
	}
	want := []row{
		{"developer-api", config.TemplateAPIReference, "dev/reference", true, false},
		{"product-guides", config.TemplateUserGuide, "dev/guides", true, false},
		{"developer-guides", config.TemplateDeveloperGuide, "dev/developers", true, true},
		{"changelog", config.TemplateCustomerChangelog, "dev/changelog", true, true},
		{"runbooks", config.TemplateRunbook, "dev/runbooks", true, true},
		{"docs-handbook", config.TemplateVerbatimDocs, "dev/handbook", false, true},
		{"memory", config.TemplateNucleusFacts, "", true, false},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("suggestions =\n%+v\nwant\n%+v", rows, want)
	}
	if got[4].Create.Visibility != "private" || got[3].Create.Type != "release-notes" {
		t.Fatalf("create = %+v %+v", got[4].Create, got[3].Create)
	}
	if got[1].Pass.Scope == nil || got[1].Pass.Scope.Paths[0] != "app/**" {
		t.Fatalf("user guide scope = %+v", got[1].Pass.Scope)
	}
	files := got[5].Pass.Options["files"].([]config.VerbatimFile)
	if files[0].Include != "docs/handbook/**/*.md" || files[0].StripPrefix != "docs/handbook" {
		t.Fatalf("verbatim files = %+v", files)
	}
	if ct := CreateTargets(got); len(ct) != 4 {
		t.Fatalf("createTargets = %+v", ct)
	}
	noMemory := Suggest(Inputs{Detect: &detect.Result{}, Site: Site{Slug: "dev", Name: "Dev", New: true}})
	if len(noMemory) != 1 || noMemory[0].Selected {
		t.Fatalf("nucleus is offered but not preselected without the memory module: %+v", noMemory)
	}
}

func TestInternalChangelogWithoutUIOrAPI(t *testing.T) {
	got := Suggest(Inputs{Detect: &detect.Result{ReleaseTags: 1, Languages: []detect.Language{{Key: "go", Name: "Go", Files: 3}}}, Site: Site{Slug: "s", Name: "S", New: true}})
	if got[0].Pass.Template != config.TemplateDeveloperGuide || got[1].Pass.Template != config.TemplateInternalChangelog {
		t.Fatalf("got = %+v", got)
	}
}

func TestScopesAndSchedule(t *testing.T) {
	passes := []config.Pass{{Name: "a", Kind: config.KindVerbatim}, {Name: "b", Kind: config.KindReference, Triggers: []string{"schedule"}}}
	if got := Scopes(passes); !reflect.DeepEqual(got, []string{"repo:connect", "runs:write", "content:read", "content:propose", "content:verbatim", "inventory:write"}) {
		t.Fatalf("scopes = %v", got)
	}
	if !HasSchedule(passes, nil) || HasSchedule(nil, []api.PlanPass{{Triggers: []string{"push"}}}) {
		t.Fatal("schedule detection")
	}
}

func TestMissingTargetsOnlyForManifestSpaces(t *testing.T) {
	passes := []api.PlanPass{
		{Name: "runbooks", Template: config.TemplateRunbook, Source: "manifest", Target: api.PassTarget{Ref: "ops/runbooks", Status: api.TargetMissing}},
		{Name: "runbooks-2", Template: config.TemplateRunbook, Source: "manifest", Target: api.PassTarget{Ref: "ops/runbooks", Status: api.TargetMissing}},
		{Name: "guides", Source: "app", Target: api.PassTarget{Ref: "product/guides", Status: api.TargetMissing}},
		{Name: "api", Source: "manifest", Target: api.PassTarget{Ref: "dev/api", Status: api.TargetOK}},
		{Name: "deep", Source: "manifest", Target: api.PassTarget{Ref: "dev/api/v2", Status: api.TargetMissing}},
	}
	want := []api.CreateTarget{{Site: "ops", Space: "runbooks", Name: "Runbooks", Type: config.TemplateSpaceTypes[config.TemplateRunbook][0], Visibility: "private"}}
	if got := MissingTargets(passes); !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingTargets = %+v, want %+v", got, want)
	}
}
