package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const (
	featureDocsGenerate   = "docs-generate"
	featureBlockAudience  = "block-audience"
	featureSpaceHierarchy = "space-hierarchy"

	featureSpaceMetadata = "space-metadata"

	featureRepos         = "repos"
	featureInventory     = "inventory"
	featureCoverage      = "coverage"
	featureDocAgentRuns  = "doc-agent-runs"
	featurePageLanguages = "page-languages"
)

func skippableFeature(err error, feature string, w io.Writer, require bool) (skipped bool, out error) {
	if err == nil {
		return false, nil
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsUnavailable() {
		if require {
			return false, Failf(CodeError, "%s is not yet available on this platform", feature)
		}
		fmt.Fprintf(w, "note: %s is not yet available on this platform; skipping\n", feature)
		return true, nil
	}
	return false, err
}
