package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

const maxMemoryTags = 32

func (e *env) knowledgeNamespace() string {
	if e.cfg.Namespace != "" {
		return e.cfg.Namespace
	}
	return e.cfg.Site
}

func enrichKickoff(ctx context.Context, client *api.Client, namespace, query, kickoff string, mctx *api.MessagesContext) string {
	site := ""
	if mctx != nil {
		site = mctx.Site
	}
	res, err := client.Recall(ctx, site, api.RecallRequest{Query: query, Limit: 8, Namespace: namespace})
	if err != nil || res == nil || len(res.Hits) == 0 {
		return kickoff
	}
	var b strings.Builder
	b.WriteString("Relevant project memory (use as context, do not invent beyond it):\n")
	for _, h := range res.Hits {
		fmt.Fprintf(&b, "- %s: %s\n", h.Title, h.Body)
	}
	b.WriteString("\n")
	return b.String() + kickoff
}

func newNucleusCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nucleus",
		Short: "Recall and contribute Nucleus memories",
		Long: `Nucleus is the Gravity memory service: small, titled facts the AI recalls
without re-reading whole documents.

Memories are idempotent by title at their scope — re-contributing the same title
revises that memory instead of creating a duplicate. Recall also augments
release-notes / check docs generation, best-effort.

The Nucleus module is enabled per organization; where it is off, these commands
report it and exit 0 (pass --require to fail instead).`,
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
		Short: "Recall the memories most relevant to a query",
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
			req := api.RecallRequest{
				Query:     strings.Join(args, " "),
				Limit:     limit,
				Namespace: ns,
				SpaceID:   spaceID(cmd.Context(), e.client, e.cfg.Site, e.cfg.Space),
			}
			res, err := e.client.Recall(cmd.Context(), e.cfg.Site, req)
			if err != nil {
				if skipped, ferr := skippableFeature(err, "nucleus memory", cmd.ErrOrStderr(), require); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("recall: %w", err))
			}
			out := cmd.OutOrStdout()
			if format == "json" {
				return writeJSON(out, res.Hits)
			}
			if len(res.Hits) == 0 {
				fmt.Fprintln(out, "(no memories)")
				return nil
			}
			for _, h := range res.Hits {
				fmt.Fprintf(out, "- %s: %s\n", h.Title, h.Body)
				if len(h.Tags) > 0 {
					fmt.Fprintf(out, "    tags: %s\n", strings.Join(h.Tags, ", "))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "knowledge namespace (default: config knowledge.namespace, else site)")
	cmd.Flags().IntVar(&limit, "limit", 8, "max memories to return")
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable nucleus as a hard error (exit 2)")
	return cmd
}

func newNucleusSyncCmd(gf *globalFlags) *cobra.Command {
	var (
		namespace string
		from      string
		to        string
		require   bool
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Distill memories from code changes and contribute them",
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

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, err)
			}
			rng, err := repo.ResolveRange(cmd.Context(), from, to)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("resolve range: %w", err))
			}
			commits, err := repo.Log(cmd.Context(), rng.From, rng.To, 1)
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(commits) == 0 {
				fmt.Fprintf(out, "no commits in range %s; nothing to do\n", rng.String())
				return nil
			}
			logw := logWriter(cmd)
			atoms, err := runNucleusDistill(cmd.Context(), e.client, repo, rng, logw,
				&api.MessagesContext{Site: e.cfg.Site, Space: e.cfg.Space, Namespace: ns})
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(atoms.Atoms) == 0 {
				fmt.Fprintln(out, "no durable memories distilled from this range")
				return nil
			}
			if dryRun {
				for _, a := range atoms.Atoms {
					fmt.Fprintf(out, "- %s: %s\n", a.Title, a.Body)
				}
				return nil
			}

			scope, spaceIDVal := api.MemoryScopeOrg, ""
			if e.cfg.Site != "" {
				scope = api.MemoryScopeSite
				spaceIDVal = spaceID(cmd.Context(), e.client, e.cfg.Site, e.cfg.Space)
			}
			n := 0
			for _, a := range atoms.Atoms {
				if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" {
					continue
				}
				_, err := e.client.UpsertMemory(cmd.Context(), api.MemoryUpsertRequest{
					Title:    a.Title,
					Body:     a.Body,
					Kind:     a.Kind,
					Tags:     memoryTags(ns, e.proj, a.Tags),
					Scope:    scope,
					SiteSlug: e.cfg.Site,
					SpaceID:  spaceIDVal,
				})
				if err != nil {
					if skipped, ferr := skippableFeature(err, "nucleus memory", cmd.ErrOrStderr(), require); skipped {
						return nil
					} else if ferr != nil {
						return ferr
					}
					return Fail(CodeError, fmt.Errorf("upsert memory: %w", err))
				}
				n++
			}
			fmt.Fprintf(out, "Contributed %d memory/memories to namespace %q\n", n, ns)
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "knowledge namespace (default: config knowledge.namespace, else site)")
	cmd.Flags().StringVar(&from, "from", "", "start git ref (default: latest tag/first commit)")
	cmd.Flags().StringVar(&to, "to", "", "end git ref (default: HEAD)")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable nucleus as a hard error (exit 2)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print distilled memories without contributing them")
	return cmd
}

func memoryTags(namespace string, proj *config.Project, tags []string) []string {
	out := []string{"ns:" + namespace}
	if proj != nil && proj.Product.Repo != "" {
		out = append(out, "repo:"+proj.Product.Repo)
	}
	seen := map[string]bool{}
	deduped := make([]string, 0, len(out)+len(tags))
	for _, t := range append(out, tags...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] || len(deduped) >= maxMemoryTags {
			continue
		}
		seen[t] = true
		deduped = append(deduped, t)
	}
	return deduped
}

var spaceIDCache sync.Map

func spaceID(ctx context.Context, client *api.Client, site, spaceSlug string) string {
	if site == "" || spaceSlug == "" {
		return ""
	}
	key := site + "\x00" + spaceSlug
	if v, ok := spaceIDCache.Load(key); ok {
		return v.(string)
	}
	id := ""
	if tree, err := client.SiteTree(ctx, site); err == nil {
		for _, s := range tree.Spaces {
			if s.Slug == spaceSlug {
				id = s.ID
				break
			}
		}
	}
	spaceIDCache.Store(key, id)
	return id
}

func runNucleusDistill(ctx context.Context, client *api.Client, repo *git.Repo, rng git.Range, logw io.Writer, mctx *api.MessagesContext) (agent.AtomsInput, error) {
	tools := append(agent.GitToolsAt(repo, rng.To), agent.SubmitAtomsTool())
	runner := &agent.Runner{
		Client:  client,
		System:  resolvePrompt(ctx, client, prompts.NameNucleus, logw),
		Tools:   tools,
		Context: mctx,
		Log:     logw,
	}
	kickoff := fmt.Sprintf(
		"Distill durable memories from the changes between %s and %s. "+
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
