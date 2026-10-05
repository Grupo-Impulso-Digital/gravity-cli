package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@b.c", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@b.c", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncAndDirtyTracked(t *testing.T) {
	ctx := context.Background()
	origin := t.TempDir()
	gitIn(t, origin, "init", "-q", "-b", "main")
	gitIn(t, origin, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "-A")
	gitIn(t, origin, "commit", "-q", "-m", "a")
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	gitIn(t, clone, "config", "commit.gpgsign", "false")
	r := &Repo{Root: clone}
	up := r.Upstream(ctx)
	if up != "origin/main" {
		t.Fatalf("upstream = %q", up)
	}
	if err := os.WriteFile(filepath.Join(origin, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "-A")
	gitIn(t, origin, "commit", "-q", "-m", "b")
	if err := r.FetchUpstream(ctx, up); err != nil {
		t.Fatal(err)
	}
	st, err := r.Sync(ctx, up)
	if err != nil || st.Behind != 1 || st.Ahead != 0 || st.Diverged() {
		t.Fatalf("sync = %+v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(clone, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := r.DirtyTracked(ctx)
	if err != nil || len(dirty) != 1 || dirty[0] != "a.txt" {
		t.Fatalf("dirty = %v %v", dirty, err)
	}
	if (&Repo{Root: origin}).Upstream(ctx) != "" {
		t.Fatal("a branch without upstream reported one")
	}
}
