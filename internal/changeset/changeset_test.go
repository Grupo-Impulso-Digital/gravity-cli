package changeset

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

var update = flag.Bool("update", false, "rewrite golden files")

const specBase = `openapi: 3.0.3
info: {title: Billing, version: "1"}
paths:
  /v1/refunds:
    post:
      operationId: createRefund
      summary: Create a refund
      parameters:
        - {name: Idempotency-Key, in: header, required: false, schema: {type: string}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount: {type: integer}
                currency: {type: string, enum: [usd, eur, gbp]}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
                  retry_count: {type: integer}
  /v1/refunds/{id}:
    get:
      operationId: getRefund
      summary: Get a refund
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200": {description: ok}
  /v1/legacy:
    get:
      summary: Legacy
      responses:
        "200": {description: ok}
`

const specHead = `openapi: 3.0.3
info: {title: Billing, version: "2"}
paths:
  /v1/refunds:
    post:
      operationId: createRefund
      summary: Create a refund
      parameters:
        - {name: Idempotency-Key, in: header, required: true, schema: {type: string}}
        - {name: dry_run, in: query, required: false, schema: {type: boolean}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount: {type: integer}
                currency: {type: string, enum: [usd, eur]}
                reason: {type: string, enum: [duplicate, fraudulent, requested_by_customer]}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
        "422": {description: invalid}
  /v1/refunds/{id}:
    get:
      operationId: getRefund
      summary: Fetch a refund
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200": {description: ok}
  /v1/refunds/{id}/cancel:
    post:
      operationId: cancelRefund
      summary: Cancel a refund
      responses:
        "200": {description: ok}
`

func TestOpenAPIDiffGolden(t *testing.T) {
	d := DiffOpenAPI("api/openapi.yaml", []byte(specBase), []byte(specHead))
	got, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "openapi-diff.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run go test -update): %v", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(got)) {
		t.Fatalf("openapi diff mismatch\n got: %s\nwant: %s", got, want)
	}
	if !d.Breaking || len(d.Added) != 1 || len(d.Removed) != 1 || len(d.Changed) != 2 {
		t.Fatalf("diff = %+v", d)
	}
}

func TestOpenAPIDiffEdgeCases(t *testing.T) {
	added := DiffOpenAPI("a.yaml", nil, []byte(specBase))
	if len(added.Added) != 3 || added.Breaking {
		t.Fatalf("new document = %+v", added)
	}
	removed := DiffOpenAPI("a.yaml", []byte(specBase), nil)
	if len(removed.Removed) != 3 || !removed.Breaking {
		t.Fatalf("deleted document = %+v", removed)
	}
	same := DiffOpenAPI("a.yaml", []byte(specBase), []byte(specBase))
	if len(same.Changed) != 0 || same.Breaking {
		t.Fatalf("identical = %+v", same)
	}
	bad := DiffOpenAPI("a.yaml", []byte(specBase), []byte("not: [valid"))
	if bad.Error == "" {
		t.Fatal("want parse error")
	}
	swagger := `swagger: "2.0"
info: {title: x, version: "1"}
paths:
  /pets:
    get:
      parameters:
        - {name: limit, in: query, type: integer, required: true}
      responses:
        "200":
          description: ok
          schema:
            type: object
            properties:
              items: {type: array}
`
	sw := DiffOpenAPI("s.yaml", []byte(strings.Replace(swagger, "required: true", "required: false", 1)), []byte(swagger))
	if len(sw.Changed) != 1 || !sw.Breaking || sw.Changed[0].Changes[0] != "parameter limit (query) is now required" {
		t.Fatalf("swagger = %+v", sw)
	}
}

