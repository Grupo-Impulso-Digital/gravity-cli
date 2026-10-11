package passes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// PR preview modes of AI passes.
const (
	PreviewOff    = "off"
	PreviewImpact = "impact"
	PreviewFull   = "full"
)

// Guides keeps guide pages true to the code: impact analysis, an AI plan, then block-level edits per page.
type Guides struct{}

// Kind is guides.
func (Guides) Kind() string { return config.KindGuides }

// Candidate is a page the change set may affect.
type Candidate struct {
	PageID  string   `json:"pageId"`
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Reasons []string `json:"reasons"`
	Units   []string `json:"units,omitempty"`
}

// ReachPage is a page outside the pass target that the pass may update through a unit this repository contributes to.
type ReachPage struct {
	PageID string   `json:"pageId"`
	Slug   string   `json:"slug"`
	Site   string   `json:"site"`
	Space  string   `json:"space"`
	Units  []string `json:"units"`
	Role   string   `json:"role"`
}

// Impact is the deterministic first step of guides: affected pages, unit-reach pages, undocumented units and hints.
type Impact struct {
	Candidates   []Candidate    `json:"candidates"`
	Reach        []ReachPage    `json:"reach,omitempty"`
	Undocumented []api.Unit     `json:"undocumented"`
	Hints        []api.PlanHint `json:"hints"`
	Tree         *api.SpaceTree `json:"-"`
}

func targetPages(in Input, tree *api.SpaceTree) map[string]api.TreePage {
	prefix := in.CollectionPrefix()
	out := map[string]api.TreePage{}
	for _, p := range tree.Pages {
		if p.Lock != nil || !hasPrefix(p.CollectionPath, prefix) {
			continue
		}
		out[p.ID] = p
	}
	return out
}

func pendingChanges(ctx context.Context, in Input, rep *Report, p api.TreePage, op *api.OpenProposal) {
	ref := api.PageRef{ID: p.ID, Slug: p.Slug, Title: p.Title}
	if op.Pending == nil {
		if op.PipelineRunID == "" || op.PipelineRunID == in.RunID {
			return
		}
		if ownPendingChange(ctx, in, p.ID, op.PipelineRunID) {
			rep.warn("%s: replaces this pass's pending change from %s, which the platform supersedes", p.Slug, op.PipelineRunID)
			return
		}
		rep.Competing = append(rep.Competing, Competition{Page: ref, With: []api.CompetingChange{{RunID: op.PipelineRunID}}, Pending: true})
		return
	}
	var others []api.CompetingChange
	var own []string
	for _, pc := range *op.Pending {
		if pc.RunID == in.RunID && in.RunID != "" {
			continue
		}
		if pc.Pass == in.Pass.Name && (in.Info.ID != "" && pc.RepoID == in.Info.ID || pc.RemoteKey != "" && pc.RemoteKey == in.Info.RemoteKey) {
			own = append(own, pc.RunID)
			continue
		}
		others = append(others, api.CompetingChange{RunID: pc.RunID, Repo: firstOf(pc.RemoteKey, pc.RepoID) + " (pass " + pc.Pass + ")"})
	}
	if len(others) > 0 {
		rep.Competing = append(rep.Competing, Competition{Page: ref, With: others, Pending: true})
		return
	}
	if len(own) > 0 {
		rep.warn("%s: replaces this pass's pending change from %s, which the platform supersedes", p.Slug, strings.Join(own, ", "))
	}
}

func createPath(prefix, planned []string) []string {
	var rest []string
	for _, seg := range planned {
		if strings.Trim(seg, " -_/") != "" {
			rest = append(rest, normalize.ProductSlug(seg))
		}
	}
	if hasPrefix(rest, prefix) {
		rest = rest[len(prefix):]
	}
	return append(append([]string{}, prefix...), rest...)
}

