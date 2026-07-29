package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

type syncTarget struct {
	kind    string
	space   string
	label   string
	home    bool
	page    api.PageUpsertRequest
	release api.ReleaseNotesRequest
}

type hierarchySpec struct {
	defaultSpace string
	parent       string
	home         string
	declared     []config.SpaceDecl
}

func hierarchyFromProject(proj *config.Project) *hierarchySpec {
	if proj == nil {
		return nil
	}
	return &hierarchySpec{
		defaultSpace: proj.Spaces.Default,
		parent:       proj.Spaces.Parent,
		home:         proj.Spaces.Home,
		declared:     proj.DeclaredSpaces(),
	}
}

func (h *hierarchySpec) declaresNesting() bool {
	for _, d := range h.declared {
		if d.Parent != "" {
			return true
		}
	}
	return false
}

func (h *hierarchySpec) flatten() {
	h.parent, h.home = "", ""
	for i := range h.declared {
		h.declared[i].Parent = ""
	}
}

func (h *hierarchySpec) stripMetadata() bool {
	stripped := false
	for i := range h.declared {
		if h.declared[i].Type != "" || h.declared[i].Visibility != "" {
			h.declared[i].Type, h.declared[i].Visibility = "", ""
			stripped = true
		}
	}
	return stripped
}

