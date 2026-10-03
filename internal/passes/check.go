package passes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// Finding categories of a check pass.
const (
	CategoryDrift    = "drift"
	CategoryCoverage = "coverage"
	CategoryClaims   = "claims"
	CategoryVerbatim = "verbatim"
)

// Finding codes of a check pass.
const (
	CodeDrift                = "drift"
	CodeDriftRemoved         = "drift_removed"
	CodeCoverage             = "coverage"
	CodeCoverageRequired     = "coverage_required"
	CodeClaimContradicted    = "claim_contradicted"
	CodeVerbatimContradicted = "verbatim_contradicted"
)

// DefaultFailOn are the categories that make findings fail a check.
var DefaultFailOn = []string{CategoryDrift, CategoryClaims, CategoryVerbatim}

// Category maps a finding code to its failOn category.
func Category(code string) string {
	switch code {
	case CodeDrift, CodeDriftRemoved:
		return CategoryDrift
	case CodeCoverage, CodeCoverageRequired:
		return CategoryCoverage
	case CodeVerbatimContradicted:
		return CategoryVerbatim
	}
	return CategoryClaims
}

// Check is the read-only documentation gate: drift, coverage, claims and verbatim contradictions.
type Check struct{}

// Kind is check.
func (Check) Kind() string { return config.KindCheck }

// FailOn returns the categories whose findings fail the check.
func FailOn(in Input) []string {
	if len(in.FailOn) > 0 {
		return in.FailOn
	}
	if list := in.StringsOption("failOn"); len(list) > 0 {
		return list
	}
	if _, set := in.Pass.Options["failOn"]; set {
		return nil
	}
	return DefaultFailOn
}

// Failing reports whether findings in failOn categories are errors.
func Failing(findings []api.Finding, failOn []string) bool {
	want := map[string]bool{}
	for _, c := range failOn {
		want[c] = true
	}
	for _, f := range findings {
		if f.Severity == api.SeverityError && want[Category(f.Code)] {
			return true
		}
	}
	return false
}

// Run computes deterministic drift and coverage findings, then reviews claims with the AI when allowed.
func (Check) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	if err := checkDrift(ctx, in, &rep); err != nil {
		return rep, err
	}
	checkCoverage(in, &rep)
	if in.Trigger == config.TriggerPR {
		verbatimNotes(in, &rep)
	}
	if in.BoolOption("claims", true) && in.Harness != nil && in.RunPassID != "" {
		if err := checkClaims(ctx, in, out, &rep); err != nil {
			return rep, err
		}
	}
	rep.FailOn = FailOn(in)
	rep.Failing = Failing(rep.Findings, rep.FailOn)
	switch n := len(rep.Findings); n {
	case 0:
		rep.Summary = "no findings"
	case 1:
		rep.Summary = "1 finding"
	default:
		rep.Summary = fmt.Sprintf("%d findings", n)
	}
	if len(rep.Notes) > 0 {
		rep.Summary += fmt.Sprintf(", %d notes", len(rep.Notes))
	}
	return rep, nil
}

type ownOp struct {
	hash string
	unit string
	spec string
}

type specOps struct {
	head map[string]ownOp
	base map[string]ownOp
}

func (o specOps) changed(key string) bool {
	h, atHead := o.head[key]
	b, atBase := o.base[key]
	return atHead != atBase || h.hash != b.hash
}

func ownOperations(ctx context.Context, in Input) (specOps, error) {
	out := specOps{head: map[string]ownOp{}, base: map[string]ownOp{}}
	sources := []SpecSource{}
	seen := map[string]bool{}
	addSrc := func(s SpecSource) {
		if !seen[s.Path] {
			seen[s.Path] = true
			sources = append(sources, s)
		}
	}
	for _, p := range in.Manifest.OpenAPIFiles() {
		addSrc(SpecSource{Path: p})
	}
	if in.Plan != nil {
		for _, pp := range in.Plan.Passes {
			if pp.Kind == config.KindReference {
				for _, s := range ReferenceSources(pp, nil) {
					addSrc(s)
				}
			}
		}
	}
	if len(sources) == 0 {
		return out, nil
	}
	specs, _, err := ResolveSpecs(ctx, in, sources)
	if err != nil {
		return out, err
	}
	for _, sf := range specs {
		for _, side := range []struct {
			ops []checks.OperationDetail
			to  map[string]ownOp
		}{{sf.Ops, out.head}, {sf.Base, out.base}} {
			for _, op := range side.ops {
				b, err := docs.APIBlock(op, "")
				if err != nil {
					return out, err
				}
				side.to[b.Key] = ownOp{hash: b.SourceBinding.Hash, unit: b.Units[0], spec: sf.File}
			}
		}
	}
	return out, nil
}