func ownPendingChange(ctx context.Context, in Input, pageID, runID string) bool {
	page, err := in.Client.Page(ctx, pageID, api.PageQuery{State: "draft", Format: "json"})
	if err != nil {
		return false
	}
	found := false
	for _, b := range page.Blocks {
		pv := b.Provenance
		if pv == nil || pv.RunID != runID {
			continue
		}
		if pv.Pass != in.Pass.Name || pv.Repo != in.Info.Name && pv.Repo != in.Info.RemoteKey {
			return false
		}
		found = true
	}
	return found
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func ownedUnit(u api.Unit, info RepoInfo) bool {
	for _, c := range u.Contributors {
		if c.Repo.RemoteKey == info.RemoteKey || c.Repo.RemoteKey == "" && c.Repo.Name == info.Name {
			if c.Active == nil || *c.Active {
				return true
			}
		}
	}
	return false
}

// ComputeImpact finds the pages a range affects inside the pass target.
func ComputeImpact(ctx context.Context, in Input) (*Impact, error) {
	tree, err := in.Client.SpaceTree(ctx, in.SpaceID())
	if err != nil {
		return nil, fmt.Errorf("read target %s: %w", in.Pass.Target.Ref, err)
	}
	imp := &Impact{Tree: tree}
	pages := targetPages(in, tree)
	inTree := map[string]api.TreePage{}
	for _, p := range tree.Pages {
		inTree[p.ID] = p
	}
	own := NewOwnership(in.Plan, in.Info)
	reach := map[string]*ReachPage{}
	var reachOrder []string
	addReach := func(b api.UnitBinding, unit string) {
		if p, ok := inTree[b.PageID]; ok && p.Lock != nil {
			return
		}
		r, ok := reach[b.PageID]
		if !ok {
			if len(reachOrder) == in.IntOption("maxPages", 25) {
				return
			}
			r = &ReachPage{PageID: b.PageID, Slug: b.PageSlug, Site: b.SiteSlug, Space: b.SpaceSlug, Role: own.Role(unit)}
			reach[b.PageID] = r
			reachOrder = append(reachOrder, b.PageID)
		}
		r.Units = appendUnique(r.Units, unit)
	}
	inv := map[string]api.Unit{}
	for _, u := range in.Plan.Inventory.Units {
		inv[u.Key] = u
	}
	kinds := map[string]bool{}
	for _, k := range in.Pass.Scope.Units {
		kinds[k] = true
	}
	inScope := func(kind string) bool { return len(kinds) == 0 || kinds[kind] }
	maxPages := in.IntOption("maxPages", 25)
	byID := map[string]*Candidate{}
	var order []string
	add := func(p api.TreePage, reason string, units ...string) {
		c, ok := byID[p.ID]
		if !ok {
			c = &Candidate{PageID: p.ID, Slug: p.Slug, Title: p.Title}
			byID[p.ID] = c
			order = append(order, p.ID)
		}
		c.Reasons = appendUnique(c.Reasons, reason)
		c.Units = appendUnique(c.Units, units...)
	}
	type touched struct{ key, kind, change string }
	var units []touched
	if in.ChangeSet != nil {
		for _, u := range in.ChangeSet.Units.Touched {
			units = append(units, touched{u.Key, u.Kind, u.Change})
		}
		for _, u := range in.ChangeSet.Units.Added {
			units = append(units, touched{u.Key, u.Kind, "added"})
		}
		for _, u := range in.ChangeSet.Units.Removed {
			units = append(units, touched{u.Key, u.Kind, "removed"})
		}
	}
	site, space := in.SiteSlug(), in.SpaceSlug()
	for _, t := range units {
		if !inScope(t.kind) {
			continue
		}
		u, known := inv[t.key]
		bound := false
		for _, b := range u.Bindings {
			if p, ok := pages[b.PageID]; ok && b.SiteSlug == site && b.SpaceSlug == space {
				add(p, fmt.Sprintf("unit %s %s", t.key, t.change), t.key)
				bound = true
				continue
			}
			if own.Contributes(t.key) {
				addReach(b, t.key)
			}
		}
		if bound {
			continue
		}
		if !known {
			u = api.Unit{Key: t.key, Kind: t.kind, Title: t.key}
		}
		if t.change != "removed" {
			imp.Undocumented = append(imp.Undocumented, u)
		}
		hits, err := in.Client.Search(ctx, api.SearchRequest{Query: firstOf(u.Title, t.key), SpaceIDs: []string{in.SpaceID()}, Limit: 3, IncludeDrafts: true})
		if err != nil {
			if api.StopsRun(err) {
				return nil, err
			}
			continue
		}
		for _, h := range hits {
			if p, ok := pages[h.PageID]; ok {
				add(p, fmt.Sprintf("search hit for unit %s (%s)", t.key, t.change), t.key)
			}
		}
	}
	for _, h := range in.Pass.Hints {
		if h.Page == nil {
			continue
		}
		if p, ok := pages[h.Page.ID]; ok {
			add(p, fmt.Sprintf("hint %s: %s", h.Kind, h.Claim), h.UnitKey)
			imp.Hints = append(imp.Hints, h)
		}
	}
	if in.Range.Kind == api.RangeSurvey {
		bound := map[string]bool{}
		for _, u := range in.Plan.Inventory.Units {
			if !inScope(u.Kind) || !ownedUnit(u, in.Info) {
				continue
			}
			documented := false
			for _, b := range u.Bindings {
				if b.SiteSlug == site && b.SpaceSlug == space {
					documented = true
					if p, ok := pages[b.PageID]; ok && !bound[p.ID] && len(bound) < maxPages {
						bound[p.ID] = true
						add(p, "survey: bound to "+u.Key, u.Key)
					}
				}
			}
			if !documented {
				imp.Undocumented = append(imp.Undocumented, u)
			}
		}
	}
	if len(order) == 0 && len(imp.Undocumented) == 0 && in.ChangeSet != nil {
		for i, c := range in.ChangeSet.Commits {
			if i == 3 {
				break
			}
			hits, err := in.Client.Search(ctx, api.SearchRequest{Query: c.Subject, SpaceIDs: []string{in.SpaceID()}, Limit: 2, IncludeDrafts: true})
			if err != nil {
				if api.StopsRun(err) {
					return nil, err
				}
				continue
			}
			for _, h := range hits {
				if p, ok := pages[h.PageID]; ok {
					add(p, "search hit for commit "+short(c.SHA))
				}
			}
		}
	}
	limit := 3 * maxPages
	for i, id := range order {
		if i == limit {
			break
		}
		imp.Candidates = append(imp.Candidates, *byID[id])
	}
	for _, id := range reachOrder {
		if _, inTarget := byID[id]; !inTarget {
			imp.Reach = append(imp.Reach, *reach[id])
		}
	}
	return imp, nil
}

func appendUnique(list []string, vals ...string) []string {
	for _, v := range vals {
		if v == "" {
			continue
		}
		dup := false
		for _, x := range list {
			if x == v {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, v)
		}
	}
	return list
}

func (imp *Impact) render(b *strings.Builder) {
	b.WriteString("<candidate_pages>\n")
	if len(imp.Candidates) == 0 {
		b.WriteString("(none found by unit bindings or search)\n")
	}
	for _, c := range imp.Candidates {
		fmt.Fprintf(b, "- %s %s %q: %s\n", c.PageID, c.Slug, c.Title, strings.Join(c.Reasons, "; "))
	}
	b.WriteString("</candidate_pages>\n")
	if len(imp.Reach) > 0 {
		b.WriteString("<reach_pages>\n")
		for _, r := range imp.Reach {
			fmt.Fprintf(b, "- %s %s/%s/%s: units %s; this repository %s them; editable: blocks bound to these units\n", r.PageID, r.Site, r.Space, r.Slug, strings.Join(r.Units, ", "), firstOf(r.Role, "contributes to"))
		}
		b.WriteString("</reach_pages>\n")
	}
	if len(imp.Undocumented) > 0 {
		b.WriteString("<undocumented_units>\n")
		for _, u := range imp.Undocumented {
			fmt.Fprintf(b, "- %s (%s) %s\n", u.Key, u.Kind, u.Title)
		}
		b.WriteString("</undocumented_units>\n")
	}
	if len(imp.Hints) > 0 {
		b.WriteString("<open_hints>\n")
		for _, h := range imp.Hints {
			from := ""
			if h.RaisedBy != nil {
				from = " from " + h.RaisedBy.Repo
			}
			fmt.Fprintf(b, "- %s %s%s: %s (unit %s, block %s)\n", h.ID, h.Kind, from, h.Claim, h.UnitKey, h.BlockKey)
		}
		b.WriteString("</open_hints>\n")
	}
}

func targetPath(in Input, collection []string) string {
	target := firstOf(in.SpaceSlug(), "-")
	if len(collection) > 0 {
		target += "/" + strings.Join(collection, "/")
	}
	return target
}

func dataSections(b *strings.Builder, in Input, units []string, cs *changeset.ChangeSet) {
	if u := strings.TrimSpace(UnitDetails(in.Plan, units)); u != "" {
		fmt.Fprintf(b, "<units>\n%s\n</units>\n", u)
	}
	fmt.Fprintf(b, "<change_summary>\n%s</change_summary>\n", ChangeSummary(cs, in.Range))
}

func planKickoff(in Input, imp *Impact, maxPages int, createPages, deprecate bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<task phase=%q>\n", api.PhaseGuidesPlan)
	b.WriteString(passHeader(in))
	fmt.Fprintf(&b, "Target: %s\n", targetPath(in, in.CollectionPrefix()))
	newPages, removed := "not allowed", "deleted"
	if createPages {
		newPages = "allowed"
	}
	if deprecate {
		removed = "kept with a deprecation notice"
	}
	fmt.Fprintf(&b, "Page budget: %d · New pages: %s · Deprecated pages: %s\n", maxPages, newPages, removed)
	b.WriteString("</task>\n")
	imp.render(&b)
	dataSections(&b, in, touchedKeys(in), in.ChangeSet)
	return b.String()
}

func toolset(in Input) []agent.Tool {
	tools := agent.RepoTools(in.Repo, agent.Range{Base: in.Range.Base, Head: in.Range.Head, WorkingTree: in.Range.Head == ""})
	return append(tools, agent.DocTools(in.Client, docScope(in))...)
}

func touchedKeys(in Input) []string {
	var keys []string
	if in.ChangeSet == nil {
		return keys
	}
	for _, u := range in.ChangeSet.Units.Touched {
		keys = append(keys, u.Key)
	}
	for _, u := range in.ChangeSet.Units.Added {
		keys = append(keys, u.Key)
	}
	for _, u := range in.ChangeSet.Units.Removed {
		keys = append(keys, u.Key)
	}
	return keys
}

// Run plans and authors guide changes for the pages the range affects.
func (Guides) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	if in.SpaceID() == "" {
		return rep, fmt.Errorf("pass %s has no target space", in.Pass.Name)
	}
	if in.Harness == nil {
		return rep, errors.New("guides passes need the LLM gateway")
	}
	mode := PreviewFull
	if in.Trigger == config.TriggerPR && !in.Preview {
		mode = in.StringOption("prPreview", PreviewImpact)
	}
	if mode == PreviewOff {
		rep.Summary = "pull request preview is off for this pass"
		return rep, nil
	}
	imp, err := ComputeImpact(ctx, in)
	if err != nil {
		return rep, err
	}
	maxPages := in.IntOption("maxPages", 25)
	createPages := in.BoolOption("createPages", true)
	deprecate := in.BoolOption("deprecate", true)
	list := agent.NewPlanList(maxPages)
	task := agent.Task{Phase: api.PhaseGuidesPlan, Purpose: api.PurposePlan, Kickoff: planKickoff(in, imp, maxPages, createPages, deprecate), Tools: append(toolset(in), list.AddTool()), Submit: list.FinishTool()}
	if _, err := in.Harness.Run(ctx, task); err != nil {
		return rep, fmt.Errorf("plan: %w", err)
	}
	if summary := list.Summary(); summary != "" {
		in.logf("plan: %s", summary)
	}
	pages := map[string]api.TreePage{}
	bySlug := map[string]api.TreePage{}
	for _, p := range imp.Tree.Pages {
		pages[p.ID] = p
		bySlug[p.Slug] = p
	}
	reach := map[string]ReachPage{}
	for _, r := range imp.Reach {
		reach[r.PageID] = r
	}
	pending := map[string]bool{}
	viaUnit := map[string]bool{}
	var actions []agent.PageAction
	for _, a := range list.Pages() {
		if a.PageID == "" && a.Action != agent.ActionCreate {
			if p, ok := bySlug[a.Slug]; ok {
				a.PageID = p.ID
			}
		}
		if r, ok := reach[a.PageID]; ok && a.Action != agent.ActionCreate {
			if a.Action != agent.ActionUpdate {
				rep.warn("page %s is outside the target; through unit reach only its bound blocks can be updated; skipped", r.Slug)
				continue
			}
			a.Slug = firstOf(r.Slug, a.Slug)
			viaUnit[a.PageID] = true
			actions = append(actions, a)
			if len(actions) == maxPages {
				break
			}
			continue
		}
		if a.Action != agent.ActionCreate {
			p, ok := pages[a.PageID]
			if !ok {
				rep.warn("plan names unknown page %s; skipped", firstOf(a.PageID, a.Slug))
				continue
			}
			if op := p.OpenProposal; op != nil && !pending[p.ID] {
				pending[p.ID] = true
				pendingChanges(ctx, in, &rep, p, op)
			}
			if p.Lock != nil {
				rep.warn("page %s is locked to %s; skipped", p.Slug, p.Lock.Path)
				continue
			}
			a.Slug, a.Title = p.Slug, firstOf(a.Title, p.Title)
		} else {
			if !createPages {
				rep.warn("plan proposes new page %s but new pages are off; skipped", a.Slug)
				continue
			}
			if _, taken := bySlug[a.Slug]; taken {
				rep.warn("plan proposes new page %s but the slug exists; skipped", a.Slug)
				continue
			}
		}
		actions = append(actions, a)
		if len(actions) == maxPages {
			break
		}
	}
	if out.Dry() && mode == PreviewImpact {
		for _, a := range actions {
			reason := a.Why
			if viaUnit[a.PageID] {
				reason = reachReason(reach[a.PageID], reason)
			}
			rep.impact(api.PageRef{ID: a.PageID, Slug: a.Slug, Title: a.Title}, a.Action, reason)
		}
		rep.Summary = impactLine(len(actions), in.TargetLabel())
		return rep, nil
	}
	var hints []api.HintInput
	var failures []string
	authored := map[string]bool{}
	for _, a := range actions {
		var h []api.HintInput
		var err error
		if viaUnit[a.PageID] {
			h, err = reachAction(ctx, in, out, &rep, a, reach[a.PageID])
		} else {
			h, err = guidesAction(ctx, in, out, &rep, a, deprecate)
		}
		if err != nil {
			if endsPass(err) {
				return rep, err
			}
			failures = append(failures, fmt.Sprintf("%s: %v", a.Slug, err))
			continue
		}
		authored[a.PageID] = true
		for _, u := range a.Units {
			authored[u] = true
		}
		hints = append(hints, h...)
	}
	if err := raiseHints(ctx, in, out, &rep, hints); err != nil {
		return rep, err
	}
	for _, h := range in.Pass.Hints {
		if h.Page != nil && authored[h.Page.ID] || h.UnitKey != "" && authored[h.UnitKey] {
			rep.ConsumedHintIDs = append(rep.ConsumedHintIDs, h.ID)
		}
	}
	rep.UnitsTouched = touchedKeys(in)
	rep.Summary = summaryLine(rep.Counts, in.TargetLabel())
	if len(failures) > 0 {
		rep.Errors = failures
		return rep, fmt.Errorf("%d page(s) failed: %s", len(failures), strings.Join(failures, "; "))
	}
	return rep, nil
}

