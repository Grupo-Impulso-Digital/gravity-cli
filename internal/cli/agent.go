package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
	"github.com/Grupo-Impulso-Digital/gravity-cli/plugin"
)

const (
	toolClaude   = "claude"
	toolCursor   = "cursor"
	toolCodex    = "codex"
	toolAgentsMD = "agents-md"

	agentsBegin = "<!-- gravity:begin -->"
	agentsEnd   = "<!-- gravity:end -->"
)

type agentData struct {
	Tool    string   `json:"tool"`
	Global  bool     `json:"global"`
	Written []string `json:"written"`
}

func newAgentCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Teach your coding agent how to work with Gravity (install)",
	}
	var tool string
	var global bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Write the Gravity skill for Claude Code, Cursor, Codex or AGENTS.md",
		Long: "Write the Gravity skill (the same one the Claude Code plugin ships) where your agent reads it:\n" +
			"  claude     .claude/skills/gravity/ (with --global: ~/.claude/skills/gravity/)\n" +
			"  cursor     .cursor/rules/gravity.mdc\n" +
			"  codex      AGENTS.md (with --global: ~/.codex/AGENTS.md), in a marked section\n" +
			"  agents-md  AGENTS.md, in a marked section\n" +
			"Re-running replaces what an earlier install wrote and nothing else. For Claude Code you can also add the plugin marketplace: /plugin marketplace add Grupo-Impulso-Digital/gravity-cli.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := a.agentInstall(cmd.Context(), tool, global)
			if err != nil {
				return err
			}
			for _, w := range d.Written {
				a.ui.Note("", ui.MarkOK, "wrote %s", w)
			}
			return a.ui.Result(d)
		},
	}
	install.Flags().StringVar(&tool, "tool", toolClaude, "claude, cursor, codex or agents-md")
	install.Flags().BoolVar(&global, "global", false, "install for every repository (claude, codex)")
	cmd.AddCommand(install)
	return cmd
}

func skillFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(plugin.Skill, "skills/gravity", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := plugin.Skill.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "skills/gravity/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the embedded skill: %w", err)
	}
	return out, nil
}

func skillBody(data []byte) []byte {
	s := string(data)
	if strings.HasPrefix(s, "---\n") {
		if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
			return []byte(strings.TrimLeft(s[4+i+5:], "\n"))
		}
	}
	return data
}

func skillDescription(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "description:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "Working with Gravity documentation and the gravity CLI"
}

func (a *app) agentInstall(ctx context.Context, tool string, global bool) (*agentData, error) {
	files, err := skillFiles()
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	d := &agentData{Tool: tool, Global: global, Written: []string{}}
	base, err := a.agentBase(ctx, global)
	if err != nil {
		return nil, err
	}
	write := func(rel string, data []byte) error {
		full := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Fail(CodeError, err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return Fail(CodeError, err)
		}
		d.Written = append(d.Written, displayPath(global, rel))
		return nil
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	switch tool {
	case toolClaude:
		for _, n := range names {
			if err := write(path.Join(".claude/skills/gravity", n), files[n]); err != nil {
				return nil, err
			}
		}
	case toolCursor:
		if global {
			return nil, Failf(CodeError, "Cursor keeps global rules in its settings; install per repository (drop --global)")
		}
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "---\ndescription: %s\nglobs:\nalwaysApply: false\n---\n\n", skillDescription(files["SKILL.md"]))
		buf.Write(inlineReferences(files, names))
		if err := write(".cursor/rules/gravity.mdc", buf.Bytes()); err != nil {
			return nil, err
		}
	case toolCodex, toolAgentsMD:
		rel := "AGENTS.md"
		if global {
			if tool != toolCodex {
				return nil, Failf(CodeError, "--global works with --tool codex (~/.codex/AGENTS.md) or claude")
			}
			rel = ".codex/AGENTS.md"
		}
		full := filepath.Join(base, filepath.FromSlash(rel))
		existing, err := os.ReadFile(full)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, Fail(CodeError, err)
		}
		if err := write(rel, mergeSection(existing, inlineReferences(files, names))); err != nil {
			return nil, err
		}
	default:
		return nil, Failf(CodeError, "--tool %q must be claude, cursor, codex or agents-md", tool)
	}
	return d, nil
}

func (a *app) agentBase(ctx context.Context, global bool) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", Fail(CodeError, err)
		}
		return home, nil
	}
	info, err := a.inspectRepo(ctx)
	if err == nil {
		return info.root, nil
	}
	return a.workdir()
}

func displayPath(global bool, rel string) string {
	if global {
		return "~/" + rel
	}
	return rel
}

func inlineReferences(files map[string][]byte, names []string) []byte {
	var buf bytes.Buffer
	buf.Write(bytes.TrimRight(skillBody(files["SKILL.md"]), "\n"))
	buf.WriteString("\n")
	for _, n := range names {
		if n == "SKILL.md" {
			continue
		}
		buf.WriteString("\n\n")
		buf.Write(bytes.TrimRight(files[n], "\n"))
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

func mergeSection(existing, body []byte) []byte {
	section := agentsBegin + "\n" + strings.TrimRight(string(body), "\n") + "\n" + agentsEnd + "\n"
	s := string(existing)
	if i := strings.Index(s, agentsBegin); i >= 0 {
		if j := strings.Index(s[i:], agentsEnd); j >= 0 {
			rest := strings.TrimPrefix(s[i+j+len(agentsEnd):], "\n")
			return []byte(s[:i] + section + rest)
		}
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if s != "" {
		s += "\n"
	}
	return []byte(s + section)
}
