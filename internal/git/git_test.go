package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/impulso/gravity-cli/internal/git"
)

// testRepo creates a temp git repo with deterministic author/committer env so
// commits work in CI, then returns its path.
func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z",
	)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	run("config", "commit.gpgsign", "false")

	writeFile("README.md", "# Project\n")
	run("add", "README.md")
	run("commit", "-q", "-m", "chore: initial commit")

	writeFile("a.txt", "alpha\n")
	run("add", "a.txt")
	run("commit", "-q", "-m", "feat: add alpha")
	run("tag", "v1.0.0")

	writeFile("b.txt", "beta\n")
	writeFile("a.txt", "alpha updated\n")
	run("add", "-A")
	run("commit", "-q", "-m", "feat: add beta and update alpha")

	return dir
}

func TestOpenAndResolveRange(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()

	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	tag, err := repo.LatestTag(ctx)
	if err != nil {
		t.Fatalf("latest tag: %v", err)
	}
	if tag != "v1.0.0" {
		t.Errorf("expected latest tag v1.0.0, got %q", tag)
	}

	// Default range: latest tag -> HEAD.
	rng, err := repo.ResolveRange(ctx, "", "")
	if err != nil {
		t.Fatalf("resolve range: %v", err)
	}
	if rng.From != "v1.0.0" || rng.To != "HEAD" {
		t.Errorf("expected v1.0.0..HEAD, got %s..%s", rng.From, rng.To)
	}

	// Explicit overrides win.
	rng2, err := repo.ResolveRange(ctx, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("resolve range override: %v", err)
	}
	if rng2.From != "HEAD~1" {
		t.Errorf("expected explicit from HEAD~1, got %q", rng2.From)
	}
}

func TestResolveRangeNoTags(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "initial")

	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first, err := repo.FirstCommit(ctx)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	rng, err := repo.ResolveRange(ctx, "", "")
	if err != nil {
		t.Fatalf("resolve range: %v", err)
	}
	if rng.From != first {
		t.Errorf("with no tags, from should be first commit %q, got %q", first, rng.From)
	}
	if rng.To != "HEAD" {
		t.Errorf("expected to=HEAD, got %q", rng.To)
	}
}

func TestLogAndChangedFiles(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	commits, err := repo.Log(ctx, "v1.0.0", "HEAD", 0)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit since v1.0.0, got %d: %+v", len(commits), commits)
	}
	if !strings.Contains(commits[0].Subject, "add beta") {
		t.Errorf("unexpected commit subject: %q", commits[0].Subject)
	}

	files, err := repo.ChangedFiles(ctx, "v1.0.0", "HEAD")
	if err != nil {
		t.Fatalf("changed files: %v", err)
	}
	want := map[string]bool{"a.txt": false, "b.txt": false}
	for _, f := range files {
		if _, ok := want[f]; ok {
			want[f] = true
		}
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("expected %s in changed files, got %v", f, files)
		}
	}
}

func TestDiffAndShow(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	diff, err := repo.Diff(ctx, "v1.0.0", "HEAD", "", false)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "b.txt") || !strings.Contains(diff, "beta") {
		t.Errorf("diff missing expected content:\n%s", diff)
	}

	content, err := repo.Show(ctx, "HEAD", "a.txt")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(content, "alpha updated") {
		t.Errorf("show returned unexpected content: %q", content)
	}

	files, err := repo.ListFiles(ctx, "")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 tracked files, got %d: %v", len(files), files)
	}
}
