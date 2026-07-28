package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// BindingCheck is the outcome of verifying one block's source binding.
type BindingCheck struct {
	Verified   bool
	Stale      bool
	Skipped    bool
	SkipReason string
	Ref        string
	Want       string
	Got        string
}

// HashRepoFile computes the lowercase hex sha256 of a repo-relative file inside repoRoot.
func HashRepoFile(repoRoot, ref string) (string, error) {
	clean, err := resolveRepoPath(repoRoot, ref)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("ref is a directory")
	}
	return hashFile(clean)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func normalizeRecorded(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[i+1:]
	}
	return strings.ToLower(h)
}

// VerifyBinding checks a single source binding against a file in repoRoot.
func VerifyBinding(repoRoot string, b *api.SourceBinding) BindingCheck {
	if b == nil {
		return BindingCheck{Skipped: true, SkipReason: "no source binding"}
	}
	if strings.TrimSpace(b.Hash) == "" {
		return BindingCheck{Skipped: true, SkipReason: "binding has no hash"}
	}
	if strings.TrimSpace(b.Ref) == "" {
		return BindingCheck{Skipped: true, SkipReason: "binding has no ref"}
	}

	clean, err := resolveRepoPath(repoRoot, b.Ref)
	if err != nil {
		return BindingCheck{Skipped: true, SkipReason: err.Error(), Ref: b.Ref}
	}

	info, err := os.Stat(clean)
	if err != nil || info.IsDir() {
		return BindingCheck{Skipped: true, SkipReason: "ref does not exist in repo", Ref: b.Ref}
	}

	got, err := hashFile(clean)
	if err != nil {
		return BindingCheck{Skipped: true, SkipReason: fmt.Sprintf("hash %s: %v", b.Ref, err), Ref: b.Ref}
	}

	want := normalizeRecorded(b.Hash)
	check := BindingCheck{Verified: true, Ref: b.Ref, Want: want, Got: got}
	check.Stale = !strings.EqualFold(want, got)
	return check
}

func resolveRepoPath(repoRoot, ref string) (string, error) {
	abs, err := pathsafe.Resolve(repoRoot, ref)
	switch {
	case errors.Is(err, pathsafe.ErrAbsolute):
		return "", errors.New("ref is an absolute path; not a repo file")
	case errors.Is(err, pathsafe.ErrEscape):
		return "", errors.New("ref escapes repository root")
	case err != nil:
		return "", err
	}
	return abs, nil
}
