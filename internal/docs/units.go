package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
)

// UnitSourceHash digests an inventory unit's bound sources so the platform can tell when the docs describe an older version of the code.
func UnitSourceHash(repoRoot string, refs []string) string {
	lines := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		h, err := checks.HashRepoFile(repoRoot, ref)
		if err != nil {
			continue
		}
		lines = append(lines, ref+"\n"+h+"\n")
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
