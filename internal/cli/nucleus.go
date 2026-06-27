package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/agent"
	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/config"
	"github.com/impulso/gravity-cli/internal/git"
	"github.com/impulso/gravity-cli/internal/prompts"
)

// knowledgeNamespace resolves the nucleus namespace: the resolved config value,
// falling back to the site so single-repo setups work with zero extra config.
func (e *env) knowledgeNamespace() string {
	if e.cfg.Namespace != "" {
		return e.cfg.Namespace
	}
	return e.cfg.Site
}

// enrichKickoff prepends relevant nucleus atoms to an agent kickoff. It is
// strictly best-effort: any error (including the feature being unavailable) or
// an empty result returns the original kickoff, so it can never regress an
// existing command.
func enrichKickoff(ctx context.Context, client *api.Client, namespace, query, kickoff string, mctx *api.MessagesContext) string {
	if namespace == "" {
		return kickoff
	}
	site, space := "", ""
	if mctx != nil {
		site, space = mctx.Site, mctx.Space
	}
	atoms, err := client.QueryAtoms(ctx, namespace, api.AtomQuery{Query: query, Site: site, Space: space, Limit: 8})
	if err != nil || len(atoms) == 0 {
		return kickoff
	}
	var b strings.Builder
	b.WriteString("Relevant project memory (atoms; use as context, do not invent beyond them):\n")
	for _, a := range atoms {
		fmt.Fprintf(&b, "- %s\n", a.Content)
	}
	b.WriteString("\n")
	return b.String() + kickoff
}

func newNucleusCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nucleus",
		Short: "Query and contribute nucleus memory atoms (preview)",
		Long: `Nucleus is the Gravity memory service: small "atoms" of knowledge that link to
other atoms, so the AI can recall product context without re-reading whole docs.

This is a preview: the nucleus API is not live yet. Until it ships, these
commands report unavailability and exit 0 (pass --require to fail instead). Atom
retrieval also augments release-notes / check docs generation, best-effort.`,
	}
	cmd.AddCommand(newNucleusQueryCmd(gf), newNucleusSyncCmd(gf))
	return cmd
}

func newNucleusQueryCmd(gf *globalFlags) *cobra.Command {
	var (
		namespace string
		format    string
		limit     int
		require   bool
	)
	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Retrieve relevant memory atoms for a query",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTextJSON(format); err != nil {
				return err
			}
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			ns := firstNonEmpty(namespace, e.knowledgeNamespace())
			if ns == "" {
				return Failf(CodeError, "no knowledge namespace; set knowledge.namespace in %s or pass --namespace", config.ProjectFileName)
			}
			atoms, err := e.client.QueryAtoms(cmd.Context(), ns, api.AtomQuery{
				Query: strings.Join(args, " "), Site: e.cfg.Site, Space: e.cfg.Space, Limit: limit,
			})
			if err != nil {
				if skipped, ferr := skippableFeature(err, "nucleus memory", cmd.ErrOrStderr(), require); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("query atoms: %w", err))
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				return writeJSON(out, atoms)
			}
			if len(atoms) == 0 {
				fmt.Fprintln(out, "(no atoms)")
				return nil
			}
			for _, a := range atoms {
				fmt.Fprintf(out, "- %s\n", a.Content)
				if len(a.Tags) > 0 {
					fmt.Fprintf(out, "    tags: %s\n", strings.Join(a.Tags, ", "))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "knowledge namespace (default: config knowledge.namespace, else site)")
	cmd.Flags().IntVar(&limit, "limit", 8, "max atoms to return")
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable nucleus as a hard error (exit 2)")
	return cmd
}

func newNucleusSyncCmd(gf *globalFlags) *cobra.Command {
	var (
		namespace string
		from      string
		to        string
		ci        bool
		require   bool
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Distill memory atoms from code changes and contribute them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			ns := firstNonEmpty(namespace, e.knowledgeNamespace())
			if ns == "" {
				return Failf(CodeError, "no knowledge namespace; set knowledge.namespace in %s or pass --namespace", config.ProjectFileName)
			}
			out := cmd.OutOrStdout()

			// Pre-check availability so we don't spend LLM calls distilling atoms
			// the platform can't store yet.
			avail, err := featureAvailable(cmd.Context(), e.client, featureNucleus)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("whoami: %w", classifyDoctorErr(err)))
			}
			if !avail {
				if require {
					return Failf(CodeError, "nucleus memory is not yet available on this platform")
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "note: nucleus memory is not yet available on this platform; skipping")
				return nil
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
			atoms, err := runNucleusDistill(cmd.Context(), e.client, repo, rng, logw,
				&api.MessagesContext{Site: e.cfg.Site, Space: e.cfg.Space, Namespace: ns})
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(atoms.Atoms) == 0 {
				fmt.Fprintln(out, "no durable atoms distilled from this range")
				return nil
			}
			if dryRun {
				for _, a := range atoms.Atoms {
					fmt.Fprintf(out, "- %s\n", a.Content)
				}
				return nil
			}

			gen := "gravity nucleus sync v" + version
			n := 0
			for _, a := range atoms.Atoms {
				_, err := e.client.UpsertAtom(cmd.Context(), ns, api.Atom{
					Content: a.Content,
					Tags:    a.Tags,
					Links:   a.Links,
					Scope:   &api.AtomScope{Namespace: ns, Site: e.cfg.Site, Space: e.cfg.Space},
					Source:  &api.AtomSource{Kind: "code", Generator: gen},
				})
				if err != nil {
					if skipped, ferr := skippableFeature(err, "nucleus memory", cmd.ErrOrStderr(), require); skipped {
						return nil
					} else if ferr != nil {
						return ferr
					}
					return Fail(CodeError, fmt.Errorf("upsert atom: %w", err))
				}
				n++
			}
			fmt.Fprintf(out, "Contributed %d atom(s) to namespace %q\n", n, ns)
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "knowledge namespace (default: config knowledge.namespace, else site)")
	cmd.Flags().StringVar(&from, "from", "", "start git ref (default: latest tag/first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref (default: HEAD)")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable nucleus as a hard error (exit 2)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print distilled atoms without contributing them")
	return cmd
}

func runNucleusDistill(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, logw io.Writer, mctx *api.MessagesContext) (agent.AtomsInput, error) {
	tools := append(agent.GitTools(repo), agent.SubmitAtomsTool())
	runner := &agent.Runner{
		Client:  client,
		System:  prompts.NucleusDistill,
		Tools:   tools,
		Context: mctx,
		Log:     logw,
	}
	kickoff := fmt.Sprintf(
		"Distill durable memory atoms from the changes between %s and %s. "+
			"Start with git_log(from=%q, to=%q), inspect the diffs, then call submit_atoms.",
		rng.From, rng.To, rng.From, rng.To,
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return agent.AtomsInput{}, err
	}
	if res.TerminalTool != agent.ToolSubmitAtoms {
		if res.Stopped {
			return agent.AtomsInput{}, fmt.Errorf("nucleus distill did not finish: %s", res.StopReason)
		}
		return agent.AtomsInput{}, errors.New("nucleus distill ended without calling submit_atoms")
	}
	return agent.ParseAtoms(res.TerminalInput)
}
