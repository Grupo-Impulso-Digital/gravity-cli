package api_test

import (
	"context"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func TestValidateEndpoint(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"ok":false,"issues":[{"severity":"error","code":"slug_taken","pass":"docs","path":"README.md","message":"taken","hint":"adopt"}],"i18n":[{"pass":"guides","languages":["fr"],"effective":[],"reason":"translations are off"}]}`)
	res, err := c.Validate(context.Background(), "github.com/acme/x", api.ValidateRequest{ManifestHash: "sha256:m", Branch: "feat/x", Verbatim: []api.ValidateVerbatim{{Pass: "docs", Slug: "overview", Title: "X", SourcePath: "README.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	expectRequest(t, s, "POST", "/api/v1/repos/self/validate")
	if s.query.Get("repo") != "github.com/acme/x" || s.body["branch"] != "feat/x" || s.body["manifestHash"] != "sha256:m" || len(s.body["verbatim"].([]any)) != 1 {
		t.Fatalf("request = %v %v", s.query, s.body)
	}
	if res.OK || res.Issues[0].Code != "slug_taken" || res.I18n[0].Reason != "translations are off" {
		t.Fatalf("response = %+v", res)
	}
}

func TestUnsupportedEndpointsAreRecognised(t *testing.T) {
	c, _ := fixtureServer(t, 404, `{"error":{"code":"not_found","message":"no route"}}`)
	_, err := c.Validate(context.Background(), "", api.ValidateRequest{Branch: "main"})
	if !api.IsUnsupported(err) {
		t.Fatalf("err = %v", err)
	}
	c, _ = fixtureServer(t, 404, `{"error":{"code":"repo_not_connected","message":"not connected"}}`)
	if _, err := c.Approvals(context.Background(), ""); api.IsUnsupported(err) {
		t.Fatal("a known 404 code is a real error")
	}
	c, _ = fixtureServer(t, 400, `{"error":{"code":"bad_request","message":"manifestHash is accepted on dry runs only."}}`)
	if _, err := c.StartRun(context.Background(), "", api.StartRunRequest{}); !api.RejectsWriteManifest(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestApprovalEndpoints(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"repo":{"id":"cr_1","remoteKey":"github.com/acme/docs","name":"docs"},"canApprove":true,"pending":[{"target":"docs/guides","site":"docs","space":"guides","passes":["guides"],"reasons":["skips_review"],"why":"verbatim imports go live without review","mayApprove":true}],"granted":[{"target":"docs/api","passes":["api"],"source":"auto"}]}`)
	got, err := c.Approvals(context.Background(), "")
	if err != nil || len(got.Pending) != 1 || !got.Pending[0].MayApprove || got.Pending[0].Target != "docs/guides" || got.Pending[0].Passes[0] != "guides" || got.Granted[0].Source != "auto" || got.Repo.Name != "docs" {
		t.Fatalf("approvals = %+v %v", got, err)
	}
	expectRequest(t, s, "GET", "/api/v1/repos/self/approvals")
	c, s = fixtureServer(t, 200, `{"approved":[{"target":"docs/guides","site":"docs","space":"guides","passes":["guides"]}],"refused":[{"target":"ops/runbooks","code":"forbidden","message":"needs docs.write"}]}`)
	res, err := c.Approve(context.Background(), "", api.ApproveRequest{Spaces: []string{"docs/guides", "ops/runbooks"}})
	if err != nil || len(res.Approved) != 1 || res.Refused[0].Code != "forbidden" || len(s.body["spaces"].([]any)) != 2 {
		t.Fatalf("approve = %+v %v %v", res, err, s.body)
	}
}

