package passes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
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
	rep.Failing = Failing(rep.Findings, FailOn(in))
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

func ownOperations(ctx context.Context, in Input) (map[string]ownOp, error) {
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
	ops := map[string]ownOp{}
	if len(sources) == 0 {
		return ops, nil
	}
	specs, _, err := ResolveSpecs(ctx, in, sources)
	if err != nil {
		return nil, err
	}
	for _, sf := range specs {
		for _, op := range sf.Ops {
			b, err := docs.APIBlock(op, "")
			if err != nil {
				return nil, err
			}
			ops[b.Key] = ownOp{hash: b.SourceBinding.Hash, unit: b.Units[0], spec: sf.File}
		}
	}
	return ops, nil
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
	if len(ops) == 0 {
		return nil
	}
	ourUnits := map[string]bool{}
	for _, op := range ops {
		ourUnits[op.unit] = true
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
				op, ours := ops[b.Key]
				switch {
				case ours && b.SourceBinding.Kind == "endpoint" && b.SourceBinding.Hash != op.hash:
					rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeDrift, Title: fmt.Sprintf("%s: %s is out of date", p.Title, b.SourceBinding.Ref), Detail: "The api block no longer matches " + op.spec + "; the reference pass rewrites it.", File: op.spec, Page: ref, BlockKey: b.Key, UnitKey: op.unit})
				case ours && b.SourceBinding.Kind == "cli":
					rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityWarning, Code: CodeDrift, Title: fmt.Sprintf("%s: %s was written by gravity v0.x", p.Title, b.Key), Detail: "The next reference run rewrites it with an endpoint binding.", Page: ref, BlockKey: b.Key, UnitKey: op.unit})
				case !ours && blockIsOurs(b, in, roles):
					units := blockUnits(b)
					if owners := otherOwners(in, units); len(owners) > 0 {
						rep.Notes = append(rep.Notes, api.Note{Title: fmt.Sprintf("%s documents %s, which this repository no longer defines; %s now owns it", p.Title, b.Key, strings.Join(owners, ", ")), Page: ref})
						continue
					}
					rep.Findings = append(rep.Findings, api.Finding{Severity: api.SeverityError, Code: CodeDriftRemoved, Title: fmt.Sprintf("%s documents %s, which this repository no longer defines", p.Title, b.Key), Page: ref, BlockKey: b.Key, UnitKey: firstUnit(units)})
				}
			}
		}
	}
	return nil
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
						out[i].verbatim = true
					}
				}
				continue
			}
			seen[p.ID] = true
			added++
			out = append(out, claimPage{id: p.ID, slug: p.Slug, title: p.Title, verbatim: true})
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
		text, err := agent.PageText(ctx, in.Client, p.id, "draft")
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return err
			}
			rep.warn("claims: read page %s: %v", p.slug, err)
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
