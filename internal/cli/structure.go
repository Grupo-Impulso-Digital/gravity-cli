package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/setup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type structurePlanData struct {
	Site      string           `json:"site"`
	Structure api.Structure    `json:"structure"`
	Pass      *config.Pass     `json:"pass,omitempty"`
	Diff      []setup.DiffItem `json:"diff"`
	Notes     []string         `json:"notes,omitempty"`
	YAML      string           `json:"yaml"`
	Written   bool             `json:"written"`
	Live      bool             `json:"live"`
}

func newStructureCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "structure",
		Short: "Declare the docs tree in .gravity.yaml and apply it to Gravity (plan, apply, show)",
		Long: "The repository declares its documentation tree under `structure:` in .gravity.yaml: the site, its spaces, collections and pages.\n" +
			"`structure plan` drafts it from the repository layout, `structure apply` creates what is missing in Gravity (never deleting anything), and `structure show` prints the live tree.\n" +
			"Pages with `source:` belong to a verbatim pass: apply never creates them as empty stubs; the import does.",
	}
	cmd.AddCommand(newStructurePlanCmd(a), newStructureApplyCmd(a), newStructureShowCmd(a))
	return cmd
}

func newStructurePlanCmd(a *app) *cobra.Command {
	var repos []string
	var site string
	var write bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Draft a structure from the repository layout and the current site",
		Long: "Draft a deterministic `structure:` block from the repository (the root README, docs/ folders, monorepo package READMEs and docs, OpenAPI specs, changelogs, runbooks), merged with what .gravity.yaml already declares and the spaces the site already has.\n" +
			"Prints the YAML and a tree diff against Gravity. --write merges it into .gravity.yaml, adding a verbatim pass for the page sources when none maps them yet. Pass --repo for sibling repositories that publish into the same site.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := a.structurePlan(cmd.Context(), repos, site, write)
			if err != nil {
				return err
			}
			return a.ui.Result(d)
		},
	}
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "another repository checkout that publishes into the same site (repeatable)")
	cmd.Flags().StringVar(&site, "site", "", "site slug (default: structure.site, else the product)")
	cmd.Flags().BoolVar(&write, "write", false, "merge the draft into .gravity.yaml")
	return cmd
}

func (a *app) layout(ctx context.Context, dir string, primary bool) (setup.Layout, error) {
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return setup.Layout{}, Fail(CodeError, fmt.Errorf("%s: %w", dir, err))
	}
	files, err := repo.ListFiles(ctx, "")
	if err != nil {
		return setup.Layout{}, Fail(CodeError, err)
	}
	if untracked, err := repo.UntrackedFiles(ctx); err == nil {
		files = append(files, untracked...)
	}
	sort.Strings(files)
	read := func(rel string) ([]byte, bool, error) {
		full, err := pathsafe.ResolveInRoot(repo.Root, rel)
		if err != nil {
			return nil, false, err
		}
		data, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
	det, err := detect.Run(detect.Input{Root: repo.Root, Files: files})
	if err != nil {
		return setup.Layout{}, Fail(CodeError, err)
	}
	return setup.Layout{Name: filepath.Base(repo.Root), Files: files, Read: read, Detect: det, Primary: primary}, nil
}

func (a *app) liveStructure(ctx context.Context, client *api.Client, site string) *api.Structure {
	if client == nil || site == "" {
		return nil
	}
	tree, err := client.StructureTree(ctx, site)
	if err == nil {
		return tree
	}
	if !api.IsUnsupported(err) {
		a.ui.Debugf("structure %s: %v", site, err)
		return nil
	}
	old, err := client.SiteTree(ctx, site)
	if err != nil {
		a.ui.Debugf("site %s: %v", site, err)
		return nil
	}
	return structureFromSiteTree(old)
}

func structureFromSiteTree(t *api.SiteTree) *api.Structure {
	st := &api.Structure{Site: api.StructureSite{Slug: t.Site.Slug, Name: t.Site.Name}}
	spaceSlug := map[string]string{}
	for _, sp := range t.Spaces {
		spaceSlug[sp.ID] = sp.Slug
	}
	for _, sp := range t.Spaces {
		parent := ""
		if sp.ParentSpaceID != nil {
			parent = spaceSlug[*sp.ParentSpaceID]
		}
		s := api.StructureSpace{Slug: sp.Slug, Name: sp.Name, Type: sp.Type, Parent: parent}
		var build func(parent *string) []api.StructureCollection
		build = func(parentID *string) []api.StructureCollection {
			var out []api.StructureCollection
			for _, c := range t.Collections {
				if firstNonEmpty(c.SpaceID, "") != sp.ID && c.SpaceSlug != sp.Slug {
					continue
				}
				if (parentID == nil) != (c.ParentID == nil) || (parentID != nil && *parentID != *c.ParentID) {
					continue
				}
				id := c.ID
				out = append(out, api.StructureCollection{Slug: c.Slug, Title: c.Name, Collections: build(&id)})
			}
			return out
		}
		s.Collections = build(nil)
		st.Spaces = append(st.Spaces, s)
	}
	return st
}

