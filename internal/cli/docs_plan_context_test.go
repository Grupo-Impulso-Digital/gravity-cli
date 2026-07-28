package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

func TestExistingPagesDigestNamesEverySlug(t *testing.T) {
	got := existingPagesDigest([]api.Page{
		{SpaceSlug: "guides", Slug: "routing", Title: "Routing"},
		{SpaceSlug: "guides", Slug: "guardrails", Title: "Guardrails"},
		{SpaceSlug: "control-plane", Slug: "architecture", Title: "Architecture"},
	})
	for _, want := range []string{"guides/routing", "guides/guardrails", "control-plane/architecture", "REUSE"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}
}

func TestExistingPagesDigestFirstPass(t *testing.T) {
	got := existingPagesDigest(nil)
	if !strings.Contains(got, "first pass") {
		t.Fatalf("empty digest = %q, want it to name the first-pass case", got)
	}
}

func TestSpacesDigestListsDefaultParentAndMappings(t *testing.T) {
	proj := &config.Project{
		Spaces:    config.Spaces{Default: "control-plane", Parent: "developers"},
		Documents: []config.DocMap{{Space: "guides"}, {Space: "guides"}, {Space: ""}},
	}
	got := spacesDigest(proj)
	for _, want := range []string{"control-plane (default)", "developers", "guides", "Do not invent"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "- guides") != 1 {
		t.Errorf("guides listed %d times, want once:\n%s", strings.Count(got, "- guides"), got)
	}
	if spacesDigest(nil) != "" {
		t.Error("nil project should produce no digest")
	}
}

func TestUnitsDigestNamesRoleAndResolvedKind(t *testing.T) {
	proj := &config.Project{Product: config.Product{Role: config.RoleAPI}}
	got := unitsDigest(proj, nil)
	if !strings.Contains(got, config.RoleAPI) || !strings.Contains(got, `"service"`) {
		t.Errorf("digest does not name role -> kind:\n%s", got)
	}
	if strings.Contains(got, "restricted to these unit kinds") {
		t.Errorf("unfiltered run should not claim a kind restriction:\n%s", got)
	}

	filtered := unitsDigest(proj, map[string]bool{api.UnitKindAPI: true, api.UnitKindService: true})
	if !strings.Contains(filtered, "restricted to these unit kinds: api, service") {
		t.Errorf("--units filter not stated:\n%s", filtered)
	}

	if bare := unitsDigest(nil, nil); !strings.Contains(bare, `"feature"`) {
		t.Errorf("nil project digest = %q", bare)
	}
}

func TestReportOrphansNamesUncoveredPages(t *testing.T) {
	existing := []api.Page{
		{SpaceSlug: "guides", Slug: "routing"},
		{SpaceSlug: "guides", Slug: "legacy-thing"},
	}
	plan := agent.DocPlanInput{Pages: []agent.DocPlanPage{{Space: "guides", Slug: "routing"}}}

	var buf bytes.Buffer
	reportOrphans(existing, plan, "", &buf)
	out := buf.String()
	if !strings.Contains(out, "guides/legacy-thing") {
		t.Errorf("orphan not reported:\n%s", out)
	}
	if strings.Contains(out, "guides/routing") {
		t.Errorf("covered page reported as orphan:\n%s", out)
	}

	buf.Reset()
	reportOrphans(existing, agent.DocPlanInput{Pages: []agent.DocPlanPage{
		{Space: "guides", Slug: "routing"}, {Space: "guides", Slug: "legacy-thing"},
	}}, "", &buf)
	if buf.Len() != 0 {
		t.Errorf("full coverage should be silent, got %q", buf.String())
	}
}

