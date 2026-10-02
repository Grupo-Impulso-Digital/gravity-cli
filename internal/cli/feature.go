package cli

import (
	"context"
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

func (e *env) features(ctx context.Context) (map[string]bool, error) {
	if e.featureSet != nil {
		return e.featureSet, nil
	}
	who, err := e.client.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	e.featureSet = who.Features
	if e.featureSet == nil {
		e.featureSet = map[string]bool{}
	}
	return e.featureSet, nil
}

func (e *env) gateFeature(ctx context.Context, feature, label string, w io.Writer, require bool) (skip bool, err error) {
	feats, err := e.features(ctx)
	if err != nil {
		return false, Fail(CodeError, fmt.Errorf("whoami: %w", classifyAuthErr(err)))
	}
	if feats[feature] {
		return false, nil
	}
	if require {
		return false, Failf(CodeError, "%s is not yet available on this platform", label)
	}
	fmt.Fprintf(w, "note: %s is not yet available on this platform; skipping\n", label)
	return true, nil
}

func skippableFeature(err error, feature string, w io.Writer, require bool) (skipped bool, out error) {
	if err == nil {
		return false, nil
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsUnavailable() {
		if require {
			return false, Failf(CodeError, "%s is not available on this platform (%s)", feature, ae.Message)
		}
		fmt.Fprintf(w, "note: %s is not available on this platform (%s); skipping\n", feature, ae.Message)
		return true, nil
	}
	return false, err
}
