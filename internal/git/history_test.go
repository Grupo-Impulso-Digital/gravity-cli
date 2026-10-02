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

func TestHistoryHelpers(t *testing.T) {
	dir := t.TempDir()
	run := gitRunner(t, dir)
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\n")
	write("old.txt", "rename me please, this content is long enough to be detected\n")
	run("add", "-A")
	run("commit", "-q", "-m", "chore: init")
	run("tag", "v1.0.0")
	write("a.txt", "one\ntwo\n")
	run("mv", "old.txt", "new.txt")
	write("img.bin", "\x00\x01\x02")
	run("add", "-A")
	run("commit", "-q", "-m", "feat: rename (#42)", "-m", "Body line")
	run("tag", "-a", "v1.1.0", "-m", "release")

	ctx := context.Background()
	repo, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := repo.ResolveRef(ctx, "HEAD")
	first, _ := repo.ResolveRef(ctx, "v1.0.0")

	mb, err := repo.MergeBase(ctx, first, head)
	if err != nil || mb != first {
		t.Fatalf("MergeBase = %q %v", mb, err)
	}
	if ok, err := repo.IsAncestor(ctx, first, head); !ok || err != nil {
		t.Fatalf("IsAncestor(first, head) = %v %v", ok, err)
	}
	if ok, err := repo.IsAncestor(ctx, head, first); ok || err != nil {
		t.Fatalf("IsAncestor(head, first) = %v %v", ok, err)
	}
	if !repo.CommitExists(ctx, head) || repo.CommitExists(ctx, strings.Repeat("0", 40)) {
		t.Fatal("CommitExists")
	}
	if shallow, err := repo.IsShallow(ctx); shallow || err != nil {
		t.Fatalf("IsShallow = %v %v", shallow, err)
	}
	if n, err := repo.CountCommits(ctx, first, head); n != 1 || err != nil {
		t.Fatalf("CountCommits = %d %v", n, err)
	}
	if n, err := repo.CountCommits(ctx, "", head); n != 2 || err != nil {
		t.Fatalf("CountCommits all = %d %v", n, err)
	}
	if a, err := repo.AncestorAt(ctx, head, 1); a != first || err != nil {
		t.Fatalf("AncestorAt 1 = %q %v", a, err)
	}
	if a, err := repo.AncestorAt(ctx, head, 5); a != "" || err != nil {
		t.Fatalf("AncestorAt 5 = %q %v", a, err)
	}
	if tree, err := repo.EmptyTree(ctx); err != nil || len(tree) < 40 {
		t.Fatalf("EmptyTree = %q %v", tree, err)
	}

	tags, err := repo.Tags(ctx, "v*")
	if err != nil || len(tags) != 2 {
		t.Fatalf("Tags = %+v %v", tags, err)
	}
	for _, tg := range tags {
		if tg.Name == "v1.1.0" && tg.Commit != head {
			t.Fatalf("annotated tag must resolve to its commit: %+v", tg)
		}
	}

	commits, err := repo.Commits(ctx, first, head, 0)
	if err != nil || len(commits) != 1 {
		t.Fatalf("Commits = %+v %v", commits, err)
	}
	c := commits[0]
	if c.SHA != head || c.Subject != "feat: rename (#42)" || c.Body != "Body line" || c.Files != 3 || !strings.Contains(c.Author, "<") {
		t.Fatalf("commit = %+v", c)
	}

	files, err := repo.ChangedFilesDetailed(ctx, first, head)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]git.FileChange{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if f := byPath["a.txt"]; f.Status != "M" || f.Additions != 1 || f.Deletions != 0 {
		t.Fatalf("a.txt = %+v", f)
	}
	if f := byPath["new.txt"]; f.Status != "R" || f.OldPath != "old.txt" {
		t.Fatalf("new.txt = %+v", f)
	}
	if f := byPath["img.bin"]; f.Status != "A" || !f.Binary {
		t.Fatalf("img.bin = %+v", f)
	}

	write("a.txt", "one\ntwo\nthree\n")
	wt, err := repo.ChangedFilesDetailed(ctx, head, "")
	if err != nil || len(wt) != 1 || wt[0].Path != "a.txt" || wt[0].Additions != 1 {
		t.Fatalf("working tree = %+v %v", wt, err)
	}
	write("docs/new-guide.md", "# Guide\n\nbody")
	write(".gitignore", "*.log\n")
	write("debug.log", "noise\n")
	wt, err = repo.ChangedFilesDetailed(ctx, head, "")
	if err != nil {
		t.Fatal(err)
	}
	untracked := map[string]git.FileChange{}
	for _, f := range wt {
		untracked[f.Path] = f
	}
	if f, ok := untracked["docs/new-guide.md"]; !ok || f.Status != "A" || f.Additions != 3 {
		t.Fatalf("untracked doc = %+v in %+v", f, wt)
	}
	if _, ok := untracked["debug.log"]; ok {
		t.Fatalf("ignored files stay out: %+v", wt)
	}
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", filepath.Join(dir, "vendor", "nested"), "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v %s", err, out)
	}
	write("vendor/nested/file.txt", "x\n")
	if err := os.Symlink(filepath.Join(dir, "docs"), filepath.Join(dir, "docs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero")); err != nil {
		t.Fatal(err)
	}
	wt, err = repo.ChangedFilesDetailed(ctx, head, "")
	if err != nil {
		t.Fatalf("nested repos and symlinks must not break the working tree change set: %v", err)
	}
	untracked = map[string]git.FileChange{}
	for _, f := range wt {
		untracked[f.Path] = f
	}
	if _, ok := untracked["vendor/nested/"]; ok {
		t.Fatalf("nested repositories stay out: %+v", wt)
	}
	if f, ok := untracked["docs-link"]; !ok || f.Additions != 1 {
		t.Fatalf("symlink = %+v in %+v", f, wt)
	}
	if f, ok := untracked["zero"]; !ok || f.Additions != 1 || f.Binary {
		t.Fatalf("symlink to a device = %+v", f)
	}
	for _, p := range []string{"docs-link", "zero"} {
		if err := os.Remove(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, "vendor")); err != nil {
		t.Fatal(err)
	}
	diff, err := repo.DiffZeroContext(ctx, head, "", "a.txt")
	if err != nil || !strings.Contains(diff, "+three") {
		t.Fatalf("diff = %q %v", diff, err)
	}

	data, ok, err := repo.FileAt(ctx, first, "a.txt")
	if err != nil || !ok || string(data) != "one\n" {
		t.Fatalf("FileAt = %q %v %v", data, ok, err)
	}
	if _, ok, err := repo.FileAt(ctx, first, "new.txt"); ok || err != nil {
		t.Fatalf("FileAt missing = %v %v", ok, err)
	}
	list, err := repo.FilesAt(ctx, first)
	if err != nil || len(list) != 2 {
		t.Fatalf("FilesAt = %v %v", list, err)
	}
	if repo.DefaultBranch(ctx) != "" {
		t.Fatal("no origin, no default branch")
	}
}
