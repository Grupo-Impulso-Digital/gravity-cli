package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func heading(level int, text string) api.ChangeBlock {
	return api.ChangeBlock{Type: agent.BlockHeading, Content: map[string]any{"text": text, "level": level}}
}

func prose(text string) api.ChangeBlock {
	return api.ChangeBlock{Type: agent.BlockProse, Content: map[string]any{"text": text}}
}

func code(lang, text string) api.ChangeBlock {
	return api.ChangeBlock{Type: agent.BlockCode, Content: map[string]any{"text": text, "language": lang}}
}

func table(rows ...[]string) api.ChangeBlock {
	cells := make([]any, 0, len(rows))
	for _, r := range rows {
		row := make([]any, 0, len(r))
		for _, c := range r {
			row = append(row, c)
		}
		cells = append(cells, row)
	}
	return api.ChangeBlock{Type: agent.BlockTable, Content: map[string]any{"rows": cells, "header": true}}
}

const detail = "Call this endpoint with a bearer token in the Authorization header. The response is JSON and lists the fields of the resource with their types. " +
	"Errors use the standard error object with a code and a message, and the request is safe to retry when the network fails before an answer arrives."

func renamed(path string) string {
	r := strings.NewReplacer("{topicId}", "{id}", "{postId}", ":postId", "{subscriptionId}", "{subscription}")
	return r.Replace(path)
}

func endpointSection(e Endpoint) []api.ChangeBlock {
	return []api.ChangeBlock{
		heading(2, e.Method+" "+renamed(e.Path)),
		prose(detail),
		code("json", `{"id": "top_1", "title": "Release planning"}`),
	}
}

func fullPage(extra ...api.ChangeBlock) []api.ChangeBlock {
	blocks := []api.ChangeBlock{heading(1, "Topics API"), prose("The Topics API manages discussion topics, their posts, webhook subscriptions and search. Every request needs a bearer token.")}
	for _, e := range Public(FixtureEndpoints) {
		blocks = append(blocks, endpointSection(e)...)
	}
	blocks = append(blocks, extra...)
	for i := range blocks {
		blocks[i].Key = fmt.Sprintf("guide:topics-api:%d", i+1)
	}
	return blocks
}

func create(title string, blocks []api.ChangeBlock) api.ChangeRequest {
	return api.ChangeRequest{Op: api.OpCreate, Title: title, Target: api.ChangeTarget{Slug: "topics-api"}, Blocks: blocks}
}

func recording(changes ...api.ChangeRequest) *run.Recording {
	return &run.Recording{
		Version: run.RecordingVersion, RunID: "prun_eval", CostUSD: 0.42,
		Passes: []run.RecordedPass{
			{
				Name: "topics-guide", Kind: "guides", Recorded: passes.Recorded{Changes: changes},
				Warnings:   []string{"partial (forced at cap): guides.write"},
				Generation: &api.Generation{InstructionsVersion: "abcdef0123456789", Model: "claude-sonnet-x", Provider: "anthropic"},
				Usage:      &run.RecordedUsage{Turns: 12, InputTokens: 90000, OutputTokens: 15000},
			},
			{Name: "notes", Kind: "nucleus", Usage: &run.RecordedUsage{Turns: 3, InputTokens: 1000, OutputTokens: 500}},
		},
	}
}

func frenchProse() string {
	return strings.Repeat("Cette page présente les sujets et les publications pour les équipes qui utilisent le service avec une clé dans chaque requête et qui sont sur la liste. ", 8)
}

