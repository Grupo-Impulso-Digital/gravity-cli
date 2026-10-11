package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Draft tool names (gravity.tools/1).
const (
	ToolDraftView    = "draft_view"
	ToolDraftAppend  = "draft_append"
	ToolDraftReplace = "draft_replace"
	ToolDraftDelete  = "draft_delete"
	ToolCheckPage    = "check_page"
	ToolRaiseHint    = "raise_hint"
	ToolSubmitPage   = "submit_page"
)

// HintKinds are the hint kinds raise_hint accepts.
var HintKinds = []string{"contradiction", "impact"}

func rationaleSchema() map[string]any {
	return object([]string{"summary"}, map[string]any{
		"summary":    map[string]any{"type": "string"},
		"sourceRefs": map[string]any{"type": "array", "items": stringItems},
		"commits":    map[string]any{"type": "array", "items": stringItems},
	})
}

func blockSchema(c api.BlockContract, audiences []string) map[string]any {
	c = blockContract(c)
	variants := append(append([]string{}, c.CalloutVariants...), c.ListVariants...)
	aud := map[string]any{"type": "array", "items": stringItems}
	if len(audiences) > 0 {
		aud["items"] = enum(audiences...)
	}
	return object([]string{"type"}, map[string]any{
		"type":      enum(c.Types...),
		"text":      map[string]any{"type": "string"},
		"level":     map[string]any{"type": "integer", "enum": []any{1, 2, 3}},
		"language":  map[string]any{"type": "string"},
		"variant":   enum(variants...),
		"rows":      map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": stringItems}},
		"header":    map[string]any{"type": "boolean"},
		"audiences": aud,
		"units":     map[string]any{"type": "array", "items": stringItems},
		"rationale": rationaleSchema(),
	})
}

func decodeInput(raw json.RawMessage, v any) error {
	if strings.TrimSpace(string(raw)) == "" {
		raw = json.RawMessage("{}")
	}
	if err := Decode(raw, v); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	return nil
}

