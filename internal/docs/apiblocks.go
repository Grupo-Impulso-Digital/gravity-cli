package docs

import (
	"fmt"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/checks"
)

// apiContent is the JSON payload of a machine api block. It mirrors the read
// side (api.APIBlockContent) so authored blocks round-trip through `check api`.
type apiContent struct {
	Method    string                  `json:"method"`
	Path      string                  `json:"path"`
	Summary   string                  `json:"summary"`
	Params    []checks.ParamDetail    `json:"params"`
	Responses []checks.ResponseDetail `json:"responses"`
}

// APIBlocks builds machine-owned `api` blocks from the OpenAPI spec at specRef
// (repo-relative). One block per operation, keyed by api:<METHOD>:<path> and
// bound (whole-file sha256) to the spec, so re-authoring is stable across
// reordering and the blocks satisfy `check api` immediately.
func APIBlocks(repoRoot, specRef, generator string) ([]api.BlockInput, error) {
	data, err := readRepoFile(repoRoot, specRef)
	if err != nil {
		return nil, fmt.Errorf("read spec %q: %w", specRef, err)
	}
	ops, err := checks.ParseOpenAPIDetailed(data)
	if err != nil {
		return nil, fmt.Errorf("parse spec %q: %w", specRef, err)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("spec %q defines no operations", specRef)
	}
	binding, err := BuildBinding(repoRoot, specRef, "openapi", generator)
	if err != nil {
		return nil, err
	}

	blocks := make([]api.BlockInput, 0, len(ops))
	for i, op := range ops {
		blocks = append(blocks, api.BlockInput{
			Key:       fmt.Sprintf("api:%s:%s", op.Method, op.Path),
			Type:      "api",
			Ownership: "machine",
			Content: apiContent{
				Method:    op.Method,
				Path:      op.Path,
				Summary:   op.Summary,
				Params:    op.Params,
				Responses: op.Responses,
			},
			SourceBinding: binding,
			Position:      i,
		})
	}
	return blocks, nil
}