func updatingPass(in Input, spec, spaceID string) string {
	if in.Plan == nil {
		return ""
	}
	for _, pp := range in.Plan.Passes {
		if pp.Kind != config.KindReference || !pp.Enabled || pp.Target.Status != api.TargetOK || pp.Target.Space == nil || spaceID != "" && pp.Target.Space.ID != spaceID {
			continue
		}
		if in.Trigger == config.TriggerPR {
			if !containsString(pp.Triggers, config.TriggerPush) || pp.SkipReason == api.SkipBranchMismatch {
				continue
			}
		} else if !pp.Applies {
			continue
		}
		for _, src := range ReferenceSources(pp, in.Manifest) {
			if specMatch(strings.TrimPrefix(src.Path, "./"), spec) {
				return pp.Name
			}
		}
	}
	return ""
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func whenMerged(in Input) string {
	if in.Trigger == config.TriggerPR {
		return "on merge"
	}
	return "when it runs"
}

func inThisChange(in Input) string {
	if in.Trigger == config.TriggerPR {
		return "this pull request"
	}
	return "this range"
}

func ownRoles(in Input) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if in.Plan == nil {
		return out
	}
	for _, u := range in.Plan.Inventory.Units {
		for _, c := range u.Contributors {
			if c.Active != nil && !*c.Active {
				continue
			}
			if isThisRepo(c.Repo, in.Info) {
				if out[u.Key] == nil {
					out[u.Key] = map[string]bool{}
				}
				out[u.Key][c.Role] = true
			}
		}
	}
	return out
}

func checkSpaces(in Input) []string {
	if id := in.SpaceID(); id != "" {
		return []string{id}
	}
	var out []string
	seen := map[string]bool{}
	if in.Plan != nil {
		for _, pp := range in.Plan.Passes {
			if pp.Target.Space != nil && pp.Target.Space.ID != "" && !seen[pp.Target.Space.ID] && (pp.Kind == config.KindReference || pp.Kind == config.KindGuides) {
				seen[pp.Target.Space.ID] = true
				out = append(out, pp.Target.Space.ID)
			}
		}
	}
	return out
}

func checkDrift(ctx context.Context, in Input, rep *Report) error {
	ops, err := ownOperations(ctx, in)
	if err != nil {
		return err
	}
	if len(ops.head)+len(ops.base) == 0 {
		return nil
	}
	ourUnits := map[string]bool{}
	for _, side := range []map[string]ownOp{ops.head, ops.base} {
		for _, op := range side {
			ourUnits[op.unit] = true
		}
	}
	roles := ownRoles(in)
	for _, spaceID := range checkSpaces(in) {
		tree, err := in.Client.SpaceTree(ctx, spaceID)
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return err
			}
			rep.warn("drift: read space %s: %v", spaceID, err)
			continue
		}
		for _, p := range tree.Pages {
			relevant := p.LastWriter != nil && (p.LastWriter.Repo == in.Info.Name || p.LastWriter.Repo == in.Info.RemoteKey)
			for _, u := range p.Units {
				if ourUnits[u] || roles[u][api.RoleImplements] {
					relevant = true
				}
			}
			if !relevant {
				continue
			}
			page, err := in.Client.Page(ctx, p.ID, api.PageQuery{State: "draft", Format: "json"})
			if err != nil {
				if api.StopsRun(err) || api.IsLicenseError(err) {
					return err
				}
				rep.warn("drift: read page %s: %v", p.Slug, err)
				continue
			}
			ref := &api.PageRef{ID: p.ID, Slug: p.Slug, Title: p.Title}
			for _, b := range page.Blocks {
				if b.Type != "api" || b.SourceBinding == nil || (b.SourceBinding.Kind != "endpoint" && b.SourceBinding.Kind != "cli") {
					continue
				}
				driftOf(in, rep, ops, roles, spaceID, ref, b)
			}
		}
	}
	return nil
}