func TestReportOrphansIgnoresSiblingRepoPages(t *testing.T) {
	mine, sibling := "cr_mine", "cr_sibling"
	existing := []api.Page{
		{SpaceSlug: "platform", Slug: "orbit-api/legacy", RepoID: &mine},
		{SpaceSlug: "platform", Slug: "orbit-web/checkout", RepoID: &sibling},
		{SpaceSlug: "platform", Slug: "hand-written"},
	}
	var buf bytes.Buffer
	reportOrphans(existing, agent.DocPlanInput{Pages: []agent.DocPlanPage{{Space: "platform", Slug: "other"}}}, mine, &buf)
	out := buf.String()
	if !strings.Contains(out, "orbit-api/legacy") || !strings.Contains(out, "hand-written") {
		t.Errorf("own and unattributed orphans must be reported:\n%s", out)
	}
	if strings.Contains(out, "orbit-web/checkout") {
		t.Errorf("sibling repo's page reported as an orphan:\n%s", out)
	}

	buf.Reset()
	reportOrphans(existing, agent.DocPlanInput{Pages: nil}, "", &buf)
	if !strings.Contains(buf.String(), "orbit-web/checkout") {
		t.Errorf("without a repo id the report must stay site-wide:\n%s", buf.String())
	}
}

func TestAudienceSpaceMapping(t *testing.T) {
	proj := &config.Project{
		Spaces: config.Spaces{Default: "platform", Shared: []string{"platform", "developers"}},
	}
	if got := audienceSpace(proj, []string{api.AudienceDevelopers}); got != "developers" {
		t.Errorf("developers-only page -> %q, want developers", got)
	}
	if got := audienceSpace(proj, []string{api.AudiencePublic, api.AudienceDevelopers}); got != "" {
		t.Errorf("multi-audience page -> %q, want the default space", got)
	}
	if got := audienceSpace(proj, []string{api.AudiencePublic}); got != "" {
		t.Errorf("public page -> %q, want the default space", got)
	}
	if got := audienceSpace(nil, []string{api.AudienceDevelopers}); got != "" {
		t.Errorf("nil project -> %q, want empty", got)
	}

	mapped := &config.Project{
		Spaces:  config.Spaces{Default: "platform"},
		Sources: []config.SourceMap{{Space: "api"}},
	}
	if got := audienceSpace(mapped, []string{api.AudienceDevelopers}); got != "api" {
		t.Errorf("mapped dev space -> %q, want api", got)
	}

	plain := &config.Project{Spaces: config.Spaces{Default: "docs", Shared: []string{"docs"}}}
	if got := audienceSpace(plain, []string{api.AudienceDevelopers}); got != "" {
		t.Errorf("unconfigured dev space -> %q, want empty", got)
	}
}

func TestResolvePlannedPagesRoutesByAudience(t *testing.T) {
	proj := &config.Project{
		Product: config.Product{Repo: "orbit-api", Slug: "orbit"},
		Spaces:  config.Spaces{Default: "platform", Shared: []string{"platform", "developers"}},
	}
	plan := agent.DocPlanInput{Pages: []agent.DocPlanPage{
		{Slug: "invoicing", Title: "Invoicing", Audiences: []string{api.AudienceDevelopers}},
		{Slug: "overview", Title: "Overview", Audiences: []string{api.AudiencePublic, api.AudienceDevelopers}},
		{Space: "guides", Slug: "setup", Title: "Setup", Audiences: []string{api.AudienceDevelopers}},
	}}
	want := []string{api.AudiencePublic, api.AudienceUsers, api.AudienceDevelopers}

	got := resolvePlannedPages(plan, proj, "", "platform", want)
	if len(got) != 3 {
		t.Fatalf("planned %d pages, want 3", len(got))
	}
	if got[0].space != "developers" {
		t.Errorf("developers-only page landed in %q, want developers", got[0].space)
	}
	if got[1].space != "platform" {
		t.Errorf("multi-audience page landed in %q, want platform", got[1].space)
	}
	if got[2].space != "guides" {
		t.Errorf("planner-named space overridden: %q", got[2].space)
	}
	if got[0].slug != "orbit-api/invoicing" || got[0].collection != "orbit-api" {
		t.Errorf("shared-space target = %s (%s)", got[0].slug, got[0].collection)
	}

	forced := resolvePlannedPages(plan, proj, "changelog", "platform", want)
	for _, p := range forced {
		if p.space != "changelog" {
			t.Errorf("--space did not override: %+v", p)
		}
	}
}

