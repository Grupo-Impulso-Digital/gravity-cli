package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type showManifest struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Product string `json:"product,omitempty"`
}

type showRepo struct {
	Name          string `json:"name"`
	Remote        string `json:"remote"`
	Branch        string `json:"branch"`
	Head          string `json:"head"`
	DefaultBranch string `json:"defaultBranch"`
}

type showPass struct {
	Name               string        `json:"name"`
	Kind               string        `json:"kind"`
	Target             string        `json:"target"`
	TargetStatus       string        `json:"targetStatus,omitempty"`
	Triggers           []string      `json:"triggers"`
	Branches           []string      `json:"branches"`
	Audiences          []string      `json:"audiences"`
	Languages          []string      `json:"languages"`
	EffectiveLanguages []string      `json:"effectiveLanguages"`
	I18n               string        `json:"i18n,omitempty"`
	AI                 bool          `json:"ai"`
	Enabled            bool          `json:"enabled"`
	Estimate           *api.Estimate `json:"estimate"`
	Grant              string        `json:"grant,omitempty"`
	ApprovalWhy        string        `json:"approvalWhy,omitempty"`
}

type ciFileState struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

type showCI struct {
	Provider string        `json:"provider"`
	Label    string        `json:"label"`
	Files    []ciFileState `json:"files"`
	Snippet  bool          `json:"snippet,omitempty"`
}

type showData struct {
	Manifest  showManifest   `json:"manifest"`
	Repo      showRepo       `json:"repo"`
	SignedIn  bool           `json:"signedIn"`
	Account   string         `json:"account,omitempty"`
	Server    string         `json:"server"`
	Structure *api.Structure `json:"structure"`
	Verbatim  []verbatimMap  `json:"verbatim"`
	Passes    []showPass     `json:"passes"`
	CI        showCI         `json:"ci"`
	Errors    int            `json:"errors"`
	Warnings  int            `json:"warnings"`
	live      map[string]bool
}

func newShowCmd(a *app) *cobra.Command {
	var only []string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show what .gravity.yaml declares: structure, verbatim mapping, passes, i18n and CI",
		Long: "A static view of the local manifest: the structure tree, which repository file becomes which page, every pass with its triggers, audiences and effective languages, and the CI status.\n" +
			"Signed in, it also marks what already exists in Gravity, the cost estimate of each pass and the languages that will really be produced.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.collect(cmd.Context(), modelOptions{server: true, plan: true})
			if err != nil {
				return err
			}
			if m.manifest == nil {
				a.printIssues(m.issues, nil)
				return a.validationExit(m)
			}
			d := a.showData(m, only)
			renderShow(a.ui, d)
			return a.ui.Result(d)
		},
	}
	cmd.Flags().StringSliceVar(&only, "pass", nil, "show only these passes")
	return cmd
}