func driftOf(in Input, rep *Report, ops specOps, roles map[string]map[string]bool, spaceID string, ref *api.PageRef, b api.PageBlock) {
	op, ours := ops.head[b.Key]
	switch {
	case ours && b.SourceBinding.Kind == "cli":
		rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityWarning, Code: CodeDrift, Title: fmt.Sprintf("%s: %s was written by gravity v0.x", ref.Title, b.Key), Detail: "The next reference run rewrites it with an endpoint binding.", Page: ref, BlockKey: b.Key, UnitKey: op.unit})
	case ours && b.SourceBinding.Hash == op.hash:
	case ours:
		before, wasThere := ops.base[b.Key]
		if !ops.changed(b.Key) || wasThere && b.SourceBinding.Hash != before.hash {
			rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityWarning, Code: CodeDrift, Title: fmt.Sprintf("%s: %s was already out of date before %s", ref.Title, b.SourceBinding.Ref, inThisChange(in)), Detail: "The api block does not match " + op.spec + "; the next reference run on the default branch rewrites it.", File: op.spec, Page: ref, BlockKey: b.Key, UnitKey: op.unit})
			return
		}
		if pass := updatingPass(in, op.spec, spaceID); pass != "" {
			rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s changes in %s; pass %s updates %s %s", b.SourceBinding.Ref, inThisChange(in), pass, ref.Title, whenMerged(in)), Page: ref})
			return
		}
		rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeDrift, Title: fmt.Sprintf("%s: %s changes in %s and no pass updates it", ref.Title, b.SourceBinding.Ref, inThisChange(in)), Detail: "No enabled reference pass with the push trigger reads " + op.spec + " into this space, so the page keeps describing the old operation. Add or fix a reference pass, or update the page by hand.", File: op.spec, Page: ref, BlockKey: b.Key, UnitKey: op.unit})
	case blockIsOurs(b, in, roles):
		units := blockUnits(b)
		if owners := otherOwners(in, units); len(owners) > 0 {
			rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s documents %s, which this repository no longer defines; %s now owns it", ref.Title, b.Key, strings.Join(owners, ", ")), Page: ref})
			return
		}
		before, removedHere := ops.base[b.Key]
		if !removedHere {
			rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityWarning, Code: CodeDriftRemoved, Title: fmt.Sprintf("%s documents %s, which this repository already did not define before %s", ref.Title, b.Key, inThisChange(in)), Page: ref, BlockKey: b.Key, UnitKey: firstUnit(units)})
			return
		}
		if pass := updatingPass(in, before.spec, spaceID); pass != "" {
			rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s is removed in %s; pass %s removes it from %s %s", b.Key, inThisChange(in), pass, ref.Title, whenMerged(in)), Page: ref})
			return
		}
		rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeDriftRemoved, Title: fmt.Sprintf("%s documents %s, which %s removes, and no pass updates it", ref.Title, b.Key, inThisChange(in)), File: before.spec, Page: ref, BlockKey: b.Key, UnitKey: firstUnit(units)})
	}
}

func isThisRepo(r api.RepoRef, info RepoInfo) bool {
	return r.RemoteKey == info.RemoteKey || r.RemoteKey == "" && r.Name == info.Name
}

func lockedToPass(lock *api.PageLock, pass string, info RepoInfo) bool {
	if lock == nil || lock.Pass != pass {
		return false
	}
	if lock.Repo == nil {
		return true
	}
	if lock.Repo.ID != "" && info.ID != "" {
		return lock.Repo.ID == info.ID
	}
	return isThisRepo(*lock.Repo, info)
}

func blockUnits(b api.PageBlock) []string {
	if len(b.Units) > 0 || b.SourceBinding == nil || b.SourceBinding.Kind != "endpoint" {
		return b.Units
	}
	method, path, ok := strings.Cut(b.SourceBinding.Ref, " ")
	if !ok || method == "" || path == "" {
		return nil
	}
	return []string{normalize.APIUnitKey(method, path)}
}