func withWhy(summary, why string) string {
	why = strings.TrimSpace(why)
	if why == "" || strings.Contains(summary, why) {
		return summary
	}
	return summary + "\n\nWhy: " + why
}

// endsPass reports an error that ends the pass instead of failing one page: the run stopped or halted, a license refusal, or a cancellation.
func endsPass(err error) bool {
	return api.StopsRun(err) || api.HaltsRun(err) || api.IsLicenseError(err) || errors.Is(err, context.Canceled)
}

func impactLine(n int, target string) string {
	switch n {
	case 0:
		return "no impact on " + target
	case 1:
		return "1 page would change in " + target
	}
	return fmt.Sprintf("%d pages would change in %s", n, target)
}

func guidesAction(ctx context.Context, in Input, out Sink, rep *Report, a agent.PageAction, deprecate bool) ([]api.HintInput, error) {
	ref := api.PageRef{ID: a.PageID, Slug: a.Slug, Title: a.Title}
	if a.Action == agent.ActionDeprecate && !deprecate {
		c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpDelete, Target: api.ChangeTarget{PageID: a.PageID}, Summary: a.Why})
		if err != nil {
			return nil, err
		}
		rep.Counts.Deleted++
		rep.applied(c)
		rep.impact(ref, api.OpDelete, a.Why)
		return nil, nil
	}
	mode, file := agent.DraftCreate, "new-"+a.Slug
	var current *api.PageContent
	if a.Action != agent.ActionCreate {
		var err error
		current, err = in.Client.Page(ctx, a.PageID, api.PageQuery{State: "draft", Format: "json"})
		if err != nil {
			return nil, fmt.Errorf("read page: %w", err)
		}
		if current.Page.Lock != nil {
			return nil, fmt.Errorf("page is locked to %s", current.Page.Lock.Path)
		}
		mode, file = agent.DraftUpdate, a.PageID
		if a.Action == agent.ActionDeprecate {
			mode = agent.DraftDeprecate
		}
	}
	d, err := newDraft(ctx, in, draftSpec{mode: mode, slug: a.Slug, keyPrefix: agent.KeyPrefixGuide, file: file, page: current, mustCover: a.MustCover})
	if err != nil {
		return nil, err
	}
	collection := a.CollectionPath
	if current != nil {
		collection = current.Page.CollectionPath
	}
	kickoff := writerKickoff(in, d, kickoffData{mode: mode, title: a.Title, slug: a.Slug, collection: collection, mustCover: a.MustCover, notes: a.Why, units: a.Units})
	if err := runDraft(ctx, in, d, api.PhaseGuidesWrite, kickoff); err != nil {
		return nil, fmt.Errorf("author: %w", err)
	}
	summary := firstOf(d.Summary(), a.Why)
	blocks, remove := draftBlocks(in, d, summary, nil)
	hints := draftHints(d, a.PageID)
	if len(blocks) == 0 && len(remove) == 0 {
		rep.Counts.Unchanged++
		return hints, nil
	}
	units := in.Known.Filter(append(append([]string{}, a.Units...), changeUnits(blocks)...))
	req := api.ChangeRequest{Summary: withWhy(summary, a.Why), Blocks: blocks, RemoveBlockKeys: remove, Units: units, Languages: in.Languages()}
	if a.Action == agent.ActionCreate {
		req.Op = api.OpCreate
		req.Title = a.Title
		req.Target = api.ChangeTarget{SpaceID: in.SpaceID(), Slug: a.Slug, CollectionPath: createPath(in.CollectionPrefix(), a.CollectionPath)}
	} else {
		req.Op = api.OpUpdate
		req.Target = api.ChangeTarget{PageID: a.PageID}
	}
	c, err := out.Change(ctx, req)
	if err != nil {
		return nil, err
	}
	if a.Action == agent.ActionCreate {
		rep.Counts.Created++
	} else {
		rep.Counts.Updated++
	}
	rep.Documented = appendUnique(rep.Documented, units...)
	rep.applied(c)
	rep.impact(ref, req.Op, req.Summary)
	return hints, nil
}