func TestPlannedPageInheritsConfiguredAudiences(t *testing.T) {
	proj := &config.Project{
		Spaces: config.Spaces{Default: "platform", Shared: []string{"platform", "dev"}},
		Discovery: config.Discovery{
			Audiences: config.DiscoveryAudiences{Default: []string{api.AudienceDevelopers}},
		},
	}
	plan := agent.DocPlanInput{Pages: []agent.DocPlanPage{{Slug: "internals", Title: "Internals"}}}
	got := resolvePlannedPages(plan, proj, "", "platform", []string{api.AudiencePublic, api.AudienceDevelopers})
	if len(got) != 1 {
		t.Fatalf("planned %d pages, want 1", len(got))
	}
	if len(got[0].audiences) != 1 || got[0].audiences[0] != api.AudienceDevelopers {
		t.Errorf("audiences = %v, want the configured default", got[0].audiences)
	}
	if got[0].space != "dev" {
		t.Errorf("space = %q, want dev", got[0].space)
	}

	narrowed := resolvePlannedPages(plan, proj, "", "platform", []string{api.AudiencePublic})
	if len(narrowed) != 1 || narrowed[0].audiences[0] != api.AudiencePublic {
		t.Errorf("narrowed = %+v, want the requested audiences", narrowed)
	}
}

func TestShouldAuthorScopesToTheChangeSet(t *testing.T) {
	changes := docs.NewChangeSet("v1.0.0", []string{"src/billing/invoice.ts", "openapi/auth.yaml"})
	plan := agent.DocPlanInput{Units: []agent.DocPlanUnit{
		{Key: "svc.billing", Kind: api.UnitKindService, Title: "Billing", SourceRefs: []string{"src/billing/invoice.ts"}},
		{Key: "svc.search", Kind: api.UnitKindService, Title: "Search", SourceRefs: []string{"src/search/index.ts"}},
		{Key: "svc.flagged", Kind: api.UnitKindService, Title: "Flagged", Changed: true},
	}}
	changed := changedUnitKeys(plan, changes)
	if !changed["svc.billing"] {
		t.Error("a unit whose bound source changed must count as changed")
	}
	if !changed["svc.flagged"] {
		t.Error("the planner's own changed flag must be honored")
	}
	if changed["svc.search"] {
		t.Error("an untouched unit must not count as changed")
	}

	billing := plannedPage{plan: agent.DocPlanPage{Slug: "invoicing", Units: []string{"svc.billing"}}}
	search := plannedPage{plan: agent.DocPlanPage{Slug: "search", Units: []string{"svc.search"}}}
	if !shouldAuthor(billing, nil, changed, changes) {
		t.Error("page of a changed unit must be authored")
	}
	if shouldAuthor(search, nil, changed, changes) {
		t.Error("page of an untouched unit must be skipped")
	}

	bound := []api.ContentBlock{{Key: "spec", SourceBinding: &api.SourceBinding{Ref: "openapi/auth.yaml"}}}
	if !shouldAuthor(search, bound, changed, changes) {
		t.Error("a page whose bound source changed must be authored")
	}
	if shouldAuthor(search, []api.ContentBlock{{Key: "prose"}}, changed, changes) {
		t.Error("an unbound block must not force a re-author")
	}

	viaSources := plannedPage{plan: agent.DocPlanPage{Slug: "auth", Sources: []string{"openapi/auth.yaml"}}}
	if !shouldAuthor(viaSources, nil, changed, changes) {
		t.Error("a page whose named source changed must be authored")
	}
}