func (a *app) optionalClient(ctx context.Context, apiURL string) (*api.Client, *api.WhoAmI) {
	creds, err := a.credentials(apiURL)
	if err != nil || creds.Token == "" {
		return nil, nil
	}
	c := a.client(creds)
	who, err := c.WhoAmI(ctx)
	if err != nil {
		a.ui.Debugf("whoami: %v", err)
		return nil, nil
	}
	return c, who
}

func (a *app) structurePlan(ctx context.Context, repos []string, site string, write bool) (*structurePlanData, error) {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return nil, err
	}
	path := config.ManifestPath(info.root, a.manifestOverride())
	man, err := config.Load(path)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	apiURL := ""
	if man != nil {
		apiURL = man.APIURL
	}
	client, _ := a.optionalClient(ctx, apiURL)
	siteRef := api.StructureSite{Slug: site}
	if man != nil && man.Structure != nil && site == "" {
		siteRef = man.Structure.Site
	}
	if siteRef.Slug == "" {
		product := ""
		if man != nil {
			product = man.Product
		}
		siteRef.Slug = normalize.ProductSlug(firstNonEmpty(product, info.name))
	}
	var declared *api.Structure
	if man != nil {
		declared = man.Structure
	}
	live := a.liveStructure(ctx, client, siteRef.Slug)
	if live != nil && siteRef.Name == "" {
		siteRef.Name = live.Site.Name
	}
	if siteRef.Name == "" {
		siteRef.Name = humanize(siteRef.Slug)
	}
	primary, err := a.layout(ctx, info.root, true)
	if err != nil {
		return nil, err
	}
	layouts := []setup.Layout{primary}
	for _, r := range repos {
		dir := r
		if !filepath.IsAbs(dir) {
			wd, _ := a.workdir()
			dir = filepath.Join(wd, r)
		}
		l, err := a.layout(ctx, dir, false)
		if err != nil {
			return nil, err
		}
		layouts = append(layouts, l)
	}
	draft := setup.DraftStructure(setup.DraftInput{Site: siteRef, Declared: declared, Live: live, Layouts: layouts})
	d := &structurePlanData{Site: siteRef.Slug, Structure: draft.Structure, Diff: setup.DiffStructure(draft.Structure, live), Notes: draft.Notes, Live: live != nil}
	if draft.Pass != nil && !passMapsSources(man, draft.Structure) {
		d.Pass = draft.Pass
	}
	block, err := config.SetKey(nil, "structure", draft.Structure)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	d.YAML = string(block)
	renderStructurePlan(a.ui, d)
	if write {
		if err := a.writeStructure(info, path, man, d); err != nil {
			return nil, err
		}
	} else if !a.ui.JSON() {
		a.ui.Println("")
		a.ui.Note("", ui.MarkInfo, "edit freely, then %s writes it into %s", a.ui.Bold("gravity structure plan --write"), relPath(info.root, path))
	}
	return d, nil
}

func passMapsSources(man *config.Manifest, st api.Structure) bool {
	if man == nil {
		return false
	}
	for _, p := range man.Passes {
		if p.Kind == config.KindVerbatim {
			return true
		}
	}
	return len(declaredPages(&st)) == 0
}

func (a *app) writeStructure(info *repoInfo, path string, man *config.Manifest, d *structurePlanData) error {
	var data []byte
	if man != nil {
		data = man.YAML
	} else {
		data = []byte("version: 2\n")
	}
	out, err := config.SetKey(data, "structure", d.Structure)
	if err != nil {
		return Fail(CodeError, err)
	}
	if d.Pass != nil {
		if out, err = config.UpsertPass(out, *d.Pass); err != nil {
			return Fail(CodeError, err)
		}
	}
	if _, err := config.Parse(out); err != nil {
		return Fail(CodeError, fmt.Errorf("the merged manifest would be invalid: %w", err))
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return Fail(CodeError, err)
	}
	d.Written = true
	a.ui.Println("")
	what := "structure:"
	if d.Pass != nil {
		what += " and pass " + d.Pass.Name
	}
	a.ui.Note("", ui.MarkOK, "wrote %s to %s; next: %s, then %s", what, relPath(info.root, path), a.ui.Bold("gravity validate"), a.ui.Bold("gravity structure apply --dry-run"))
	return nil
}

