package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// SyncState compares the checked-out branch with its upstream.
type SyncState struct {
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
}

// Diverged reports commits on both sides.
func (s SyncState) Diverged() bool { return s.Ahead > 0 && s.Behind > 0 }

// Upstream returns the upstream of the current branch, or "" when it has none.
func (r *Repo) Upstream(ctx context.Context) string {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// FetchUpstream fetches the remote of an upstream such as origin/main.
func (r *Repo) FetchUpstream(ctx context.Context, upstream string) error {
	remote, branch, ok := strings.Cut(upstream, "/")
	if !ok {
		return fmt.Errorf("upstream %q has no remote", upstream)
	}
	return r.FetchBranch(ctx, remote, branch)
}

// Sync counts the commits HEAD and its upstream do not share.
func (r *Repo) Sync(ctx context.Context, upstream string) (SyncState, error) {
	st := SyncState{Upstream: upstream}
	out, err := r.git(ctx, "rev-list", "--left-right", "--count", "HEAD..."+upstream)
	if err != nil {
		return st, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return st, fmt.Errorf("unexpected rev-list output %q", strings.TrimSpace(out))
	}
	st.Ahead, _ = strconv.Atoi(f[0])
	st.Behind, _ = strconv.Atoi(f[1])
	return st, nil
}

// DirtyTracked lists tracked files with uncommitted changes.
func (r *Repo) DirtyTracked(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 3 {
			files = append(files, strings.TrimSpace(line[3:]))
		}
	}
	return files, nil
}
