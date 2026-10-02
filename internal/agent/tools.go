// Package agent implements the tool-using loop that runs against the Gravity LLM gateway.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

const (
	maxFileBytes  = 64 * 1024
	maxDiffBytes  = 96 * 1024
	maxGrepBytes  = 32 * 1024
	maxLogEntries = 500
)

// Tool is a tool the model can call during the loop.
type Tool struct {
	Def      api.Tool
	Run      func(ctx context.Context, input json.RawMessage) (string, error)
	Terminal bool
	Validate func(input json.RawMessage) error
}

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

func modelRefs(refs ...string) error {
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if err := git.ValidateRef(ref); err != nil {
			return err
		}
	}
	return nil
}

func truncate(s string, limit int, label string) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n\n... [truncated %d of %d bytes of %s] ...", len(s)-limit, len(s), label)
}

// Range is the commit range the git tools default to; an empty Head with WorkingTree reads the working tree.
type Range struct {
	Base        string
	Head        string
	WorkingTree bool
}

func (r Range) headLabel() string {
	switch {
	case r.WorkingTree:
		return "the working tree"
	case r.Head == "":
		return "HEAD"
	}
	return r.Head
}

// GitTools returns the read-only git tool set bound to repo, reading files at HEAD.
func GitTools(repo *git.Repo) []Tool {
	return RepoTools(repo, Range{})
}

// GitToolsAt returns the read-only git tool set whose read_file reads at ref (HEAD when empty).
func GitToolsAt(repo *git.Repo, ref string) []Tool {
	return RepoTools(repo, Range{Head: ref})
}

func readWorkingTree(root, p string) (string, error) {
	full, err := pathsafe.ResolveInRoot(root, p)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return string(data), nil
}

// RepoTools returns the read-only git tool set for a range: git_log, git_diff, git_show, list_files, read_file and grep.
func RepoTools(repo *git.Repo, rng Range) []Tool {
	head := rng.Head
	if head == "" && !rng.WorkingTree {
		head = "HEAD"
	}
	return []Tool{
		{
			Def: api.Tool{
				Name:        "git_log",
				Description: fmt.Sprintf("List commits as `git log --oneline`. Defaults to the range under review (%s..%s).", orRoot(rng.Base), orHEAD(head)),
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from":     map[string]any{"type": "string", "description": "Start ref (exclusive). Defaults to the range base."},
						"to":       map[string]any{"type": "string", "description": "End ref (inclusive). Defaults to the range head."},
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
				if err := modelRefs(in.From, in.To); err != nil {
					return "", err
				}
				if in.From == "" && in.To == "" {
					in.From, in.To = rng.Base, head
				}
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
				Description: fmt.Sprintf("Show the diff between two refs, optionally scoped to a path. Defaults to the range under review (%s..%s). Large patches are truncated.", orRoot(rng.Base), rng.headLabel()),
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{"type": "string", "description": "Start ref. Defaults to the range base."},
						"to":   map[string]any{"type": "string", "description": "End ref. Defaults to the range head."},
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
				if err := modelRefs(in.From, in.To); err != nil {
					return "", err
				}
				if in.Path != "" {
					clean, err := sandboxPath(in.Path)
					if err != nil {
						return "", err
					}
					in.Path = clean
				}
				var out string
				var err error
				switch {
				case in.From == "" && in.To == "" && rng.WorkingTree:
					out, err = repo.DiffWorkingTree(ctx, rng.Base, in.Path)
				case in.From == "" && in.To == "":
					out, err = repo.Diff(ctx, rng.Base, head, in.Path)
				default:
					out, err = repo.Diff(ctx, in.From, in.To, in.Path)
				}
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
				if err := modelRefs(in.Ref); err != nil {
					return "", err
				}
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
				Description: fmt.Sprintf("Read a file at %s, the end of the range under review (size-capped). Pass ref to read it at another commit, tag or branch.", rng.headLabel()),
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Repo-relative path to the file."},
						"ref":  map[string]any{"type": "string", "description": "Optional git ref to read the file at instead of the range head."},
					},
					"required": []any{"path"},
				},
			},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Path string `json:"path"`
					Ref  string `json:"ref"`
				}
				_ = json.Unmarshal(input, &in)
				if err := modelRefs(in.Ref); err != nil {
					return "", err
				}
				clean, err := sandboxPath(in.Path)
				if err != nil {
					return "", err
				}
				var out string
				switch {
				case in.Ref != "":
					out, err = repo.Show(ctx, in.Ref, clean)
				case rng.WorkingTree:
					out, err = readWorkingTree(repo.Root, clean)
				default:
					out, err = repo.Show(ctx, head, clean)
				}
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

func orRoot(s string) string {
	if s == "" {
		return "the first commit"
	}
	return s
}

func orHEAD(s string) string {
	if s == "" {
		return "HEAD"
	}
	return s
}
