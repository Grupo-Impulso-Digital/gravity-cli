package docs

import (
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// APIParam is one parameter of an api block.
type APIParam struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// APIResponse is one documented response of an api block.
type APIResponse struct {
	Status      string `json:"status"`
	Description string `json:"description"`
}

// APIContent is the content of an api block.
type APIContent struct {
	Method      string        `json:"method"`
	Path        string        `json:"path"`
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Params      []APIParam    `json:"params"`
	Responses   []APIResponse `json:"responses"`
}

// APIBlockKey is the page-identity key of the api block of an operation.
func APIBlockKey(method, path string) string {
	return fmt.Sprintf("api:%s:%s", strings.ToUpper(method), path)
}

// Operations parses an OpenAPI document into its operations, sorted by path then method.
func Operations(spec []byte) ([]checks.OperationDetail, error) {
	ops, err := checks.ParseOpenAPIDetailed(spec)
	if err != nil {
		return nil, err
	}
	return ops, nil
}

var paramLocations = map[string]bool{"path": true, "query": true, "header": true, "body": true}

// Content converts an operation into api block content.
func Content(op checks.OperationDetail) APIContent {
	c := APIContent{Method: strings.ToUpper(op.Method), Path: op.Path, Summary: op.Summary, Description: op.Description, Params: []APIParam{}, Responses: []APIResponse{}}
	for _, p := range op.Params {
		if !paramLocations[p.In] {
			continue
		}
		c.Params = append(c.Params, APIParam{Name: p.Name, In: p.In, Type: "string", Required: p.Required, Description: p.Description})
	}
	for _, r := range op.Responses {
		c.Responses = append(c.Responses, APIResponse{Status: r.Code, Description: r.Description})
	}
	return c
}

// APIBlock builds the machine-owned api block of one operation with its endpoint binding and unit key.
func APIBlock(op checks.OperationDetail, generator string) (api.ChangeBlock, error) {
	content := Content(op)
	canon, err := normalize.CanonicalJSON(content)
	if err != nil {
		return api.ChangeBlock{}, err
	}
	return api.ChangeBlock{
		Key:       APIBlockKey(op.Method, op.Path),
		Type:      "api",
		Ownership: api.OwnershipMachine,
		Content:   content,
		SourceBinding: &api.SourceBinding{
			Kind:      "endpoint",
			Ref:       strings.ToUpper(op.Method) + " " + op.Path,
			Hash:      normalize.SHA256(canon),
			Generator: generator,
		},
		Units: []string{normalize.APIUnitKey(op.Method, op.Path)},
	}, nil
}

// APIBlocks builds one api block per operation of an OpenAPI document.
func APIBlocks(spec []byte, generator string) ([]api.ChangeBlock, error) {
	ops, err := Operations(spec)
	if err != nil {
		return nil, err
	}
	blocks := make([]api.ChangeBlock, 0, len(ops))
	for _, op := range ops {
		b, err := APIBlock(op, generator)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// Slug lowercases text and collapses non-alphanumerics into single dashes, like the viewer's anchor slug.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
