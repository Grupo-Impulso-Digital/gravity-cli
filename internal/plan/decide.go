package plan

import (
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
)

// Decision is whether an applying pass does work over its range.
type Decision struct {
	Pass   string `json:"pass"`
	Run    bool   `json:"run"`
	Skip   string `json:"skip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Advances reports whether a run must be created so finish can advance this pass's watermark.
func (d Decision) Advances() bool {
	return d.Skip == api.SkipScopeUnchanged || d.Skip == api.SkipNoChanges
}

// Decide applies the zero-cost scope skip rules of a pass to its range and ChangeSet.
func Decide(pp api.PlanPass, rng changeset.Range, cs *changeset.ChangeSet, m *config.Manifest) Decision {
	d := Decision{Pass: pp.Name}
	if !pp.Applies {
		d.Skip = pp.SkipReason
		return d
	}
	if rng.Skip != "" {
		d.Skip = rng.Skip
		return d
	}
	survey := rng.Kind == api.RangeSurvey || rng.Kind == api.RangeWorkingTree
	trigger := ""
	if cs != nil {
		trigger = cs.Trigger
	}
	if pp.Kind == config.KindCheck && trigger == config.TriggerPR {
		d.Run = true
		return d
	}
	if rng.Kind != api.RangeWorkingTree && (cs == nil || len(cs.Commits) == 0) {
		d.Skip = api.SkipNoChanges
		return d
	}
	if pp.Kind == config.KindChangelog && trigger == config.TriggerRelease {
		d.Run = true
		return d
	}
	if survey {
		d.Run = true
		d.Reason = "survey"
		return d
	}
	var hit bool
	switch pp.Kind {
	case config.KindReference:
		hit = filesHit(cs, referenceFiles(pp, m), pp.Scope.Exclude, m.CodeExclude())
	case config.KindVerbatim:
		hit = verbatimHit(cs, pp)
	case config.KindChangelog:
		hit = len(cs.Commits) > 0
	default:
		paths := pp.Scope.Paths
		if len(paths) == 0 {
			paths = []string{"**"}
		}
		hit = filesHit(cs, paths, pp.Scope.Exclude, m.CodeExclude()) || unitsHit(cs, pp.Scope.Units) || len(pp.Hints) > 0
	}
	if !hit {
		d.Skip = api.SkipScopeUnchanged
		return d
	}
	d.Run = true
	return d
}

func filesHit(cs *changeset.ChangeSet, include, exclude, codeExclude []string) bool {
	if len(include) == 0 {
		return false
	}
	for _, p := range cs.Paths() {
		if glob.MatchAny(include, p) && !glob.MatchAny(exclude, p) && !glob.MatchAny(codeExclude, p) {
			return true
		}
	}
	return false
}

func unitsHit(cs *changeset.ChangeSet, kinds []string) bool {
	if len(kinds) == 0 {
		return false
	}
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	for _, u := range cs.Units.Touched {
		if want[u.Kind] {
			return true
		}
	}
	for _, list := range [][]changeset.UnitRef{cs.Units.Added, cs.Units.Removed} {
		for _, u := range list {
			if want[u.Kind] {
				return true
			}
		}
	}
	return false
}

func referenceFiles(pp api.PlanPass, m *config.Manifest) []string {
	var out []string
	if sources, ok := pp.Options["sources"].([]any); ok {
		for _, raw := range sources {
			if s, ok := raw.(map[string]any); ok {
				if p, ok := s["path"].(string); ok && p != "" {
					out = append(out, p)
				}
			}
		}
	}
	if len(out) == 0 {
		out = m.OpenAPIFiles()
	}
	return out
}

func verbatimHit(cs *changeset.ChangeSet, pp api.PlanPass) bool {
	for _, f := range verbatimEntries(pp) {
		inc, _ := f["include"].(string)
		if inc == "" {
			continue
		}
		var exclude []string
		if list, ok := f["exclude"].([]any); ok {
			for _, e := range list {
				if s, ok := e.(string); ok {
					exclude = append(exclude, s)
				}
			}
		}
		if filesHit(cs, []string{strings.TrimPrefix(inc, "./")}, exclude, nil) {
			return true
		}
	}
	return false
}

func verbatimEntries(pp api.PlanPass) []map[string]any {
	files, _ := pp.Options["files"].([]any)
	out := make([]map[string]any, 0, len(files))
	for _, raw := range files {
		if f, ok := raw.(map[string]any); ok {
			out = append(out, f)
		}
	}
	return out
}

// NeedsRun applies the no-run rule: a run is created only when a pass will do work or, in write mode, needs a watermark advance.
func NeedsRun(decisions []Decision, write bool) bool {
	for _, d := range decisions {
		if d.Run {
			return true
		}
		if write && d.Advances() {
			return true
		}
	}
	return false
}
