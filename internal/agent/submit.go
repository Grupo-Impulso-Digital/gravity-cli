package agent

import (
	"context"
	"encoding/json"

	"github.com/impulso/gravity-cli/internal/api"
)

// Terminal tool names.
const (
	ToolSubmitReleaseNotes = "submit_release_notes"
	ToolReportFindings     = "report_findings"
	ToolSubmitAtoms        = "submit_atoms"
	ToolSubmitDocPlan      = "submit_doc_plan"
	ToolSubmitPageDoc      = "submit_page_doc"
)

// ReleaseNotesInput is the structured input the model passes to
// submit_release_notes.
type ReleaseNotesInput struct {
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Sections []struct {
		Heading string   `json:"heading"`
		Items   []string `json:"items"`
	} `json:"sections"`
}

// FindingsInput is the structured input the model passes to report_findings.
type FindingsInput struct {
	Findings []struct {
		Severity      string `json:"severity"`
		Title         string `json:"title"`
		Detail        string `json:"detail"`
		SuggestedPage string `json:"suggestedPage"`
	} `json:"findings"`
}

// AtomsInput is the structured input the model passes to submit_atoms.
type AtomsInput struct {
	Atoms []struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
		Links   []string `json:"links"`
	} `json:"atoms"`
}

// SubmitAtomsTool builds the terminal tool that ends the nucleus-distill loop.
func SubmitAtomsTool() Tool {
	return Tool{
		Terminal: true,
		Def: api.Tool{
			Name:        ToolSubmitAtoms,
			Description: "Submit the distilled memory atoms. Call this exactly once when done, with an empty array if there is nothing worth remembering.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"atoms": map[string]any{
						"type":        "array",
						"description": "Small, self-contained facts worth remembering across the product.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"content": map[string]any{"type": "string", "description": "One concise, self-contained fact (1-2 sentences)."},
								"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								"links":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional ids of related atoms."},
							},
							"required": []any{"content"},
						},
					},
				},
				"required": []any{"atoms"},
			},
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", nil
		},
	}
}

// ParseAtoms decodes a submit_atoms input.
func ParseAtoms(raw json.RawMessage) (AtomsInput, error) {
	var in AtomsInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// SubmitReleaseNotesTool builds the terminal tool that ends the release-notes
// loop.
func SubmitReleaseNotesTool() Tool {
	return Tool{
		Terminal: true,
		Def: api.Tool{
			Name:        ToolSubmitReleaseNotes,
			Description: "Submit the finished release notes. Call this exactly once when you are done. After calling it, do not write any further text.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":   map[string]any{"type": "string", "description": "Release notes title (e.g. a version or date)."},
					"summary": map[string]any{"type": "string", "description": "A one-or-two sentence overview of the release."},
					"sections": map[string]any{
						"type":        "array",
						"description": "Grouped, user-facing changes.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"heading": map[string]any{"type": "string", "description": "One of Added, Changed, Fixed, Removed, Security, Breaking."},
								"items": map[string]any{
									"type":  "array",
									"items": map[string]any{"type": "string"},
								},
							},
							"required": []any{"heading", "items"},
						},
					},
				},
				"required": []any{"title", "sections"},
			},
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", nil // never invoked: terminal tools end the loop.
		},
	}
}

// ReportFindingsTool builds the terminal tool that ends the docs-gap loop.
func ReportFindingsTool() Tool {
	return Tool{
		Terminal: true,
		Def: api.Tool{
			Name:        ToolReportFindings,
			Description: "Report the documentation gaps you found. Call this exactly once when you are done, with an empty array if there are no gaps.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"findings": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"severity":      map[string]any{"type": "string", "description": "high, medium, or low."},
								"title":         map[string]any{"type": "string", "description": "Short summary of the gap."},
								"detail":        map[string]any{"type": "string", "description": "What changed in code and why the docs are now incomplete."},
								"suggestedPage": map[string]any{"type": "string", "description": "Optional page slug that should be updated."},
							},
							"required": []any{"severity", "title"},
						},
					},
				},
				"required": []any{"findings"},
			},
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", nil
		},
	}
}

// DocPlanInput is the structured input the model passes to submit_doc_plan.
type DocPlanInput struct {
	Pages []DocPlanPage `json:"pages"`
}

