// Package git wraps the system `git` binary (via os/exec). It deliberately
// avoids a pure-Go git implementation so behaviour matches whatever git CI
// already has. All operations are read-only and run within a fixed repo root.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a handle to a git working tree rooted at Root.
type Repo struct {
	Root string
}

// Open verifies that dir is inside a git work tree and returns a Repo rooted at
// its top level.
func Open(ctx context.Context, dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir: %w", err)
	}
	out, err := run(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	return &Repo{Root: strings.TrimSpace(out)}, nil
}

// run executes git in dir and returns stdout, wrapping failures with stderr.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, r.Root, args...)
}

// LatestTag returns the most recent tag reachable from HEAD, or "" if none.
func (r *Repo) LatestTag(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "describe", "--tags", "--abbrev=0")
	if err != nil {
		// No tags is not an error for our purposes.
		if strings.Contains(err.Error(), "No names found") ||
			strings.Contains(err.Error(), "cannot describe") ||
			strings.Contains(err.Error(), "No tags can describe") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// FirstCommit returns the oldest commit hash reachable from HEAD.
func (r *Repo) FirstCommit(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", err
	}
	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) == 0 {
		return "", errors.New("repository has no commits")
	}
	// In the presence of multiple roots, take the last (oldest listed).
	return lines[len(lines)-1], nil
}

// Range describes the commit range release notes / diffs are computed over.
type Range struct {
	From string
	To   string
}

// String renders the range as git range notation.
func (rg Range) String() string {
	return rg.From + ".." + rg.To
}

// ResolveRange determines the effective range given optional from/to overrides.
// Defaults: from = latest tag (or first commit if no tags), to = HEAD.
func (r *Repo) ResolveRange(ctx context.Context, from, to string) (Range, error) {
	rng := Range{From: from, To: to}
	if rng.To == "" {
		rng.To = "HEAD"
	}
	if rng.From == "" {
		tag, err := r.LatestTag(ctx)
		if err != nil {
			return Range{}, err
		}
		if tag != "" {
			rng.From = tag
		} else {
			first, err := r.FirstCommit(ctx)
			if err != nil {
				return Range{}, err
			}
			rng.From = first
		}
	}
	return rng, nil
}

// Commit is a single log entry.
type Commit struct {
	Hash    string
	Subject string
}

// Log returns the commits in (from, to]. If from is empty the full history of
// to is returned. maxCount <= 0 means no limit.
func (r *Repo) Log(ctx context.Context, from, to string, maxCount int) ([]Commit, error) {
	if to == "" {
		to = "HEAD"
	}
	args := []string{"log", "--no-color", "--pretty=format:%H%x09%s"}
	if maxCount > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", maxCount))
	}
	args = append(args, rangeArg(from, to))
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		c := Commit{Hash: parts[0]}
		if len(parts) == 2 {
			c.Subject = parts[1]
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// LogOneline returns `git log --oneline` text for the range.
func (r *Repo) LogOneline(ctx context.Context, from, to string, maxCount int) (string, error) {
	if to == "" {
		to = "HEAD"
	}
	args := []string{"log", "--no-color", "--oneline"}
	if maxCount > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", maxCount))
	}
	args = append(args, rangeArg(from, to))
	return r.git(ctx, args...)
}

// ChangedFiles returns the files changed between from and to.
func (r *Repo) ChangedFiles(ctx context.Context, from, to string) ([]string, error) {
	if to == "" {
		to = "HEAD"
	}
	args := []string{"diff", "--name-only", diffRange(from, to)}
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

// Diff returns the unified diff between from and to, optionally limited to a
// path. nameOnly toggles `--name-only`.
func (r *Repo) Diff(ctx context.Context, from, to, path string, nameOnly bool) (string, error) {
	if to == "" {
		to = "HEAD"
	}
	args := []string{"diff", "--no-color"}
	if nameOnly {
		args = append(args, "--name-only")
	}
	args = append(args, diffRange(from, to))
	if path != "" {
		args = append(args, "--", path)
	}
	return r.git(ctx, args...)
}

// Show returns the contents of path at ref.
func (r *Repo) Show(ctx context.Context, ref, path string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	return r.git(ctx, "show", fmt.Sprintf("%s:%s", ref, path))
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
		// git grep exits 1 when there are no matches; treat as empty.
		if strings.TrimSpace(out) == "" {
			return "", nil
		}
		return out, nil
	}
	return out, nil
}

// rangeArg renders a log range. Empty from means "all history of to".
func rangeArg(from, to string) string {
	if from == "" {
		return to
	}
	return from + ".." + to
}

// diffRange renders a diff range. Empty from diffs against the empty tree by
// comparing to itself (so a single ref diffs working state); we instead use the
// from..to form which git accepts for diff as well.
func diffRange(from, to string) string {
	if from == "" {
		return to
	}
	return from + ".." + to
}