func otherOwners(in Input, units []string) []string {
	if in.Plan == nil || len(units) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, u := range units {
		want[u] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, u := range in.Plan.Inventory.Units {
		if !want[u.Key] {
			continue
		}
		for _, c := range u.Contributors {
			if c.Active != nil && !*c.Active || isThisRepo(c.Repo, in.Info) {
				continue
			}
			if c.Role != api.RoleImplements && c.Role != api.RoleDeclares {
				continue
			}
			if label := c.Repo.Label(); label != "" && !seen[label] {
				seen[label] = true
				out = append(out, label)
			}
		}
	}
	sort.Strings(out)
	return out
}

func blockIsOurs(b api.PageBlock, in Input, roles map[string]map[string]bool) bool {
	if b.Provenance != nil && (b.Provenance.Repo == in.Info.Name || b.Provenance.Repo == in.Info.RemoteKey) {
		return true
	}
	if len(b.Units) == 0 {
		return false
	}
	for _, u := range b.Units {
		if !roles[u][api.RoleImplements] {
			return false
		}
	}
	return true
}

func firstUnit(units []string) string {
	if len(units) > 0 {
		return units[0]
	}
	return ""
}

func checkCoverage(in Input, rep *Report) {
	if in.Plan == nil {
		return
	}
	minimum, hasMin := in.Pass.Options["coverageMin"].(float64)
	require := in.StringsOption("require")
	if !hasMin && len(require) == 0 {
		defaultCoverage(in, rep)
		return
	}
	roles := ownRoles(in)
	var implemented []api.Unit
	for _, u := range in.Plan.Inventory.Units {
		if roles[u.Key][api.RoleImplements] {
			implemented = append(implemented, u)
		}
	}
	var undocumented []string
	for _, u := range implemented {
		if len(u.Bindings) == 0 {
			undocumented = append(undocumented, u.Key)
		}
	}
	sort.Strings(undocumented)
	if hasMin && len(implemented) > 0 {
		ratio := float64(len(implemented)-len(undocumented)) / float64(len(implemented))
		if ratio < minimum {
			list := undocumented
			if len(list) > 10 {
				list = append(append([]string{}, list[:10]...), fmt.Sprintf("… %d more", len(undocumented)-10))
			}
			rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeCoverage, Title: fmt.Sprintf("Documentation coverage %.0f%% is below the %.0f%% minimum", ratio*100, minimum*100), Detail: "Undocumented units: " + strings.Join(list, ", ")})
		}
	}
	byKey := map[string]api.Unit{}
	for _, u := range in.Plan.Inventory.Units {
		byKey[u.Key] = u
	}
	for _, req := range require {
		if u, ok := byKey[req]; ok {
			if len(u.Bindings) == 0 {
				rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeCoverageRequired, Title: "Required unit " + req + " has no documentation page", UnitKey: req})
			}
			continue
		}
		for _, u := range implemented {
			if u.Kind == req && len(u.Bindings) == 0 {
				rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeCoverageRequired, Title: fmt.Sprintf("Required %s unit %s has no documentation page", req, u.Key), UnitKey: u.Key})
			}
		}
	}
}