func raiseHints(ctx context.Context, in Input, out Sink, rep *Report, hints []api.HintInput) error {
	if len(hints) == 0 {
		return nil
	}
	if in.Plan != nil && !in.Plan.Capabilities.Features["cross-repo-hints"] {
		rep.warn("%d hint(s) for other repositories not sent: this server does not accept cross-repo hints", len(hints))
		return nil
	}
	hints, dropped := deliverableHints(in, hints)
	if dropped > 0 {
		rep.warn("%d hint(s) not sent: no other repository of this product contributes to the unit", dropped)
	}
	if len(hints) == 0 {
		return nil
	}
	sort.SliceStable(hints, func(i, j int) bool { return hints[i].Claim < hints[j].Claim })
	res, err := out.Hints(ctx, hints)
	if err != nil && api.HasCode(err, api.CodeBadRequest) && len(hints) > 1 {
		var created []string
		for _, h := range hints {
			one, oneErr := out.Hints(ctx, []api.HintInput{h})
			if oneErr != nil {
				if api.StopsRun(oneErr) || api.IsLicenseError(oneErr) {
					return oneErr
				}
				rep.warn("raise hint %q: %v", h.Claim, oneErr)
				continue
			}
			created = append(created, one.Created...)
		}
		res, err = &api.HintsResult{Created: created}, nil
	}
	if err != nil {
		if api.StopsRun(err) || api.IsLicenseError(err) {
			return err
		}
		rep.warn("raise hints: %v", err)
		return nil
	}
	rep.Counts.Hints += len(res.Created)
	if out.Dry() {
		rep.Counts.Hints += len(hints)
	}
	return nil
}

