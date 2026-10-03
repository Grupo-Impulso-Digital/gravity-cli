package cisetup

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var legacyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gravity-cli/ci/github@(v0|main|master)\b`),
	regexp.MustCompile(`GRAVITY_(CLI_)?VERSION\s*[=:]\s*["']?(0|v0)(\.[0-9.]+)?["']?(\s|$)`),
	regexp.MustCompile(`GRAVITY_CLI_VERSION`),
	regexp.MustCompile(`\bgravity\s+(sync|release-notes|coverage|ping|doctor|docs\s+generate|check\s+(api|docs)|nucleus\s+sync)\b`),
}

var ciFileGlobs = []string{
	".github/workflows/*.yml", ".github/workflows/*.yaml", ".gitlab-ci.yml", ".gitlab/*.yml", ".gitlab/*.yaml",
	"bitbucket-pipelines.yml", "azure-pipelines*.yml", "azure-pipelines*.yaml", ".azure-pipelines/*.yml",
	"Jenkinsfile", ".circleci/config.yml",
}

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

// LegacyPipelines lists the CI files under root that still run gravity 0.x, whose routes refuse repository tokens.
func LegacyPipelines(root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range ciFileGlobs {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		for _, path := range matches {
			rel, err := filepath.Rel(root, path)
			if err != nil || seen[rel] {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			data, err := fs.ReadFile(os.DirFS(root), filepath.ToSlash(rel))
			if err != nil {
				continue
			}
			for _, re := range legacyPatterns {
				if re.Match(data) {
					seen[rel] = true
					out = append(out, filepath.ToSlash(rel))
					break
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