func TestGrantKeyAndWhy(t *testing.T) {
	for ref, want := range map[string]string{"docs/guides": "docs/guides", "docs/guides/setup/advanced": "docs/guides", "docs": "docs"} {
		if got := api.GrantKey(ref); got != want {
			t.Errorf("GrantKey(%q) = %q, want %q", ref, got, want)
		}
	}
	if got := api.ApprovalWhy("verbatim", []string{api.ApprovalSkipsReview, api.ApprovalPrivateSpace}); got != "verbatim imports go live without review; the space is not public, so the repository token could read its content" {
		t.Errorf("why = %q", got)
	}
	if got := api.ApprovalWhy("guides", []string{api.ApprovalSkipsReview}); got != "changes are auto-accepted without review" {
		t.Errorf("why = %q", got)
	}
}

func TestRunListAndCancelEndpoints(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"runs":[{"id":"prun_1","trigger":"push","mode":"write","status":"running"}]}`)
	runs, err := c.RepoRuns(context.Background(), "github.com/acme/x", "running", 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	if s.query.Get("status") != "running" || s.query.Get("limit") != "5" || s.query.Get("repo") != "github.com/acme/x" {
		t.Fatalf("query = %v", s.query)
	}
	c, s = fixtureServer(t, 200, `{"run":{"id":"prun_1","status":"cancelled"}}`) //nolint:misspell // platform status value
	res, err := c.CancelRun(context.Background(), "prun_1")
	if err != nil || res.Run.Status != api.StatusCancelled {
		t.Fatalf("cancel = %+v %v", res, err)
	}
	expectRequest(t, s, "POST", "/api/v1/runs/prun_1/cancel")
}

func TestStructureEndpoints(t *testing.T) {
	c, s := fixtureServer(t, 200, `{"site":{"slug":"polaris","name":"Polaris"},"spaces":[{"slug":"docs","pages":[{"slug":"overview","title":"Polaris","status":"published","lockedByRepo":"github.com/acme/web"}]}]}`)
	tree, err := c.StructureTree(context.Background(), "polaris")
	if err != nil || tree.Spaces[0].Pages[0].LockedByRepo != "github.com/acme/web" {
		t.Fatalf("tree = %+v %v", tree, err)
	}
	if s.path != "/api/v1/structure" || s.query.Get("site") != "polaris" {
		t.Fatalf("request = %s %v", s.path, s.query)
	}
	c, s = fixtureServer(t, 200, `{"created":[{"kind":"space","path":"docs"}],"updated":[],"unchanged":[],"extra":[],"conflicts":[],"deferred":[{"kind":"page","path":"docs#overview","reason":"deferred_to_import"}]}`)
	res, err := c.ApplyStructure(context.Background(), "", api.StructureApplyRequest{Structure: api.Structure{Site: api.StructureSite{Slug: "polaris"}}, DryRun: true})
	if err != nil || len(res.Created) != 1 || res.Deferred[0].Reason != "deferred_to_import" || s.body["dryRun"] != true {
		t.Fatalf("apply = %+v %v %v", res, err, s.body)
	}
	expectRequest(t, s, "POST", "/api/v1/structure/apply")
}

func TestPlanEstimateAndRunProgressDecode(t *testing.T) {
	c, _ := fixtureServer(t, 200, `{"planHash":"h","passes":[{"name":"guides","kind":"guides","estimate":{"ai":true,"firstRun":true,"commits":50,"files":120,"approxInputTokens":90000,"approxCostUsd":0.6,"model":"m"}}]}`)
	p, err := c.Plan(context.Background(), api.PlanQuery{Trigger: "manual"})
	if err != nil || p.Passes[0].Estimate == nil || *p.Passes[0].Estimate.ApproxCostUSD != 0.6 || !p.Passes[0].Estimate.FirstRun {
		t.Fatalf("plan = %+v %v", p, err)
	}
	c, _ = fixtureServer(t, 200, `{"run":{"id":"prun_1","status":"running","lease":{"key":"cr_1:main","expiresAt":"x"}},"passes":[{"name":"guides","status":"running","progress":{"step":"writing","done":1,"total":4}}],"bundle":{}}`)
	run, err := c.GetRun(context.Background(), "prun_1")
	if err != nil || run.Run.Lease.Key != "cr_1:main" || run.Passes[0].Progress.Total != 4 {
		t.Fatalf("run = %+v %v", run, err)
	}
}
