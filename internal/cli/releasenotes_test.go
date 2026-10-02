package cli

import (
	"encoding/json"
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

func releaseNotesPlatform() *fakePlatform {
	return &fakePlatform{
		llm: func([]byte) any {
			return toolUseResponse("submit_release_notes", map[string]any{
				"title":    "v1.1.0",
				"sections": []any{map[string]any{"heading": "Added", "items": []any{"A feature"}}},
			})
		},
		routes: map[string]http.HandlerFunc{
			"POST /api/v1/sites/acme/release-notes": func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"pageSlug": "v1-1-0", "status": "open", "proposalId": "p1", "reviewUrl": "https://x/review"})
			},
		},
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
