package docs

import (
	"fmt"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

type apiContent struct {
	Method    string                  `json:"method"`
	Path      string                  `json:"path"`
	Summary   string                  `json:"summary"`
	Params    []checks.ParamDetail    `json:"params"`
	Responses []checks.ResponseDetail `json:"responses"`
}

// APIBlockKey is the page-identity key of the api block of an operation.
func APIBlockKey(method, path string) string {
	return fmt.Sprintf("api:%s:%s", method, path)
}

// APIBlocks builds machine-owned api blocks, one per operation, from the OpenAPI document at specRef.
func APIBlocks(repoRoot, specRef, generator string) ([]api.ChangeBlock, error) {
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
	blocks := make([]api.ChangeBlock, 0, len(ops))
	for _, op := range ops {
		content := apiContent{Method: op.Method, Path: op.Path, Summary: op.Summary, Params: op.Params, Responses: op.Responses}
		canon, err := normalize.CanonicalJSON(content)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, api.ChangeBlock{
			Key:       APIBlockKey(op.Method, op.Path),
			Type:      "api",
			Ownership: api.OwnershipMachine,
			Content:   content,
			SourceBinding: &api.SourceBinding{
				Kind:      "endpoint",
				Ref:       op.Method + " " + op.Path,
				Hash:      normalize.SHA256(canon),
				Generator: generator,
			},
			Units: []string{normalize.APIUnitKey(op.Method, op.Path)},
		})
	}
	return blocks, nil
}
