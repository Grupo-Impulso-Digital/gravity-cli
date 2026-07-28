package git_test

import (
	"context"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func TestChangedFiles(t *testing.T) {
	repo, err := git.Open(context.Background(), testRepo(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()

	got, err := repo.ChangedFiles(ctx, "v1.0.0", "HEAD")
	if err != nil {
		t.Fatalf("changed files: %v", err)
	}
	want := map[string]bool{"a.txt": true, "b.txt": true}
	if len(got) != len(want) {
		t.Fatalf("changed = %v, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected changed path %q", p)
		}
	}

	empty, err := repo.ChangedFiles(ctx, "HEAD", "HEAD")
	if err != nil || len(empty) != 0 {
		t.Errorf("empty range = %v, %v", empty, err)
	}
}

func TestResolveRef(t *testing.T) {
	repo, err := git.Open(context.Background(), testRepo(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sha, err := repo.ResolveRef(context.Background(), "v1.0.0")
	if err != nil || len(sha) < 40 {
		t.Fatalf("resolve = %q, %v", sha, err)
	}
	if _, err := repo.ResolveRef(context.Background(), "no-such-ref"); err == nil {
		t.Error("an unknown ref must be an error")
	}
}