func diffMark(p *ui.Printer, op string) string {
	switch op {
	case setup.DiffCreate:
		return p.Paint(ui.ToneInfo, "+")
	case setup.DiffExists:
		return p.Paint(ui.ToneOK, "=")
	case setup.DiffImport:
		return p.Paint(ui.ToneAccent, p.Glyph("↓", "v"))
	case setup.DiffExtra:
		return p.Paint(ui.ToneWarn, "!")
	}
	return " "
}

func renderStructurePlan(p *ui.Printer, d *structurePlanData) {
	p.Section("Draft", "structure: for site "+d.Site)
	for _, line := range strings.Split(strings.TrimRight(d.YAML, "\n"), "\n") {
		p.Println("  %s", p.Dim(line))
	}
	if d.Pass != nil {
		p.Section("Verbatim pass", "fills the pages that have a source")
		block, err := config.RenderPasses([]config.Pass{*d.Pass})
		if err == nil {
			for _, line := range strings.Split(strings.TrimRight(string(block), "\n"), "\n") {
				p.Println("  %s", p.Dim(line))
			}
		}
	}
	sub := "not signed in: everything is new"
	if d.Live {
		sub = "+ create  = exists  " + p.Glyph("↓", "v") + " created by the import  ! only in Gravity (kept)"
	}
	p.Section("Against Gravity", sub)
	counts := map[string]int{}
	for _, it := range d.Diff {
		counts[it.Op]++
		depth := strings.Count(it.Path, "/")
		if it.Kind == "page" {
			depth++
		}
		name := it.Path[strings.LastIndexAny(it.Path, "/#")+1:]
		label := firstNonEmpty(it.Title, name)
		if label != name {
			label += "  " + p.Dim(name)
		}
		if it.Kind == "collection" {
			label = p.Paint(ui.ToneInfo, firstNonEmpty(it.Title, name)+"/") + "  " + p.Dim(name)
		}
		if it.Kind == "space" {
			label = p.Bold(firstNonEmpty(it.Title, name)) + "  " + p.Dim("space "+name)
		}
		p.Println("  %s %s%s", diffMark(p, it.Op), strings.Repeat("  ", depth), label)
	}
	p.Println("")
	p.Note("", ui.MarkInfo, "%d to create, %d imported by the verbatim pass, %d already there, %d only in Gravity", counts[setup.DiffCreate], counts[setup.DiffImport], counts[setup.DiffExists], counts[setup.DiffExtra])
	for _, n := range d.Notes {
		p.Note("", ui.MarkInfo, "%s", n)
	}
}

func newStructureApplyCmd(a *app) *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Create the spaces, collections and pages structure: declares and Gravity lacks",
		Long: "Send `structure:` to Gravity: missing spaces, collections and pages are created, titles are updated, nothing is ever deleted (extra items are reported).\n" +
			"Pages with `source:` are not created here (deferred to the verbatim import), so no empty stub ever blocks an import. --dry-run shows what would change.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.structureApply(cmd.Context(), dry)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show what would change without changing anything")
	return cmd
}

func (a *app) structureApply(ctx context.Context, dry bool) error {
	s, err := a.openSession(ctx)
	if err != nil {
		return err
	}
	if s.manifest == nil || s.manifest.Structure == nil {
		return &ExitError{Code: CodeError, ErrCode: "structure_missing", Err: errors.New("no structure: in .gravity.yaml; draft one with `gravity structure plan --write`")}
	}
	res, err := s.client.ApplyStructure(ctx, repoParam(s.who, s.info), api.StructureApplyRequest{Structure: *s.manifest.Structure, DryRun: dry})
	if api.IsUnsupported(err) {
		return &ExitError{Code: CodeError, ErrCode: "server_unsupported", Err: errors.New("this Gravity server cannot apply a structure yet; create the spaces in the app, or upgrade the platform")}
	}
	if err != nil {
		return explainAPI(err)
	}
	renderApply(a.ui, res, dry)
	if len(res.Conflicts) > 0 {
		msg := plural(len(res.Conflicts), "conflict", "conflicts") + " in structure:"
		if ferr := a.ui.Failure(ui.ErrorInfo{Code: "structure_conflicts", Message: msg, ExitCode: CodeFindings}, res); ferr != nil {
			return ferr
		}
		return &ExitError{Code: CodeFindings, ErrCode: "structure_conflicts", Err: errors.New(msg)}
	}
	return a.ui.Result(res)
}