func TestScore(t *testing.T) {
	tests := []struct {
		name         string
		rec          *run.Recording
		status       string
		pass         bool
		covered      int
		detailed     int
		leaks        int
		failedChecks map[string]int
		failures     []string
	}{
		{
			name: "complete page passes", rec: recording(create("Topics API", fullPage())),
			status: StatusScored, pass: true, covered: 14, detailed: 14,
		},
		{
			name: "endpoints only in a table are covered but thin",
			rec: recording(create("Topics API", func() []api.ChangeBlock {
				rows := [][]string{{"Method", "Path"}}
				for _, e := range Public(FixtureEndpoints) {
					rows = append(rows, []string{e.Method, e.Path})
				}
				return []api.ChangeBlock{heading(2, "Endpoints"), table(rows...)}
			}())),
			status: StatusScored, covered: 14, detailed: 0,
			failures: []string{"14 covered endpoints have fewer than 40 words"},
		},
		{
			name: "overview table plus a section per endpoint",
			rec: recording(create("Topics API", func() []api.ChangeBlock {
				rows := [][]string{{"Method", "Path", "Purpose"}}
				var sections []api.ChangeBlock
				for i, e := range Public(FixtureEndpoints) {
					rows = append(rows, []string{e.Method, e.Path, "Works with the topics, posts and subscriptions that this service keeps for each team"})
					sections = append(sections, heading(2, fmt.Sprintf("Operation %d", i+1)), prose("Send "+e.Method+" "+e.Path+". "+detail))
				}
				return append([]api.ChangeBlock{heading(1, "Topics API"), heading(2, "Overview"), table(rows...)}, sections...)
			}())),
			status: StatusScored, pass: true, covered: 14, detailed: 14,
		},
		{
			name: "internal endpoints leak",
			rec: recording(create("Topics API", fullPage(
				heading(2, "Operations"),
				prose("Operators call POST /internal/reindex to rebuild the index."),
				code("bash", "curl -X POST https://api.topics.example.com/v1/topics/:topicId/legacy-import"),
			))),
			status: StatusScored, covered: 14, detailed: 14, leaks: 2,
			failures: []string{"2 internal endpoint mentions"},
		},
		{
			name: "check errors fail their codes",
			rec: recording(create("Topics API", fullPage(
				prose("The response looks like {\"items\": [], \"nextCursor\": null}."),
				prose("## Pagination"),
				prose("This section will describe pagination in detail."),
				prose("TODO: document rate limits."),
				prose(frenchProse()),
			))),
			status: StatusScored, covered: 14, detailed: 14,
			failedChecks: map[string]int{agent.CheckCodeInProse: 1, agent.CheckMarkdownHead: 1, agent.CheckPlanningVoice: 1, agent.CheckOutputMarker: 1},
		},
		{
			name: "missing endpoints fail coverage",
			rec: recording(create("Topics API", func() []api.ChangeBlock {
				var blocks []api.ChangeBlock
				for _, e := range Public(FixtureEndpoints)[:10] {
					blocks = append(blocks, endpointSection(e)...)
				}
				return blocks
			}())),
			status: StatusScored, covered: 10, detailed: 10,
			failedChecks: map[string]int{agent.CheckCoverage: 1},
			failures:     []string{"public endpoints covered 10/14"},
		},
		{
			name: "two pages",
			rec: recording(
				create("Topics", fullPage()),
				api.ChangeRequest{Op: api.OpUpdate, Target: api.ChangeTarget{PageID: "pg_2"}, Blocks: []api.ChangeBlock{heading(2, "Search"), prose(detail)}},
			),
			status: StatusScored, covered: 14, detailed: 14,
			failures: []string{"2 pages, expected 1"},
		},
		{
			name:   "no page change",
			rec:    recording(api.ChangeRequest{Op: api.OpDelete, Target: api.ChangeTarget{PageID: "pg_1"}}),
			status: StatusFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Score(roundTrip(t, tt.rec), Options{Model: "claude-sonnet-x", Duration: 95 * time.Second})
			if r.Status != tt.status {
				t.Fatalf("status = %s (%s), want %s", r.Status, r.Reason, tt.status)
			}
			if r.Turns != 15 || r.InputTokens != 91000 || r.OutputTokens != 15500 || r.TotalTokens != 106500 {
				t.Errorf("usage = %d turns, %d in, %d out, %d total", r.Turns, r.InputTokens, r.OutputTokens, r.TotalTokens)
			}
			if r.DurationSeconds != 95 || r.Generation == nil || r.Generation.Provider != "anthropic" {
				t.Errorf("duration %v, generation %+v", r.DurationSeconds, r.Generation)
			}
			if tt.status == StatusFailed {
				if r.Pass || len(r.Failures) == 0 {
					t.Errorf("failed report: pass %v, failures %v", r.Pass, r.Failures)
				}
				return
			}
			if r.Pass != tt.pass {
				t.Errorf("pass = %v, failures %v", r.Pass, r.Failures)
			}
			if r.Coverage.Covered != tt.covered || r.Coverage.Detailed != tt.detailed || r.Coverage.Total != 14 {
				t.Errorf("coverage = %+v", r.Coverage)
			}
			if len(r.Leaks) != tt.leaks {
				t.Errorf("leaks = %+v", r.Leaks)
			}
			for _, code := range append(append([]string(nil), PageChecks...), agent.CheckCoverage) {
				c := r.Checks.ByCode[code]
				if got := c.Total - c.Passed; got != tt.failedChecks[code] {
					t.Errorf("%s failed %d times, want %d (findings %v)", code, got, tt.failedChecks[code], r.Pages[0].Findings)
				}
			}
			if wantTotal := len(r.Pages)*len(PageChecks) + 1; r.Checks.Total != wantTotal {
				t.Errorf("checks total = %d, want %d", r.Checks.Total, wantTotal)
			}
			for _, f := range tt.failures {
				if !contains(r.Failures, f) {
					t.Errorf("failures %v lack %q", r.Failures, f)
				}
			}
			if len(r.Warnings) != 1 || !strings.HasPrefix(r.Warnings[0], "topics-guide: partial") {
				t.Errorf("warnings = %v", r.Warnings)
			}
		})
	}
}

