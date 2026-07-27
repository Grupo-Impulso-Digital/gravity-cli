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

// syncTarget is one resolved authoring action: either a page upsert or a
// release-notes upsert.
type syncTarget struct {
	kind    string // "page" | "release"
	space   string
	label   string
	home    bool // pin this page as its space's home page after authoring
	page    api.PageUpsertRequest
	release api.ReleaseNotesRequest
}

// hierarchySpec carries the manifest's space-hierarchy declarations into
// runSync: the parent space to nest the default space under, and the page to
// pin as the default space's home. All fields optional; a nil spec (or a
// platform without the space-hierarchy feature) syncs exactly as before.
type hierarchySpec struct {
	defaultSpace string
	parent       string // spaces.parent — makes defaultSpace a subspace of it
	home         string // spaces.home — page slug pinned as defaultSpace's home
}

// hierarchyFromProject extracts the sync-relevant hierarchy declarations.
func hierarchyFromProject(proj *config.Project) *hierarchySpec {
	if proj == nil {
		return nil
	}
	return &hierarchySpec{
		defaultSpace: proj.Spaces.Default,
		parent:       proj.Spaces.Parent,
		home:         proj.Spaces.Home,
	}
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

			targets, err := buildSyncTargets(e.proj, repo.Root, generator, only, page)
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

// buildSyncTargets turns the manifest mappings into concrete authoring actions.
func buildSyncTargets(proj *config.Project, repoRoot, generator, only, pageFilter string) ([]syncTarget, error) {
	var targets []syncTarget

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
				page:  api.PageUpsertRequest{SpaceSlug: sp, Slug: slug, Title: title, Collection: col, Blocks: blocks},
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
					kind:    "release",
					space:   sp,
					label:   fmt.Sprintf("release %s -> %s", d.File, sp),
					release: api.ReleaseNotesRequest{SpaceSlug: sp, Title: title, BodyMarkdown: body},
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
				page:  api.PageUpsertRequest{SpaceSlug: sp, Slug: slug, Title: title, Collection: col, Blocks: blocks},
			})
		}
	}

	// Guard: two page targets must not collide on (space, slug) — they would
	// silently clobber each other on the platform. Surface it as a config error
	// (runs offline, so it fires in --dry-run / --output stdout too).
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

// reconcileResult is the reconcile decision for one page target.
type reconcileResult struct {
	index         int // index into targets
	space         string
	configSlug    string // the slug from .gravity.yaml
	effectiveSlug string // the slug to actually send (an existing page's slug on update)
	update        bool   // true => updates an existing page; false => creates a new one
}

// orphanPage is an existing page that duplicates a managed page (same space +
// title) but was not the one chosen to keep. ofSlug is the kept page's slug.
type orphanPage struct {
	page   api.Page
	ofSlug string
}

// duplicate reports whether the orphan is a server-minted collision duplicate of
// the kept page — its slug is the kept slug plus a suffix (e.g. `agents-md` →
// `agents-md-ed44`). Only these are safe to prune automatically; a same-title
// page with an unrelated slug is left for manual review.
func (o orphanPage) duplicate() bool {
	return strings.HasPrefix(o.page.Slug, o.ofSlug+"-")
}

// reconcilePageTargets matches each page target to the existing page that already
// represents it, so the server's (space, slug) upsert updates in place instead of
// minting a duplicate. Matching is by exact slug, then by title within the space
// (the server slugs new pages from the title, so a re-sync's configured slug may
// not match what was stored). Orphans are managed-space pages whose title matches
// a managed target but were not picked (the suffixed duplicates), each tagged
// with the slug of the page kept in its place.
func reconcilePageTargets(existing []api.Page, targets []syncTarget) (plan []reconcileResult, orphans []orphanPage) {
	bySpace := map[string][]api.Page{}
	for _, p := range existing {
		bySpace[p.SpaceSlug] = append(bySpace[p.SpaceSlug], p)
	}
	pickedID := map[string]bool{}
	keptForTitle := map[string]string{} // space\x00title -> kept slug

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
			pickedID[chosen.ID] = true
			keptForTitle[space+"\x00"+title] = chosen.Slug
		}
		plan = append(plan, res)
	}

	for _, p := range existing {
		if kept, ok := keptForTitle[p.SpaceSlug+"\x00"+p.Title]; ok && !pickedID[p.ID] {
			orphans = append(orphans, orphanPage{page: p, ofSlug: kept})
		}
	}
	return plan, orphans
}