func renderApply(p *ui.Printer, r *api.StructureApplyResult, dry bool) {
	title, verb := "Structure applied", "created"
	if dry {
		title, verb = "Structure dry run", "would create"
	}
	groups := []struct {
		label string
		mark  string
		items []api.StructureItem
	}{
		{verb, "+", r.Created},
		{"updated", "~", r.Updated},
		{"left for the verbatim import", p.Glyph("↓", "v"), r.Deferred},
		{"only in Gravity, kept", "!", r.Extra},
		{"conflicts", "x", r.Conflicts},
	}
	p.Section(title, fmt.Sprintf("%d %s, %d updated, %d unchanged, %d deferred, %d extra, %d conflicts", len(r.Created), verb, len(r.Updated), len(r.Unchanged), len(r.Deferred), len(r.Extra), len(r.Conflicts)))
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		p.Println("  %s", p.Bold(g.label))
		for _, it := range g.items {
			line := g.mark + " " + it.Kind + " " + it.Path
			if it.Title != "" {
				line += "  " + p.Dim(it.Title)
			}
			if it.Reason != "" {
				line += "  " + p.Dim("("+strings.ReplaceAll(it.Reason, "_", " ")+")")
			}
			tone := ui.ToneInfo
			switch g.mark {
			case "x":
				tone = ui.ToneFail
			case "!":
				tone = ui.ToneWarn
			}
			p.Println("    %s", p.Paint(tone, line))
		}
	}
	if dry && len(r.Conflicts) == 0 {
		p.Println("")
		p.Note("", ui.MarkInfo, "run %s to make these changes", p.Bold("gravity structure apply"))
	}
}

func newStructureShowCmd(a *app) *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the live tree of a site in Gravity, pages included",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.openSession(ctx)
			if err != nil {
				return err
			}
			if site == "" && s.manifest != nil && s.manifest.Structure != nil {
				site = s.manifest.Structure.Site.Slug
			}
			if site == "" {
				return Failf(CodeError, "which site? pass --site <slug> (or declare structure.site in .gravity.yaml)")
			}
			tree, err := s.client.StructureTree(ctx, site)
			pagesKnown := true
			if api.IsUnsupported(err) {
				old, oerr := s.client.SiteTree(ctx, site)
				if oerr != nil {
					return explainAPI(oerr)
				}
				tree, err, pagesKnown = structureFromSiteTree(old), nil, false
			}
			if err != nil {
				return explainAPI(err)
			}
			a.ui.Section("Gravity", "site "+site+optional(" · this server does not list pages", !pagesKnown))
			a.ui.Tree("  ", liveTree(a.ui, tree))
			return a.ui.Result(tree)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site slug (default: structure.site)")
	return cmd
}

func liveTree(p *ui.Printer, st *api.Structure) ui.TreeNode {
	decorate := func(pages []api.StructurePage) []api.StructurePage {
		out := make([]api.StructurePage, len(pages))
		for i, pg := range pages {
			out[i] = pg
			meta := []string{}
			if pg.Status != "" {
				meta = append(meta, pg.Status)
			}
			if pg.LockedByRepo != "" {
				meta = append(meta, "locked by "+pg.LockedByRepo)
			}
			if len(meta) > 0 {
				out[i].Title = firstNonEmpty(pg.Title, pg.Slug) + "  " + p.Dim("("+strings.Join(meta, " · ")+")")
			}
			out[i].Source = ""
		}
		return out
	}
	var cols func(list []api.StructureCollection) []api.StructureCollection
	cols = func(list []api.StructureCollection) []api.StructureCollection {
		out := make([]api.StructureCollection, len(list))
		for i, c := range list {
			out[i] = c
			out[i].Pages = decorate(c.Pages)
			out[i].Collections = cols(c.Collections)
		}
		return out
	}
	view := api.Structure{Site: st.Site}
	for _, sp := range st.Spaces {
		c := sp
		c.Pages = decorate(sp.Pages)
		c.Collections = cols(sp.Collections)
		view.Spaces = append(view.Spaces, c)
	}
	return structureTree(p, &view, nil)
}

func humanize(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
