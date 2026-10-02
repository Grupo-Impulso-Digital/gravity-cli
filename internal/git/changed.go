package git

import (
	"context"
	"strings"
)

// ResolveRef resolves a ref to its commit sha.
func (r *Repo) ResolveRef(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