func deliverableHints(in Input, hints []api.HintInput) ([]api.HintInput, int) {
	if in.Plan == nil {
		return hints, 0
	}
	siblings := map[string]string{}
	add := func(name, remoteKey string) {
		if remoteKey == "" || remoteKey == in.Info.RemoteKey {
			return
		}
		siblings[remoteKey] = remoteKey
		if name != "" {
			siblings[name] = remoteKey
		}
	}
	for _, s := range in.Plan.Siblings {
		add(s.Name, s.RemoteKey)
	}
	for _, u := range in.Plan.Inventory.Units {
		for _, c := range u.Contributors {
			add(c.Repo.Name, c.Repo.RemoteKey)
		}
	}
	if len(siblings) == 0 {
		return nil, len(hints)
	}
	own := NewOwnership(in.Plan, in.Info)
	out := make([]api.HintInput, 0, len(hints))
	dropped := 0
	for _, h := range hints {
		var to []string
		for _, r := range h.ForRepos {
			if key, ok := siblings[r]; ok {
				to = append(to, key)
			}
		}
		h.ForRepos = to
		switch {
		case len(to) > 0, h.PageID != "" && h.BlockKey != "",
			h.UnitKey != "" && len(own.Others(h.UnitKey, api.RoleImplements, api.RoleDeclares, api.RoleDocuments)) > 0:
			out = append(out, h)
		default:
			dropped++
		}
	}
	return out, dropped
}
