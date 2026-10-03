// Package plan fetches the app's plan for a repository, merges it with manifest-declared passes and decides per pass whether it runs.
package plan

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
)

// FeaturePipelines is the server capability CLI 1.0 requires.
const FeaturePipelines = "pipelines"

// ErrNoPipelines means the server predates CLI 1.0 pipelines.
var ErrNoPipelines = errors.New("this Gravity server does not support CLI 1.0 pipelines; use gravity v0.3")

// RequirePipelines fails when the advertised server features lack pipelines.
func RequirePipelines(features map[string]bool) error {
	if !features[FeaturePipelines] {
		return ErrNoPipelines
	}
	return nil
}

// Fetch calls the plan endpoint.
func Fetch(ctx context.Context, client *api.Client, q api.PlanQuery) (*api.Plan, error) {
	p, err := client.Plan(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("fetch plan: %w", err)
	}
	return p, nil
}

// Pass is one effective pass: the plan's view, overridden by the local manifest where it declares the pass.
type Pass struct {
	api.PlanPass
	Declared bool `json:"declared"`
	Pending  bool `json:"pending"`
	Archived bool `json:"archived,omitempty"`
}

// Effective is the merged pass list of a plan and a manifest.
type Effective struct {
	Passes   []Pass        `json:"passes"`
	Warnings []api.Warning `json:"warnings,omitempty"`
}

// Synced reports whether the plan already reflects the manifest.
func Synced(p *api.Plan, m *config.Manifest) bool {
	if m == nil {
		return true
	}
	return p.Overlay || (p.Repo.ManifestHash != "" && p.Repo.ManifestHash == m.Hash)
}

// Merge overlays the local manifest's declared passes on the plan: the repository wins on what it declares.
func Merge(p *api.Plan, m *config.Manifest) Effective {
	var out Effective
	declared := map[string]config.Pass{}
	if m != nil {
		for _, mp := range m.Passes {
			declared[mp.Name] = mp
		}
	}
	synced := Synced(p, m)
	ignoreApp := m != nil && m.AppPasses == "ignore"
	seen := map[string]bool{}
	for _, pp := range p.Passes {
		mp, isDeclared := declared[pp.Name]
		ep := Pass{PlanPass: pp, Declared: isDeclared}
		seen[pp.Name] = true
		switch {
		case isDeclared && !synced:
			ep = overlay(ep, mp, p)
		case !isDeclared && pp.Source == "manifest" && m != nil && !synced:
			ep.Archived = true
			ep.Applies = false
			ep.SkipReason = api.SkipNotSelected
			out.Warnings = append(out.Warnings, api.Warning{Code: "pass_undeclared", Message: fmt.Sprintf("pass %s is no longer declared in %s; it is archived when this manifest reaches %s", pp.Name, config.ManifestFileName, authoritative(p))})
		case !isDeclared && ignoreApp && pp.Source == "app":
			ep.Applies = false
			ep.SkipReason = api.SkipNotSelected
		}
		out.Passes = append(out.Passes, ep)
	}
	if m != nil {
		for _, mp := range m.Passes {
			if seen[mp.Name] {
				continue
			}
			ep := Pass{PlanPass: api.PlanPass{Name: mp.Name, Source: "manifest", Locked: true, Overlay: true}, Declared: true}
			ep = overlay(ep, mp, p)
			out.Passes = append(out.Passes, ep)
		}
	}
	if !synced && m != nil && len(m.Passes) > 0 {
		out.Warnings = append(out.Warnings, api.Warning{Code: "manifest_not_authoritative", Message: fmt.Sprintf("%s differs from the configuration stored in Gravity; declared passes are shown as this file defines them and take effect when it reaches %s", config.ManifestFileName, authoritative(p))})
	}
	return out
}

func authoritative(p *api.Plan) string {
	if p.Repo.AuthoritativeBranch != "" {
		return p.Repo.AuthoritativeBranch
	}
	if p.Repo.DefaultBranch != "" {
		return p.Repo.DefaultBranch
	}
	return "the default branch"
}

