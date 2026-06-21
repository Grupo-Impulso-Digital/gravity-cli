package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/agent"
	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/git"
	"github.com/impulso/gravity-cli/internal/prompts"
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
		ci        bool
		changelog string
	)
	cmd := &cobra.Command{
		Use:   "release-notes",
		Short: "Generate release notes from git history via the AI harness",
		Long: `Resolve a git commit range, run the release-notes agent over it, and emit
structured notes. The agent inspects commits and diffs and groups user-facing
changes into sections, ending by calling submit_release_notes.

Output modes:
  proposal  POST a draft + proposal to Gravity and print the review URL (default)
  file      write/prepend the notes to CHANGELOG.md
  stdout    print the notes as markdown`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputFile, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|file|stdout)", output)
			}

			e, err := resolveEnv(*gf, space)
			if err != nil {
				return err
			}
			// The agent loop always runs through the gateway, so auth is
			// required for every output mode.
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

			logw := logWriter(cmd, ci)
			fmt.Fprintf(logw, "release-notes: range %s\n", rng.String())

			commits, err := repo.Log(cmd.Context(), rng.From, rng.To, 0)
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(commits) == 0 {
				return Failf(CodeError, "no commits in range %s", rng.String())
			}
			fmt.Fprintf(logw, "release-notes: %d commit(s) in range\n", len(commits))

			notes, err := runReleaseNotesAgent(cmd.Context(), e.client, repo, rng, logw)
			if err != nil {
				return Fail(CodeError, err)
			}
			if title != "" {
				notes.Title = title
			}
			if strings.TrimSpace(notes.Title) == "" {
				notes.Title = defaultTitle(rng)
			}

			return emitReleaseNotes(cmd, e, space, output, dryRun, changelog, notes)
		},
	}
	cmd.Flags().StringVar(&space, "space", "changelog", "target space slug")
	cmd.Flags().StringVar(&from, "from", "", "start git ref (default: latest tag, or first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref (default: HEAD)")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|file|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the structured notes without writing or posting")
	cmd.Flags().StringVar(&title, "title", "", "override the release-notes title")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().StringVar(&changelog, "changelog", "CHANGELOG.md", "path for --output file")
	return cmd
}

// logWriter returns where progress logs go. Logs always go to stderr (whether
// or not --ci is set) so stdout stays clean for machine consumption; the ci
// flag is retained for callers that want to vary verbosity in the future.
func logWriter(cmd *cobra.Command, ci bool) io.Writer {
	_ = ci
	return cmd.ErrOrStderr()
}

func defaultTitle(rng git.Range) string {
	to := rng.To
	if to == "HEAD" || to == "" {
		return "Unreleased"
	}
	return to
}

// runReleaseNotesAgent runs the agent loop and returns the parsed submission.
func runReleaseNotesAgent(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, logw io.Writer) (*agent.ReleaseNotesInput, error) {
	tools := append(agent.GitTools(repo), agent.SubmitReleaseNotesTool())
	runner := &agent.Runner{
		Client: client,
		System: prompts.ReleaseNotes,
		Tools:  tools,
		Log:    logw,
	}
	kickoff := fmt.Sprintf(
		"Generate release notes for the changes between %s and %s. "+
			"Start by listing the commits with git_log(from=%q, to=%q), then inspect the diffs. "+
			"When finished, call submit_release_notes.",
		rng.From, rng.To, rng.From, rng.To,
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return nil, err
	}
	if res.TerminalTool != agent.ToolSubmitReleaseNotes {
		if res.Stopped {
			return nil, fmt.Errorf("agent did not submit release notes: %s", res.StopReason)
		}
		return nil, fmt.Errorf("agent ended without calling submit_release_notes")
	}
	notes, err := agent.ParseReleaseNotes(res.TerminalInput)
	if err != nil {
		return nil, fmt.Errorf("parse submitted notes: %w", err)
	}
	return &notes, nil
}

func emitReleaseNotes(cmd *cobra.Command, e *env, space, output string, dryRun bool, changelogPath string, notes *agent.ReleaseNotesInput) error {
	out := cmd.OutOrStdout()
	md := renderReleaseNotesMarkdown(notes)

	if dryRun {
		fmt.Fprintln(out, "--- dry run: structured release notes ---")
		fmt.Fprint(out, md)
		return nil
	}

	switch output {
	case outputStdout:
		fmt.Fprint(out, md)
		return nil

	case outputFile:
		if err := prependChangelog(changelogPath, md); err != nil {
			return Fail(CodeError, err)
		}
		fmt.Fprintf(out, "Wrote release notes to %s\n", changelogPath)
		return nil

	case outputProposal:
		if e.cfg.Site == "" {
			if _, err := e.requireSite(); err != nil {
				return err
			}
		}
		req := api.ReleaseNotesRequest{
			SpaceSlug: space,
			Title:     notes.Title,
			Summary:   notes.Summary,
			Sections:  toAPISections(notes),
		}
		resp, err := e.client.CreateReleaseNotes(cmd.Context(), e.cfg.Site, req)
		if err != nil {
			return Fail(CodeError, fmt.Errorf("create release-notes proposal: %w", err))
		}
		fmt.Fprintf(out, "Proposed release notes: %s\n", resp.PageSlug)
		fmt.Fprintf(out, "Status:   %s\n", resp.Status)
		fmt.Fprintf(out, "Proposal: %s\n", resp.ProposalID)
		fmt.Fprintf(out, "Review:   %s\n", resp.ReviewURL)
		return nil
	}
	return Failf(CodeError, "unhandled output mode %q", output)
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

// prependChangelog inserts md at the top of the changelog (after any leading
// title), creating the file if needed.
func prependChangelog(path, md string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		// New file.
		header := "# Changelog\n\n"
		return os.WriteFile(path, []byte(header+md), 0o644)
	}

	content := string(existing)
	// If the file starts with a top-level "# " title, keep it on top.
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
