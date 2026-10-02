package passes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

type repoT struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repoT {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &repoT{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.git("config", "commit.gpgsign", "false")
	r.git("config", "tag.gpgsign", "false")
	return r
}

func (r *repoT) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=ana@acme.io", "GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=ana@acme.io", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repoT) write(name, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repoT) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for name, body := range files {
		if body == "" {
			r.git("rm", "-q", name)
			continue
		}
		r.write(name, body)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *repoT) open() *git.Repo {
	r.t.Helper()
	repo, err := git.Open(context.Background(), r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return repo
}

type fakeAPI struct {
	mu        sync.Mutex
	pages     map[string]*api.PageContent
	trees     map[string]*api.SpaceTree
	search    []api.SearchHit
	inventory []api.Unit
	recall    []api.RecallHit
	runs      []*api.Run
	llm       func(req api.MessagesRequest) api.MessagesResponse
	requests  []api.MessagesRequest
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{pages: map[string]*api.PageContent{}, trees: map[string]*api.SpaceTree{}}
}

func (f *fakeAPI) addPage(spaceID string, pc *api.PageContent) {
	f.pages[pc.Page.ID] = pc
	f.pages[spaceID+"/"+pc.Page.Slug] = pc
	tree := f.trees[spaceID]
	if tree == nil {
		tree = &api.SpaceTree{Space: api.NamedRef{ID: spaceID, Slug: "space"}}
		f.trees[spaceID] = tree
	}
	tree.Pages = append(tree.Pages, api.TreePage{ID: pc.Page.ID, Slug: pc.Page.Slug, Title: pc.Page.Title, CollectionPath: pc.Page.CollectionPath, Units: pc.Page.Units, Lock: pc.Page.Lock, Status: pc.Page.Status})
}

func notFoundErr() error {
	return &api.APIError{StatusCode: http.StatusNotFound, Code: api.CodeNotFound, Message: "no page"}
}

func (f *fakeAPI) Page(_ context.Context, id string, _ api.PageQuery) (*api.PageContent, error) {
	if pc, ok := f.pages[id]; ok {
		return pc, nil
	}
	return nil, notFoundErr()
}

func (f *fakeAPI) PageBySlug(_ context.Context, spaceID, slug string, _ api.PageQuery) (*api.PageContent, error) {
	if pc, ok := f.pages[spaceID+"/"+slug]; ok {
		return pc, nil
	}
	return nil, notFoundErr()
}

func (f *fakeAPI) Search(context.Context, api.SearchRequest) ([]api.SearchHit, error) {
	return f.search, nil
}

func (f *fakeAPI) Recall(context.Context, api.RecallRequest) (*api.RecallResult, error) {
	return &api.RecallResult{Hits: f.recall}, nil
}

func (f *fakeAPI) Inventory(context.Context, api.InventoryQuery) (*api.Inventory, error) {
	return &api.Inventory{Units: f.inventory}, nil
}

func (f *fakeAPI) SpaceTree(_ context.Context, id string) (*api.SpaceTree, error) {
	if t, ok := f.trees[id]; ok {
		return t, nil
	}
	return &api.SpaceTree{Space: api.NamedRef{ID: id}}, nil
}

func (f *fakeAPI) GetRun(context.Context, string) (*api.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runs) == 0 {
		return &api.Run{}, nil
	}
	r := f.runs[0]
	if len(f.runs) > 1 {
		f.runs = f.runs[1:]
	}
	return r, nil
}

func (f *fakeAPI) Messages(_ context.Context, req api.MessagesRequest) (*api.MessagesResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.llm == nil {
		return nil, fmt.Errorf("unexpected LLM call")
	}
	r := f.llm(req)
	return &r, nil
}

func submit(name string, input any) api.MessagesResponse {
	data, _ := json.Marshal(input)
	return api.MessagesResponse{
		StopReason: api.StopToolUse, Usage: api.Usage{InputTokens: 100, OutputTokens: 10},
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: "tu_1", Name: name, Input: data}},
	}
}

func lastTool(req api.MessagesRequest) string {
	for _, t := range req.Tools {
		if strings.HasPrefix(t.Name, "submit_") || t.Name == agent.ToolReportFindings {
			return t.Name
		}
	}
	return ""
}

func planPass(kind, name string, opts map[string]any) api.PlanPass {
	return api.PlanPass{
		ID: "rp_" + name, Name: name, Kind: kind, Enabled: true, Applies: true, Audiences: []string{"developers"},
		Target:  api.PassTarget{Status: api.TargetOK, Ref: "dev/api", Site: &api.NamedRef{ID: "site_1", Slug: "dev", Name: "Dev"}, Space: &api.NamedRef{ID: "sp_1", Slug: "api", Name: "API"}},
		Options: opts,
	}
}

