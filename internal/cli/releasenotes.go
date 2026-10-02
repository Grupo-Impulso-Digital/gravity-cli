package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

const (
	outputProposal = "proposal"
	outputFile     = "file"
	outputStdout   = "stdout"
)

func newReleaseNotesCmd(gf *globalFlags) *cobra.Command {
	var (
		space     string
		from      string
		to        string
		output    string
		dryRun    bool
		title     string
		changelog string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "release-notes",
		Short: "Generate release notes from git history via the AI harness",
		Long: `Resolve a git commit range, run the release-notes agent over it, and emit
structured notes. The agent inspects commits and diffs and groups user-facing
changes into sections, ending by calling submit_release_notes.

Output modes:
  proposal  POST a draft + proposal to Gravity and print the review URL (default)
  file      write/prepend the notes to the changelog file
  stdout    print the notes as markdown

The proposal lands in releaseNotes.space from .gravity.yaml (default
"changelog"); --space overrides it. The file mode writes releaseNotes.changelog
(default CHANGELOG.md); --changelog overrides it. An empty commit range is a
no-op that exits 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputFile, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|file|stdout)", output)
			}

			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			sp := releaseNotesSpace(e.proj, space)
			e.targetSpace = sp
			changelogPath := releaseNotesChangelog(e.proj, changelog)
			if err := e.requireAuth(); err != nil {
				return err
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, err)
			}
			rng, err := repo.ResolveRange(cmd.Context(), from, to)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("resolve range: %w", err))
			}

			logw := logWriter(cmd)
			fmt.Fprintf(logw, "release-notes: range %s\n", rng.String())

			commits, err := repo.Log(cmd.Context(), rng.From, rng.To, 0)
			if err != nil {
				return Fail(CodeError, err)
			}
			view := releaseNotesView{Range: rng.String(), Commits: len(commits), Space: sp, Output: output, DryRun: dryRun}
			if len(commits) == 0 {
				if jsonOut {
					view.Skipped = "empty commit range"
					return writeJSON(cmd.OutOrStdout(), view)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "no commits in range %s; nothing to do\n", rng.String())
				return nil
			}
			fmt.Fprintf(logw, "release-notes: %d commit(s) in range\n", len(commits))

			notes, err := runReleaseNotesAgent(cmd.Context(), e.client, repo, rng, logw, &api.MessagesContext{Site: e.cfg.Site, Space: sp, Namespace: e.cfg.Namespace})
			if err != nil {
				return Fail(CodeError, err)
			}
			if title != "" {
				notes.Title = title
			}
			if strings.TrimSpace(notes.Title) == "" {
				notes.Title = defaultTitle(rng)
			}

			view.Notes = notes
			return emitReleaseNotes(cmd, e, releaseNotesEmit{
				space: sp, output: output, dryRun: dryRun, changelog: changelogPath, json: jsonOut,
			}, view)
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "target space slug (default: releaseNotes.space from .gravity.yaml, else changelog)")
	cmd.Flags().StringVar(&from, "from", "", "start git ref (default: latest tag, or first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref (default: HEAD)")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|file|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the structured notes without writing or posting")
	cmd.Flags().StringVar(&title, "title", "", "override the release-notes title")
	cmd.Flags().StringVar(&changelog, "changelog", "", "path for --output file (default: releaseNotes.changelog from .gravity.yaml, else CHANGELOG.md)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the result as JSON")
	return cmd
}

func releaseNotesSpace(proj *config.Project, flag string) string {
	if flag != "" {
		return flag
	}
	if proj != nil && proj.ReleaseNotes.Space != "" {
		return proj.ReleaseNotes.Space
	}
	return config.DefaultReleaseNotesSpace
}

func releaseNotesChangelog(proj *config.Project, flag string) string {
	if flag != "" {
		return flag
	}
	if proj != nil && proj.ReleaseNotes.Changelog != "" {
		return proj.ReleaseNotes.Changelog
	}
	return config.DefaultChangelog
}

type releaseNotesView struct {
	Range    string                    `json:"range"`
	Commits  int                       `json:"commits"`
	Space    string                    `json:"space,omitempty"`
	Output   string                    `json:"output"`
	DryRun   bool                      `json:"dryRun,omitempty"`
	Skipped  string                    `json:"skipped,omitempty"`
	Notes    *agent.ReleaseNotesInput  `json:"notes,omitempty"`
	Markdown string                    `json:"markdown,omitempty"`
	File     string                    `json:"file,omitempty"`
	Proposal *api.ReleaseNotesResponse `json:"proposal,omitempty"`
}

type releaseNotesEmit struct {
	space     string
	output    string
	dryRun    bool
	changelog string
	json      bool
}

func defaultTitle(rng git.Range) string {
	to := rng.To
	if to == "HEAD" || to == "" {
		return "Unreleased"
	}
	return to
}

func runReleaseNotesAgent(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, logw io.Writer, mctx *api.MessagesContext) (*agent.ReleaseNotesInput, error) {
	tools := append(agent.GitToolsAt(repo, rng.To), agent.SubmitReleaseNotesTool())
	runner := &agent.Runner{
		Client:  client,
		System:  resolvePrompt(ctx, client, prompts.NameReleaseNotes, logw),
		Tools:   tools,
		Context: mctx,
		Log:     logw,
	}
	kickoff := fmt.Sprintf(
		"Generate release notes for the changes between %s and %s. "+
			"Start by listing the commits with git_log(from=%q, to=%q), then inspect the diffs. "+
			"When finished, call submit_release_notes.",
		rng.From, rng.To, rng.From, rng.To,
	)
	if mctx != nil {
		kickoff = enrichKickoff(ctx, client, mctx.Namespace, "release notes "+rng.String(), kickoff, mctx)
	}
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return nil, err
	}
	if res.TerminalTool != agent.ToolSubmitReleaseNotes {
		if res.Stopped {
			return nil, fmt.Errorf("agent did not submit release notes: %s", res.StopReason)
		}
		return nil, errors.New("agent ended without calling submit_release_notes")
	}
	notes, err := agent.ParseReleaseNotes(res.TerminalInput)
	if err != nil {
		return nil, fmt.Errorf("parse submitted notes: %w", err)
	}
	return &notes, nil
}

func emitReleaseNotes(cmd *cobra.Command, e *env, opts releaseNotesEmit, view releaseNotesView) error {
	out := cmd.OutOrStdout()
	notes := view.Notes
	md := renderReleaseNotesMarkdown(notes)
	view.Markdown = md
	finish := func(text func()) error {
		if opts.json {
			return writeJSON(out, view)
		}
		text()
		return nil
	}

	if opts.dryRun {
		return finish(func() {
			fmt.Fprintln(out, "--- dry run: structured release notes ---")
			fmt.Fprint(out, md)
		})
	}

	switch opts.output {
	case outputStdout:
		return finish(func() { fmt.Fprint(rawWriter(out), md) })

	case outputFile:
		if err := prependChangelog(opts.changelog, md); err != nil {
			return Fail(CodeError, err)
		}
		view.File = opts.changelog
		return finish(func() { fmt.Fprintf(out, "Wrote release notes to %s\n", opts.changelog) })

	case outputProposal:
		if _, err := e.requireSite(); err != nil {
			return err
		}
		req := api.ReleaseNotesRequest{
			SpaceSlug: opts.space,
			Title:     notes.Title,
			Summary:   notes.Summary,
			Sections:  toAPISections(notes),
		}
		resp, err := e.client.CreateReleaseNotes(cmd.Context(), e.cfg.Site, req)
		if err != nil {
			return Fail(CodeError, fmt.Errorf("create release-notes proposal: %w", err))
		}
		view.Proposal = resp
		return finish(func() {
			fmt.Fprintf(out, "Proposed release notes: %s  (space %s)\n", resp.PageSlug, opts.space)
			fmt.Fprintf(out, "Status:   %s\n", resp.Status)
			fmt.Fprintf(out, "Proposal: %s\n", resp.ProposalID)
			fmt.Fprintf(out, "Review:   %s\n", resp.ReviewURL)
		})
	}
	return Failf(CodeError, "unhandled output mode %q", opts.output)
}

func toAPISections(notes *agent.ReleaseNotesInput) []api.ReleaseNoteSection {
	var secs []api.ReleaseNoteSection
	for _, s := range notes.Sections {
		secs = append(secs, api.ReleaseNoteSection{Heading: s.Heading, Items: s.Items})
	}
	return secs
}

func renderReleaseNotesMarkdown(notes *agent.ReleaseNotesInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", strings.TrimSpace(notes.Title))
	if s := strings.TrimSpace(notes.Summary); s != "" {
		fmt.Fprintf(&b, "%s\n\n", s)
	}
	for _, sec := range notes.Sections {
		if len(sec.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", strings.TrimSpace(sec.Heading))
		for _, item := range sec.Items {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(item))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func prependChangelog(path, md string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		header := "# Changelog\n\n"
		return os.WriteFile(path, []byte(header+md), 0o644)
	}

	content := string(existing)
	if strings.HasPrefix(content, "# ") {
		if idx := strings.Index(content, "\n"); idx >= 0 {
			head := content[:idx+1]
			rest := strings.TrimLeft(content[idx+1:], "\n")
			combined := head + "\n" + md
			if rest != "" {
				combined += "\n" + rest
			}
			return os.WriteFile(path, []byte(combined), 0o644)
		}
	}
	combined := md
	if strings.TrimSpace(content) != "" {
		combined += "\n" + content
	}
	return os.WriteFile(path, []byte(combined), 0o644)
}
