package passes_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

const specV1 = `openapi: 3.0.0
info: {title: Billing API, version: 1.0.0}
paths:
  /v1/refunds:
    post:
      tags: [Refunds]
      operationId: createRefund
      summary: Create a refund
      responses: {'201': {description: created}}
    get:
      tags: [Refunds]
      summary: List refunds
      responses: {'200': {description: ok}}
  /v1/charges:
    get:
      tags: [Charges]
      summary: List charges
      responses: {'200': {description: ok}}
`

const specV2 = `openapi: 3.0.0
info: {title: Billing API, version: 1.1.0}
paths:
  /v1/refunds:
    post:
      tags: [Refunds]
      operationId: createRefund
      summary: Create a refund with a reason
      responses: {'201': {description: created}}
  /v1/charges:
    get:
      tags: [Charges]
      summary: List charges
      responses: {'200': {description: ok}}
`

func manifest() *config.Manifest {
	return &config.Manifest{Version: 2, Code: &config.Code{OpenAPI: []string{"api/openapi.yaml"}}}
}

func apiBlocks(t *testing.T, spec string) map[string]api.ChangeBlock {
	t.Helper()
	blocks, err := docs.APIBlocks([]byte(spec), "gravity-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.ChangeBlock{}
	for _, b := range blocks {
		out[b.Key] = b
	}
	return out
}

func pageBlock(b api.ChangeBlock) api.PageBlock {
	data, _ := json.Marshal(b.Content)
	return api.PageBlock{Key: b.Key, Type: b.Type, Ownership: b.Ownership, Content: data, SourceBinding: b.SourceBinding, Units: b.Units}
}

func TestReferenceCreatesPagesPerTagOnSurvey(t *testing.T) {
	r := newRepo(t)
	head := r.commit("spec", map[string]string{"api/openapi.yaml": specV1})
	fake := newFakeAPI()
	fake.inventory = []api.Unit{{Key: "api:post:/v1/refunds", Kind: "api"}}
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindReference, "developer-api", nil), manifest(), "", head, api.ModeWrite)
	rep, err := passes.Reference{}.Run(context.Background(), in, sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 2 || rep.Counts.Created != 2 {
		t.Fatalf("changes = %+v", w.changes)
	}
	refunds := w.changes[1]
	if refunds.Target.Slug != "refunds" || refunds.Op != api.OpCreate || refunds.Title != "Refunds" || len(refunds.Blocks) != 2 {
		t.Fatalf("refunds page = %+v", refunds)
	}
	for _, b := range refunds.Blocks {
		if b.SourceBinding.Kind != "endpoint" || b.Ownership != api.OwnershipMachine {
			t.Fatalf("block %s = %+v", b.Key, b)
		}
	}
	if units := refunds.Blocks[1].Units; len(units) != 1 || units[0] != "api:post:/v1/refunds" {
		t.Fatalf("known unit keys must bind: %+v", refunds.Blocks[1])
	}
	if len(refunds.Blocks[0].Units) != 0 {
		t.Fatalf("unknown unit keys must be dropped before the write: %+v", refunds.Blocks[0].Units)
	}
	if len(rep.ChangeIDs) != 2 {
		t.Fatalf("change ids = %v", rep.ChangeIDs)
	}
}

func TestReferenceUpgradesV03PagesAndRemovesDeletedOperations(t *testing.T) {
	r := newRepo(t)
	base := r.commit("spec v1", map[string]string{"api/openapi.yaml": specV1})
	head := r.commit("spec v2", map[string]string{"api/openapi.yaml": specV2})
	fake := newFakeAPI()
	v1 := apiBlocks(t, specV1)
	v2 := apiBlocks(t, specV2)
	legacy := pageBlock(v1["api:POST:/v1/refunds"])
	legacy.SourceBinding = &api.SourceBinding{Kind: "cli", Ref: "api/openapi.yaml", Hash: "sha256:old"}
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_ref", Slug: "refunds", Title: "Refunds"}, Blocks: []api.PageBlock{
		legacy, pageBlock(v1["api:GET:/v1/refunds"]), {Key: "guide:refunds:intro", Type: "prose", Ownership: api.OwnershipHuman},
	}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_chg", Slug: "charges", Title: "Charges"}, Blocks: []api.PageBlock{pageBlock(v2["api:GET:/v1/charges"])}})
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindReference, "developer-api", nil), manifest(), base, head, api.ModeWrite)
	rep, err := passes.Reference{}.Run(context.Background(), in, sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 1 {
		t.Fatalf("only the refunds page changed: %+v", w.changes)
	}
	c := w.changes[0]
	if c.Op != api.OpUpdate || c.Target.PageID != "pg_ref" || len(c.Blocks) != 1 || c.Blocks[0].Key != "api:POST:/v1/refunds" || c.Blocks[0].SourceBinding.Kind != "endpoint" {
		t.Fatalf("update = %+v", c)
	}
	if len(c.RemoveBlockKeys) != 1 || c.RemoveBlockKeys[0] != "api:GET:/v1/refunds" {
		t.Fatalf("removed operations must be removed explicitly: %v", c.RemoveBlockKeys)
	}
	if rep.Counts.Updated != 1 || rep.Counts.Unchanged != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
}

