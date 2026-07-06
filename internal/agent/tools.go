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
	"path/filepath"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
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
}

// sandboxPath validates a model-supplied path stays within the repo root and
// returns it cleaned and repo-relative. It rejects absolute paths and any `..`
// escape.
func sandboxPath(root, p string) (string, error) {
	if p == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root: %q", p)
	}
	// Defensive: resolve against root and ensure it stays inside.
	abs := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root: %q", p)
	}
	return clean, nil
}

func truncate(s string, max int, label string) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n\n... [truncated %d of %d bytes of %s] ...", len(s)-max, len(s), label)
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
					if clean, err := sandboxPath(repo.Root, in.Path); err != nil {
						return "", err
					} else {
						in.Path = clean
					}
				}
				out, err := repo.Diff(ctx, in.From, in.To, in.Path, false)
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
				clean, err := sandboxPath(repo.Root, in.Path)
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
				Description: "Read the current contents of a tracked file (size-capped).",
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
				clean, err := sandboxPath(repo.Root, in.Path)
				if err != nil {
					return "", err
				}
				// Read from the working tree via git show of the index/HEAD is
				// not ideal for uncommitted files, so read the file directly
				// but keep it sandboxed.
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