// DocPlanPage is one proposed documentation page.
type DocPlanPage struct {
	Space     string   `json:"space"`
	Slug      string   `json:"slug"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Audiences []string `json:"audiences"`
	Sources   []string `json:"sources"`
}

// PageDocInput is the structured input the model passes to submit_page_doc.
type PageDocInput struct {
	Title  string          `json:"title"`
	Blocks []DocBlockInput `json:"blocks"`
}

// DocBlockInput is one authored block. Reusing an existing Key updates that
// block, a new Key adds one, and omitting an existing Key proposes its removal.
type DocBlockInput struct {
	Key       string          `json:"key"`
	Type      string          `json:"type"`
	Ownership string          `json:"ownership"`
	Audiences []string        `json:"audiences"`
	Content   json.RawMessage `json:"content"`
	Sources   []string        `json:"sources"`
}

// SubmitDocPlanTool builds the terminal tool that ends the docs-plan loop.
func SubmitDocPlanTool() Tool {
	audienceItems := map[string]any{"type": "string", "enum": []any{"public", "users", "developers"}}
	return Tool{
		Terminal: true,
		Def: api.Tool{
			Name:        ToolSubmitDocPlan,
			Description: "Submit the proposed documentation pages. Call this exactly once when done.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pages": map[string]any{
						"type":        "array",
						"description": "The documentation pages to author, covering the public/users/developers audiences.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"space":     map[string]any{"type": "string", "description": "Target space slug."},
								"slug":      map[string]any{"type": "string", "description": "Stable kebab-case page slug."},
								"title":     map[string]any{"type": "string"},
								"summary":   map[string]any{"type": "string", "description": "What this page should contain."},
								"audiences": map[string]any{"type": "array", "items": audienceItems, "description": "Audiences this page serves."},
								"sources":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Repo files most relevant to authoring this page."},
							},
							"required": []any{"slug", "title", "audiences"},
						},
					},
				},
				"required": []any{"pages"},
			},
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", nil
		},
	}
}

// SubmitPageDocTool builds the terminal tool that ends a docs-author loop.
func SubmitPageDocTool() Tool {
	audienceItems := map[string]any{"type": "string", "enum": []any{"public", "users", "developers"}}
	return Tool{
		Terminal: true,
		Def: api.Tool{
			Name:        ToolSubmitPageDoc,
			Description: "Submit the complete set of blocks for this page. Call this exactly once when done.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
					"blocks": map[string]any{
						"type":        "array",
						"description": "The page's blocks, in reading order, covering all its audiences.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"key":       map[string]any{"type": "string", "description": "Stable block identity; reuse an existing key to update, omit to remove."},
								"type":      map[string]any{"type": "string", "enum": []any{"heading", "prose", "code", "table"}},
								"ownership": map[string]any{"type": "string", "enum": []any{"machine", "hybrid", "human"}, "description": "Defaults to hybrid."},
								"audiences": map[string]any{"type": "array", "items": audienceItems, "description": "Audiences for this block; empty = everyone."},
								"content":   map[string]any{"type": "object", "description": "Shape depends on type: heading/prose/code use {text,...}; table uses {rows: string[][] with the header row first, header: true}."},
								"sources":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Repo files this block draws from."},
							},
							"required": []any{"key", "type", "content"},
						},
					},
				},
				"required": []any{"title", "blocks"},
			},
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", nil
		},
	}
}

// ParseDocPlan decodes a submit_doc_plan input.
func ParseDocPlan(raw json.RawMessage) (DocPlanInput, error) {
	var in DocPlanInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// ParsePageDoc decodes a submit_page_doc input.
func ParsePageDoc(raw json.RawMessage) (PageDocInput, error) {
	var in PageDocInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// ParseReleaseNotes decodes a submit_release_notes input.
func ParseReleaseNotes(raw json.RawMessage) (ReleaseNotesInput, error) {
	var in ReleaseNotesInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// ParseFindings decodes a report_findings input.
func ParseFindings(raw json.RawMessage) (FindingsInput, error) {
	var in FindingsInput
	err := json.Unmarshal(raw, &in)
	return in, err
}