func TestReferencePerOperationDeletesRemovedPagesAndDryRunRecords(t *testing.T) {
	r := newRepo(t)
	base := r.commit("spec v1", map[string]string{"api/openapi.yaml": specV1})
	head := r.commit("spec v2", map[string]string{"api/openapi.yaml": specV2})
	fake := newFakeAPI()
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_list", Slug: "get-v1-refunds", Title: "List refunds"}})
	pp := planPass(config.KindReference, "developer-api", map[string]any{"pageStrategy": "per-operation"})
	w := &writes{}
	if _, err := (passes.Reference{}).Run(context.Background(), input(t, r, fake, pp, manifest(), base, head, api.ModeWrite), sink(w)); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	for _, c := range w.changes {
		if c.Op == api.OpDelete && c.Target.PageID == "pg_list" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("the page of a removed operation must get a deletion proposal: %+v", w.changes)
	}
	rec := &passes.Recorder{}
	rep, err := passes.Reference{}.Run(context.Background(), input(t, r, fake, pp, manifest(), base, head, api.ModeDry), rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Recorded().Changes) != len(w.changes) || len(rep.Impact) != len(w.changes) {
		t.Fatalf("dry run must record the same changes: %d vs %d, impact %d", len(rec.Recorded().Changes), len(w.changes), len(rep.Impact))
	}
}

const legacySpec = `openapi: 3.0.0
info: {title: Legacy API, version: 1.0.0}
paths:
  /v1/old:
    get:
      tags: [Legacy]
      summary: Old endpoint
      responses: {'200': {description: ok}}
`

const traceSpec = specV2 + `  /v1/debug:
    trace:
      tags: [Charges]
      summary: Trace
      responses: {'200': {description: ok}}
`

func TestReferenceRemovesOperationsOfADeletedSpecAndSkipsTrace(t *testing.T) {
	r := newRepo(t)
	base := r.commit("specs", map[string]string{"api/openapi.yaml": specV2, "api/legacy.yaml": legacySpec})
	head := r.commit("drop legacy", map[string]string{"api/openapi.yaml": traceSpec, "api/legacy.yaml": ""})
	fake := newFakeAPI()
	v2 := apiBlocks(t, specV2)
	legacy := apiBlocks(t, legacySpec)
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_legacy", Slug: "legacy", Title: "Legacy"}, Blocks: []api.PageBlock{pageBlock(legacy["api:GET:/v1/old"])}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_ref", Slug: "refunds", Title: "Refunds"}, Blocks: []api.PageBlock{pageBlock(v2["api:POST:/v1/refunds"])}})
	fake.addPage("sp_1", &api.PageContent{Page: api.PageInfo{ID: "pg_chg", Slug: "charges", Title: "Charges"}, Blocks: []api.PageBlock{pageBlock(v2["api:GET:/v1/charges"])}})
	m := &config.Manifest{Version: 2, Code: &config.Code{OpenAPI: []string{"api/*.yaml"}}}
	w := &writes{}
	rep, err := passes.Reference{}.Run(context.Background(), input(t, r, fake, planPass(config.KindReference, "developer-api", nil), m, base, head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 1 {
		t.Fatalf("only the legacy page changes: %+v", w.changes)
	}
	c := w.changes[0]
	if c.Op != api.OpUpdate || c.Target.PageID != "pg_legacy" || len(c.RemoveBlockKeys) != 1 || c.RemoveBlockKeys[0] != "api:GET:/v1/old" {
		t.Fatalf("operations of a deleted spec must be removed: %+v", c)
	}
	var traced bool
	for _, warn := range rep.Warnings {
		if strings.Contains(warn, "TRACE /v1/debug skipped") {
			traced = true
		}
	}
	if !traced {
		t.Fatalf("a TRACE operation is skipped with a warning: %v", rep.Warnings)
	}
}

const specTagged = `openapi: 3.0.0
info: {title: Billing API, version: 1.0.0}
tags:
  - name: refunds
    description: Money going back to the customer.
    x-displayName: Refunds and reversals
  - name: payment_methods
paths:
  /v1/refunds:
    post:
      tags: [refunds]
      summary: Create a refund
      responses: {'201': {description: created}}
  /v1/payment-methods:
    get:
      tags: [payment_methods]
      summary: List payment methods
      responses: {'200': {description: ok}}
  /v1/charges:
    get:
      tags: [charges]
      summary: List charges
      responses: {'200': {description: ok}}
`

func TestReferencePerTagTitlesUseDisplayNames(t *testing.T) {
	r := newRepo(t)
	head := r.commit("spec", map[string]string{"api/openapi.yaml": specTagged})
	fake := newFakeAPI()
	w := &writes{}
	in := input(t, r, fake, planPass(config.KindReference, "developer-api", nil), manifest(), "", head, api.ModeWrite)
	if _, err := (passes.Reference{}).Run(context.Background(), in, sink(w)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range w.changes {
		got[c.Target.Slug] = c.Title
	}
	want := map[string]string{
		"refunds":         "Refunds and reversals",
		"payment-methods": "Payment Methods",
		"charges":         "Charges",
	}
	if len(got) != len(want) {
		t.Fatalf("pages = %v", got)
	}
	for slug, title := range want {
		if got[slug] != title {
			t.Errorf("page %s title = %q, want %q", slug, got[slug], title)
		}
	}
}
