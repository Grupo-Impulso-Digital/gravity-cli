package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func TestReleaseNotesSpaceResolution(t *testing.T) {
	cases := []struct {
		name string
		proj *config.Project
		flag string
		want string
	}{
		{"no manifest", nil, "", "changelog"},
		{"manifest default never used", &config.Project{Spaces: config.Spaces{Default: "docs"}}, "", "changelog"},
		{"releaseNotes.space", &config.Project{Spaces: config.Spaces{Default: "docs"}, ReleaseNotes: config.ReleaseNotes{Space: "news"}}, "", "news"},
		{"flag overrides", &config.Project{ReleaseNotes: config.ReleaseNotes{Space: "news"}}, "releases", "releases"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseNotesSpace(tc.proj, tc.flag); got != tc.want {
				t.Errorf("space = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReleaseNotesChangelogResolution(t *testing.T) {
	if got := releaseNotesChangelog(nil, ""); got != "CHANGELOG.md" {
		t.Errorf("default = %q", got)
	}
	proj := &config.Project{ReleaseNotes: config.ReleaseNotes{Changelog: "docs/HISTORY.md"}}
	if got := releaseNotesChangelog(proj, ""); got != "docs/HISTORY.md" {
		t.Errorf("manifest = %q, want docs/HISTORY.md", got)
	}
	if got := releaseNotesChangelog(proj, "OTHER.md"); got != "OTHER.md" {
		t.Errorf("flag = %q, want OTHER.md", got)
	}
}

func releaseNotesPlatform(spaces ...string) *fakePlatform {
	if len(spaces) == 0 {
		spaces = []string{"changelog", "news", "releases"}
	}
	return &fakePlatform{
		llm: func([]byte) any {
			return toolUseResponse("submit_release_notes", map[string]any{
				"title":    "v1.1.0",
				"sections": []any{map[string]any{"heading": "Added", "items": []any{"A feature"}}},
			})
		},
		routes: map[string]http.HandlerFunc{
			"GET /api/v1/sites/acme": siteTreeHandler(spaces...),
			"POST /api/v1/sites/acme/release-notes": func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "v1-1-0", "status": "open", "proposalId": "p1", "reviewUrl": "https://x/review"})
			},
		},
	}
}

func siteTreeHandler(spaces ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list := make([]any, 0, len(spaces))
		for i, sp := range spaces {
			list = append(list, map[string]any{"id": fmt.Sprintf("s%d", i), "slug": sp})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"site": map[string]any{"slug": "acme"}, "spaces": list})
	}
}

func TestReleaseNotesCreatesMissingChangelogSpace(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\nspaces:\n  default: docs\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform("docs")
	fp.routes["POST /api/v1/sites/acme/spaces"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"space": map[string]any{"id": "s9", "slug": "changelog"}})
	}
	srv := fp.serve(t)

	if _, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1"); err != nil {
		t.Fatalf("release-notes with a missing changelog space: %v", err)
	}
	created := fp.bodies("POST /api/v1/sites/acme/spaces")
	if len(created) != 1 {
		t.Fatalf("expected one space creation, got %d", len(created))
	}
	var req struct {
		Slug string `json:"slug"`
		Type string `json:"type"`
	}
	_ = json.Unmarshal(created[0], &req)
	if req.Slug != "changelog" || req.Type != "release-notes" {
		t.Errorf("space create = %+v, want changelog/release-notes", req)
	}
	if len(fp.bodies("POST /api/v1/sites/acme/release-notes")) != 1 {
		t.Error("the proposal was not posted after creating the space")
	}
}

func TestReleaseNotesExistingSpaceIsNotRecreated(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform("changelog")
	srv := fp.serve(t)

	if _, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1"); err != nil {
		t.Fatal(err)
	}
	if n := len(fp.bodies("POST /api/v1/sites/acme/spaces")); n != 0 {
		t.Errorf("an existing space must not be re-posted, got %d create(s)", n)
	}
}