func TestSymbols(t *testing.T) {
	diff := `diff --git a/src/refunds/events.ts b/src/refunds/events.ts
--- a/src/refunds/events.ts
+++ b/src/refunds/events.ts
@@ -1 +0,0 @@
-export interface RefundEvent { retryCount: number }
-export function oldHelper() {}
+export function newHelper() {}
diff --git a/svc/handler.go b/svc/handler.go
--- a/svc/handler.go
+++ b/svc/handler.go
@@ -1,2 +0,0 @@
-func (s *Server) HandleRefund(w http.ResponseWriter) {}
-type Legacy struct{}
diff --git a/a/mover.py b/a/mover.py
--- a/a/mover.py
+++ /dev/null
@@ -1 +0,0 @@
-def moved():
diff --git a/b/mover.py b/b/mover.py
--- /dev/null
+++ b/b/mover.py
@@ -0,0 +1 @@
+def moved():
diff --git a/vendor/x.go b/vendor/x.go
--- a/vendor/x.go
+++ b/vendor/x.go
@@ -1 +0,0 @@
-func Vendored() {}
`
	got := ParseSymbols(diff, func(p string) bool { return !Excluded(p, nil) })
	names := map[string]string{}
	for _, s := range got.Removed {
		names[s.Name] = s.File + "|" + s.Language
	}
	if len(got.Removed) != 4 || names["oldHelper"] == "" || names["RefundEvent"] != "src/refunds/events.ts|typescript" || names["HandleRefund"] != "svc/handler.go|go" || names["Legacy"] == "" {
		t.Fatalf("removed = %+v", got.Removed)
	}
	if _, moved := names["moved"]; moved {
		t.Fatal("a symbol moved to another file is not removed")
	}
	rename := ParseSymbols("diff --git a/x.py b/x.py\n--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n-def compute_total():\n+def compute_sum():\n", nil)
	if len(rename.Renamed) != 1 || rename.Renamed[0].From != "compute_total" || rename.Renamed[0].To != "compute_sum" {
		t.Fatalf("renamed = %+v", rename)
	}
	java := ParseSymbols("diff --git a/A.java b/A.java\n--- a/A.java\n+++ b/A.java\n@@ -1,2 +0,0 @@\n-public class Refunds {\n-  public static String format(int x) {\n", nil)
	if len(java.Removed) != 2 {
		t.Fatalf("java = %+v", java)
	}
}

func TestPRNumber(t *testing.T) {
	cases := map[string]int{
		"feat(refunds): add reason (#42)":      42,
		"Merge pull request #7 from acme/feat": 7,
		"Merged in feat/x (pull request #12)":  12,
		"fix: something":                       0,
		"chore: bump (#3) and more":            0,
	}
	for subject, want := range cases {
		got := prNumber(subject)
		if (want == 0) != (got == nil) || (got != nil && *got != want) {
			t.Errorf("prNumber(%q) = %v, want %d", subject, got, want)
		}
	}
}

func TestMatchSourceRef(t *testing.T) {
	cases := []struct {
		ref, file string
		want      bool
	}{
		{"api/openapi.yaml#/paths/~1v1~1refunds/post", "api/openapi.yaml", true},
		{"src/server/refunds/**", "src/server/refunds/handler.ts", true},
		{"src/server/refunds/**", "src/server/payments/handler.ts", false},
		{"routes/billing.ts", "routes/billing.ts", true},
		{"routes/billing.ts", "routes/billing.tsx", false},
		{"src/ui/", "src/ui/page.tsx", true},
		{"#only-fragment", "x", false},
	}
	for _, tc := range cases {
		if got := MatchSourceRef(tc.ref, tc.file); got != tc.want {
			t.Errorf("MatchSourceRef(%q, %q) = %v", tc.ref, tc.file, got)
		}
	}
}

func inventory() []api.Unit {
	active := true
	return []api.Unit{
		{Key: "api:post:/v1/refunds", Kind: "api", Contributors: []api.Contributor{
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: "implements", Active: &active, SourceRefs: []string{"api/openapi.yaml#/paths/~1v1~1refunds/post", "src/server/refunds/**"}},
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/gateway"}, Role: "declares", SourceRefs: []string{"routes/billing.ts"}},
		}},
		{Key: "api:get:/v1/legacy", Kind: "api", Contributors: []api.Contributor{
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: "implements", SourceRefs: []string{"api/openapi.yaml#/paths/~1v1~1legacy/get"}},
		}},
		{Key: "api:get:/v1/refunds/:id", Kind: "api", Contributors: []api.Contributor{
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: "implements", SourceRefs: []string{"api/openapi.yaml#/paths/~1v1~1refunds~1{id}/get"}},
		}},
		{Key: "gateway-routing", Kind: "service", Contributors: []api.Contributor{
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/gateway"}, Role: "implements", SourceRefs: []string{"routes/billing.ts"}},
		}},
		{Key: "ui-refunds", Kind: "feature", Contributors: []api.Contributor{
			{Repo: api.RepoRef{RemoteKey: "github.com/acme/billing-api"}, Role: "implements", SourceRefs: []string{"src/ui/refunds/**"}},
		}},
	}
}

