package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

// The planner must be told which pages already exist, or a re-run mints a
// near-synonym slug for a feature that is already documented and forks the docs
// instead of updating them.
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

// A first pass must say so explicitly rather than emit an empty section the
// model could read as "there are no pages, so anything goes".
func TestExistingPagesDigestFirstPass(t *testing.T) {
	got := existingPagesDigest(nil)
	if !strings.Contains(got, "first pass") {
		t.Fatalf("empty digest = %q, want it to name the first-pass case", got)
	}
}

// The planner may only target spaces this repo actually publishes into.
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

// A page the plan omits stays published and is reported. Silently retiring
// documentation because one planning run forgot it would be worse than noise.
func TestReportOrphansNamesUncoveredPages(t *testing.T) {
	existing := []api.Page{
		{SpaceSlug: "guides", Slug: "routing"},
		{SpaceSlug: "guides", Slug: "legacy-thing"},
	}
	plan := agent.DocPlanInput{Pages: []agent.DocPlanPage{{Space: "guides", Slug: "routing"}}}

	var buf bytes.Buffer
	reportOrphans(existing, plan, &buf)
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
	}}, &buf)
	if buf.Len() != 0 {
		t.Errorf("full coverage should be silent, got %q", buf.String())
	}
}