func input(t *testing.T, r *repoT, fake *fakeAPI, pp api.PlanPass, m *config.Manifest, base, head, mode string) passes.Input {
	t.Helper()
	repo := r.open()
	p := &api.Plan{
		Repo:    api.PlanRepo{ID: "cr_1", Name: "billing-api", RemoteKey: "github.com/acme/billing-api", DefaultBranch: "main", WebURL: "https://github.com/acme/billing-api"},
		Product: api.Product{Slug: "acme", NucleusNamespace: "product:acme"}, Passes: []api.PlanPass{pp}, Inventory: api.PlanInventory{Units: fake.inventory},
		Capabilities: api.Capabilities{Features: map[string]bool{"cross-repo-hints": true}},
	}
	kind := api.RangeWatermark
	if base == "" {
		kind = api.RangeSurvey
	}
	rng := changeset.Range{Kind: kind, Base: base, Head: head}
	opts := changeset.Options{Trigger: "push", Branch: "main", OpenAPI: m.OpenAPIFiles(), Inventory: fake.inventory, RepoKey: "github.com/acme/billing-api"}
	b := changeset.NewBuilder(repo, opts)
	cs, err := b.For(context.Background(), rng)
	if err != nil {
		t.Fatal(err)
	}
	h := &agent.Harness{LLM: fake, RunID: "prun_1", RunPassID: "ppr_1"}
	return passes.Input{
		Pass: pp, Plan: p, Manifest: m, Range: rng, ChangeSet: cs, Builder: b, RunID: "prun_1", RunPassID: "ppr_1", Mode: mode, Trigger: "push",
		Repo: repo, Info: passes.RepoInfo{ID: "cr_1", RemoteKey: "github.com/acme/billing-api", Name: "billing-api", WebURL: "https://github.com/acme/billing-api", Provider: "github", Branch: "main"},
		Client: fake, Harness: h, Known: passes.NewUnits(fake.inventory), Generator: "gravity-cli/test",
	}
}

type writes struct {
	mu        sync.Mutex
	changes   []api.ChangeRequest
	verbatim  []api.VerbatimRequest
	deletions []api.VerbatimDeleteRequest
	assets    []string
	assetPass []string
	hints     []api.HintInput
	memories  []api.MemoryWrite
	fail      map[string]error
}

func (w *writes) ProposeChange(_ context.Context, _ string, req api.ChangeRequest) (*api.Change, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.fail[req.Target.Slug+req.Target.PageID]; err != nil {
		return nil, err
	}
	w.changes = append(w.changes, req)
	return &api.Change{ID: fmt.Sprintf("chg_%d", len(w.changes)), Op: req.Op, Status: "applied"}, nil
}

func (w *writes) ImportVerbatim(_ context.Context, _ string, req api.VerbatimRequest) (*api.VerbatimResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.verbatim = append(w.verbatim, req)
	if err := w.fail[req.File.Path]; err != nil {
		return nil, err
	}
	return &api.VerbatimResult{Change: &api.Change{ID: fmt.Sprintf("chg_v%d", len(w.verbatim)), Op: api.OpImport, Status: "applied"}}, nil
}

func (w *writes) DeleteVerbatim(_ context.Context, _ string, req api.VerbatimDeleteRequest) (*api.VerbatimResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deletions = append(w.deletions, req)
	if err := w.fail[req.Path]; err != nil {
		return nil, err
	}
	return &api.VerbatimResult{Change: &api.Change{ID: "chg_d", Op: api.OpDelete, Status: "applied"}}, nil
}

func (w *writes) UploadAsset(_ context.Context, _, runPassID, repoPath, _, sha string, _ []byte) (*api.Asset, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.assets = append(w.assets, repoPath)
	w.assetPass = append(w.assetPass, runPassID)
	return &api.Asset{Key: sha, URL: "https://media.test/" + sha[:8]}, nil
}

func (w *writes) RaiseHints(_ context.Context, _, _ string, hints []api.HintInput) (*api.HintsResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hints = append(w.hints, hints...)
	ids := make([]string, len(hints))
	for i := range hints {
		ids[i] = fmt.Sprintf("hint_%d", i)
	}
	return &api.HintsResult{Created: ids}, nil
}

func (w *writes) WriteMemory(_ context.Context, req api.MemoryWrite) (*api.MemoryResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.memories = append(w.memories, req)
	outcome := api.OutcomeCreated
	if strings.Contains(req.Title, "Retries") {
		outcome = api.OutcomeQueuedForReview
	}
	return &api.MemoryResult{Outcome: outcome}, nil
}

func sink(w *writes) *passes.PlatformSink {
	return &passes.PlatformSink{W: w, RunID: "prun_1", RunPassID: "ppr_1", Assets: passes.NewAssets()}
}