func TestBuildChangeSet(t *testing.T) {
	s := newScripted(t)
	base := s.commit("chore: init", map[string]string{
		"api/openapi.yaml":        specBase,
		"src/server/refunds/a.ts": "export function oldHelper() {}\nexport interface RefundEvent {}\n",
		"routes/billing.ts":       "x",
		"docs/guide.md":           "# Guide\n",
		"legacy/notes.txt":        "rename me please, this content is long enough to be detected as a rename\n",
		"src/ui/refunds/page.tsx": "export const Page = 1\n",
		"scripts/gen.sh":          "echo\n",
		"package-lock.json":       "{}\n",
		"node_modules/x/index.js": "x\n",
	})
	s.git("mv", "legacy/notes.txt", "legacy/renamed.txt")
	s.remove("src/ui/refunds/page.tsx")
	head := s.commit("feat(refunds): add reason (#42)", map[string]string{
		"api/openapi.yaml":        specHead,
		"src/server/refunds/a.ts": "export function newHelper() {}\n",
		"routes/billing.ts":       "y",
		"docs/guide.md":           "# Guide\n\nMore.\n",
		"scripts/gen.sh":          "echo 2\n",
		"package-lock.json":       "{\"a\":1}\n",
		"node_modules/x/index.js": "y\n",
	})
	repo := s.repo()
	b := NewBuilder(repo, Options{
		Trigger: "push", Branch: "main",
		CodeExclude: []string{"scripts/**"},
		OpenAPI:     []string{"api/*.yaml"},
		Inventory:   inventory(),
		RepoKey:     "github.com/acme/billing-api",
	})
	cs, err := b.For(context.Background(), Range{Kind: api.RangeWatermark, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := b.For(context.Background(), Range{Kind: api.RangeWatermark, Base: base, Head: head})
	if again != cs {
		t.Fatal("ChangeSet must be computed once per range")
	}
	if len(cs.Commits) != 1 || cs.Commits[0].PR == nil || *cs.Commits[0].PR != 42 || cs.Commits[0].Author != "Ana <ana@acme.io>" {
		t.Fatalf("commits = %+v", cs.Commits)
	}
	paths := map[string]File{}
	for _, f := range cs.Files {
		paths[f.Path] = f
	}
	for _, excluded := range []string{"scripts/gen.sh", "package-lock.json", "node_modules/x/index.js"} {
		if _, ok := paths[excluded]; ok {
			t.Errorf("%s must be excluded", excluded)
		}
	}
	if f := paths["legacy/renamed.txt"]; f.Status != "R" || f.OldPath == nil || *f.OldPath != "legacy/notes.txt" {
		t.Errorf("rename = %+v", f)
	}
	if f := paths["src/ui/refunds/page.tsx"]; f.Status != "D" {
		t.Errorf("delete = %+v", f)
	}
	if len(cs.Docs.Changed) != 1 || cs.Docs.Changed[0] != "docs/guide.md" {
		t.Errorf("docs = %v", cs.Docs.Changed)
	}
	touched := map[string]TouchedUnit{}
	for _, u := range cs.Units.Touched {
		touched[u.Key] = u
	}
	if _, ok := touched["gateway-routing"]; ok {
		t.Error("units of another repository must not be touched by this repository's files")
	}
	if u := touched["api:post:/v1/refunds"]; u.Change != "modified" || len(u.MatchedRefs) != 2 {
		t.Errorf("refunds = %+v", u)
	}
	if u := touched["ui-refunds"]; u.Change != "removed" {
		t.Errorf("ui-refunds = %+v", u)
	}
	if len(cs.OpenAPI) != 1 || cs.OpenAPI[0].Path != "api/openapi.yaml" || !cs.OpenAPI[0].Breaking {
		t.Fatalf("openapi = %+v", cs.OpenAPI)
	}
	if len(cs.Units.Added) != 1 || cs.Units.Added[0].Key != "api:post:/v1/refunds/:id/cancel" || !strings.HasPrefix(cs.Units.Added[0].Ref, "api/openapi.yaml#/paths/~1v1~1refunds~1{id}~1cancel/post") {
		t.Errorf("added units = %+v", cs.Units.Added)
	}
	if len(cs.Units.Removed) != 1 || cs.Units.Removed[0].Key != "api:get:/v1/legacy" {
		t.Errorf("removed units = %+v", cs.Units.Removed)
	}
	removedSyms := map[string]bool{}
	for _, sym := range cs.Symbols.Removed {
		removedSyms[sym.Name] = true
	}
	if len(cs.Symbols.Removed) != 3 || !removedSyms["RefundEvent"] || !removedSyms["oldHelper"] || !removedSyms["Page"] {
		t.Errorf("symbols = %+v", cs.Symbols)
	}
	if len(cs.Symbols.Renamed) != 0 {
		t.Errorf("renamed = %+v", cs.Symbols.Renamed)
	}
	data, err := json.Marshal(cs)
	if err != nil || !strings.Contains(string(data), `"rangeKind":"watermark"`) || !strings.Contains(string(data), `"mergeBase":null`) {
		t.Fatalf("json = %s %v", data, err)
	}
}

func TestBuildSkipsAndRoots(t *testing.T) {
	s := newScripted(t)
	first := s.commit("a", map[string]string{"a.go": "package a\n"})
	s.commit("b", map[string]string{"b.go": "package a\n"})
	repo := s.repo()
	cs, err := Build(context.Background(), repo, Range{Kind: api.RangeWatermark, Base: first, Head: first, Skip: api.SkipNoChanges}, Options{})
	if err != nil || len(cs.Files) != 0 || len(cs.Commits) != 0 {
		t.Fatalf("skip = %+v %v", cs, err)
	}
	head := s.git("rev-parse", "HEAD")
	root, err := Build(context.Background(), repo, Range{Kind: api.RangeSurvey, Base: "", Head: head}, Options{})
	if err != nil || len(root.Commits) != 2 || len(root.Files) != 2 {
		t.Fatalf("root survey = %+v %v", root, err)
	}
	capped, err := Build(context.Background(), repo, Range{Kind: api.RangeSurvey, Base: "", Head: head}, Options{MaxCommits: 1, MaxFiles: 1})
	if err != nil || !capped.Truncated || len(capped.Commits) != 1 || len(capped.Files) != 1 {
		t.Fatalf("capped = %+v %v", capped, err)
	}
}

func TestBuildWorkingTree(t *testing.T) {
	s := newScripted(t)
	s.commit("a", map[string]string{"api/openapi.yaml": specBase})
	s.write("api/openapi.yaml", specHead)
	repo := s.repo()
	rng, err := NewResolver(repo).Resolve(context.Background(), RangeInput{Trigger: "manual", WorkingTree: true})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := Build(context.Background(), repo, rng, Options{OpenAPI: []string{"api/openapi.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if cs.RangeKind != api.RangeWorkingTree || len(cs.Files) != 1 || len(cs.OpenAPI) != 1 || len(cs.OpenAPI[0].Added) != 1 || len(cs.Commits) != 0 {
		t.Fatalf("working tree = %+v", cs)
	}
}

func TestSymbolsFollowCodeInclude(t *testing.T) {
	s := newScripted(t)
	base := s.commit("init", map[string]string{
		"src/billing.go":     "package billing\n\nfunc Refund() {}\n",
		"scripts/release.go": "package main\n\nfunc Bump() {}\n",
	})
	head := s.commit("drop both", map[string]string{
		"src/billing.go":     "package billing\n",
		"scripts/release.go": "package main\n",
	})
	rng := Range{Kind: "watermark", Base: base, Head: head}
	for _, tc := range []struct {
		include []string
		want    []string
	}{{nil, []string{"Bump", "Refund"}}, {[]string{"src/**"}, []string{"Refund"}}} {
		cs, err := Build(context.Background(), s.repo(), rng, Options{Trigger: "push", CodeInclude: tc.include})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, sym := range cs.Symbols.Removed {
			got = append(got, sym.Name)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("include %v: removed symbols = %v, want %v", tc.include, got, tc.want)
		}
		if len(cs.Files) != 2 {
			t.Fatalf("code.include never hides files from the ChangeSet: %+v", cs.Files)
		}
	}
}
