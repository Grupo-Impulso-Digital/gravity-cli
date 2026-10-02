package changeset

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

type scripted struct {
	t   *testing.T
	dir string
}

func newScripted(t *testing.T) *scripted {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &scripted{t: t, dir: dir}
	s.git("init", "-q", "-b", "main")
	s.git("config", "commit.gpgsign", "false")
	s.git("config", "tag.gpgsign", "false")
	return s
}

func (s *scripted) git(args ...string) string {
	s.t.Helper()
	return runGit(s.t, s.dir, args...)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=ana@acme.io",
		"GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=ana@acme.io",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (s *scripted) write(name, body string) {
	s.t.Helper()
	p := filepath.Join(s.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *scripted) remove(name string) {
	s.t.Helper()
	s.git("rm", "-q", name)
}

func (s *scripted) commit(msg string, files map[string]string) string {
	s.t.Helper()
	for name, body := range files {
		s.write(name, body)
	}
	s.git("add", "-A")
	s.git("commit", "-q", "--allow-empty", "-m", msg)
	return s.git("rev-parse", "HEAD")
}

func (s *scripted) repo() *git.Repo {
	s.t.Helper()
	r, err := git.Open(context.Background(), s.dir)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *scripted) linear(n int) []string {
	s.t.Helper()
	shas := make([]string, 0, n)
	for i := range n {
		shas = append(shas, s.commit("commit "+itoa(i), map[string]string{"f" + itoa(i) + ".txt": itoa(i)}))
	}
	return shas
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return string(digits[i])
	}
	return itoa(i/10) + string(digits[i%10])
}