func TestReleaseNotesMissingSpaceWithoutWriteFailsBeforeLLM(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform("docs")
	fp.routes["POST /api/v1/sites/acme/spaces"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"Missing docs.spaces.manage."}}`))
	}
	srv := fp.serve(t)

	_, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1")
	if CodeFor(err) != CodeError {
		t.Fatalf("exit code = %d (%v), want %d", CodeFor(err), err, CodeError)
	}
	want := "space 'changelog' does not exist on site 'acme' and this token cannot create it"
	if !strings.HasPrefix(err.Error(), want) || strings.Contains(err.Error(), "gravity sync") {
		t.Errorf("error = %q, want prefix %q and no sync hint", err.Error(), want)
	}
	if n := len(fp.bodies("POST /api/llm/v1/messages")); n != 0 {
		t.Errorf("the agent ran %d turn(s) before the space check failed", n)
	}
	if n := len(fp.bodies("POST /api/v1/sites/acme/release-notes")); n != 0 {
		t.Errorf("release notes were posted %d time(s) to a missing space", n)
	}
}

func TestReleaseNotesDryRunSkipsSpaceCheck(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform("docs")
	srv := fp.serve(t)

	if _, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if n := len(fp.bodies("POST /api/v1/sites/acme/spaces")); n != 0 {
		t.Errorf("--dry-run must not create spaces, got %d", n)
	}
}

func TestReleaseNotesPostsToReleaseNotesSpaceNotDefault(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\nspaces:\n  default: product-docs\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform()
	srv := fp.serve(t)

	if _, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1"); err != nil {
		t.Fatalf("release-notes: %v", err)
	}
	posted := fp.bodies("POST /api/v1/sites/acme/release-notes")
	if len(posted) != 1 {
		t.Fatalf("expected one release-notes POST, got %d", len(posted))
	}
	var req struct {
		SpaceSlug string `json:"spaceSlug"`
	}
	_ = json.Unmarshal(posted[0], &req)
	if req.SpaceSlug != "changelog" {
		t.Errorf("spaceSlug = %q, want changelog (never spaces.default)", req.SpaceSlug)
	}

	if _, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1", "--space", "news"); err != nil {
		t.Fatalf("release-notes --space: %v", err)
	}
	posted = fp.bodies("POST /api/v1/sites/acme/release-notes")
	_ = json.Unmarshal(posted[len(posted)-1], &req)
	if req.SpaceSlug != "news" {
		t.Errorf("--space spaceSlug = %q, want news", req.SpaceSlug)
	}
}

func TestReleaseNotesEmptyRangeIsANoop(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\n"})
	chdirTemp(t, dir)
	fp := releaseNotesPlatform()
	srv := fp.serve(t)

	stdout, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD", "--to", "HEAD")
	if err != nil {
		t.Fatalf("an empty range must exit 0, got %v (code %d)", err, CodeFor(err))
	}
	if !strings.Contains(stdout, "no commits in range HEAD..HEAD; nothing to do") {
		t.Errorf("stdout = %q", stdout)
	}
	if len(fp.bodies("POST /api/llm/v1/messages")) != 0 {
		t.Error("an empty range must not spend an LLM call")
	}

	stdout, _, err = runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD", "--to", "HEAD", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var view releaseNotesView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil || view.Skipped == "" || view.Commits != 0 {
		t.Errorf("--json empty range = %q (%v)", stdout, err)
	}
}

func TestReleaseNotesJSONOutput(t *testing.T) {
	dir := newGitRepo(t, map[string]string{".gravity.yaml": "site: acme\nreleaseNotes:\n  space: news\n"})
	chdirTemp(t, dir)
	srv := releaseNotesPlatform().serve(t)

	stdout, _, err := runRoot(t, "release-notes", "--api-url", srv.URL, "--token", "sk_live_x", "--from", "HEAD~1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var view releaseNotesView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if view.Space != "news" || view.Commits != 1 || view.Proposal == nil || view.Proposal.ProposalID != "p1" || view.Notes == nil {
		t.Errorf("view = %+v", view)
	}
}

func TestReleaseNotesOutsideRepoIsHumanized(t *testing.T) {
	chdirTemp(t, t.TempDir())
	_, _, err := runRoot(t, "release-notes", "--api-url", "http://127.0.0.1:1", "--token", "sk_live_x")
	if err == nil || err.Error() != "not a git repository — run inside your repo" {
		t.Errorf("err = %v", err)
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d", CodeFor(err))
	}
}