func overlay(ep Pass, mp config.Pass, p *api.Plan) Pass {
	prevTarget := ep.Target.Ref
	ep.Pending = true
	ep.Source = "manifest"
	ep.Locked = true
	ep.Kind = mp.Kind
	if mp.Title != "" {
		ep.Title = mp.Title
	}
	if mp.Template != "" {
		ep.Template = mp.Template
	}
	if mp.Triggers != nil {
		ep.Triggers = mp.Triggers
	}
	if mp.Branches != nil {
		ep.Branches = mp.Branches
	}
	if mp.Scope != nil {
		ep.Scope = api.PassScope{Paths: mp.Scope.Paths, Exclude: mp.Scope.Exclude, Units: mp.Scope.Units}
	}
	if mp.Audiences != nil {
		ep.Audiences = mp.Audiences
	}
	if mp.Options != nil {
		ep.Options = mp.Options
	}
	if mp.Publish != "" {
		ep.PublishMode = mp.Publish
	}
	ep.Enabled = mp.IsEnabled()
	switch {
	case mp.Target == "":
		ep.Target = api.PassTarget{Status: api.TargetNone}
	case mp.Target != prevTarget:
		ep.Target = api.PassTarget{Ref: mp.Target, Status: api.TargetUnapproved}
	}
	ep.Applies, ep.SkipReason = Applies(ep.PlanPass, p.Trigger, p.Branch, p.Repo.DefaultBranch)
	return ep
}

var precedence = []string{
	api.SkipDisabled, api.SkipNotSelected, api.SkipTriggerMismatch, api.SkipBranchMismatch,
	api.SkipModuleDisabled, api.SkipTargetMissing, api.SkipTargetUnapproved, api.SkipScopeMissing,
}

// Applies evaluates the plan skip reasons the CLI can compute locally, in the platform's precedence order.
func Applies(pp api.PlanPass, trigger, branch, defaultBranch string) (bool, string) {
	reasons := map[string]bool{}
	if !pp.Enabled {
		reasons[api.SkipDisabled] = true
	}
	if trigger != "" && !TriggerMatches(pp.Triggers, trigger) {
		reasons[api.SkipTriggerMismatch] = true
	}
	if trigger != config.TriggerRelease && !BranchMatches(pp.Branches, branch, defaultBranch) {
		reasons[api.SkipBranchMismatch] = true
	}
	switch pp.Target.Status {
	case api.TargetMissing:
		reasons[api.SkipTargetMissing] = true
	case api.TargetUnapproved:
		reasons[api.SkipTargetUnapproved] = true
	}
	if pp.SkipReason == api.SkipModuleDisabled || pp.SkipReason == api.SkipScopeMissing || pp.SkipReason == api.SkipNotSelected {
		reasons[pp.SkipReason] = true
	}
	for _, r := range precedence {
		if reasons[r] {
			return false, r
		}
	}
	return true, ""
}

// TriggerMatches reports whether a pass with these triggers runs on trigger; a manual run also runs push and schedule passes.
func TriggerMatches(triggers []string, trigger string) bool {
	if contains(triggers, trigger) {
		return true
	}
	return trigger == config.TriggerManual && (contains(triggers, config.TriggerPush) || contains(triggers, config.TriggerSchedule))
}

// BranchMatches applies a pass branch filter; an empty filter means the default branch only.
func BranchMatches(patterns []string, branch, defaultBranch string) bool {
	if branch == "" {
		return true
	}
	if len(patterns) == 0 {
		return defaultBranch == "" || branch == defaultBranch
	}
	return glob.MatchAny(patterns, branch)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SortByName orders passes by name, keeping applying passes first.
func SortByName(passes []Pass) {
	sort.SliceStable(passes, func(i, j int) bool {
		if passes[i].Applies != passes[j].Applies {
			return passes[i].Applies
		}
		return passes[i].Name < passes[j].Name
	})
}
