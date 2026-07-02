// Package agent implements the tool-using loop that runs against the Gravity
// LLM gateway. The model is given read-only git tools plus a terminal "submit"
// tool that ends the loop and returns its structured input. Every filesystem
// path the model can reach is sandboxed to the repository root.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/git"
	"github.com/impulso/gravity-cli/internal/pathsafe"
)

// Caps on output sizes so a single tool result can't blow the context budget.
const (
	maxFileBytes  = 64 * 1024
	maxDiffBytes  = 96 * 1024
	maxGrepBytes  = 32 * 1024
	maxLogEntries = 500
)

// Tool is a tool the model can call during the loop.
type Tool struct {
	Def api.Tool
	// Run executes the tool with the model-supplied input and returns the
	// string result to feed back as a tool_result.
	Run func(ctx context.Context, input json.RawMessage) (string, error)
	// Terminal marks the submit tools that end the loop. Their parsed input is
	// captured as the loop's result instead of being echoed back.
	Terminal bool
	// Validate, if set on a terminal tool, checks the model-supplied input
	// before the loop accepts it. A validation error does NOT end the loop: it
	// is fed back as an error tool_result so the model can correct its own
	// mistake (e.g. a JSON-encoded string where an array belongs) instead of
	// sinking the whole run after the tokens are already spent.
	Validate func(input json.RawMessage) error
}

// sandboxPath validates a model-supplied path stays within the repo root and
// returns it cleaned and repo-relative. It rejects empty paths, absolute paths,
// and any `..` escape.
func sandboxPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("path is required")
	}
	clean, err := pathsafe.Rel(p)
	switch {
	case errors.Is(err, pathsafe.ErrAbsolute):
		return "", fmt.Errorf("absolute paths are not allowed: %q", p)
	case errors.Is(err, pathsafe.ErrEscape):
		return "", fmt.Errorf("path escapes repository root: %q", p)
	case err != nil:
		return "", err
	}
	return clean, nil
}

func truncate(s string, limit int, label string) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n\n... [truncated %d of %d bytes of %s] ...", len(s)-limit, len(s), label)
}

// GitTools returns the read-only git tool set bound to repo.
func GitTools(repo *git.Repo) []Tool {
	return []Tool{
		{
			Def: api.Tool{
				Name:        "git_log",
				Description: "List commits as `git log --oneline` for an optional from..to range. Defaults to the configured range when omitted.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from":     map[string]any{"type": "string", "description": "Start ref (exclusive). Optional."},
						"to":       map[string]any{"type": "string", "description": "End ref (inclusive). Defaults to HEAD."},
						"maxCount": map[string]any{"type": "integer", "description": "Limit number of commits."},
					},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					From     string `json:"from"`
					To       string `json:"to"`
					MaxCount int    `json:"maxCount"`
				}
				_ = json.Unmarshal(input, &in)
				if in.MaxCount <= 0 || in.MaxCount > maxLogEntries {
					in.MaxCount = maxLogEntries
				}
				out, err := repo.LogOneline(ctx, in.From, in.To, in.MaxCount)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(out) == "" {
					return "(no commits in range)", nil
				}
				return out, nil
			},
		},
		{
			Def: api.Tool{
				Name:        "git_diff",
				Description: "Show the diff between two refs, optionally scoped to a path. Large patches are truncated.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{"type": "string", "description": "Start ref. Optional."},
						"to":   map[string]any{"type": "string", "description": "End ref. Defaults to HEAD."},
						"path": map[string]any{"type": "string", "description": "Limit the diff to this repo-relative path. Optional."},
					},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					From string `json:"from"`
					To   string `json:"to"`
					Path string `json:"path"`
				}
				_ = json.Unmarshal(input, &in)
				if in.Path != "" {
					clean, err := sandboxPath(in.Path)
					if err != nil {
						return "", err
					}
					in.Path = clean
				}
				out, err := repo.Diff(ctx, in.From, in.To, in.Path)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(out) == "" {
					return "(no changes)", nil
				}
				return truncate(out, maxDiffBytes, "diff"), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "git_show",
				Description: "Show the contents of a file at a specific ref.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ref":  map[string]any{"type": "string", "description": "The git ref. Defaults to HEAD."},
						"path": map[string]any{"type": "string", "description": "Repo-relative path to the file."},
					},
					"required": []any{"path"},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Ref  string `json:"ref"`
					Path string `json:"path"`
				}
				_ = json.Unmarshal(input, &in)
				clean, err := sandboxPath(in.Path)
				if err != nil {
					return "", err
				}
				out, err := repo.Show(ctx, in.Ref, clean)
				if err != nil {
					return "", err
				}
				return truncate(out, maxFileBytes, "file"), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "list_files",
				Description: "List tracked files (git ls-files), optionally filtered by a glob/pathspec.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"glob": map[string]any{"type": "string", "description": "Optional pathspec/glob, e.g. 'src/**/*.go'."},
					},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Glob string `json:"glob"`
				}
				_ = json.Unmarshal(input, &in)
				files, err := repo.ListFiles(ctx, in.Glob)
				if err != nil {
					return "", err
				}
				if len(files) == 0 {
					return "(no matching files)", nil
				}
				return truncate(strings.Join(files, "\n"), maxFileBytes, "file list"), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "read_file",
				Description: "Read the contents of a tracked file at HEAD (size-capped). Use git_show for other refs.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Repo-relative path to the file."},
					},
					"required": []any{"path"},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(input, &in)
				clean, err := sandboxPath(in.Path)
				if err != nil {
					return "", err
				}
				// Returns the file as committed at HEAD, so uncommitted edits
				// are not visible. In CI the working tree equals HEAD; for
				// other refs the model should use git_show.
				out, err := repo.Show(ctx, "HEAD", clean)
				if err != nil {
					return "", err
				}
				return truncate(out, maxFileBytes, "file"), nil
			},
		},
		{
			Def: api.Tool{
				Name:        "grep",
				Description: "Search tracked files with `git grep` for a pattern, optionally scoped to a glob.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{"type": "string", "description": "The pattern to search for."},
						"glob":    map[string]any{"type": "string", "description": "Optional pathspec/glob to scope the search."},
					},
					"required": []any{"pattern"},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Pattern string `json:"pattern"`
					Glob    string `json:"glob"`
				}
				_ = json.Unmarshal(input, &in)
				if strings.TrimSpace(in.Pattern) == "" {
					return "", errors.New("pattern is required")
				}
				out, err := repo.Grep(ctx, in.Pattern, in.Glob)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(out) == "" {
					return "(no matches)", nil
				}
				return truncate(out, maxGrepBytes, "grep output"), nil
			},
		},
	}
}