func defaultCoverage(in Input, rep *Report) {
	severity := api.SeverityWarning
	if containsString(FailOn(in), CategoryCoverage) {
		severity = api.SeverityError
	}
	if in.ChangeSet != nil {
		bound := map[string]bool{}
		for _, u := range in.Plan.Inventory.Units {
			if len(u.Bindings) > 0 {
				bound[u.Key] = true
			}
		}
		covered := map[string][]string{}
		var passOrder []string
		for _, d := range in.ChangeSet.OpenAPI {
			for _, op := range d.Added {
				key := normalize.APIUnitKey(op.Method, op.Path)
				if bound[key] {
					continue
				}
				if pass := updatingPass(in, d.Path, ""); pass != "" {
					if covered[pass] == nil {
						passOrder = append(passOrder, pass)
					}
					covered[pass] = append(covered[pass], key)
					continue
				}
				rep.Findings = append(rep.Findings, api.Finding{Severity: severity, Code: CodeCoverage, Title: fmt.Sprintf("%s is new in %s and no pass documents it", key, inThisChange(in)), Detail: "No enabled reference pass with the push trigger reads " + d.Path + "; add one, or document the operation by hand.", File: d.Path, UnitKey: key})
			}
		}
		for _, pass := range passOrder {
			keys := covered[pass]
			rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s new in %s; pass %s documents %s %s", plural(len(keys), "operation is", "operations are"), inThisChange(in), pass, listShort(keys, 5), whenMerged(in))})
		}
	}
	roles := ownRoles(in)
	implemented, documented := 0, 0
	for _, u := range in.Plan.Inventory.Units {
		if !roles[u.Key][api.RoleImplements] {
			continue
		}
		implemented++
		if len(u.Bindings) > 0 {
			documented++
		}
	}
	if implemented > 0 {
		rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("Documentation coverage: %d of %d units this repository implements have a page (%.0f%%)", documented, implemented, float64(documented)*100/float64(implemented))})
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func listShort(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

func verbatimNotes(in Input, rep *Report) {
	if in.Plan == nil || in.ChangeSet == nil {
		return
	}
	for _, pp := range in.Plan.Passes {
		if pp.Kind != config.KindVerbatim {
			continue
		}
		for _, spec := range FileSpecs(pp) {
			for _, f := range in.ChangeSet.Files {
				if f.Status == "D" || !glob.Match(strings.TrimPrefix(spec.Include, "./"), f.Path) || glob.MatchAny(spec.Exclude, f.Path) {
					continue
				}
				rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s is managed by pass %s and will be re-imported on merge", f.Path, pp.Name)})
			}
		}
	}
}

type claimPage struct {
	id       string
	slug     string
	title    string
	verbatim bool
	path     string
}

func claimPages(ctx context.Context, in Input) ([]claimPage, error) {
	touched := map[string]bool{}
	for _, k := range touchedKeys(in) {
		touched[k] = true
	}
	var out []claimPage
	seen := map[string]bool{}
	for _, u := range in.Plan.Inventory.Units {
		if !touched[u.Key] {
			continue
		}
		for _, b := range u.Bindings {
			if !seen[b.PageID] && len(out) < 6 {
				seen[b.PageID] = true
				out = append(out, claimPage{id: b.PageID, slug: b.PageSlug})
			}
		}
	}
	codeChanged := false
	if in.ChangeSet != nil {
		for _, f := range in.ChangeSet.Files {
			if !strings.HasSuffix(f.Path, ".md") && !strings.HasSuffix(f.Path, ".mdx") {
				codeChanged = true
			}
		}
	}
	if !codeChanged {
		return out, nil
	}
	for _, pp := range in.Plan.Passes {
		if pp.Kind != config.KindVerbatim || pp.Target.Space == nil || pp.Target.Space.ID == "" {
			continue
		}
		tree, err := in.Client.SpaceTree(ctx, pp.Target.Space.ID)
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return nil, err
			}
			continue
		}
		added := 0
		for _, p := range tree.Pages {
			if !lockedToPass(p.Lock, pp.Name, in.Info) || added == 4 {
				continue
			}
			if seen[p.ID] {
				for i := range out {
					if out[i].id == p.ID {
						out[i].verbatim, out[i].path = true, p.Lock.Path
					}
				}
				continue
			}
			seen[p.ID] = true
			added++
			out = append(out, claimPage{id: p.ID, slug: p.Slug, title: p.Title, verbatim: true, path: p.Lock.Path})
		}
	}
	return out, nil
}

