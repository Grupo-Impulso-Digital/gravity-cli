package cisetup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var sharedWorkflow = regexp.MustCompile(`Grupo-Impulso-Digital/workflows/\.github/workflows/gravity-docs\.yml@`)

// SharedWorkflowCaller returns the GitHub workflow that calls the organization's shared gravity-docs.yml, or "".
func SharedWorkflowCaller(root string) string {
	for _, g := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		sort.Strings(matches)
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil || !sharedWorkflow.Match(data) {
				continue
			}
			if rel, err := filepath.Rel(root, path); err == nil {
				return filepath.ToSlash(rel)
			}
		}
	}
	return ""
}
