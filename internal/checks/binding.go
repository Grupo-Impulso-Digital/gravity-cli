package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// BindingCheck is the outcome of verifying one block's source binding.
type BindingCheck struct {
	// Verified is true when the binding had a hash + a repo-resident ref and a
	// sha256 was computed.
	Verified bool
	// Stale is true when the recomputed hash differs from the recorded one.
	Stale bool
	// Skipped is true when the binding could not be verified.
	Skipped bool
	// SkipReason explains why the block was skipped.
	SkipReason string
	// Ref is the source path that was (or would be) checked.
	Ref string
	// Want / Got are the recorded and recomputed hashes.
	Want string
	Got  string
}

// hashFile computes the sha256 of the file at path.
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

// normalizeRecorded strips an optional "sha256:" prefix from a recorded hash.
func normalizeRecorded(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[i+1:]
	}
	return strings.ToLower(h)
}

// VerifyBinding checks a single source binding against a file in repoRoot.
//
// A block is verifiable only when the binding has a non-empty Hash and a Ref
// that resolves to an existing file inside the repo. Otherwise it is skipped
// (the caller is responsible for logging the skip count).
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

// resolveRepoPath validates ref is a repo-relative path inside repoRoot and
// returns the absolute path.
func resolveRepoPath(repoRoot, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		return "", errors.New("ref is an absolute path; not a repo file")
	}
	clean := filepath.Clean(ref)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("ref escapes repository root")
	}
	abs := filepath.Join(repoRoot, clean)
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("ref escapes repository root")
	}
	return abs, nil
}
