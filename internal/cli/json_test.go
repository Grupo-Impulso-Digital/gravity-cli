package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func TestJSONFlagIsConsistent(t *testing.T) {
	root := NewRootCommand()
	for _, path := range [][]string{
		{"ping"},
		{"repos"},
		{"spaces"},
		{"release-notes"},
		{"sync"},
		{"docs", "generate"},
		{"check", "api"},
		{"check", "docs"},
		{"coverage"},
		{"capture"},
		{"capture", "status"},
		{"nucleus", "query"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		f := cmd.Flags().Lookup("json")
		if f == nil || f.Value.Type() != "bool" {
			t.Errorf("%s has no boolean --json flag", cmd.CommandPath())
		}
	}
}

func TestDocsGenerateJSONSkip(t *testing.T) {
	dir := newGitRepo(t, map[string]string{config.ProjectFileName: "site: acme\n"})
	chdirTemp(t, dir)
	stdout, _, err := runRoot(t, "docs", "generate", "--since", "HEAD", "--json", "--api-url", "http://127.0.0.1:1", "--token", "sk_live_x")
	if err != nil {
		t.Fatal(err)
	}
	var rep docsReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if rep.Site != "acme" || rep.Skipped != "no changes since HEAD; nothing to do" {
		t.Errorf("report = %+v", rep)
	}
}

func TestDocsGenerateJSONDryRun(t *testing.T) {
	root := NewRootCommand()
	cmd, _, err := root.Find([]string{"docs", "generate"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	rep := &docsReport{Site: "acme"}
	targets := []syncTarget{{kind: "page", space: "guides", label: "docs users -> guides/intro", page: api.PageUpsertRequest{SpaceSlug: "guides", Slug: "intro", Title: "Intro"}}}
	if err := finishDocs(cmd, &env{}, "acme", targets, outputProposal, true, "", rep); err != nil {
		t.Fatal(err)
	}
	var got docsReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got.Authored) != 1 || got.Authored[0] != "guides/intro" || got.Sync == nil || !got.Sync.DryRun || len(got.Sync.Targets) != 1 {
		t.Errorf("report = %+v", got)
	}
}

func TestDocsGenerateJSONWhenEveryPageFails(t *testing.T) {
	dir := newGitRepo(t, map[string]string{config.ProjectFileName: "site: acme\nspaces:\n  default: guides\n"})
	chdirTemp(t, dir)
	fp := &fakePlatform{
		features: map[string]bool{featureDocsGenerate: true},
		llm: func(body []byte) any {
			var req api.MessagesRequest
			_ = json.Unmarshal(body, &req)
			for _, tool := range req.Tools {
				if tool.Name == agent.ToolSubmitDocPlan {
					return toolUseResponse(agent.ToolSubmitDocPlan, map[string]any{
						"pages": []any{map[string]any{"slug": "overview", "title": "Overview", "summary": "x"}},
						"units": []any{},
					})
				}
			}
			return map[string]any{
				"id": "msg_x", "type": "message", "role": "assistant", "stop_reason": "end_turn",
				"content": []any{map[string]any{"type": "text", "text": "I would rather not."}},
			}
		},
		routes: map[string]http.HandlerFunc{
			"GET /api/v1/sites/acme/pages": func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"pages": []any{}})
			},
		},
	}
	srv := fp.serve(t)
	stdout, stderr, err := runRoot(t, "docs", "generate", "--json", "--no-inventory", "--api-url", srv.URL, "--token", "sk_live_x")
	if CodeFor(err) != CodeError {
		t.Fatalf("exit = %d (%v), want error\nstderr:\n%s", CodeFor(err), err, stderr)
	}
	var rep docsReport
	if jerr := json.Unmarshal([]byte(stdout), &rep); jerr != nil {
		t.Fatalf("--json must still emit the report: %v\n%s", jerr, stdout)
	}
	if rep.Planned != 1 || len(rep.Failed) != 1 || rep.Failed[0] != "overview" || rep.Authored == nil || len(rep.Authored) != 0 {
		t.Errorf("report = %+v", rep)
	}
}
