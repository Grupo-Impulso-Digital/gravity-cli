package run

import (
	"context"
	"sort"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

// Handoff statuses.
const (
	HandoffDetected = "detected"
	HandoffExpected = "expected"
)

// Handoff is a unit role moving between repositories: detected by the platform on ingest, or expected from the change set.
type Handoff struct {
	UnitKey string `json:"unitKey"`
	Role    string `json:"role"`
	From    string `json:"from"`
	To      string `json:"to"`
	Status  string `json:"status"`
}

func detectedHandoffs(res *api.IngestResult) []Handoff {
	if res == nil {
		return nil
	}
	out := make([]Handoff, 0, len(res.Handoffs))
	for _, h := range res.Handoffs {
		out = append(out, Handoff{UnitKey: h.UnitKey, Role: h.Role, From: h.From, To: h.To, Status: HandoffDetected})
	}
	return out
}

func expectedHandoffs(p *api.Plan, info passes.RepoInfo, roles []string, prep *prepared) []Handoff {
	if p == nil || prep == nil {
		return nil
	}
	own := passes.NewOwnership(p, info)
	me := firstOf(info.Name, info.RemoteKey)
	seen := map[string]bool{}
	var out []Handoff
	add := func(h Handoff) {
		id := h.UnitKey + "|" + h.Role + "|" + h.From + "|" + h.To
		if !seen[id] {
			seen[id] = true
			out = append(out, h)
		}
	}
	for _, pp := range prep.passes {
		if pp.cs == nil {
			continue
		}
		for _, u := range pp.cs.Units.Added {
			unit, ok := own.Unit(u.Key)
			if !ok {
				continue
			}
			for _, role := range roles {
				if own.Has(u.Key, role) {
					continue
				}
				for _, c := range unit.Contributors {
					if c.Role == role && c.Active != nil && !*c.Active && !own.IsThisRepo(c.Repo.RemoteKey) && !own.IsThisRepo(c.Repo.Name) {
						add(Handoff{UnitKey: unit.Key, Role: role, From: c.Repo.Label(), To: me, Status: HandoffExpected})
					}
				}
			}
		}
		for _, u := range pp.cs.Units.Removed {
			for _, role := range roles {
				if !own.Has(u.Key, role) {
					continue
				}
				for _, r := range own.Others(u.Key, role) {
					add(Handoff{UnitKey: u.Key, Role: role, From: me, To: r.Label(), Status: HandoffExpected})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UnitKey < out[j].UnitKey })
	return out
}

func keepDocuments(units []api.IngestUnit, p *api.Plan, info passes.RepoInfo) []api.IngestUnit {
	if p == nil {
		return units
	}
	own := passes.NewOwnership(p, info)
	index := map[string]int{}
	for i, u := range units {
		index[u.Key] = i
	}
	for _, u := range p.Inventory.Units {
		if !own.Has(u.Key, api.RoleDocuments) {
			continue
		}
		if i, ok := index[u.Key]; ok {
			if !contains(units[i].Roles, api.RoleDocuments) {
				units[i].Roles = append(append([]string{}, units[i].Roles...), api.RoleDocuments)
			}
			continue
		}
		var refs []string
		for _, c := range u.Contributors {
			if c.Role == api.RoleDocuments && own.IsThisRepo(c.Repo.RemoteKey) || c.Role == api.RoleDocuments && c.Repo.RemoteKey == "" && own.IsThisRepo(c.Repo.Name) {
				refs = append(refs, c.SourceRefs...)
			}
		}
		index[u.Key] = len(units)
		units = append(units, api.IngestUnit{Key: u.Key, Kind: u.Kind, Title: firstOf(u.Title, u.Key), Roles: []string{api.RoleDocuments}, SourceRefs: refs})
	}
	return units
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *runState) ingestDocuments(ctx context.Context, results []PassResult) error {
	own := passes.NewOwnership(s.plan, s.env.Info)
	reported := map[string]bool{}
	for _, u := range s.ingested {
		reported[u.Key] = true
	}
	var units []api.IngestUnit
	seen := map[string]bool{}
	for _, r := range results {
		if r.Report == nil || r.Status != api.StatusSucceeded {
			continue
		}
		for _, key := range r.Report.Documented {
			u, ok := own.Unit(key)
			if !ok || seen[u.Key] || reported[u.Key] || own.Role(u.Key) != "" {
				continue
			}
			seen[u.Key] = true
			units = append(units, api.IngestUnit{Key: u.Key, Kind: u.Kind, Title: firstOf(u.Title, u.Key), Roles: []string{api.RoleDocuments}, SourceRefs: []string{"pass:" + r.Name}})
		}
	}
	if len(units) == 0 {
		return nil
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Key < units[j].Key })
	if _, err := s.env.Client.IngestInventory(ctx, api.IngestRequest{Product: productParam(s.opts, s.plan), RunID: s.started.Run.ID, HeadSHA: s.prep.headSHA, Complete: false, Units: units}); err != nil {
		return err
	}
	s.env.Log.Infof("Recorded %s as documenting %d unit(s) it does not implement", firstOf(s.env.Info.Name, s.env.Info.RemoteKey), len(units))
	return nil
}
