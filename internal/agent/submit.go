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