// DraftTools returns the draft tools of a write phase; reach drafts get draft_view, draft_replace, check_page and raise_hint only.
func DraftTools(d *Draft) []Tool {
	view := Tool{
		Def: api.Tool{Name: ToolDraftView, Description: "Show the page draft: one line per block ([key], locked or not, type, text), then the check status.", InputSchema: object(nil, map[string]any{})},
		Run: func(context.Context, json.RawMessage) (string, error) { return d.View(), nil },
	}
	appendTool := Tool{
		Def: api.Tool{
			Name:        ToolDraftAppend,
			Description: "Insert blocks after a key (\"^\" for the top; the end by default). Returns the keys assigned.",
			InputSchema: object([]string{"blocks"}, map[string]any{
				"after":     map[string]any{"type": "string"},
				"rationale": rationaleSchema(),
				"blocks":    map[string]any{"type": "array", "items": blockSchema(d.opts.Contract, d.opts.Audiences)},
			}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				After     string     `json:"after"`
				Rationale *Rationale `json:"rationale"`
				Blocks    []Block    `json:"blocks"`
			}
			if err := decodeInput(raw, &in); err != nil {
				return "", err
			}
			keys, err := d.Append(in.After, in.Rationale, in.Blocks)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Added %s. The draft has %d blocks.", strings.Join(keys, ", "), len(d.blocks)), nil
		},
	}
	replace := Tool{
		Def: api.Tool{
			Name:        ToolDraftReplace,
			Description: "Replace an editable block, keeping its key and position.",
			InputSchema: object([]string{"key", "block"}, map[string]any{
				"key":   map[string]any{"type": "string"},
				"block": blockSchema(d.opts.Contract, d.opts.Audiences),
			}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				Key   string `json:"key"`
				Block *Block `json:"block"`
			}
			if err := decodeInput(raw, &in); err != nil {
				return "", err
			}
			if in.Block == nil {
				return "", errors.New("block is required")
			}
			if err := d.Replace(in.Key, *in.Block); err != nil {
				return "", err
			}
			return "Replaced " + strings.TrimSpace(in.Key) + ".", nil
		},
	}
	del := Tool{
		Def: api.Tool{
			Name:        ToolDraftDelete,
			Description: "Delete editable blocks by key.",
			InputSchema: object([]string{"keys"}, map[string]any{"keys": map[string]any{"type": "array", "items": stringItems}}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				Keys []string `json:"keys"`
			}
			if err := decodeInput(raw, &in); err != nil {
				return "", err
			}
			if err := d.Delete(in.Keys); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted %s. The draft has %d blocks.", strings.Join(in.Keys, ", "), len(d.blocks)), nil
		},
	}
	check := Tool{
		Def: api.Tool{Name: ToolCheckPage, Description: "Run the page checks on the authored and changed blocks.", InputSchema: object(nil, map[string]any{})},
		Run: func(context.Context, json.RawMessage) (string, error) {
			findings := d.Check(nil)
			if len(findings) == 0 {
				return "No findings.", nil
			}
			return fmt.Sprintf("%d error(s), %d warning(s):\n%s", len(Errors(findings)), len(findings)-len(Errors(findings)), formatFindings(findings)), nil
		},
	}
	hint := Tool{
		Def: api.Tool{
			Name:        ToolRaiseHint,
			Description: "Raise a hint for the repository that owns a claim instead of editing it.",
			InputSchema: object([]string{"kind", "claim", "detail"}, map[string]any{
				"kind":     enum(HintKinds...),
				"claim":    map[string]any{"type": "string"},
				"unitKey":  map[string]any{"type": "string"},
				"blockKey": map[string]any{"type": "string"},
				"detail":   map[string]any{"type": "string"},
				"forRepos": map[string]any{"type": "array", "items": stringItems},
			}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in HintDraft
			if err := decodeInput(raw, &in); err != nil {
				return "", err
			}
			switch {
			case !oneOf(in.Kind, HintKinds):
				return "", fmt.Errorf("kind %q must be one of %s", in.Kind, strings.Join(HintKinds, ", "))
			case strings.TrimSpace(in.Claim) == "":
				return "", errors.New("claim is required")
			case strings.TrimSpace(in.Detail) == "":
				return "", errors.New("detail is required")
			}
			d.AddHint(in)
			return fmt.Sprintf("Hint recorded (%d so far).", len(d.hints)), nil
		},
	}
	if d.opts.Mode == DraftReach {
		return []Tool{view, replace, check, hint}
	}
	return []Tool{view, appendTool, replace, del, check, hint}
}

type submitInput struct {
	Summary   string      `json:"summary"`
	Uncovered []Uncovered `json:"uncovered"`
}

func parseSubmit(raw json.RawMessage, reach bool) (submitInput, error) {
	var in submitInput
	if err := Decode(raw, &in); err != nil {
		return in, fmt.Errorf("submit_page takes {summary, uncovered?}: %w", err)
	}
	if strings.TrimSpace(in.Summary) == "" {
		return in, errors.New("summary is required: one sentence for reviewers on what changed and why")
	}
	if reach {
		in.Uncovered = nil
	}
	for i, u := range in.Uncovered {
		if strings.TrimSpace(u.Token) == "" || strings.TrimSpace(u.Reason) == "" {
			return in, fmt.Errorf("uncovered[%d] needs a token and a reason", i)
		}
	}
	return in, nil
}

// SubmitPageTool is the terminal tool of a write phase: it runs the checks and refuses a page with error findings.
func SubmitPageTool(d *Draft) Tool {
	reach := d.opts.Mode == DraftReach
	props := map[string]any{"summary": map[string]any{"type": "string"}}
	if !reach {
		props["uncovered"] = map[string]any{"type": "array", "items": object([]string{"token", "reason"}, map[string]any{
			"token":  map[string]any{"type": "string"},
			"reason": map[string]any{"type": "string"},
		})}
	}
	return Tool{
		Terminal: true,
		Def:      api.Tool{Name: ToolSubmitPage, Description: "Submit the page draft once the checks pass.", InputSchema: object([]string{"summary"}, props)},
		Validate: func(raw json.RawMessage) error {
			_, err := parseSubmit(raw, reach)
			return err
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			in, err := parseSubmit(raw, reach)
			if err != nil {
				return "", err
			}
			if d.opts.Mode == DraftCreate && len(d.blocks) == 0 {
				return "", errors.New("the draft is empty; add the page's blocks with draft_append")
			}
			findings := d.Check(in.Uncovered)
			if errs := Errors(findings); len(errs) > 0 {
				return "", fmt.Errorf("the page fails %d check(s); fix them with the draft tools:\n%s", len(errs), formatFindings(errs))
			}
			d.submit(strings.TrimSpace(in.Summary), in.Uncovered)
			if len(findings) > 0 {
				return "Submitted with warnings:\n" + formatFindings(findings), nil
			}
			return "Submitted.", nil
		},
	}
}