func TestScoreLanguage(t *testing.T) {
	blocks := fullPage()
	for i := range blocks {
		if blocks[i].Type == agent.BlockProse {
			blocks[i].Content = map[string]any{"text": frenchProse()}
		}
	}
	r := Score(recording(create("Topics API", blocks)), Options{})
	if c := r.Checks.ByCode[agent.CheckLanguage]; c.Passed != 0 || c.Total != 1 {
		t.Fatalf("E1 = %+v, findings %v", c, r.Pages[0].Findings)
	}
	if r.Checks.PassRate != 0.833 {
		t.Errorf("pass rate = %v", r.Checks.PassRate)
	}
}

func TestWordsPerEndpointSection(t *testing.T) {
	short := "Returns the tags."
	blocks := []api.ChangeBlock{
		heading(2, "Topics"),
		prose("Overview of GET /topics and GET /tags in one line."),
		heading(3, "GET /topics"),
		prose(detail),
		code("json", `{"items": []}`),
		heading(3, "GET /tags"),
		prose(short),
		heading(2, "Search"),
		prose("GET /search finds topics. " + detail),
	}
	r := Score(recording(create("Topics API", blocks)), Options{Endpoints: []Endpoint{{Method: "GET", Path: "/topics"}, {Method: "GET", Path: "/tags"}, {Method: "GET", Path: "/search"}, {Method: "GET", Path: "/missing"}}})
	want := map[string]int{"GET /topics": countWords("GET /topics") + countWords(detail), "GET /tags": countWords("GET /tags") + countWords(short), "GET /search": countWords("Search") + countWords("GET /search finds topics. "+detail), "GET /missing": 0}
	for _, e := range r.Coverage.Endpoints {
		if e.Words != want[e.Endpoint] {
			t.Errorf("%s: %d words, want %d", e.Endpoint, e.Words, want[e.Endpoint])
		}
	}
	if r.Words.Min != want["GET /tags"] || r.Words.Median != want["GET /topics"] {
		t.Errorf("word stats = %+v", r.Words)
	}
}

