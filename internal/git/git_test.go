package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	env := append(
		os.Environ(),
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

func TestCurrentBranchAndRemoteURL(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	remote, err := repo.RemoteURL(ctx)
	if err != nil {
		t.Fatalf("remote url (none): %v", err)
	}
	if remote != "" {
		t.Errorf("expected no remote, got %q", remote)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), e, out)
		}
	}
	run("branch", "-M", "main")
	run("remote", "add", "upstream", "https://example.com/upstream.git")
	run("remote", "add", "origin", "git@github.com:acme/api.git")

	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected branch main, got %q", branch)
	}

	remote, err = repo.RemoteURL(ctx)
	if err != nil {
		t.Fatalf("remote url: %v", err)
	}
	if remote != "git@github.com:acme/api.git" {
		t.Errorf("expected origin remote, got %q", remote)
	}
}

func TestDiffAndShow(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	diff, err := repo.Diff(ctx, "v1.0.0", "HEAD", "")
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

func TestDiffFromTheRootOfHistory(t *testing.T) {
	dir := t.TempDir()
	run := gitRunner(t, dir)
	run("init", "-q")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "add a.txt")
	run("tag", "v0.1.0")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := repo.Diff(ctx, "", "HEAD", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+++ b/a.txt") || !strings.Contains(diff, "+a.txt") || strings.Contains(diff, "dirty") {
		t.Fatalf("the diff of a root release is the whole committed tree:\n%s", diff)
	}
	tagged, err := repo.Diff(ctx, "", "v0.1.0", "a.txt")
	if err != nil || !strings.Contains(tagged, "+a.txt") {
		t.Fatalf("diff from the root to a tag = %q, %v", tagged, err)
	}
	log, err := repo.LogOneline(ctx, "", "HEAD", 0)
	if err != nil || !strings.Contains(log, "add a.txt") {
		t.Fatalf("the root commit belongs to a root range: %q, %v", log, err)
	}
}

func gitRunner(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	env := append(
		os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func TestRefsCannotBecomeOptions(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "pwned")
	evil := "--output=" + target
	calls := map[string]func() error{
		"Diff":            func() error { _, err := repo.Diff(ctx, "", evil, ""); return err },
		"DiffWorkingTree": func() error { _, err := repo.DiffWorkingTree(ctx, evil, ""); return err },
		"LogOneline":      func() error { _, err := repo.LogOneline(ctx, evil, "HEAD", 1); return err },
		"Show":            func() error { _, err := repo.Show(ctx, evil, "a.txt"); return err },
		"FileAt":          func() error { _, _, err := repo.FileAt(ctx, evil, "a.txt"); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s accepted %q", name, evil)
		}
	}
	matches, _ := filepath.Glob(target + "*")
	if len(matches) > 0 {
		t.Fatalf("git wrote %v", matches)
	}
	for _, ref := range []string{"v1.0.0", "HEAD~1", "main@{0}", "a1b2c3"} {
		if err := git.ValidateRef(ref); err != nil {
			t.Errorf("ValidateRef(%q) = %v", ref, err)
		}
	}
	for _, ref := range []string{"", " ", "-x", "a b", "a\x00b", "a\nb"} {
		if err := git.ValidateRef(ref); err == nil {
			t.Errorf("ValidateRef(%q) accepted", ref)
		}
	}
	if diff, err := repo.Diff(ctx, "v1.0.0", "HEAD", "b.txt"); err != nil || !strings.Contains(diff, "beta") {
		t.Fatalf("a path-scoped diff still works: %q, %v", diff, err)
	}
}