func checkClaims(ctx context.Context, in Input, out Sink, rep *Report) error {
	pages, err := claimPages(ctx, in)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return nil
	}
	var b strings.Builder
	byID := map[string]claimPage{}
	bySlug := map[string]claimPage{}
	writers := map[string]map[string]string{}
	for _, p := range pages {
		text, skip, err := claimText(ctx, in, p)
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return err
			}
			rep.warn("claims: read page %s: %v", p.slug, err)
			continue
		}
		if skip {
			continue
		}
		byID[p.id] = p
		bySlug[p.slug] = p
		writers[p.id] = blockWriters(ctx, in, p.id)
		label := "claims"
		if p.verbatim {
			label = "verbatim (locked to this repository; a contradiction is a finding against the repository)"
		}
		fmt.Fprintf(&b, "\n## Page %s (%s) — category %s\n%s\n", p.id, p.slug, label, clip(text, 24*1024))
	}
	if len(byID) == 0 {
		return nil
	}
	kickoff := passHeader(in) + "\nReview the claims of these pages against this repository at the head of the range.\n\n" + ChangeSummary(in.ChangeSet, in.Range) + UnitDetails(in.Plan, touchedKeys(in)) + b.String()
	res, err := agent.Submit[agent.FindingsInput](ctx, in.Harness, agent.Task{Prompt: prompts.PassCheck, Purpose: api.PurposeCheck, Kickoff: kickoff, Tools: toolset(in), Submit: agent.ReportFindingsTool()})
	if err != nil {
		return fmt.Errorf("claims: %w", err)
	}
	own := NewOwnership(in.Plan, in.Info)
	var hints []api.HintInput
	for _, f := range res.Findings {
		p, ok := byID[f.PageID]
		if !ok {
			p, ok = bySlug[f.PageSlug]
		}
		var page *api.PageRef
		if ok {
			page = &api.PageRef{ID: p.id, Slug: p.slug, Title: p.title}
			f.PageID, f.PageSlug = p.id, p.slug
		}
		v := own.Settle(f, writers[p.id][f.BlockKey], p.verbatim)
		f = v.Claim
		switch f.Verdict {
		case api.VerdictContradicted:
			if len(v.HintFor) > 0 {
				hints = append(hints, claimHint(f, page, v.HintFor))
				rep.Notes = append(rep.Notes, api.Note{Verdict: f.Verdict, Title: fmt.Sprintf("%s (owned by %s; a hint was raised there)", firstOf(f.Title, f.Claim), strings.Join(v.HintFor, ", ")), Page: page})
				continue
			}
			code := CodeClaimContradicted
			if p.verbatim {
				code = CodeVerbatimContradicted
			}
			finding := api.Finding{Severity: api.SeverityError, Code: code, Title: f.Title, Detail: firstOf(f.Detail, f.Claim), Verdict: f.Verdict, File: f.File, Line: f.Line, Page: page, BlockKey: f.BlockKey, UnitKey: f.UnitKey}
			for _, e := range f.Evidence {
				finding.Evidence = append(finding.Evidence, api.Evidence{Kind: e.Kind, Ref: e.Ref})
			}
			rep.Findings = append(rep.Findings, finding)
		case api.VerdictUnverifiable:
			rep.Notes = append(rep.Notes, api.Note{Verdict: f.Verdict, Title: f.Title, Page: page})
			rep.Claims = append(rep.Claims, f)
		default:
			rep.Claims = append(rep.Claims, f)
		}
	}
	return raiseHints(ctx, in, out, rep, hints)
}

func claimText(ctx context.Context, in Input, p claimPage) (string, bool, error) {
	if p.verbatim && p.path != "" && in.ChangeSet != nil {
		for _, f := range in.ChangeSet.Files {
			if f.Path != strings.TrimPrefix(p.path, "./") {
				continue
			}
			if f.Status == "D" {
				return "", true, nil
			}
			data, ok, err := in.ReadFile(ctx, f.Path)
			if err != nil {
				return "", false, err
			}
			if ok {
				return "(" + f.Path + " as changed in " + inThisChange(in) + "; it replaces this page " + whenMerged(in) + ")\n" + string(data), false, nil
			}
		}
	}
	text, err := agent.PageText(ctx, in.Client, p.id, "draft")
	return text, false, err
}

func claimHint(f agent.ClaimFinding, page *api.PageRef, recipients []string) api.HintInput {
	h := api.HintInput{Kind: "contradiction", UnitKey: f.UnitKey, BlockKey: f.BlockKey, Claim: firstOf(f.Claim, f.Title), Detail: f.Detail, ForRepos: recipients}
	if page != nil {
		h.PageID = page.ID
	}
	for _, e := range f.Evidence {
		switch e.Kind {
		case "commit":
			h.Evidence.Commits = append(h.Evidence.Commits, e.Ref)
		case "file":
			h.Evidence.Files = append(h.Evidence.Files, e.Ref)
		}
	}
	if f.File != "" && len(h.Evidence.Files) == 0 {
		ref := f.File
		if f.Line > 0 {
			ref = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		h.Evidence.Files = append(h.Evidence.Files, ref)
	}
	return h
}

func blockWriters(ctx context.Context, in Input, pageID string) map[string]string {
	out := map[string]string{}
	pc, err := in.Client.Page(ctx, pageID, api.PageQuery{State: "draft", Format: "json"})
	if err != nil {
		return out
	}
	for _, b := range pc.Blocks {
		if b.Key != "" && b.Provenance != nil && b.Provenance.Repo != "" {
			out[b.Key] = b.Provenance.Repo
		}
	}
	return out
}
