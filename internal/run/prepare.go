package run

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/plan"
)

type preparedPass struct {
	pass     api.PlanPass
	rng      changeset.Range
	ranged   bool
	cs       *changeset.ChangeSet
	decision plan.Decision
}

type prepared struct {
	passes    []preparedPass
	builder   *changeset.Builder
	resolver  *changeset.Resolver
	runRange  *changeset.Range
	commits   int
	inventory []api.IngestUnit
	headSHA   string
}

// WatermarkBranch is the watermark key of a trigger: the branch, or @release for releases.
func WatermarkBranch(trigger, branch string) string {
	if trigger == config.TriggerRelease {
		return "@release"
	}
	return branch
}

func specPatterns(p *api.Plan, m *config.Manifest) []string {
	out := append([]string{}, m.OpenAPIFiles()...)
	for _, pp := range p.Passes {
		if pp.Kind != config.KindReference {
			continue
		}
		for _, s := range passes.ReferenceSources(pp, nil) {
			out = append(out, s.Path)
		}
	}
	seen := map[string]bool{}
	uniq := out[:0]
	for _, s := range out {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	return uniq
}

func prepare(ctx context.Context, env *Env, opts Options, p *api.Plan) (*prepared, error) {
	m := env.Manifest
	var docsInclude, docsExclude []string
	if m != nil && m.Docs != nil {
		docsInclude, docsExclude = m.Docs.Include, m.Docs.Exclude
	}
	prep := &prepared{
		resolver: changeset.NewResolver(env.Repo),
		builder: changeset.NewBuilder(env.Repo, changeset.Options{
			Trigger: opts.Trigger, Branch: opts.Branch, CodeExclude: m.CodeExclude(), CodeInclude: m.CodeInclude(), OpenAPI: specPatterns(p, m),
			DocsInclude: docsInclude, DocsExclude: docsExclude, Inventory: p.Inventory.Units, RepoKey: env.Info.RemoteKey, RepoName: env.Info.Name,
		}),
	}
	wmBranch := WatermarkBranch(opts.Trigger, opts.Branch)
	for _, pp := range p.Passes {
		item := preparedPass{pass: pp}
		if !pp.Applies {
			item.decision = plan.Decision{Pass: pp.Name, Skip: firstOf(pp.SkipReason, api.SkipNotSelected)}
			prep.passes = append(prep.passes, item)
			continue
		}
		in := changeset.RangeInput{
			Trigger: opts.Trigger, Head: opts.Head, PRBase: opts.PRBase, Tag: opts.Tag,
			From: opts.From, To: opts.To, WorkingTree: opts.WorkingTree,
			TagPattern: optionString(pp, "tagPattern", changeset.DefaultTagPattern),
			SurveyMax:  optionInt(pp, "surveyCommits", surveyDepth(p)),
		}
		if opts.PR != nil && opts.From == "" {
			in.PRTarget = opts.PR.TargetBranch
		}
		if pp.Watermark != nil && pp.Watermark.Branch == wmBranch && opts.Trigger != config.TriggerPR {
			in.Watermark = pp.Watermark.CommitSHA
		}
		rng, err := prep.resolver.Resolve(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("range of pass %s: %w", pp.Name, err)
		}
		for _, w := range rng.Warnings {
			env.Log.Warn(w.Code, fmt.Sprintf("%s: %s", pp.Name, w.Message))
		}
		cs, err := prep.builder.For(ctx, rng)
		if err != nil {
			return nil, fmt.Errorf("change set of pass %s: %w", pp.Name, err)
		}
		item.rng, item.ranged, item.cs = rng, true, cs
		item.decision = plan.Decide(pp, rng, cs, m)
		if item.decision.Run && prep.runRange == nil {
			r := rng
			prep.runRange = &r
			prep.commits = len(cs.Commits)
		}
		prep.passes = append(prep.passes, item)
	}
	head := opts.Head
	if prep.runRange != nil && prep.runRange.Head != "" {
		head = prep.runRange.Head
	}
	if head == "" || opts.WorkingTree {
		sha, err := env.Repo.ResolveRef(ctx, firstOf(opts.Head, "HEAD"))
		if err != nil {
			return nil, err
		}
		head = sha
	}
	prep.headSHA = head
	if opts.Mode == api.ModeWrite {
		units, err := apiInventory(ctx, env, p, head)
		if err != nil {
			return nil, err
		}
		prep.inventory = units
	}
	return prep, nil
}

func surveyDepth(p *api.Plan) int {
	if n := p.Capabilities.Limits.SurveyMaxCommits; n > 0 {
		return n
	}
	return changeset.DefaultSurveyCommits
}

func optionString(pp api.PlanPass, key, def string) string {
	if v, ok := pp.Options[key].(string); ok && v != "" {
		return v
	}
	return def
}

func optionInt(pp api.PlanPass, key string, def int) int {
	switch v := pp.Options[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// Roles returns the contributor roles the manifest declares, implements by default.
func Roles(m *config.Manifest) []string {
	if m != nil && m.Code != nil && m.Code.Units != nil && len(m.Code.Units.Role) > 0 {
		return append([]string{}, m.Code.Units.Role...)
	}
	return []string{api.RoleImplements}
}

func apiInventory(ctx context.Context, env *Env, p *api.Plan, head string) ([]api.IngestUnit, error) {
	patterns := specPatterns(p, env.Manifest)
	if len(patterns) == 0 {
		return nil, nil
	}
	files, err := env.Repo.FilesAt(ctx, head)
	if err != nil {
		return nil, err
	}
	roles := Roles(env.Manifest)
	type origin struct{ file, op string }
	seen := map[string]origin{}
	var units []api.IngestUnit
	for _, f := range files {
		if !glob.MatchAny(patterns, f) {
			continue
		}
		data, ok, err := env.Repo.FileAt(ctx, head, f)
		if err != nil || !ok {
			continue
		}
		ops, err := docs.Operations(data)
		if err != nil {
			env.Log.Warn("openapi_unparsable", fmt.Sprintf("%s: %v", f, err))
			continue
		}
		for _, op := range ops {
			key := normalize.APIUnitKey(op.Method, op.Path)
			label := strings.ToUpper(op.Method) + " " + op.Path
			if prev, dup := seen[key]; dup {
				if prev.file == f {
					return nil, fmt.Errorf("unit_key_collision: %s and %s in %s both normalize to %s", prev.op, label, f, key)
				}
				continue
			}
			seen[key] = origin{file: f, op: label}
			canon, err := normalize.CanonicalJSON(docs.Content(op))
			if err != nil {
				return nil, err
			}
			units = append(units, api.IngestUnit{
				Key: key, Kind: api.UnitKindAPI, Title: firstOf(op.Summary, label), Summary: clip(op.Description, 500),
				Roles: roles, SourceRefs: []string{f + "#/paths/" + strings.ReplaceAll(strings.ReplaceAll(op.Path, "~", "~0"), "/", "~1") + "/" + strings.ToLower(op.Method)},
				SourceHash: normalize.SHA256(canon),
			})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Key < units[j].Key })
	return units, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func startRequest(env *Env, opts Options, p *api.Plan, prep *prepared) api.StartRunRequest {
	req := api.StartRunRequest{
		ClientKey: env.key(), Trigger: opts.Trigger, Mode: opts.Mode, Origin: firstOf(opts.Origin, api.OriginLocal),
		Branch: opts.Branch, HeadSHA: prep.headSHA, PR: opts.PR, CI: opts.CI,
		CLI: api.CLIInfo{Version: strings.TrimPrefix(env.Generator, "gravity-cli/")}, Note: opts.Note, PlanHash: p.PlanHash,
	}
	if opts.Trigger == config.TriggerRelease {
		req.Branch = ""
		req.Release = &api.ReleaseInfo{Tag: opts.Tag}
	}
	if opts.Mode == api.ModeDry && env.Manifest != nil {
		h := env.Manifest.Hash
		req.ManifestHash = &h
	}
	runRange := prep.runRange
	for i := 0; runRange == nil && i < len(prep.passes); i++ {
		if prep.passes[i].ranged {
			runRange = &prep.passes[i].rng
		}
	}
	if runRange != nil {
		req.BaseSHA, req.RangeKind = runRange.Base, runRange.Kind
		if runRange.PreviousTag != "" && req.Release != nil {
			req.Release.PreviousTag = runRange.PreviousTag
		}
	}
	wmBranch := WatermarkBranch(opts.Trigger, opts.Branch)
	for _, pp := range prep.passes {
		entry := api.RunPassStart{Name: pp.pass.Name}
		if pp.ranged {
			entry.RangeKind, entry.BaseSHA = pp.rng.Kind, pp.rng.Base
			if pp.pass.Watermark != nil && pp.pass.Watermark.Branch == wmBranch {
				seen := pp.pass.Watermark.CommitSHA
				entry.WatermarkSeen = &seen
			}
		}
		if !pp.decision.Run {
			entry.Skip = pp.decision.Skip
		}
		if !pp.ranged {
			entry.RangeKind, entry.BaseSHA, entry.WatermarkSeen = "", "", nil
		}
		req.Passes = append(req.Passes, entry)
	}
	return req
}
