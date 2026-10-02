// Package git wraps the system `git` binary.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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

// MarkerTag is the CI bookkeeping tag that records the last docs-synced commit.
const MarkerTag = "docs-synced"

// LatestTag returns the most recent release tag reachable from HEAD, or "" if none.
func (r *Repo) LatestTag(ctx context.Context) (string, error) {
	return r.describeRelease(ctx, "HEAD")
}

// TagBefore returns the most recent release tag reachable from ref's first parent, so a
// release checkout sitting on its own tag ranges from the previous one.
func (r *Repo) TagBefore(ctx context.Context, ref string) (string, error) {
	return r.describeRelease(ctx, ref+"^")
}

func (r *Repo) describeRelease(ctx context.Context, rev string) (string, error) {
	for _, filter := range [][]string{
		{"--match", "v[0-9]*"},
		{"--exclude", MarkerTag, "--exclude", MarkerTag + "-*"},
	} {
		args := append([]string{"describe", "--tags", "--abbrev=0"}, filter...)
		out, err := r.git(ctx, append(args, rev)...)
		if err == nil {
			return strings.TrimSpace(out), nil
		}
		if !stderrHas(err, "No names found", "cannot describe", "No tags can describe",
			"Not a valid object name", "unknown revision") {
			return "", err
		}
	}
	return "", nil
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

// Range describes the commit range release notes / diffs are computed over.
type Range struct {
	From string
	To   string
}

// String renders the range as git range notation; an empty From is the root of history.
func (rg Range) String() string {
	if rg.From == "" {
		return rg.To
	}
	return rg.From + ".." + rg.To
}

// Since names the range start for people and prompts.
func (rg Range) Since() string {
	if rg.From == "" {
		return "the start of history"
	}
	return rg.From
}

// ToolArgs renders the range as git_log/git_diff tool arguments.
func (rg Range) ToolArgs() string {
	if rg.From == "" {
		return fmt.Sprintf("to=%q", rg.To)
	}
	return fmt.Sprintf("from=%q, to=%q", rg.From, rg.To)
}

// ResolveRange determines the effective range given optional from/to overrides; with no earlier tag From stays empty, the root of history.
func (r *Repo) ResolveRange(ctx context.Context, from, to string) (Range, error) {
	rng := Range{From: from, To: to}
	if rng.To == "" {
		rng.To = "HEAD"
	}
	if rng.From == "" {
		tag, err := r.TagBefore(ctx, rng.To)
		if err != nil {
			return Range{}, err
		}
		rng.From = tag
	}
	return rng, nil
}

// Commit is a single log entry.
type Commit struct {
	Hash    string
	Subject string
}

// Log returns the commits in (from, to].
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

// Diff returns the unified diff between from and to; an empty from diffs from the empty tree.
func (r *Repo) Diff(ctx context.Context, from, to, path string) (string, error) {
	if to == "" {
		to = "HEAD"
	}
	if from == "" {
		empty, err := r.git(ctx, "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return "", err
		}
		from = strings.TrimSpace(empty)
	}
	args := []string{"diff", "--no-color", rangeArg(from, to)}
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