func TestInventoryForBindsUnitsToResolvedSlugs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/billing/invoice.ts", "export const x = 1\n")
	proj := &config.Project{
		Product: config.Product{Repo: "orbit-api", Slug: "orbit", Role: config.RoleAPI},
		Spaces:  config.Spaces{Default: "platform", Shared: []string{"platform"}},
	}
	plan := agent.DocPlanInput{
		Units: []agent.DocPlanUnit{
			{Key: "svc.billing", Kind: api.UnitKindService, Title: "Billing", SourceRefs: []string{"src/billing/invoice.ts"}},
			{Key: "sys.auth", Title: "Auth", PageSlugs: []string{"auth"}},
			{Key: "NOT A KEY", Kind: api.UnitKindService, Title: "Rejected"},
		},
		Pages: []agent.DocPlanPage{{Slug: "invoicing", Title: "Invoicing", Audiences: []string{api.AudienceDevelopers}, Units: []string{"svc.billing"}}},
	}
	planned := resolvePlannedPages(plan, proj, "", "platform", []string{api.AudienceDevelopers})
	req := inventoryFor(root, plan, planned, proj, &api.RepoRef{RemoteKey: "github.com/Acme/orbit-api", Name: "orbit-api"}, true)

	if req.Repo.RemoteKey != "github.com/Acme/orbit-api" || !req.Replace {
		t.Errorf("request head = %+v", req)
	}
	if len(req.Units) != 2 {
		t.Fatalf("units = %d, want the 2 with valid keys: %+v", len(req.Units), req.Units)
	}
	billing := req.Units[0]
	if billing.Key != "svc.billing" || billing.Kind != api.UnitKindService {
		t.Errorf("unit = %+v", billing)
	}
	if len(billing.PageSlugs) != 1 || billing.PageSlugs[0] != "orbit-api/invoicing" {
		t.Errorf("page slugs = %v, want the manifest-resolved slug", billing.PageSlugs)
	}
	if !strings.HasPrefix(billing.SourceHash, "sha256:") {
		t.Errorf("source hash = %q, want a sha256 digest", billing.SourceHash)
	}
	auth := req.Units[1]
	if auth.Kind != api.UnitKindService {
		t.Errorf("unkinded unit resolved to %q, want service (product.role: api)", auth.Kind)
	}
	if len(auth.PageSlugs) != 1 || auth.PageSlugs[0] != "orbit-api/auth" {
		t.Errorf("planner-declared slugs = %v, want them namespaced too", auth.PageSlugs)
	}
	if auth.SourceHash != "" {
		t.Errorf("unit with no sources has hash %q; it could never go stale", auth.SourceHash)
	}
}

func TestFilterPlanKinds(t *testing.T) {
	plan := agent.DocPlanInput{
		Units: []agent.DocPlanUnit{
			{Key: "svc.a", Kind: api.UnitKindService, Title: "A"},
			{Key: "feat.b", Kind: api.UnitKindFeature, Title: "B"},
		},
		Pages: []agent.DocPlanPage{
			{Slug: "a", Units: []string{"svc.a"}},
			{Slug: "b", Units: []string{"feat.b"}},
			{Slug: "c", Kind: api.UnitKindService},
		},
	}
	got := filterPlanKinds(plan, map[string]bool{api.UnitKindService: true})
	if len(got.Units) != 1 || got.Units[0].Key != "svc.a" {
		t.Errorf("units = %+v", got.Units)
	}
	if len(got.Pages) != 2 || got.Pages[0].Slug != "a" || got.Pages[1].Slug != "c" {
		t.Errorf("pages = %+v", got.Pages)
	}
	if same := filterPlanKinds(plan, nil); len(same.Pages) != 3 {
		t.Errorf("no filter must keep the plan intact: %+v", same)
	}
}

func TestParseUnitKinds(t *testing.T) {
	got, err := parseUnitKinds([]string{"service", "api"})
	if err != nil || !got[api.UnitKindService] || !got[api.UnitKindAPI] {
		t.Fatalf("parse = %v, %v", got, err)
	}
	if _, err := parseUnitKinds([]string{"microservice"}); err == nil {
		t.Error("an unknown kind must be rejected before any token is spent")
	}
	if got, err := parseUnitKinds(nil); got != nil || err != nil {
		t.Errorf("no flag = %v, %v", got, err)
	}
}

func fakeReadFileTool() agent.Tool {
	return agent.Tool{
		Def: api.Tool{Name: "read_file"},
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(input, &in)
			return "contents of " + in.Path, nil
		},
	}
}

