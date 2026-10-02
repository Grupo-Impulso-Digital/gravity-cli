package passes

import (
	"sort"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Ownership is the product-wide view of unit contributors, seen from the repository a run processes.
type Ownership struct {
	info  RepoInfo
	units map[string]api.Unit
	own   map[string]map[string]bool
}

// NewOwnership indexes the plan inventory by unit key and alias.
func NewOwnership(p *api.Plan, info RepoInfo) *Ownership {
	o := &Ownership{info: info, units: map[string]api.Unit{}, own: map[string]map[string]bool{}}
	if p == nil {
		return o
	}
	for _, u := range p.Inventory.Units {
		o.units[u.Key] = u
		for _, a := range u.Aliases {
			if _, taken := o.units[a]; !taken {
				o.units[a] = u
			}
		}
		for _, c := range u.Contributors {
			if !activeContributor(c) || !isThisRepo(c.Repo, info) {
				continue
			}
			if o.own[u.Key] == nil {
				o.own[u.Key] = map[string]bool{}
			}
			o.own[u.Key][c.Role] = true
		}
	}
	return o
}

func activeContributor(c api.Contributor) bool {
	return c.Active == nil || *c.Active
}

// Unit returns the inventory entry of a key or alias.
func (o *Ownership) Unit(key string) (api.Unit, bool) {
	u, ok := o.units[key]
	return u, ok
}

func (o *Ownership) roles(key string) map[string]bool {
	if u, ok := o.units[key]; ok {
		return o.own[u.Key]
	}
	return nil
}

// Has reports whether this repository actively holds role on the unit.
func (o *Ownership) Has(key, role string) bool {
	return o.roles(key)[role]
}

// Contributes reports whether this repository declares or implements the unit.
func (o *Ownership) Contributes(key string) bool {
	r := o.roles(key)
	return r[api.RoleImplements] || r[api.RoleDeclares]
}

// Role is the strongest role this repository holds on the unit: implements, declares, documents or "".
func (o *Ownership) Role(key string) string {
	r := o.roles(key)
	for _, role := range []string{api.RoleImplements, api.RoleDeclares, api.RoleDocuments} {
		if r[role] {
			return role
		}
	}
	return ""
}

// Others lists the other repositories actively holding one of roles on the unit, implementers first.
func (o *Ownership) Others(key string, roles ...string) []api.RepoRef {
	u, ok := o.units[key]
	if !ok {
		return nil
	}
	want := map[string]int{}
	for i, r := range roles {
		want[r] = i + 1
	}
	type ranked struct {
		ref  api.RepoRef
		rank int
	}
	var list []ranked
	seen := map[string]int{}
	for _, c := range u.Contributors {
		rank := want[c.Role]
		if rank == 0 || !activeContributor(c) || isThisRepo(c.Repo, o.info) {
			continue
		}
		id := repoIdentity(c.Repo)
		if i, dup := seen[id]; dup {
			if rank < list[i].rank {
				list[i].rank = rank
			}
			continue
		}
		seen[id] = len(list)
		list = append(list, ranked{ref: c.Repo, rank: rank})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].rank < list[j].rank })
	out := make([]api.RepoRef, 0, len(list))
	for _, r := range list {
		out = append(out, r.ref)
	}
	return out
}

func repoIdentity(r api.RepoRef) string {
	switch {
	case r.RemoteKey != "":
		return r.RemoteKey
	case r.Name != "":
		return r.Name
	}
	return r.ID
}

// IsThisRepo reports whether a repository name or remote key designates the repository of the run.
func (o *Ownership) IsThisRepo(repo string) bool {
	return repo != "" && (repo == o.info.Name || repo == o.info.RemoteKey || repo == o.info.ID)
}

// Verdict is the settled outcome of one reviewed claim.
type Verdict struct {
	Claim      agent.ClaimFinding
	Elsewhere  string
	HintFor    []string
	Downgraded bool
}

// Settle applies the fluid-ownership rules (spec §9.5) to a claim verdict the agent proposed; lastWriter is the repository that last wrote the claim's block.
func (o *Ownership) Settle(f agent.ClaimFinding, lastWriter string, verbatim bool) Verdict {
	v := Verdict{Claim: f}
	unit := f.UnitKey
	switch f.Verdict {
	case api.VerdictContradicted:
		if verbatim || unit == "" || o.Has(unit, api.RoleImplements) {
			return v
		}
		recipients := o.Others(unit, api.RoleImplements)
		if len(recipients) == 0 && !o.Contributes(unit) {
			recipients = o.Others(unit, api.RoleDeclares)
		}
		for _, r := range recipients {
			if r.RemoteKey != "" {
				v.HintFor = append(v.HintFor, r.RemoteKey)
			}
		}
		return v
	case api.VerdictTrueElsewhere, api.VerdictUnverifiable:
		repo := ""
		if f.Verdict == api.VerdictTrueElsewhere && !o.IsThisRepo(f.Repo) {
			repo = f.Repo
		}
		if repo == "" && unit != "" {
			if others := o.Others(unit, api.RoleImplements, api.RoleDeclares); len(others) > 0 {
				repo = others[0].Label()
			}
		}
		if repo == "" && lastWriter != "" && !o.IsThisRepo(lastWriter) {
			repo = lastWriter
		}
		if repo == "" {
			v.Claim.Verdict = api.VerdictUnverifiable
			v.Downgraded = f.Verdict == api.VerdictTrueElsewhere
			return v
		}
		v.Claim.Verdict = api.VerdictTrueElsewhere
		v.Claim.Repo = repo
		v.Elsewhere = repo
		if !hasEvidence(v.Claim.Evidence, "repo", repo) {
			v.Claim.Evidence = append(v.Claim.Evidence, agent.ClaimEvidence{Kind: "repo", Ref: repo})
		}
		if unit != "" && !hasEvidence(v.Claim.Evidence, "unit", unit) {
			v.Claim.Evidence = append(v.Claim.Evidence, agent.ClaimEvidence{Kind: "unit", Ref: unit})
		}
	}
	return v
}

func hasEvidence(list []agent.ClaimEvidence, kind, ref string) bool {
	for _, e := range list {
		if e.Kind == kind && e.Ref == ref {
			return true
		}
	}
	return false
}
