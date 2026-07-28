package git

import (
	"context"
	"strings"
)

// ChangedFiles returns the repo-relative paths touched between from and to.
func (r *Repo) ChangedFiles(ctx context.Context, from, to string) ([]string, error) {
	if to == "" {
		to = "HEAD"
	}
	out, err := r.git(ctx, "diff", "--no-color", "--name-only", rangeArg(from, to))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// ResolveRef resolves a ref to its commit sha.
func (r *Repo) ResolveRef(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