func TestEndpointMatching(t *testing.T) {
	topics := Endpoint{Method: "GET", Path: "/topics"}
	one := Endpoint{Method: "GET", Path: "/topics/{topicId}"}
	tests := []struct {
		text string
		e    Endpoint
		want bool
	}{
		{"GET /topics", topics, true},
		{"`GET /topics?limit=20`", topics, true},
		{"GET /topics/{id}", topics, false},
		{"GET /topics/{id}", one, true},
		{"get /topics/:topic_id returns one topic", one, true},
		{`curl -X GET "https://api.topics.example.com/v1/topics/{topicId}"`, one, true},
		{"GET /topics/{id}/posts", one, false},
		{"POST /topics", topics, false},
		{"FORGET /topics", topics, false},
	}
	for _, tt := range tests {
		if got := endpointPattern(tt.e).MatchString(normalize(tt.text)); got != tt.want {
			t.Errorf("%q matches %s = %v, want %v", tt.text, tt.e, got, tt.want)
		}
	}
}

func TestFailedAndMarkdown(t *testing.T) {
	r := Failed(Options{Model: "gpt-x", MaxOutput: 4096}, "the dry run recorded no changes")
	if r.Status != StatusFailed || r.Coverage.Total != 14 || r.Pass {
		t.Fatalf("failed report = %+v", r)
	}
	md := Markdown(r)
	for _, want := range []string{"# Model eval: gpt-x", "FAILED: the dry run recorded no changes", "| Max output | 4096 tokens |", "| Public endpoints covered | 0/14 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	scored := Score(recording(create("Topics API", fullPage(prose("POST /internal/reindex")))), Options{Model: "claude-sonnet-x"})
	md = Markdown(scored)
	for _, want := range []string{"Verdict: **FAIL**", "| Checks passed | 6/6 (100%) |", "| `GET /topics` | yes |", "## Internal leaks", "`POST /internal/reindex` on Topics API", "instructions abcdef01", "## Pass warnings"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}

func TestFindRecordingAndWrite(t *testing.T) {
	root := t.TempDir()
	if _, err := FindRecording(root, time.Time{}); err == nil {
		t.Fatal("no runs dir: want an error")
	}
	dir := filepath.Join(root, filepath.FromSlash(run.RecordingDir))
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for name, at := range map[string]time.Time{"old.json": start.Add(-time.Hour), "new.json": start.Add(time.Minute), "newer.json": start.Add(2 * time.Minute), "skip.txt": start.Add(time.Hour)} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindRecording(root, start)
	if err != nil || filepath.Base(got) != "newer.json" {
		t.Fatalf("FindRecording = %s, %v", got, err)
	}
	if _, err := FindRecording(root, start.Add(3*time.Minute)); err == nil {
		t.Error("nothing since: want an error")
	}

	rec := recording(create("Topics API", fullPage()))
	recPath := filepath.Join(dir, "prun_eval.json")
	if _, err := run.SaveRecording(root, rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecording(recPath)
	if err != nil || loaded.RunID != "prun_eval" {
		t.Fatalf("LoadRecording = %+v, %v", loaded, err)
	}
	out := t.TempDir()
	name := ResultName("Claude Sonnet/4.5", 4096, start)
	if name != "claude-sonnet-4.5-mo4096-20261011T120000Z" {
		t.Errorf("ResultName = %s", name)
	}
	jsonPath, mdPath, err := Write(out, name, Score(loaded, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	data, _ := os.ReadFile(jsonPath)
	if err := json.Unmarshal(data, &back); err != nil || !back.Pass || back.Model != "claude-sonnet-x" {
		t.Errorf("written JSON = %+v, %v", back, err)
	}
	if md, _ := os.ReadFile(mdPath); !strings.Contains(string(md), "Verdict: **PASS**") {
		t.Errorf("written markdown:\n%s", md)
	}
}

func roundTrip(t *testing.T, rec *run.Recording) *run.Recording {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var out run.Recording
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