func TestPlanReadToolsGating(t *testing.T) {
	windowed := &config.Project{Discovery: config.Discovery{
		Include:     []string{"src/**"},
		Exclude:     []string{"src/generated/**"},
		Entrypoints: []string{"README.md"},
	}}
	changes := docs.NewChangeSet("v1.0.0", []string{"src/billing/invoice.ts"})

	tests := []struct {
		name       string
		scope      planScope
		path       string
		wantErr    bool
		wantErrHas string
	}{
		{
			name:  "full run + window: included path is readable",
			scope: planScope{proj: windowed},
			path:  "src/billing/invoice.ts",
		},
		{
			name:       "full run + window: excluded path is rejected",
			scope:      planScope{proj: windowed},
			path:       "src/generated/api.ts",
			wantErr:    true,
			wantErrHas: "discovery.include/discovery.exclude",
		},
		{
			name:       "full run + window: path outside include is rejected",
			scope:      planScope{proj: windowed},
			path:       "docs/other.md",
			wantErr:    true,
			wantErrHas: "discovery.include/discovery.exclude",
		},
		{
			name:  "full run + window: entrypoint stays readable",
			scope: planScope{proj: windowed},
			path:  "README.md",
		},
		{
			name:  "full run, empty discovery: any path is readable",
			scope: planScope{proj: &config.Project{}},
			path:  "docs/anything.md",
		},
		{
			name:  "full run, nil project: any path is readable",
			scope: planScope{proj: nil},
			path:  "docs/anything.md",
		},
		{
			name:  "change-scoped: changed file is readable",
			scope: planScope{proj: windowed, changes: changes},
			path:  "src/billing/invoice.ts",
		},
		{
			name:  "change-scoped: entrypoint stays readable though unchanged",
			scope: planScope{proj: windowed, changes: changes},
			path:  "README.md",
		},
		{
			name:       "change-scoped: unchanged non-entrypoint path is rejected",
			scope:      planScope{proj: windowed, changes: changes},
			path:       "src/other/file.ts",
			wantErr:    true,
			wantErrHas: "change-scoped run",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tools := planReadTools([]agent.Tool{fakeReadFileTool()}, tc.scope)
			if len(tools) != 1 {
				t.Fatalf("planReadTools dropped or added tools: got %d, want 1", len(tools))
			}
			input, _ := json.Marshal(map[string]string{"path": tc.path})
			_, err := tools[0].Run(context.Background(), input)
			if tc.wantErr && err == nil {
				t.Fatalf("reading %q: got no error, want one", tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("reading %q: unexpected error: %v", tc.path, err)
			}
			if tc.wantErrHas != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrHas)) {
				t.Fatalf("reading %q: error = %v, want it to mention %q", tc.path, err, tc.wantErrHas)
			}
		})
	}
}

func TestPlanReadToolsNoWindowReturnsToolsUnwrapped(t *testing.T) {
	base := []agent.Tool{fakeReadFileTool()}
	got := planReadTools(base, planScope{proj: &config.Project{}})
	if len(got) != 1 || &got[0] != &base[0] {
		t.Errorf("an unrestricted run must return the input slice untouched")
	}
}

func TestPlanReadToolsFiltersListingAndGrepOnFullRunWithWindow(t *testing.T) {
	proj := &config.Project{Discovery: config.Discovery{Include: []string{"src/**"}}}
	listTool := agent.Tool{
		Def: api.Tool{Name: "list_files"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			return "README.md\nsrc/a.go\n", nil
		},
	}
	grepTool := agent.Tool{
		Def: api.Tool{Name: "grep"},
		Run: func(context.Context, json.RawMessage) (string, error) {
			return "README.md:1:TODO\nsrc/a.go:2:TODO\n", nil
		},
	}

	tools := planReadTools([]agent.Tool{listTool, grepTool}, planScope{proj: proj})

	listOut, err := tools[0].Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if strings.Contains(listOut, "README.md") {
		t.Errorf("list_files leaked a path outside the discovery window:\n%s", listOut)
	}
	if !strings.Contains(listOut, "src/a.go") {
		t.Errorf("list_files dropped a path inside the discovery window:\n%s", listOut)
	}

	grepOut, err := tools[1].Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if strings.Contains(grepOut, "README.md") {
		t.Errorf("grep leaked a path outside the discovery window:\n%s", grepOut)
	}
	if !strings.Contains(grepOut, "src/a.go") {
		t.Errorf("grep dropped a path inside the discovery window:\n%s", grepOut)
	}
}
