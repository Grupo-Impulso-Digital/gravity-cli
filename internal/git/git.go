// Package git wraps the system `git` binary.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Repo is a handle to a git working tree rooted at Root.
type Repo struct {
	Root string
}

// Open verifies that dir is inside a git work tree.
func Open(ctx context.Context, dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir: %w", err)
	}
	out, err := run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	return &Repo{Root: strings.TrimSpace(out)}, nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, r.Root, args...)
}

// Dirty reports whether the working tree has uncommitted or untracked changes.
func (r *Repo) Dirty(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// CurrentBranch returns the checked-out branch name.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoteURL returns the fetch URL of a remote, preferring "origin".
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	list, err := r.git(ctx, "remote")
	if err != nil {
		//nolint:nilerr // a remote lookup failure degrades to "no remote", never fatal
		return "", nil
	}
	remotes := strings.Fields(list)
	if len(remotes) == 0 {
		return "", nil
	}
	name := remotes[0]
	for _, rem := range remotes {
		if rem == "origin" {
			name = "origin"
			break
		}
	}
	out, err := r.git(ctx, "remote", "get-url", name)
	if err != nil {
		//nolint:nilerr // best-effort: an unreadable remote URL is reported as absent
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// ValidateRef refuses a revision that git could parse as an option or that carries whitespace or control characters.
func ValidateRef(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("invalid ref %q: empty", ref)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid ref %q: must not start with '-'", ref)
	}
	for _, c := range ref {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return fmt.Errorf("invalid ref %q: whitespace or control character", ref)
		}
	}
	return nil
}

func validateRefs(refs ...string) error {
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if err := ValidateRef(ref); err != nil {
			return err
		}
	}
	return nil
}

// LogOneline returns `git log --oneline` text for the range.
func (r *Repo) LogOneline(ctx context.Context, from, to string, maxCount int) (string, error) {
	if to == "" {
		to = "HEAD"
	}
	if err := validateRefs(from, to); err != nil {
		return "", err
	}
	args := []string{"log", "--no-color", "--oneline"}
	if maxCount > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", maxCount))
	}
	args = append(args, "--end-of-options", rangeArg(from, to), "--")
	return r.git(ctx, args...)
}

// Diff returns the unified diff between from and to; an empty from diffs from the empty tree.
func (r *Repo) Diff(ctx context.Context, from, to, path string) (string, error) {
	if to == "" {
		to = "HEAD"
	}
	if err := validateRefs(from, to); err != nil {
		return "", err
	}
	if from == "" {
		tree, err := r.EmptyTree(ctx)
		if err != nil {
			return "", err
		}
		from = tree
	}
	args := []string{"diff", "--no-color", "--end-of-options", rangeArg(from, to), "--"}
	if path != "" {
		args = append(args, path)
	}
	return r.git(ctx, args...)
}

// Show returns the contents of path at ref.
func (r *Repo) Show(ctx context.Context, ref, path string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	return r.git(ctx, "show", "--end-of-options", fmt.Sprintf("%s:%s", ref, path))
}

// ListFiles returns tracked files, optionally filtered by a pathspec/glob.
func (r *Repo) ListFiles(ctx context.Context, glob string) ([]string, error) {
	args := []string{"ls-files"}
	if glob != "" {
		args = append(args, "--", glob)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Grep runs `git grep` for pattern, optionally scoped to a glob.
func (r *Repo) Grep(ctx context.Context, pattern, glob string) (string, error) {
	args := []string{"grep", "-n", "--no-color", "-e", pattern}
	if glob != "" {
		args = append(args, "--", glob)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		if strings.TrimSpace(out) == "" {
			return "", nil
		}
		return out, nil
	}
	return out, nil
}

func rangeArg(from, to string) string {
	if from == "" {
		return to
	}
	return from + ".." + to
}

// DiffWorkingTree returns the unified diff between from and the working tree.
func (r *Repo) DiffWorkingTree(ctx context.Context, from, path string) (string, error) {
	if from == "" {
		from = "HEAD"
	}
	if err := ValidateRef(from); err != nil {
		return "", err
	}
	args := []string{"diff", "--no-color", "--end-of-options", from, "--"}
	if path != "" {
		args = append(args, path)
	}
	return r.git(ctx, args...)
}
