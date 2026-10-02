package git_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func TestOpenOutsideARepoIsHumanized(t *testing.T) {
	_, err := git.Open(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error outside a git repo")
	}
	if !errors.Is(err, git.ErrNotRepository) {
		t.Errorf("errors.Is(ErrNotRepository) = false for %v", err)
	}
	if got := err.Error(); got != "not a git repository — run inside your repo" {
		t.Errorf("message = %q", got)
	}
}

func TestUnknownRefIsHumanized(t *testing.T) {
	repo, err := git.Open(context.Background(), testRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Log(context.Background(), "no-such-tag", "HEAD", 0)
	if err == nil {
		t.Fatal("expected an unknown-ref error")
	}
	if !errors.Is(err, git.ErrUnknownRef) {
		t.Errorf("errors.Is(ErrUnknownRef) = false for %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "fatal:") || strings.Contains(msg, "usage:") {
		t.Errorf("raw git stderr leaked: %q", msg)
	}
	if !strings.Contains(msg, "no-such-tag") || !strings.Contains(msg, "fetch-depth: 0") {
		t.Errorf("message should name the ref and the shallow-clone fix: %q", msg)
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		name string
		err  *git.Error
		want string
	}{
		{"no commits", &git.Error{Args: []string{"log"}, Stderr: "fatal: your current branch 'main' does not have any commits yet\n"}, "this repository has no commits yet — commit something first"},
		{"dubious", &git.Error{Args: []string{"rev-parse"}, Stderr: "fatal: detected dubious ownership in repository at '/src'\nTo add an exception..."}, "git refuses this checkout (dubious ownership)"},
		{"missing binary", &git.Error{Args: []string{"rev-parse"}, Err: exec.ErrNotFound}, "git is not installed or not on PATH"},
		{"other first line", &git.Error{Args: []string{"show"}, Stderr: "fatal: path 'x.go' exists on disk, but not in 'HEAD'\nmore"}, "git show: path 'x.go' exists on disk, but not in 'HEAD'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); !strings.HasPrefix(got, tc.want) {
				t.Errorf("Error() = %q, want prefix %q", got, tc.want)
			}
		})
	}
}