func newSyncCmd(gf *globalFlags) *cobra.Command {
	var (
		only           string
		page           string
		output         string
		dryRun         bool
		ci             bool
		space          string
		keepDuplicates bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Author docs to Gravity from .gravity.yaml (API blocks + Markdown)",
		Long: `Author the doc mappings declared in .gravity.yaml onto the Gravity platform:

  sources    OpenAPI/code files -> machine-owned, drift-locked api blocks
  documents  Markdown files     -> native blocks on a page, or a release

Machine blocks are bound to their source file by sha256, so they change only when
the code changes and are verifiable by ` + "`gravity check api`" + ` / ` + "`gravity check docs`" + `.
Every write creates a draft + open proposal — nothing is published directly.

Use --only to author just one kind, and --page to target a single mapping.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputProposal, outputStdout:
			default:
				return Failf(CodeError, "invalid --output %q (want proposal|stdout)", output)
			}
			switch only {
			case "", "api", "docs":
			default:
				return Failf(CodeError, "invalid --only %q (want api|docs)", only)
			}

			e, err := resolveEnv(*gf, space)
			if err != nil {
				return err
			}
			if e.proj == nil {
				return Failf(CodeError, "no %s found; run `gravity init` to create one", config.ProjectFileName)
			}
			if len(e.proj.Sources) == 0 && len(e.proj.Documents) == 0 {
				return Failf(CodeError, "%s declares no `sources` or `documents` to sync", config.ProjectFileName)
			}

			repo, err := git.Open(cmd.Context(), ".")
			if err != nil {
				return Fail(CodeError, fmt.Errorf("sync must run inside a git repo: %w", err))
			}
			generator := "gravity sync v" + version

			targets, err := buildSyncTargets(e.proj, repo.Root, generator, only, page, localRepoRef(cmd.Context(), e.proj))
			if err != nil {
				return Fail(CodeError, err)
			}
			if len(targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to sync (no matching mappings)")
				return nil
			}

			out := cmd.OutOrStdout()
			if output == outputStdout {
				printSyncStdout(out, targets)
				return nil
			}
			if dryRun {
				return printSyncDryRun(out, targets)
			}

			if err := e.requireAuth(); err != nil {
				return err
			}
			site, err := e.requireSite()
			if err != nil {
				return err
			}
			return runSync(cmd.Context(), e.client, site, targets, !keepDuplicates, hierarchyFromProject(e.proj), logWriter(cmd, ci), out)
		},
	}
	cmd.Flags().StringVar(&only, "only", "", "author only one kind: api|docs")
	cmd.Flags().StringVar(&page, "page", "", "author only the mapping(s) for this page slug")
	cmd.Flags().StringVar(&space, "space", "", "override the target space for all mappings")
	cmd.Flags().StringVar(&output, "output", outputProposal, "proposal|stdout")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be posted without calling the API")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().BoolVar(&keepDuplicates, "keep-duplicates", false, "report duplicate pages but don't propose deleting them")
	return cmd
}

func buildSyncTargets(proj *config.Project, repoRoot, generator, only, pageFilter string, repo *api.RepoRef) ([]syncTarget, error) {
	var targets []syncTarget
	languages := proj.I18n.Languages

	if only == "" || only == "api" {
		for _, s := range proj.Sources {
			if pageFilter != "" && s.Page != pageFilter {
				continue
			}
			blocks, err := docs.APIBlocks(repoRoot, s.Source, generator)
			if err != nil {
				return nil, fmt.Errorf("source %s: %w", s.Source, err)
			}
			sp, slug, col := proj.PageTarget(s.Space, s.Page, s.Collection)
			title := firstNonEmpty(s.Title, s.Page)
			targets = append(targets, syncTarget{
				kind:  "page",
				space: sp,
				label: fmt.Sprintf("api %s -> %s/%s", s.Source, sp, slug),
				home:  proj.Spaces.Home != "" && sp == proj.Spaces.Default && slug == proj.Spaces.Home,
				page: api.PageUpsertRequest{
					SpaceSlug: sp, Slug: slug, Title: title, Collection: col, Blocks: blocks,
					Repo: repo, Languages: languages,
				},
			})
		}
	}

	if only == "" || only == "docs" {
		for _, d := range proj.Documents {
			if pageFilter != "" && d.Page != pageFilter {
				continue
			}
			if d.As == "release" {
				body, title, err := docs.ReadVerbatim(repoRoot, d.File)
				if err != nil {
					return nil, fmt.Errorf("document %s: %w", d.File, err)
				}
				title = firstNonEmpty(d.Title, d.Version, title)
				sp := d.Space
				if sp == "" {
					sp = proj.ReleaseNotes.Space
				}
				targets = append(targets, syncTarget{
					kind:  "release",
					space: sp,
					label: fmt.Sprintf("release %s -> %s", d.File, sp),
					release: api.ReleaseNotesRequest{
						SpaceSlug: sp, Title: title, BodyMarkdown: body,
						Repo: repo, Languages: languages,
					},
				})
				continue
			}
			blocks, title, err := docs.MarkdownPage(repoRoot, d.File, d.Ownership, generator)
			if err != nil {
				return nil, fmt.Errorf("document %s: %w", d.File, err)
			}
			title = firstNonEmpty(d.Title, title)
			sp, slug, col := proj.PageTarget(d.Space, d.Page, d.Collection)
			targets = append(targets, syncTarget{
				kind:  "page",
				space: sp,
				label: fmt.Sprintf("doc %s -> %s/%s", d.File, sp, slug),
				home:  proj.Spaces.Home != "" && sp == proj.Spaces.Default && slug == proj.Spaces.Home,
				page: api.PageUpsertRequest{
					SpaceSlug: sp, Slug: slug, Title: title, Collection: col, Blocks: blocks,
					Repo: repo, Languages: languages,
				},
			})
		}
	}

	seen := map[string]string{}
	for _, t := range targets {
		if t.kind != "page" {
			continue
		}
		k := t.space + "/" + t.page.Slug
		if prev, ok := seen[k]; ok {
			return nil, fmt.Errorf("two documents target the same page %q (%s and %s) — give each a distinct `page` slug", k, prev, t.label)
		}
		seen[k] = t.label
	}

	return targets, nil
}

func copyHierarchy(hier *hierarchySpec) *hierarchySpec {
	if hier == nil {
		return nil
	}
	spec := *hier
	spec.declared = append([]config.SpaceDecl(nil), hier.declared...)
	return &spec
}

type reconcileResult struct {
	index         int
	space         string
	configSlug    string
	effectiveSlug string
	update        bool
	draft         bool
}

type orphanPage struct {
	page   api.Page
	ofSlug string
}

func (o orphanPage) duplicate() bool {
	return strings.HasPrefix(o.page.Slug, o.ofSlug+"-")
}

func reconcilePageTargets(existing []api.Page, targets []syncTarget, myRemoteKey string) (plan []reconcileResult, orphans []orphanPage) {
	bySpace := map[string][]api.Page{}
	for _, p := range existing {
		if !ownedByRepo(p, myRemoteKey) {
			continue
		}
		bySpace[p.SpaceSlug] = append(bySpace[p.SpaceSlug], p)
	}
	pickedID := map[string]bool{}
	keptForTitle := map[string]string{}

	for i, t := range targets {
		if t.kind != "page" {
			continue
		}
		space, cfgSlug, title := t.space, t.page.Slug, t.page.Title

		cands := bySpace[space]
		chosen := exactSlugMatch(cands, cfgSlug)
		if chosen == nil {
			chosen = bestTitleMatch(cands, title, cfgSlug)
		}
		res := reconcileResult{index: i, space: space, configSlug: cfgSlug, effectiveSlug: cfgSlug}
		if chosen != nil {
			res.effectiveSlug = chosen.Slug
			res.update = true
			res.draft = chosen.IsDraft()
			pickedID[chosen.ID] = true
			keptForTitle[space+"\x00"+title] = chosen.Slug
		}
		plan = append(plan, res)
	}

	for _, p := range existing {
		if !ownedByRepo(p, myRemoteKey) {
			continue
		}
		if kept, ok := keptForTitle[p.SpaceSlug+"\x00"+p.Title]; ok && !pickedID[p.ID] {
			orphans = append(orphans, orphanPage{page: p, ofSlug: kept})
		}
	}
	return plan, orphans
}

func ownedByRepo(p api.Page, myRemoteKey string) bool {
	if myRemoteKey == "" {
		return true
	}
	if p.RepoRemoteKey == nil || *p.RepoRemoteKey == "" {
		return true
	}
	return *p.RepoRemoteKey == myRemoteKey
}

func writtenByRepo(p api.Page, myRemoteKey string) bool {
	return myRemoteKey != "" && p.RepoRemoteKey != nil && *p.RepoRemoteKey == myRemoteKey
}

func prunableDuplicate(o orphanPage, myRemoteKey string) bool {
	if !o.duplicate() {
		return false
	}
	if myRemoteKey == "" {
		return true
	}
	return writtenByRepo(o.page, myRemoteKey)
}

func stripRepoRefs(targets []syncTarget) bool {
	stripped := false
	for i := range targets {
		if targets[i].page.Repo != nil {
			targets[i].page.Repo = nil
			stripped = true
		}
		if targets[i].release.Repo != nil {
			targets[i].release.Repo = nil
			stripped = true
		}
	}
	return stripped
}

func stripLanguages(targets []syncTarget) bool {
	stripped := false
	for i := range targets {
		if len(targets[i].page.Languages) > 0 {
			targets[i].page.Languages = nil
			stripped = true
		}
		if len(targets[i].release.Languages) > 0 {
			targets[i].release.Languages = nil
			stripped = true
		}
	}
	return stripped
}

func targetsRemoteKey(targets []syncTarget) string {
	for _, t := range targets {
		if t.page.Repo != nil && t.page.Repo.RemoteKey != "" {
			return t.page.Repo.RemoteKey
		}
		if t.release.Repo != nil && t.release.Repo.RemoteKey != "" {
			return t.release.Repo.RemoteKey
		}
	}
	return ""
}

func exactSlugMatch(cands []api.Page, slug string) *api.Page {
	for i := range cands {
		if cands[i].Slug == slug {
			return &cands[i]
		}
	}
	return nil
}

func bestTitleMatch(cands []api.Page, title, cfgSlug string) *api.Page {
	canonical := docs.Slug(title)
	var best *api.Page
	rank := func(p *api.Page) int {
		switch p.Slug {
		case cfgSlug:
			return 0
		case canonical:
			return 1
		default:
			return 2
		}
	}
	for i := range cands {
		if cands[i].Title != title {
			continue
		}
		c := &cands[i]
		if best == nil {
			best = c
			continue
		}
		switch {
		case rank(c) != rank(best):
			if rank(c) < rank(best) {
				best = c
			}
		case len(c.Slug) != len(best.Slug):
			if len(c.Slug) < len(best.Slug) {
				best = c
			}
		case c.Slug < best.Slug:
			best = c
		}
	}
	return best
}

func runSync(ctx context.Context, client *api.Client, site string, targets []syncTarget, pruneDuplicates bool, hier *hierarchySpec, logw, out io.Writer) error {
	features := map[string]bool{}
	if who, err := client.WhoAmI(ctx); err == nil {
		features = who.Features
	} else {
		fmt.Fprintf(out, "warning: could not probe platform features (%v); syncing without hierarchy fields\n", err)
	}
	hier = copyHierarchy(hier)
	if !features[featureSpaceHierarchy] {
		stripped := false
		for i := range targets {
			if targets[i].page.Collection != "" {
				targets[i].page.Collection = ""
				stripped = true
			}
		}
		if stripped || (hier != nil && (hier.parent != "" || hier.home != "" || hier.declaresNesting())) {
			fmt.Fprintln(out, "note: this platform predates subspaces/collections — pages sync flat; spaces.parent / spaces.home / collections take effect once the platform supports them")
		}
		if hier != nil {
			hier.flatten()
		}
	}
	if !features[featureSpaceMetadata] && hier != nil && hier.stripMetadata() {
		fmt.Fprintln(out, "note: this platform does not type spaces yet — declared spaces sync without type/visibility; they take effect once it does")
	}

	myRemoteKey := ""
	if features[featureRepos] {
		myRemoteKey = targetsRemoteKey(targets)
	} else if stripRepoRefs(targets) {
		fmt.Fprintln(out, "note: this platform does not attribute pages to repos yet — syncing unattributed; a sibling repo's pages are indistinguishable from this repo's until it does")
	}
	if !features[featurePageLanguages] && stripLanguages(targets) {
		fmt.Fprintln(out, "note: this platform does not accept per-page translation requests yet — i18n.languages takes effect once it does")
	}

	var orphans []orphanPage
	if existing, err := client.ListPages(ctx, site, api.PageListOptions{IncludeDraft: true}); err != nil {
		fmt.Fprintf(out, "warning: could not read existing pages to reconcile (%v); authoring with configured slugs\n", err)
	} else {
		var plan []reconcileResult
		plan, orphans = reconcilePageTargets(existing, targets, myRemoteKey)
		for _, r := range plan {
			verb := "CREATE"
			if r.update {
				verb = "UPDATE"
			}
			note := ""
			if r.update && r.effectiveSlug != r.configSlug {
				note = fmt.Sprintf(" (config slug %q remapped onto the existing page)", r.configSlug)
			}
			if r.draft {
				note += " (draft — updating the open proposal in place)"
			}
			fmt.Fprintf(out, "plan: %-6s %s/%s%s\n", verb, r.space, r.effectiveSlug, note)
			targets[r.index].page.Slug = r.effectiveSlug
		}
		for _, o := range orphans {
			switch {
			case prunableDuplicate(o, myRemoteKey) && pruneDuplicates:
				fmt.Fprintf(out, "plan: DELETE %s/%s (duplicate of %q; proposing removal)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			case prunableDuplicate(o, myRemoteKey):
				fmt.Fprintf(out, "plan: ORPHAN %s/%s (duplicate of %q; kept via --keep-duplicates)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			case o.duplicate():
				fmt.Fprintf(out, "note: %s/%s predates repo attribution; re-run after this repo has synced once, or delete it in-app\n", o.page.SpaceSlug, o.page.Slug)
			default:
				fmt.Fprintf(out, "plan: ORPHAN %s/%s (same title as %q but unrelated slug; left for manual review)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			}
		}
	}

	ensured := map[string]bool{}
	if hier != nil {
		for _, d := range hier.declared {
			req := api.SpaceUpsertRequest{
				Slug: d.Slug, Name: d.Name, Parent: d.Parent, Type: d.Type, Visibility: d.Visibility,
			}
			if _, err := client.EnsureSpace(ctx, site, req); err != nil {
				return syncAPIError(err, fmt.Sprintf("ensure declared space %q", d.Slug))
			}
			ensured[d.Slug] = true
		}
	}
	if hier != nil && hier.parent != "" && !ensured[hier.parent] {
		if _, err := client.EnsureSpace(ctx, site, api.SpaceUpsertRequest{Slug: hier.parent}); err != nil {
			return syncAPIError(err, fmt.Sprintf("ensure parent space %q", hier.parent))
		}
		ensured[hier.parent] = true
	}
	for _, t := range targets {
		if t.space == "" || ensured[t.space] {
			continue
		}
		req := api.SpaceUpsertRequest{
			Slug:        t.space,
			Description: "Maintained by the gravity CLI.",
		}
		if hier != nil && hier.parent != "" && t.space == hier.defaultSpace {
			req.Parent = hier.parent
		}
		if _, err := client.EnsureSpace(ctx, site, req); err != nil {
			return syncAPIError(err, fmt.Sprintf("ensure space %q", t.space))
		}
		ensured[t.space] = true
	}

	var failures []string
	proposed := 0
	homeSpace, homeSlug := "", ""
	for _, t := range targets {
		fmt.Fprintf(logw, "sync: %s\n", t.label)
		var resp *api.ReleaseNotesResponse
		var err error
		switch t.kind {
		case "release":
			resp, err = client.CreateReleaseNotes(ctx, site, t.release)
		default:
			resp, err = client.UpsertPage(ctx, site, t.page)
		}
		if err != nil {
			var ae *api.APIError
			if errors.As(err, &ae) && ae.IsAuth() {
				return syncAPIError(err, t.label)
			}
			fmt.Fprintf(out, "FAILED: %s: %v\n", t.label, err)
			failures = append(failures, t.label)
			continue
		}
		if t.home && t.kind == "page" && hier != nil && hier.home != "" {
			homeSpace, homeSlug = t.space, firstNonEmpty(resp.PageSlug, t.page.Slug)
		}
		proposed++
		fmt.Fprintf(out, "Proposed: %s  status=%s  proposal=%s\n", resp.PageSlug, resp.Status, resp.ProposalID)
		if resp.ReviewURL != "" {
			fmt.Fprintf(out, "  review: %s\n", resp.ReviewURL)
		}
	}

	if homeSlug != "" {
		if _, err := client.UpdateSpace(ctx, site, homeSpace, api.SpacePatchRequest{HomePage: &homeSlug}); err != nil {
			fmt.Fprintf(out, "note: could not pin %q as the home page of space %q (%v); re-run `gravity sync` to retry\n", homeSlug, homeSpace, err)
		} else {
			fmt.Fprintf(out, "Home page: %s/%s\n", homeSpace, homeSlug)
		}
	}

	if pruneDuplicates {
		for _, o := range orphans {
			if !prunableDuplicate(o, myRemoteKey) {
				continue
			}
			resp, err := client.DeletePage(ctx, site, o.page.SpaceSlug, o.page.Slug)
			if err != nil {
				fmt.Fprintf(out, "note: page deletion is unavailable on this platform (%v) — left the duplicate page(s) in place; re-run `gravity sync` once it ships\n", deleteErrHint(err))
				break
			}
			fmt.Fprintf(out, "Delete proposed: %s  status=%s  proposal=%s\n", resp.PageSlug, resp.Status, resp.ProposalID)
			if resp.ReviewURL != "" {
				fmt.Fprintf(out, "  review: %s\n", resp.ReviewURL)
			}
		}
	}

	if len(failures) > 0 {
		return Failf(CodeError, "%d of %d target(s) failed to author (%d proposed): %s",
			len(failures), len(targets), proposed, strings.Join(failures, "; "))
	}
	return nil
}

type savedDoc struct {
	Version int           `json:"version"`
	Targets []savedTarget `json:"targets"`
}

type savedTarget struct {
	Kind    string                   `json:"kind"`
	Space   string                   `json:"space"`
	Label   string                   `json:"label"`
	Home    bool                     `json:"home,omitempty"`
	Page    *api.PageUpsertRequest   `json:"page,omitempty"`
	Release *api.ReleaseNotesRequest `json:"release,omitempty"`
}

func saveTargets(path string, targets []syncTarget) error {
	doc := savedDoc{Version: 1}
	for _, t := range targets {
		st := savedTarget{Kind: t.kind, Space: t.space, Label: t.label, Home: t.home}
		if t.kind == "release" {
			r := t.release
			st.Release = &r
		} else {
			p := t.page
			st.Page = &p
		}
		doc.Targets = append(doc.Targets, st)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

func loadTargets(path string) ([]syncTarget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc savedDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse saved docs: %w", err)
	}
	targets := make([]syncTarget, 0, len(doc.Targets))
	for _, st := range doc.Targets {
		t := syncTarget{kind: st.Kind, space: st.Space, label: st.Label, home: st.Home}
		if st.Page != nil {
			t.page = *st.Page
			for i := range t.page.Blocks {
				_ = docs.SanitizeBlock(&t.page.Blocks[i])
			}
		}
		if st.Release != nil {
			t.release = *st.Release
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func printSyncStdout(out io.Writer, targets []syncTarget) {
	for _, t := range targets {
		if t.kind == "release" {
			fmt.Fprintf(out, "# %s  (release -> space %s)\n%s\n\n", t.release.Title, t.release.SpaceSlug, t.release.BodyMarkdown)
			continue
		}
		docs.WritePageText(out, t.page)
	}
}

func printSyncDryRun(out io.Writer, targets []syncTarget) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	for _, t := range targets {
		fmt.Fprintf(out, "--- %s ---\n", t.label)
		var payload any
		if t.kind == "release" {
			payload = t.release
		} else {
			payload = t.page
		}
		if err := enc.Encode(payload); err != nil {
			return Fail(CodeError, err)
		}
	}
	return nil
}

func deleteErrHint(err error) string {
	var ae *api.APIError
	if errors.As(err, &ae) {
		return fmt.Sprintf("HTTP %d", ae.StatusCode)
	}
	return err.Error()
}

func syncAPIError(err error, what string) error {
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsAuth() {
		return Failf(CodeError, "%s: %s (check %s and that the key is authorized for this site)", what, ae.Message, config.EnvToken)
	}
	return Fail(CodeError, fmt.Errorf("%s: %w", what, err))
}
