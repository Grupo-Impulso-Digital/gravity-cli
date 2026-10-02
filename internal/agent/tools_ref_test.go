package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func twoVersionRepo(t *testing.T) *git.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("config", "commit.gpgsign", "false")
	write("release one\n")
	run("add", "-A")
	run("commit", "-q", "-m", "one")
	run("tag", "v1")
	write("release two\n")
	run("add", "-A")
	run("commit", "-q", "-m", "two")
	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func readFileTool(t *testing.T, tools []Tool) Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Def.Name == "read_file" {
			return tool
		}
	}
	t.Fatal("no read_file tool")
	return Tool{}
}

func TestReadFileReadsAtTheRangeEnd(t *testing.T) {
	repo := twoVersionRepo(t)
	input := json.RawMessage(`{"path":"api.go"}`)

	got, err := readFileTool(t, GitToolsAt(repo, "v1")).Run(context.Background(), input)
	if err != nil || got != "release one\n" {
		t.Errorf("read_file at v1 = %q, %v; want the v1 content", got, err)
	}
	got, err = readFileTool(t, GitTools(repo)).Run(context.Background(), input)
	if err != nil || got != "release two\n" {
		t.Errorf("read_file at HEAD = %q, %v; want the HEAD content", got, err)
	}
	if desc := readFileTool(t, GitToolsAt(repo, "v1")).Def.Description; !strings.Contains(desc, "at v1") {
		t.Errorf("the tool must tell the model which ref it reads: %q", desc)
	}
	got, err = readFileTool(t, GitTools(repo)).Run(context.Background(), json.RawMessage(`{"path":"api.go","ref":"v1"}`))
	if err != nil || got != "release one\n" {
		t.Errorf("read_file with ref = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(repo.Root, "api.go"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := RepoTools(repo, Range{Base: "HEAD", WorkingTree: true})
	got, err = readFileTool(t, wt).Run(context.Background(), input)
	if err != nil || got != "uncommitted\n" {
		t.Errorf("read_file on the working tree = %q, %v", got, err)
	}
	for _, tl := range wt {
		if tl.Def.Name == "git_diff" {
			diff, err := tl.Run(context.Background(), json.RawMessage(`{}`))
			if err != nil || !strings.Contains(diff, "+uncommitted") {
				t.Errorf("git_diff on the working tree = %q, %v", diff, err)
			}
		}
	}
}