func (a *app) showData(m *model, only []string) showData {
	keep := map[string]bool{}
	for _, n := range only {
		keep[n] = true
	}
	man := m.manifest
	d := showData{
		Manifest:  showManifest{Path: manifestRel(m.info, man), Hash: man.Hash, Product: man.Product},
		Repo:      showRepo{Name: m.info.name, Remote: m.info.remoteKey, Branch: m.info.branch, Head: m.info.head, DefaultBranch: m.info.defaultBranch},
		SignedIn:  m.signedIn,
		Server:    m.server,
		Structure: man.Structure,
		Verbatim:  []verbatimMap{},
		Passes:    []showPass{},
		Errors:    m.errors(),
		Warnings:  m.warnings(),
	}
	if m.who != nil {
		d.Account = accountLabel(m.who)
	}
	if m.live != nil {
		d.live = map[string]bool{}
		walkStructure(m.live, func(space api.StructureSpace, path []string, coll *api.StructureCollection, page *api.StructurePage) {
			switch {
			case page != nil:
				d.live[space.Slug+"/"+strings.Join(path, "/")+"#"+page.Slug] = true
			case coll != nil:
				d.live[space.Slug+"/"+strings.Join(path, "/")] = true
			default:
				d.live[space.Slug] = true
			}
		})
	}
	for _, vm := range m.verbatim {
		if len(keep) == 0 || keep[vm.Pass] {
			d.Verbatim = append(d.Verbatim, vm)
		}
	}
	i18n := map[string]api.I18nOutcome{}
	if m.validation != nil {
		for _, o := range m.validation.I18n {
			i18n[o.Pass] = o
		}
	}
	planned := map[string]api.PlanPass{}
	if m.plan != nil {
		for _, pp := range m.plan.Passes {
			planned[pp.Name] = pp
		}
	}
	for _, p := range man.Passes {
		if len(keep) > 0 && !keep[p.Name] {
			continue
		}
		sp := showPass{
			Name: p.Name, Kind: p.Kind, Target: p.Target, Triggers: orEmpty(config.PassTriggers(p)), Branches: orEmpty(p.Branches), Audiences: orEmpty(p.Audiences),
			Languages: orEmpty(stringsOption(p.Options, "languages")), EffectiveLanguages: []string{}, Enabled: p.IsEnabled(),
			AI: engine.UsesAI(api.PlanPass{Kind: p.Kind, Options: p.Options}),
		}
		if o, ok := i18n[p.Name]; ok {
			sp.EffectiveLanguages, sp.I18n = orEmpty(o.Effective), o.Reason
		}
		if pp, ok := planned[p.Name]; ok {
			sp.TargetStatus = pp.Target.Status
			sp.Estimate = pp.Estimate
			if pp.Target.Status == api.TargetUnapproved {
				sp.Grant, sp.ApprovalWhy = pp.Target.GrantKey(), api.ApprovalWhy(pp.Kind, pp.Target.Reasons)
			}
		}
		d.Passes = append(d.Passes, sp)
	}
	d.CI = a.ciState(m.info, man)
	return d
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func accountLabel(who *api.WhoAmI) string {
	org := firstNonEmpty(who.OrganizationName, "-")
	if who.Organization != nil {
		org = firstNonEmpty(who.Organization.Name, who.Organization.Slug, org)
	}
	switch {
	case who.Principal != nil && who.Principal.User != nil:
		return who.Principal.User.Email + " · " + org
	case who.Principal != nil && who.Principal.Kind == api.TokenKindRepo:
		return "repository token · " + org
	}
	return "organization token · " + org
}

func renderShow(p *ui.Printer, d showData) {
	lines := []string{
		"Product   " + firstNonEmpty(d.Manifest.Product, "(the repository's product)"),
		"Manifest  " + d.Manifest.Path + "  " + p.Dim(shortHash(d.Manifest.Hash)),
		"Branch    " + firstNonEmpty(d.Repo.Branch, "detached") + " @ " + shortSHA(d.Repo.Head),
	}
	if d.SignedIn {
		lines = append(lines, "Account   "+d.Account)
	} else {
		lines = append(lines, "Account   "+p.Paint(ui.ToneWarn, "not signed in")+" (local view; gravity login adds server checks)")
	}
	p.Card("Gravity · "+d.Repo.Name, lines, nil)
	renderStructure(p, d.Structure, d.live)
	for _, vm := range d.Verbatim {
		renderMapping(p, vm)
	}
	renderPasses(p, d.Passes)
	renderCI(p, d.CI)
	p.Println("")
	switch {
	case d.Errors > 0:
		p.Note("", ui.MarkFail, "%s, %s · run %s for details and fixes", plural(d.Errors, "error", "errors"), plural(d.Warnings, "warning", "warnings"), p.Bold("gravity validate"))
	case d.Warnings > 0:
		p.Note("", ui.MarkWarn, "%s · run %s for details", plural(d.Warnings, "warning", "warnings"), p.Bold("gravity validate"))
	default:
		p.Note("", ui.MarkOK, "no issues found%s", optional(" (local checks only)", d.Server != serverChecked))
	}
}

func renderStructure(p *ui.Printer, st *api.Structure, live map[string]bool) {
	if st == nil {
		p.Section("Structure", "")
		p.Println("  No structure: declared. Draft one with %s.", p.Bold("gravity structure plan"))
		return
	}
	sub := ""
	if live != nil {
		sub = p.Paint(ui.ToneOK, "=") + " exists  " + p.Paint(ui.ToneInfo, "+") + " created by structure apply  " + p.Paint(ui.ToneAccent, p.Glyph("↓", "v")) + " created by the import"
	}
	p.Section("Structure", sub)
	p.Tree("  ", structureTree(p, st, live))
}

func liveMark(p *ui.Printer, live map[string]bool, key string, imported bool) string {
	switch {
	case live == nil:
		return ""
	case live[key]:
		return p.Paint(ui.ToneOK, "=") + " "
	case imported:
		return p.Paint(ui.ToneAccent, p.Glyph("↓", "v")) + " "
	}
	return p.Paint(ui.ToneInfo, "+") + " "
}

func structureTree(p *ui.Printer, st *api.Structure, live map[string]bool) ui.TreeNode {
	root := ui.TreeNode{Label: p.Bold(firstNonEmpty(st.Site.Name, st.Site.Slug)) + "  " + p.Dim("site "+st.Site.Slug)}
	byParent := map[string][]api.StructureSpace{}
	for _, sp := range st.Spaces {
		byParent[sp.Parent] = append(byParent[sp.Parent], sp)
	}
	var space func(sp api.StructureSpace) ui.TreeNode
	space = func(sp api.StructureSpace) ui.TreeNode {
		meta := []string{sp.Slug}
		if sp.Type != "" {
			meta = append(meta, sp.Type)
		}
		if sp.Visibility != "" {
			meta = append(meta, sp.Visibility)
		}
		n := ui.TreeNode{Label: liveMark(p, live, sp.Slug, false) + p.Bold(firstNonEmpty(sp.Name, sp.Slug)) + "  " + p.Dim(strings.Join(meta, " · "))}
		n.Children = append(n.Children, pageNodes(p, live, sp.Slug, nil, sp.Pages)...)
		n.Children = append(n.Children, collectionNodes(p, live, sp.Slug, nil, sp.Collections)...)
		for _, child := range byParent[sp.Slug] {
			n.Children = append(n.Children, space(child))
		}
		return n
	}
	for _, sp := range byParent[""] {
		root.Children = append(root.Children, space(sp))
	}
	return root
}

func collectionNodes(p *ui.Printer, live map[string]bool, space string, path []string, cols []api.StructureCollection) []ui.TreeNode {
	out := make([]ui.TreeNode, 0, len(cols))
	for _, c := range cols {
		cp := append(append([]string{}, path...), c.Slug)
		key := space + "/" + strings.Join(cp, "/")
		n := ui.TreeNode{Label: liveMark(p, live, key, false) + p.Paint(ui.ToneInfo, firstNonEmpty(c.Title, c.Slug)+"/") + "  " + p.Dim(c.Slug)}
		n.Children = append(n.Children, pageNodes(p, live, space, cp, c.Pages)...)
		n.Children = append(n.Children, collectionNodes(p, live, space, cp, c.Collections)...)
		out = append(out, n)
	}
	return out
}

func pageNodes(p *ui.Printer, live map[string]bool, space string, path []string, pages []api.StructurePage) []ui.TreeNode {
	out := make([]ui.TreeNode, 0, len(pages))
	for _, pg := range pages {
		label := liveMark(p, live, space+"/"+strings.Join(path, "/")+"#"+pg.Slug, pg.Source != "") + firstNonEmpty(pg.Title, pg.Slug) + "  " + p.Dim(pg.Slug)
		if pg.Source != "" {
			label += "  " + p.Paint(ui.ToneAccent, p.Glyph("←", "<-")+" "+pg.Source)
		}
		out = append(out, ui.TreeNode{Label: label})
	}
	return out
}

var flagLabels = map[string]struct{ text, tone string }{
	flagEmpty:          {"empty", ui.ToneWarn},
	flagDuplicateSlug:  {"duplicate slug", ui.ToneFail},
	flagPackageTitle:   {"package-name title", ui.ToneWarn},
	flagExistingPage:   {"page exists", ui.ToneFail},
	flagNotInStructure: {"not in structure", ui.ToneDim},
}

func flagText(p *ui.Printer, flags []string) string {
	parts := make([]string, 0, len(flags))
	for _, f := range flags {
		l, ok := flagLabels[f]
		if !ok {
			l.text, l.tone = f, ui.ToneDim
		}
		parts = append(parts, p.Paint(l.tone, l.text))
	}
	return strings.Join(parts, ", ")
}

func renderMapping(p *ui.Printer, vm verbatimMap) {
	p.Section("Verbatim · "+vm.Pass, vm.Target+" · "+plural(len(vm.Pages), "page", "pages"))
	if len(vm.Pages) == 0 {
		p.Println("  No file matches options.files.")
		return
	}
	rows := make([][]string, 0, len(vm.Pages))
	arrow := p.Glyph("→", "->")
	for _, pg := range vm.Pages {
		rows = append(rows, []string{pg.Source, arrow, orDash(strings.Join(pg.CollectionPath, "/")), pg.Slug, pg.Title, flagText(p, pg.Flags)})
		for _, t := range pg.Translations {
			rows = append(rows, []string{"  " + p.Glyph("↳", "+") + " " + t.Source, arrow, "", "", "[" + t.Language + "]", ""})
		}
	}
	p.Grid("  ", []string{"Source", "", "Collection", "Slug", "Title", "Flags"}, rows)
	for _, w := range vm.Warnings {
		p.Note("  ", ui.MarkWarn, "%s", w)
	}
}

func estimateText(e *api.Estimate) string {
	if e == nil {
		return "-"
	}
	parts := []string{}
	switch {
	case !e.AI && (e.ApproxCostUSD == nil || *e.ApproxCostUSD == 0):
		parts = append(parts, "free")
	case e.ApproxCostUSD != nil:
		parts = append(parts, fmt.Sprintf("~$%.2f", *e.ApproxCostUSD))
	case e.AI:
		parts = append(parts, "cost n/a")
	}
	if e.FirstRun {
		parts = append(parts, "first run")
	} else if e.Commits > 0 {
		parts = append(parts, plural(e.Commits, "commit", "commits"))
	}
	return strings.Join(parts, " · ")
}

func languagesText(sp showPass) string {
	if len(sp.Languages) == 0 && len(sp.EffectiveLanguages) == 0 {
		return "-"
	}
	asked := strings.Join(sp.Languages, ",")
	if sp.I18n == "" && len(sp.EffectiveLanguages) == 0 {
		return asked
	}
	got := strings.Join(sp.EffectiveLanguages, ",")
	if got == asked {
		return asked
	}
	return orDash(asked) + " → " + firstNonEmpty(got, "none")
}

func renderPasses(p *ui.Printer, list []showPass) {
	p.Section("Passes", plural(len(list), "pass", "passes"))
	if len(list) == 0 {
		p.Println("  No passes declared. %s suggests them from the repository.", p.Bold("gravity setup"))
		return
	}
	rows := make([][]string, 0, len(list))
	for _, sp := range list {
		name := sp.Name
		if !sp.Enabled {
			name += " " + p.Dim("(disabled)")
		}
		target := orDash(sp.Target)
		if sp.TargetStatus != "" && sp.TargetStatus != api.TargetOK {
			target += " " + p.Paint(ui.ToneWarn, "("+strings.ReplaceAll(sp.TargetStatus, "_", " ")+")")
		}
		ai := p.Dim("no")
		if sp.AI {
			ai = p.Paint(ui.ToneAccent, "yes")
		}
		when := strings.Join(sp.Triggers, ",")
		if len(sp.Branches) > 0 {
			when += " @ " + strings.Join(sp.Branches, ",")
		}
		rows = append(rows, []string{name, sp.Kind, target, orDash(when), orDash(strings.Join(sp.Audiences, ",")), languagesText(sp), ai, estimateText(sp.Estimate)})
	}
	p.Grid("  ", []string{"Pass", "Kind", "Target", "Runs on", "Audiences", "Languages", "AI", "Estimate"}, rows)
	for _, sp := range list {
		if sp.I18n != "" {
			p.Note("  ", ui.MarkInfo, "%s languages: %s", sp.Name, sp.I18n)
		}
		if sp.Grant != "" {
			why := ""
			if sp.ApprovalWhy != "" {
				why = " (" + sp.ApprovalWhy + ")"
			}
			p.Note("  ", ui.MarkWarn, "%s needs a grant for %s%s: gravity approve %s", sp.Name, sp.Grant, why, sp.Grant)
		}
	}
}

func renderCI(p *ui.Printer, c showCI) {
	p.Section("CI", c.Label)
	if len(c.Files) == 0 {
		if c.Snippet {
			p.Println("  %s is configured by hand: %s prints the snippet.", c.Label, p.Bold("gravity ci setup"))
		} else {
			p.Println("  No CI provider detected. %s writes one.", p.Bold("gravity ci setup"))
		}
		return
	}
	for _, f := range c.Files {
		mark := ui.MarkOK
		switch f.Status {
		case "missing":
			mark = ui.MarkSkip
		case "outdated":
			mark = ui.MarkWarn
		}
		line := f.Path + "  " + p.Dim(f.Status)
		if f.Note != "" {
			line += "  " + p.Dim(f.Note)
		}
		p.Note("  ", mark, "%s", line)
	}
	for _, f := range c.Files {
		if f.Status == "missing" {
			p.Println("  Run %s to write it and install the token.", p.Bold("gravity ci setup"))
			break
		}
	}
}
