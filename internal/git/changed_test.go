package git_test

import (
	"context"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

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
