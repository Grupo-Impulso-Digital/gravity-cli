package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether ancestor is an ancestor of (or equal to) descendant.
func (r *Repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var ge *Error
	var ee *exec.ExitError
	if errors.As(err, &ge) && errors.As(ge.Err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// CommitExists reports whether sha names a commit present in the local object store.
func (r *Repo) CommitExists(ctx context.Context, sha string) bool {
	_, err := r.git(ctx, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// IsShallow reports whether the clone is shallow.
func (r *Repo) IsShallow(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// Deepen fetches n more commits of history from origin, or the whole history when n is 0.
func (r *Repo) Deepen(ctx context.Context, n int) error {
	args := []string{"fetch", "--quiet", "--tags", "origin"}
	if n > 0 {
		args = append(args, "--deepen="+strconv.Itoa(n))
	} else {
		args = append(args, "--unshallow")
	}
	_, err := r.git(ctx, args...)
	return err
}

// CountCommits counts the commits in base..head; an empty base counts every commit reachable from head.
func (r *Repo) CountCommits(ctx context.Context, base, head string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--count", rangeArg(base, head))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// AncestorAt returns the commit n first-parent steps behind ref, or "" when history is shorter.
func (r *Repo) AncestorAt(ctx context.Context, ref string, n int) (string, error) {
	out, err := r.git(ctx, "rev-list", "--first-parent", "--skip="+strconv.Itoa(n), "--max-count=1", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// EmptyTree returns the object id of the empty tree in this repository's hash format.
func (r *Repo) EmptyTree(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DefaultBranch returns origin's default branch from refs/remotes/origin/HEAD, or "".
func (r *Repo) DefaultBranch(ctx context.Context) string {
	out, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
}

// RemoteBranchesContaining lists remote branches (without the remote prefix) that contain sha.
func (r *Repo) RemoteBranchesContaining(ctx context.Context, sha string) ([]string, error) {
	out, err := r.git(ctx, "branch", "-r", "--contains", sha, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, "/HEAD") {
			continue
		}
		if _, name, ok := strings.Cut(line, "/"); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

// TagInfo is one tag and the commit it points at.
type TagInfo struct {
	Name        string
	Commit      string
	CreatorDate string
}

// Tags lists tags matching a glob pattern (all tags when empty).
func (r *Repo) Tags(ctx context.Context, pattern string) ([]TagInfo, error) {
	ref := "refs/tags/"
	if pattern != "" {
		ref += pattern
	}
	out, err := r.git(ctx, "for-each-ref", "--format=%(refname:short)%1f%(objectname)%1f%(*objectname)%1f%(creatordate:iso-strict)", ref)
	if err != nil {
		return nil, err
	}
	var tags []TagInfo
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 4 {
			continue
		}
		commit := f[2]
		if commit == "" {
			commit = f[1]
		}
		tags = append(tags, TagInfo{Name: f[0], Commit: commit, CreatorDate: f[3]})
	}
	return tags, nil
}

// CommitInfo is one commit of a range with its metadata.
type CommitInfo struct {
	SHA     string
	Author  string
	Date    string
	Subject string
	Body    string
	Files   int
}

// Commits returns the commits in base..head, newest first, at most limit (0 = no limit).
func (r *Repo) Commits(ctx context.Context, base, head string, limit int) ([]CommitInfo, error) {
	args := []string{"log", "--no-color", "--format=%x1e%H%x1f%an <%ae>%x1f%aI%x1f%s%x1f%b%x1f", "--numstat"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, rangeArg(base, head), "--")
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []CommitInfo
	for _, rec := range strings.Split(out, "\x1e") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 6 {
			continue
		}
		c := CommitInfo{SHA: f[0], Author: f[1], Date: f[2], Subject: f[3], Body: strings.TrimSpace(f[4])}
		for _, line := range strings.Split(f[5], "\n") {
			if strings.Count(line, "\t") >= 2 {
				c.Files++
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// FileChange is one changed path of a diff.
type FileChange struct {
	Path      string
	Status    string
	OldPath   string
	Additions int
	Deletions int
	Binary    bool
}

func diffArgs(base, head string, extra ...string) []string {
	args := append([]string{"diff", "--no-color", "--no-ext-diff", "-M"}, extra...)
	if head == "" {
		return append(args, base, "--")
	}
	return append(args, base, head, "--")
}

// ChangedFilesDetailed returns name-status and numstat for base..head; an empty head diffs against the working tree.
func (r *Repo) ChangedFilesDetailed(ctx context.Context, base, head string) ([]FileChange, error) {
	out, err := r.git(ctx, diffArgs(base, head, "--name-status", "-z")...)
	if err != nil {
		return nil, err
	}
	var files []FileChange
	index := map[string]int{}
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		status := parts[i]
		if status == "" {
			continue
		}
		fc := FileChange{Status: status[:1]}
		switch fc.Status {
		case "R", "C":
			if i+2 >= len(parts) {
				return nil, fmt.Errorf("parse git diff --name-status: truncated rename record")
			}
			fc.OldPath, fc.Path = parts[i+1], parts[i+2]
			i += 2
		default:
			if i+1 >= len(parts) {
				return nil, fmt.Errorf("parse git diff --name-status: truncated record")
			}
			fc.Path = parts[i+1]
			i++
		}
		index[fc.Path] = len(files)
		files = append(files, fc)
	}
	num, err := r.git(ctx, diffArgs(base, head, "--numstat", "-z")...)
	if err != nil {
		return nil, err
	}
	nparts := strings.Split(num, "\x00")
	for i := 0; i < len(nparts); i++ {
		rec := nparts[i]
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\t", 3)
		if len(f) < 3 {
			continue
		}
		path := f[2]
		if path == "" && i+2 < len(nparts) {
			path = nparts[i+2]
			i += 2
		}
		pos, ok := index[path]
		if !ok {
			continue
		}
		if f[0] == "-" || f[1] == "-" {
			files[pos].Binary = true
			continue
		}
		files[pos].Additions, _ = strconv.Atoi(f[0])
		files[pos].Deletions, _ = strconv.Atoi(f[1])
	}
	return files, nil
}

// DiffZeroContext returns the zero-context unified diff of base..head (working tree when head is empty).
func (r *Repo) DiffZeroContext(ctx context.Context, base, head string, paths ...string) (string, error) {
	args := diffArgs(base, head, "-U0")
	return r.git(ctx, append(args, paths...)...)
}

// FileAt returns the content of path at ref, or ok=false when the path does not exist there.
func (r *Repo) FileAt(ctx context.Context, ref, path string) ([]byte, bool, error) {
	out, err := r.git(ctx, "show", ref+":"+path)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && (strings.Contains(ge.Stderr, "does not exist") || strings.Contains(ge.Stderr, "exists on disk, but not in")) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return []byte(out), true, nil
}

// FilesAt lists the tracked files at ref.
func (r *Repo) FilesAt(ctx context.Context, ref string) ([]string, error) {
	out, err := r.git(ctx, "ls-tree", "-r", "--name-only", "-z", ref)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// FetchBranch fetches branch from remote into refs/remotes/<remote>/<branch>.
func (r *Repo) FetchBranch(ctx context.Context, remote, branch string) error {
	_, err := r.git(ctx, "fetch", "--quiet", remote, "+refs/heads/"+branch+":refs/remotes/"+remote+"/"+branch)
	return err
}