func exactSlugMatch(cands []api.Page, slug string) *api.Page {
	for i := range cands {
		if cands[i].Slug == slug {
			return &cands[i]
		}
	}
	return nil
}

// bestTitleMatch returns the existing page that best represents a title. Among
// same-title pages it prefers one whose slug equals the configured slug, then one
// whose slug equals the title's canonical slug, then the shortest slug, then the
// lexicographically first — a deterministic choice so re-syncs converge on the
// same page and the suffixed duplicates fall out as orphans.
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

// runSync reconciles against existing pages, ensures each unique space (nesting
// the default space under its declared parent), performs every upsert, pins the
// declared home page, then (when pruneDuplicates) proposes removal of the
// duplicate pages it converged off of. hier may be nil.
func runSync(ctx context.Context, client *api.Client, site string, targets []syncTarget, pruneDuplicates bool, hier *hierarchySpec, logw, out io.Writer) error {
	// One capability probe decides whether the hierarchy fields (space parent,
	// home page, page collections) may be sent. Older platforms reject unknown
	// request fields, so on a probe failure we degrade to the flat shape — the
	// prefixed shared-space slugs keep multi-repo pages apart either way.
	hierarchyOK := false
	if who, err := client.WhoAmI(ctx); err == nil {
		hierarchyOK = who.Features[featureSpaceHierarchy]
	} else {
		fmt.Fprintf(out, "warning: could not probe platform features (%v); syncing without hierarchy fields\n", err)
	}
	if !hierarchyOK {
		stripped := false
		for i := range targets {
			if targets[i].page.Collection != "" {
				targets[i].page.Collection = ""
				stripped = true
			}
		}
		if stripped || (hier != nil && (hier.parent != "" || hier.home != "")) {
			fmt.Fprintln(out, "note: this platform predates subspaces/collections — pages sync flat; spaces.parent / spaces.home / collections take effect once the platform supports them")
		}
		hier = nil
	}

	// Read existing pages and reconcile so a re-sync updates the same page instead
	// of duplicating it. Non-fatal: if the read fails we author with the
	// configured slugs and say reconciliation was skipped.
	var orphans []orphanPage
	if existing, err := client.Pages(ctx, site, ""); err != nil {
		fmt.Fprintf(out, "warning: could not read existing pages to reconcile (%v); authoring with configured slugs\n", err)
	} else {
		var plan []reconcileResult
		plan, orphans = reconcilePageTargets(existing, targets)
		for _, r := range plan {
			verb := "CREATE"
			if r.update {
				verb = "UPDATE"
			}
			note := ""
			if r.update && r.effectiveSlug != r.configSlug {
				note = fmt.Sprintf(" (config slug %q remapped onto the existing page)", r.configSlug)
			}
			fmt.Fprintf(out, "plan: %-6s %s/%s%s\n", verb, r.space, r.effectiveSlug, note)
			targets[r.index].page.Slug = r.effectiveSlug
		}
		for _, o := range orphans {
			switch {
			case o.duplicate() && pruneDuplicates:
				fmt.Fprintf(out, "plan: DELETE %s/%s (duplicate of %q; proposing removal)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			case o.duplicate():
				fmt.Fprintf(out, "plan: ORPHAN %s/%s (duplicate of %q; kept via --keep-duplicates)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			default:
				fmt.Fprintf(out, "plan: ORPHAN %s/%s (same title as %q but unrelated slug; left for manual review)\n", o.page.SpaceSlug, o.page.Slug, o.ofSlug)
			}
		}
	}

	// Ensure spaces in dependency order: the declared parent space first (it
	// must exist before anything can nest under it), then every target space.
	// The default space carries `parent` so it is created as — or reparented
	// into — a subspace, converging on the manifest.
	ensured := map[string]bool{}
	if hier != nil && hier.parent != "" {
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

	// Author every target independently. A single bad target (e.g. a block the
	// server rejects with a 400) must not discard the siblings that authored
	// cleanly — those are real proposals the user keeps. Record failures and press
	// on; the only fail-fast is a systemic auth error, which every remaining
	// target would hit identically.
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
		if t.home && t.kind == "page" && hier != nil {
			// The upsert's response slug is the page's real identity (the
			// server may have honored an existing page), so pin that one.
			homeSpace, homeSlug = t.space, firstNonEmpty(resp.PageSlug, t.page.Slug)
		}
		proposed++
		fmt.Fprintf(out, "Proposed: %s  status=%s  proposal=%s\n", resp.PageSlug, resp.Status, resp.ProposalID)
		if resp.ReviewURL != "" {
			fmt.Fprintf(out, "  review: %s\n", resp.ReviewURL)
		}
	}

	// Pin the declared home page onto its space. Best-effort: the pages above
	// are already proposed, so a failed pin is a note (re-run to retry), not a
	// failed sync.
	if homeSlug != "" {
		if _, err := client.UpdateSpace(ctx, site, homeSpace, api.SpacePatchRequest{HomePage: &homeSlug}); err != nil {
			fmt.Fprintf(out, "note: could not pin %q as the home page of space %q (%v); re-run `gravity sync` to retry\n", homeSlug, homeSpace, err)
		} else {
			fmt.Fprintf(out, "Home page: %s/%s\n", homeSpace, homeSlug)
		}
	}

	// Propose removal of the server-minted duplicate pages we converged off of.
	// Deletion is a governed change request (a `delete` proposal a reviewer
	// approves), so this never hard-deletes; failures are reported, not fatal.
	// The first error is treated as endpoint-level (e.g. the delete route not yet
	// deployed): report it once and stop, leaving the rest for a later re-run.
	if pruneDuplicates {
		for _, o := range orphans {
			if !o.duplicate() {
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

// savedDoc is the on-disk form of an authored target set, so an expensive `docs
// generate` run can be replayed through runSync without re-invoking the AI (the
// sync is the only step that can fail late, after all the tokens are spent).
type savedDoc struct {
	Version int           `json:"version"`
	Targets []savedTarget `json:"targets"`
}

// savedTarget mirrors a syncTarget with exported fields so it round-trips JSON.
type savedTarget struct {
	Kind    string                   `json:"kind"`
	Space   string                   `json:"space"`
	Label   string                   `json:"label"`
	Home    bool                     `json:"home,omitempty"`
	Page    *api.PageUpsertRequest   `json:"page,omitempty"`
	Release *api.ReleaseNotesRequest `json:"release,omitempty"`
}

// saveTargets persists targets to path as JSON, creating parent dirs.
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

// loadTargets reads a target set previously written by saveTargets.
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
			// Re-canonicalize replayed blocks: an artifact saved by an earlier
			// CLI may carry shapes the server has since rejected (the legacy
			// array-header table) or machine-owned narrative blocks. Sanitizing
			// here means a saved run stays replayable after the contract fix
			// instead of failing with the same 400 forever. Best-effort — a
			// block we can't fix is sent as-is so the server reports it.
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

// deleteErrHint reduces a page-deletion error to a short phrase for the
// single-line "deletion unavailable" notice (the full envelope is noise here).
func deleteErrHint(err error) string {
	var ae *api.APIError
	if errors.As(err, &ae) {
		return fmt.Sprintf("HTTP %d", ae.StatusCode)
	}
	return err.Error()
}

// syncAPIError surfaces a helpful auth hint for *api.APIError.
func syncAPIError(err error, what string) error {
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsAuth() {
		return Failf(CodeError, "%s: %s (check %s and that the key is authorized for this site)", what, ae.Message, config.EnvToken)
	}
	return Fail(CodeError, fmt.Errorf("%s: %w", what, err))
}
