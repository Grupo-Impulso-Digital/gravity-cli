package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

func newDocsCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation from the codebase (preview)",
		Long: `Scan the repository with the AI harness and author documentation pages for
three audiences — public, users, and developers — as draft proposals on the
Gravity platform.

This is a preview: it gates on platform support and, until the server features
are live, reports unavailability and exits 0 (pass --require to fail instead).`,
	}
	cmd.AddCommand(newDocsGenerateCmd(gf))
	return cmd
}

func newDocsGenerateCmd(gf *globalFlags) *cobra.Command {
	var (
		site        string
		space       string
		audiences   []string
		pageSlug    string
		output      string
		dryRun      bool
		require     bool
		from        string
		save        string
		since       string
		kinds       []string
		noInventory bool
	)
	cmd := &cobra.Command{
		Use:     "generate",
		Aliases: []string{"init"},
		Short:   "Author multi-audience docs from the codebase",
		Long: `Two-phase AI authoring: first inventory the repository's documentable units
and plan the pages that cover them, then author each page's blocks for the
public/users/developers audiences. Existing pages are read first so a re-run
updates blocks in place instead of duplicating them. Every write is a draft +
open proposal — nothing is published directly.

Audience is a per-block attribute: one page can carry blocks for different
audiences, and the platform renders the blocks matching the viewer.

In CI, run it change-scoped:

  gravity docs generate --since <last successful run's sha> --ci

--since restricts the survey to what <ref>..HEAD touched and re-authors only the
pages whose units or bound sources changed; every other page is skipped at zero
LLM cost. An empty change set exits 0. Use the previous SUCCESSFUL run's commit,
never HEAD~1, or a failed run becomes a documentation gap.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|stdout)", output)
			}
			if err := docs.ValidAudiences(audiences); err != nil {
				return Failf(CodeError, "%v", err)
			}
			if len(audiences) == 0 {
				return Failf(CodeError, "no audiences selected")
			}
			if since != "" && from != "" {
				return Failf(CodeError, "--since and --from are mutually exclusive (--from replays an already-authored set)")
			}
			wantKinds, err := parseUnitKinds(kinds)
			if err != nil {
				return Fail(CodeError, err)
			}
			if site != "" {
				gf.site = site
			}
			e, err := resolveEnv(*gf, space)
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			siteSlug, err := e.requireSite()
			if err != nil {
				return err
			}

			if from != "" {
				targets, err := loadTargets(from)
				if err != nil {
					return Fail(CodeError, fmt.Errorf("load %s: %w", from, err))
				}
				return finishDocs(cmd, e, siteSlug, targets, output, dryRun, from)
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, err)
			}
			logw := logWriter(cmd)

			changes, err := resolveChangeScope(cmd.Context(), repo, e.proj, since)
			if err != nil {
				return err
			}
			if since != "" && changes.Len() == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no changes since %s; nothing to do\n", since)
				return nil
			}
			if changes != nil {
				fmt.Fprintf(logw, "docs: change-scoped run — %d file(s) changed since %s\n", changes.Len(), since)
			}

			who, err := e.client.WhoAmI(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("whoami: %w", err))
			}
			if !who.Features[featureDocsGenerate] {
				if require {
					return Failf(CodeError, "docs generation is not yet available on this platform")
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "note: docs generation is not yet available on this platform; skipping")
				return nil
			}
			audienceOK := who.Features[featureBlockAudience]
			if !audienceOK {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: block-level audiences not yet supported by this platform; blocks will render to everyone")
			}

			mctx := &api.MessagesContext{Site: siteSlug, Space: e.cfg.Space, Namespace: e.cfg.Namespace}
			planPrompt := resolvePrompt(cmd.Context(), e.client, prompts.NameDocsPlan, logw)
			authorPrompt := resolvePrompt(cmd.Context(), e.client, prompts.NameDocsAuthor, logw)

			existing, err := e.client.ListPages(cmd.Context(), siteSlug, api.PageListOptions{IncludeDraft: true})
			if err != nil {
				return Fail(CodeError, fmt.Errorf("fetch pages: %w", err))
			}
			byPage := make(map[string][]api.ContentBlock, len(existing))
			for _, p := range existing {
				byPage[p.SpaceSlug+"\x00"+p.Slug] = p.Blocks
			}

			repoRef := repoRefFor(cmd.Context(), repo, e.proj)
			myRepoID := repoIDFor(existing, repoRef)

			plan, err := runDocsPlan(cmd.Context(), e.client, repo, planPrompt, planScope{
				audiences: audiences,
				existing:  existing,
				proj:      e.proj,
				changes:   changes,
				kinds:     wantKinds,
			}, logw, mctx)
			if err != nil {
				return Fail(CodeError, err)
			}
			plan = filterPlanKinds(plan, wantKinds)
			reportOrphans(existing, plan, myRepoID, logw)

			planned := resolvePlannedPages(plan, e.proj, space, e.cfg.Space, audiences)

			switch {
			case noInventory:
			case !who.Features[featureInventory]:
				fmt.Fprintln(cmd.ErrOrStderr(), "note: feature inventory is not yet available on this platform; skipping")
			default:
				publishInventory(cmd, e, siteSlug, inventoryFor(repo.Root, plan, planned, e.proj, repoRef,
					since == "" && pageSlug == ""), logw)
			}

			generator := "gravity docs generate v" + version.String()
			changedUnits := changedUnitKeys(plan, changes)
			var targets []syncTarget
			var failedPages []string
			for i, p := range planned {
				pg := p.plan
				if pageSlug != "" && pg.Slug != pageSlug {
					continue
				}
				if changes != nil && !shouldAuthor(p, byPage[p.space+"\x00"+p.slug], changedUnits, changes) {
					fmt.Fprintf(logw, "skip %s/%s (unchanged since %s)\n", p.space, p.slug, since)
					continue
				}
				pg.Audiences = p.audiences
				fmt.Fprintf(logw, "agent: authoring page %d/%d: %s/%s (%s)\n", i+1, len(planned), p.space, p.slug, strings.Join(p.audiences, "+"))

				authored, stopped, err := runDocsAuthor(cmd.Context(), e.client, repo, authorPrompt, pg, byPage[p.space+"\x00"+p.slug], logw, mctx)
				if err != nil {
					if cmd.Context().Err() != nil {
						return Fail(CodeError, err)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: page %q failed to author (%v); continuing with the remaining pages\n", pg.Slug, err)
					failedPages = append(failedPages, pg.Slug)
					continue
				}
				if stopped {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: skipped page %q (author run hit a cap before completing)\n", pg.Slug)
					failedPages = append(failedPages, pg.Slug)
					continue
				}
				blocks, err := docs.AssembleBlocks(repo.Root, generator, toAuthored(authored))
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: page %q authored invalid blocks (%v); continuing with the remaining pages\n", pg.Slug, err)
					failedPages = append(failedPages, pg.Slug)
					continue
				}
				if !audienceOK {
					for i := range blocks {
						blocks[i].Audiences = nil
					}
				}
				title := firstNonEmpty(authored.Title, pg.Title, pg.Slug)
				page := api.PageUpsertRequest{SpaceSlug: p.space, Slug: p.slug, Title: title, Collection: p.collection, Blocks: blocks}
				if who.Features[featureRepos] && repoRef != nil {
					page.Repo = repoRef
				}
				if who.Features[featurePageLanguages] && e.proj != nil {
					page.Languages = e.proj.I18n.Languages
				}
				targets = append(targets, syncTarget{
					kind:  "page",
					space: p.space,
					label: fmt.Sprintf("docs %s -> %s/%s", strings.Join(p.audiences, "+"), p.space, p.slug),
					home:  p.home,
					page:  page,
				})
			}

			if len(targets) == 0 && len(failedPages) > 0 {
				return Failf(CodeError, "all %d planned page(s) failed to author: %s", len(failedPages), strings.Join(failedPages, ", "))
			}

			artifact := save
			if artifact == "" {
				artifact = defaultDocsArtifact(repo.Root)
			}
			if err := finishDocs(cmd, e, siteSlug, targets, output, dryRun, artifact); err != nil {
				return err
			}
			if len(failedPages) > 0 {
				return Failf(CodeError, "%d page(s) were not authored (%s); re-run with --page <slug> to retry just those",
					len(failedPages), strings.Join(failedPages, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug")
	cmd.Flags().StringVar(&space, "space", "", "override the target space for all pages")
	cmd.Flags().StringSliceVar(&audiences, "audiences",
		[]string{api.AudiencePublic, api.AudienceUsers, api.AudienceDevelopers},
		"audiences to author: public,users,developers")
	cmd.Flags().StringVar(&pageSlug, "page", "", "author only the planned page with this slug")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without calling the API")
	cmd.Flags().BoolVar(&require, "require", false, "treat unavailable docs generation as a hard error (exit 2)")
	cmd.Flags().StringVar(&from, "from", "", "replay a saved doc set through sync, skipping the AI phases (use the file printed by a prior run)")
	cmd.Flags().StringVar(&save, "save", "", "path to persist the authored doc set for replay (default .gravity/generated/docs.json)")
	cmd.Flags().StringVar(&since, "since", "", "change-scoped run: survey and re-author only what <ref>..HEAD touched (use the previous successful run's sha, not HEAD~1)")
	cmd.Flags().StringSliceVar(&kinds, "units", nil, "restrict the plan to these unit kinds: feature,service,system,api,capability")
	cmd.Flags().BoolVar(&noInventory, "no-inventory", false, "skip publishing the unit inventory to the platform (author only)")
	return cmd
}

func parseUnitKinds(kinds []string) (map[string]bool, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	out := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		switch k {
		case api.UnitKindFeature, api.UnitKindService, api.UnitKindSystem, api.UnitKindAPI, api.UnitKindCapability:
			out[k] = true
		default:
			return nil, fmt.Errorf("invalid --units %q (want feature|service|system|api|capability)", k)
		}
	}
	return out, nil
}

func resolveChangeScope(ctx context.Context, repo *git.Repo, proj *config.Project, since string) (*docs.ChangeSet, error) {
	if since == "" {
		return nil, nil
	}
	if _, err := repo.ResolveRef(ctx, since); err != nil {
		return nil, Fail(CodeError, fmt.Errorf("--since %s: %w", since, err))
	}
	files, err := repo.ChangedFiles(ctx, since, "HEAD")
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	return docs.NewChangeSet(since, discoveryScope(proj).Filter(files)), nil
}

func discoveryScope(proj *config.Project) docs.Scope {
	if proj == nil {
		return docs.Scope{}
	}
	return docs.Scope{Include: proj.Discovery.Include, Exclude: proj.Discovery.Exclude}
}

func repoRefFor(ctx context.Context, repo *git.Repo, proj *config.Project) *api.RepoRef {
	name := ""
	if proj != nil {
		name = proj.Product.Repo
	}
	remote, _ := repo.RemoteURL(ctx)
	key := git.NormalizeRemoteKey(remote)
	if key == "" && proj != nil && proj.Product.Slug != "" && proj.Product.Repo != "" {
		key = proj.Product.Slug + "/" + proj.Product.Repo
	}
	if key == "" {
		return nil
	}
	if name == "" {
		name = key[strings.LastIndex(key, "/")+1:]
	}
	return &api.RepoRef{RemoteKey: key, Name: name}
}

func repoIDFor(existing []api.Page, ref *api.RepoRef) string {
	if ref == nil {
		return ""
	}
	for _, p := range existing {
		if p.RepoID != nil && p.RepoRemoteKey != nil && *p.RepoRemoteKey == ref.RemoteKey {
			return *p.RepoID
		}
	}
	return ""
}

type plannedPage struct {
	plan       agent.DocPlanPage
	space      string
	slug       string
	collection string
	home       bool
	audiences  []string
}

func resolvePlannedPages(plan agent.DocPlanInput, proj *config.Project, spaceFlag, cfgSpace string, want []string) []plannedPage {
	out := make([]plannedPage, 0, len(plan.Pages))
	for _, pg := range plan.Pages {
		aud := pageAudiences(pg, proj, want)
		if len(aud) == 0 {
			continue
		}
		sp := firstNonEmpty(spaceFlag, pg.Space, audienceSpace(proj, aud), cfgSpace, "docs")
		slug, col, home := pg.Slug, "", false
		if proj != nil {
			home = proj.Spaces.Home != "" && sp == proj.Spaces.Default && slug == proj.Spaces.Home
			sp, slug, col = proj.PageTarget(sp, slug, "")
		}
		out = append(out, plannedPage{plan: pg, space: sp, slug: slug, collection: col, home: home, audiences: aud})
	}
	return out
}

func pageAudiences(pg agent.DocPlanPage, proj *config.Project, want []string) []string {
	if len(pg.Audiences) > 0 {
		return intersect(pg.Audiences, want)
	}
	if proj != nil && len(proj.Discovery.Audiences.Default) > 0 {
		if aud := intersect(proj.Discovery.Audiences.Default, want); len(aud) > 0 {
			return aud
		}
	}
	return want
}

var devSpaceNames = map[string]bool{"developers": true, "developer": true, "dev": true, "dev-docs": true, "api": true}

func audienceSpace(proj *config.Project, audiences []string) string {
	if proj == nil || len(audiences) == 0 {
		return ""
	}
	if s := proj.SpaceForAudiences(audiences); s != "" {
		return s
	}
	return devNamedSpace(proj, audiences)
}

func devNamedSpace(proj *config.Project, audiences []string) string {
	if len(audiences) != 1 || audiences[0] != api.AudienceDevelopers {
		return ""
	}
	for _, s := range proj.Spaces.Shared {
		if devSpaceNames[strings.ToLower(s)] {
			return s
		}
	}
	for _, s := range proj.Sources {
		if devSpaceNames[strings.ToLower(s.Space)] {
			return s.Space
		}
	}
	for _, d := range proj.Documents {
		if devSpaceNames[strings.ToLower(d.Space)] {
			return d.Space
		}
	}
	return ""
}

func filterPlanKinds(plan agent.DocPlanInput, want map[string]bool) agent.DocPlanInput {
	if len(want) == 0 {
		return plan
	}
	kept := make(map[string]bool, len(plan.Units))
	units := make([]agent.DocPlanUnit, 0, len(plan.Units))
	for _, u := range plan.Units {
		if want[u.Kind] {
			kept[u.Key] = true
			units = append(units, u)
		}
	}
	pages := make([]agent.DocPlanPage, 0, len(plan.Pages))
	for _, pg := range plan.Pages {
		if want[pg.Kind] || anyKept(pg.Units, kept) {
			pages = append(pages, pg)
		}
	}
	return agent.DocPlanInput{Pages: pages, Units: units}
}

func anyKept(keys []string, kept map[string]bool) bool {
	for _, k := range keys {
		if kept[k] {
			return true
		}
	}
	return false
}

func changedUnitKeys(plan agent.DocPlanInput, changes *docs.ChangeSet) map[string]bool {
	if changes == nil {
		return nil
	}
	out := make(map[string]bool, len(plan.Units))
	for _, u := range plan.Units {
		if u.Changed || changes.ContainsAny(u.SourceRefs) {
			out[u.Key] = true
		}
	}
	return out
}

func shouldAuthor(p plannedPage, existing []api.ContentBlock, changedUnits map[string]bool, changes *docs.ChangeSet) bool {
	for _, key := range p.plan.Units {
		if changedUnits[key] {
			return true
		}
	}
	for _, b := range existing {
		if b.SourceBinding != nil && changes.Contains(b.SourceBinding.Ref) {
			return true
		}
	}
	return changes.ContainsAny(p.plan.Sources)
}

func inventoryFor(repoRoot string, plan agent.DocPlanInput, planned []plannedPage, proj *config.Project, ref *api.RepoRef, replace bool) api.InventoryRequest {
	slugs := map[string][]string{}
	for _, p := range planned {
		for _, key := range p.plan.Units {
			slugs[key] = append(slugs[key], p.slug)
		}
	}
	defaultKind := api.UnitKindFeature
	if proj != nil {
		defaultKind = proj.ResolveUnitKind()
	}
	units := make([]api.InventoryUnit, 0, len(plan.Units))
	for _, u := range plan.Units {
		key := strings.TrimSpace(u.Key)
		if !agent.UnitKeyPattern.MatchString(key) {
			continue
		}
		kind := strings.TrimSpace(u.Kind)
		if kind == "" {
			kind = defaultKind
		}
		pages := slugs[u.Key]
		if len(pages) == 0 {
			pages = resolveUnitSlugs(u.PageSlugs, proj)
		}
		units = append(units, api.InventoryUnit{
			Key:        key,
			Kind:       kind,
			Title:      strings.TrimSpace(u.Title),
			Summary:    u.Summary,
			SourceRefs: u.SourceRefs,
			SourceHash: docs.UnitSourceHash(repoRoot, u.SourceRefs),
			Audiences:  u.Audiences,
			PageSlugs:  dedupe(pages),
		})
	}
	req := api.InventoryRequest{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Replace: replace, Units: units}
	if ref != nil {
		req.Repo = *ref
	}
	return req
}

func resolveUnitSlugs(slugs []string, proj *config.Project) []string {
	if proj == nil {
		return slugs
	}
	out := make([]string, 0, len(slugs))
	for _, s := range slugs {
		_, slug, _ := proj.PageTarget(proj.Spaces.Default, s, "")
		out = append(out, slug)
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func publishInventory(cmd *cobra.Command, e *env, siteSlug string, req api.InventoryRequest, logw io.Writer) {
	errw := cmd.ErrOrStderr()
	if req.Repo.RemoteKey == "" {
		fmt.Fprintln(errw, "note: no repo identity (no git remote and no product.slug/product.repo); skipping the inventory")
		return
	}
	if len(req.Units) == 0 {
		fmt.Fprintln(errw, "note: the plan declared no units; skipping the inventory")
		return
	}
	res, err := e.client.PublishInventory(cmd.Context(), siteSlug, req)
	if err != nil {
		if skipped, _ := skippableFeature(err, "feature inventory", errw, false); skipped {
			return
		}
		fmt.Fprintf(errw, "warning: could not publish the feature inventory (%v); continuing\n", err)
		return
	}
	fmt.Fprintf(logw, "inventory: %d unit(s) — %d created, %d updated, %d unchanged, %d removed\n",
		res.Units.Received, res.Units.Created, res.Units.Updated, res.Units.Unchanged, res.Units.Removed)
}

func defaultDocsArtifact(repoRoot string) string {
	return filepath.Join(repoRoot, ".gravity", "generated", "docs.json")
}

func finishDocs(cmd *cobra.Command, e *env, siteSlug string, targets []syncTarget, output string, dryRun bool, artifactPath string) error {
	out := cmd.OutOrStdout()
	if len(targets) == 0 {
		fmt.Fprintln(out, "no pages authored")
		return nil
	}
	if output == outputStdout {
		printSyncStdout(out, targets)
		return nil
	}
	if dryRun {
		return printSyncDryRun(out, targets)
	}
	if artifactPath != "" {
		if err := saveTargets(artifactPath, targets); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not save authored docs to %s (%v); proceeding to sync\n", artifactPath, err)
			artifactPath = ""
		} else {
			fmt.Fprintf(out, "Saved authored docs to %s\n", artifactPath)
		}
	}
	err := runSync(cmd.Context(), e.client, siteSlug, targets, true, hierarchyFromProject(e.proj), logWriter(cmd), out)
	if err != nil && artifactPath != "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: authored docs are saved at %s — fix the cause and replay with `gravity docs generate --from %s` (no AI re-run)\n",
			artifactPath, artifactPath)
	}
	return err
}

type planScope struct {
	audiences []string
	existing  []api.Page
	proj      *config.Project
	changes   *docs.ChangeSet
	kinds     map[string]bool
}

func runDocsPlan(ctx context.Context, client *api.Client, repo *git.Repo, system string, scope planScope, logw io.Writer, mctx *api.MessagesContext) (agent.DocPlanInput, error) {
	tools := planReadTools(agent.GitTools(repo), scope)
	runner := &agent.Runner{
		Client:        client,
		System:        system,
		Tools:         append(tools, agent.SubmitDocPlanTool()),
		Context:       mctx,
		Log:           logw,
		MaxTokens:     agent.DefaultPlanMaxTokens,
		MaxIterations: agent.DefaultAuthorMaxIterations,
	}
	kickoff := fmt.Sprintf(
		"Survey this repository, inventory its documentable units, and propose documentation pages "+
			"covering these audiences: %s.\n\n"+
			"%s%s%s%s%s"+
			"Start with list_files, read the README and the main entrypoints, then enumerate the "+
			"repository's units before calling submit_doc_plan.",
		strings.Join(scope.audiences, ", "),
		unitsDigest(scope.proj, scope.kinds),
		entrypointsDigest(scope.proj),
		spacesDigest(scope.proj),
		existingPagesDigest(scope.existing),
		scope.changes.Digest(),
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return agent.DocPlanInput{}, err
	}
	if res.TerminalTool != agent.ToolSubmitDocPlan {
		if res.Stopped {
			return agent.DocPlanInput{}, fmt.Errorf("docs plan did not finish: %s", res.StopReason)
		}
		return agent.DocPlanInput{}, errors.New("docs plan ended without calling submit_doc_plan")
	}
	plan, err := agent.ParseDocPlan(res.TerminalInput)
	if err != nil {
		return agent.DocPlanInput{}, fmt.Errorf("docs plan: parsing submit_doc_plan input: %w", err)
	}
	fmt.Fprintf(logw, "agent: plan proposed %d unit(s) and %d page(s)\n", len(plan.Units), len(plan.Pages))
	if len(plan.Pages) == 0 {
		return agent.DocPlanInput{}, errors.New(
			"docs plan proposed zero pages — the model surveyed the repo but submit_doc_plan's " +
				"pages array was empty; retry, or narrow --audiences and check the survey went well",
		)
	}
	return plan, nil
}

func entrypoints(proj *config.Project) []string {
	if proj == nil {
		return nil
	}
	return proj.Discovery.Entrypoints
}

func planReadTools(tools []agent.Tool, scope planScope) []agent.Tool {
	extra := allowSet(entrypoints(scope.proj))
	switch {
	case scope.changes != nil:
		changes := scope.changes
		readable := func(path string) bool { return extra[path] || changes.Contains(path) }
		return scopeReadTools(tools, readable, fmt.Sprintf(
			"this change-scoped run (%s..HEAD); readable files are the changed set and the configured entrypoints", changes.Ref,
		))
	case !discoveryScope(scope.proj).IsZero():
		sc := discoveryScope(scope.proj)
		readable := func(path string) bool { return extra[path] || sc.Allows(path) }
		return scopeReadTools(tools, readable,
			"the manifest's discovery.include/discovery.exclude window; readable files are inside it plus the configured entrypoints")
	default:
		return tools
	}
}

func allowSet(allow []string) map[string]bool {
	out := make(map[string]bool, len(allow))
	for _, p := range allow {
		out[strings.TrimSpace(p)] = true
	}
	return out
}

func scopeReadTools(tools []agent.Tool, readable func(path string) bool, boundary string) []agent.Tool {
	out := make([]agent.Tool, 0, len(tools))
	for _, t := range tools {
		switch t.Def.Name {
		case "read_file", "git_show":
			inner := t.Run
			t.Run = func(ctx context.Context, input json.RawMessage) (string, error) {
				var in struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(input, &in)
				if in.Path != "" && !readable(in.Path) {
					return "", fmt.Errorf("%q is outside %s", in.Path, boundary)
				}
				return inner(ctx, input)
			}
		case "list_files":
			inner := t.Run
			t.Run = func(ctx context.Context, input json.RawMessage) (string, error) {
				out, err := inner(ctx, input)
				if err != nil {
					return "", err
				}
				return filterLines(out, readable), nil
			}
		case "grep":
			inner := t.Run
			t.Run = func(ctx context.Context, input json.RawMessage) (string, error) {
				out, err := inner(ctx, input)
				if err != nil {
					return "", err
				}
				return filterLines(out, func(line string) bool {
					path, _, _ := strings.Cut(line, ":")
					return readable(path)
				}), nil
			}
		}
		out = append(out, t)
	}
	return out
}

func filterLines(s string, keep func(string) bool) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" && keep(line) {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return "(nothing inside this run's change scope)"
	}
	return strings.Join(kept, "\n")
}

func runDocsAuthor(ctx context.Context, client *api.Client, repo *git.Repo, system string, page agent.DocPlanPage, existing []api.ContentBlock, logw io.Writer, mctx *api.MessagesContext) (agent.PageDocInput, bool, error) {
	tools := append(agent.GitTools(repo), agent.SubmitPageDocTool())
	runner := &agent.Runner{
		Client:        client,
		System:        system,
		Tools:         tools,
		Context:       mctx,
		Log:           logw,
		MaxTokens:     agent.DefaultAuthorMaxTokens,
		MaxIterations: agent.DefaultAuthorMaxIterations,
	}
	kickoff := fmt.Sprintf(
		"Author the documentation page %q (slug %q) for audiences: %s.\n"+
			"This page documents a %s unit — write it accordingly.\n"+
			"Intended contents: %s\n"+
			"Relevant source files: %s\n\n"+
			"Existing blocks on this page:\n%s\n\n"+
			"Read the relevant source, then call submit_page_doc with the complete block set.",
		page.Title, page.Slug, strings.Join(page.Audiences, ", "),
		firstNonEmpty(page.Kind, api.UnitKindFeature),
		firstNonEmpty(page.Summary, "(none given)"),
		firstNonEmpty(strings.Join(page.Sources, ", "), "(none given)"),
		pageBlockDigest(existing),
	)
	res, err := runner.Run(ctx, kickoff)
	if err != nil {
		return agent.PageDocInput{}, false, err
	}
	if res.TerminalTool != agent.ToolSubmitPageDoc {
		if res.Stopped {
			return agent.PageDocInput{}, true, nil
		}
		return agent.PageDocInput{}, false, errors.New("docs author ended without calling submit_page_doc")
	}
	parsed, err := agent.ParsePageDoc(res.TerminalInput)
	return parsed, false, err
}

const (
	authorBlockTextLimit = 800
	authorDigestLimit    = 24 * 1024
)

func pageBlockDigest(blocks []api.ContentBlock) string {
	if len(blocks) == 0 {
		return "(none — this is a new page)"
	}
	var b strings.Builder
	for i, blk := range blocks {
		aud := "everyone"
		if len(blk.Audiences) > 0 {
			aud = strings.Join(blk.Audiences, ",")
		}
		key := blk.Key
		if key == "" {
			key = "(server returned no key)"
		}
		entry := fmt.Sprintf("- key=%s type=%s ownership=%s audiences=%s\n", key, blk.Type, blk.Ownership, aud)
		if text := docs.BlockText(blk); text != "" {
			entry += "  text: " + docs.ClipText(text, authorBlockTextLimit) + "\n"
		}
		if b.Len()+len(entry) > authorDigestLimit {
			fmt.Fprintf(&b, "[... %d more block(s) omitted to stay within the digest budget]\n", len(blocks)-i)
			break
		}
		b.WriteString(entry)
	}
	return b.String()
}

func toAuthored(in agent.PageDocInput) []docs.AuthoredBlock {
	out := make([]docs.AuthoredBlock, 0, len(in.Blocks))
	for _, b := range in.Blocks {
		out = append(out, docs.AuthoredBlock{
			Key:       b.Key,
			Type:      b.Type,
			Ownership: b.Ownership,
			Audiences: b.Audiences,
			Content:   b.Content,
			Sources:   b.Sources,
		})
	}
	return out
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, x := range b {
		set[x] = true
	}
	var out []string
	for _, x := range a {
		if set[x] {
			out = append(out, x)
		}
	}
	return out
}

func unitsDigest(proj *config.Project, kinds map[string]bool) string {
	var b strings.Builder
	role, kind := "(unset)", api.UnitKindFeature
	if proj != nil {
		role = firstNonEmpty(proj.Product.Role, "(unset)")
		kind = proj.ResolveUnitKind()
	}
	fmt.Fprintf(&b, "This repository's product.role is %s, so its default unit kind is %q — prefer it unless the evidence says otherwise.\n", role, kind)
	if len(kinds) > 0 {
		want := make([]string, 0, len(kinds))
		for k := range kinds {
			want = append(want, k)
		}
		sort.Strings(want)
		fmt.Fprintf(&b, "This run is restricted to these unit kinds: %s. Do not plan units or pages of any other kind.\n", strings.Join(want, ", "))
	}
	b.WriteString("\n")
	return b.String()
}

func entrypointsDigest(proj *config.Project) string {
	if proj == nil || len(proj.Discovery.Entrypoints) == 0 {
		return ""
	}
	return "Read these entrypoints first — they are the highest-signal files in this repo:\n- " +
		strings.Join(proj.Discovery.Entrypoints, "\n- ") + "\n\n"
}

func spacesDigest(proj *config.Project) string {
	if proj == nil {
		return ""
	}
	declared := make(map[string]config.SpaceDecl, len(proj.Spaces.Declare))
	for _, d := range proj.Spaces.Declare {
		declared[d.Slug] = d
	}
	var b strings.Builder
	b.WriteString("Spaces available to this repo:\n")
	line := func(slug, note string) {
		attrs := []string{}
		if note != "" {
			attrs = append(attrs, note)
		}
		if d, ok := declared[slug]; ok {
			if d.Parent != "" {
				attrs = append(attrs, "under "+d.Parent)
			}
			if d.Type != "" {
				attrs = append(attrs, "type "+d.Type)
			}
			if len(d.Audiences) > 0 {
				attrs = append(attrs, "serves "+strings.Join(d.Audiences, "+"))
			}
		}
		if len(attrs) == 0 {
			fmt.Fprintf(&b, "- %s\n", slug)
			return
		}
		fmt.Fprintf(&b, "- %s (%s)\n", slug, strings.Join(attrs, ", "))
	}
	line(proj.Spaces.Default, "default")
	if proj.Spaces.Parent != "" {
		fmt.Fprintf(&b, "  nested under %q\n", proj.Spaces.Parent)
	}
	seen := map[string]bool{proj.Spaces.Default: true}
	for _, d := range proj.Spaces.Declare {
		if d.Slug != "" && !seen[d.Slug] {
			seen[d.Slug] = true
			line(d.Slug, "")
		}
	}
	for _, d := range proj.Documents {
		if d.Space != "" && !seen[d.Space] {
			seen[d.Space] = true
			line(d.Space, "")
		}
	}
	if dev := audienceSpace(proj, []string{api.AudienceDevelopers}); dev != "" {
		fmt.Fprintf(&b, "Developer-only pages belong in %q.\n", dev)
	}
	if len(declared) > 0 {
		b.WriteString("Route each page to the space whose audiences cover the page's audiences; the narrowest matching space wins.\n")
	}
	b.WriteString("Target one of these exactly. Do not invent a space slug.\n\n")
	return b.String()
}

func existingPagesDigest(existing []api.Page) string {
	if len(existing) == 0 {
		return "No pages exist yet for this site — this is the first pass.\n\n"
	}
	var b strings.Builder
	b.WriteString("Pages that already exist (REUSE these slugs when your page covers the same feature):\n")
	drafts := false
	for _, p := range existing {
		marker := ""
		if p.IsDraft() {
			marker = " [draft]"
			drafts = true
		}
		fmt.Fprintf(&b, "- %s/%s — %s%s\n", p.SpaceSlug, p.Slug, p.Title, marker)
	}
	if drafts {
		b.WriteString("A [draft] page is a live page whose latest version is still an open proposal — edit it like any other existing page; it is not a gap, and a second slug for it would fork the docs.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func reportOrphans(existing []api.Page, plan agent.DocPlanInput, myRepoID string, logw io.Writer) {
	planned := make(map[string]bool, len(plan.Pages))
	for _, pg := range plan.Pages {
		planned[pg.Space+"\x00"+pg.Slug] = true
	}
	var orphans []string
	for _, p := range existing {
		if myRepoID != "" && p.RepoID != nil && *p.RepoID != myRepoID {
			continue
		}
		if !planned[p.SpaceSlug+"\x00"+p.Slug] {
			orphans = append(orphans, p.SpaceSlug+"/"+p.Slug)
		}
	}
	if len(orphans) > 0 {
		fmt.Fprintf(logw, "note: %d existing page(s) not covered by this plan, left unchanged: %s\n",
			len(orphans), strings.Join(orphans, ", "))
	}
}
