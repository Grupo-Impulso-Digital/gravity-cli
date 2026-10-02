package config

import "sort"

// Repository token scopes.
const (
	ScopeRepoConnect     = "repo:connect"
	ScopeRunsWrite       = "runs:write"
	ScopeContentRead     = "content:read"
	ScopeContentPropose  = "content:propose"
	ScopeContentVerbatim = "content:verbatim"
	ScopeInventoryWrite  = "inventory:write"
	ScopeNucleusRead     = "nucleus:read"
	ScopeNucleusWrite    = "nucleus:write"
	ScopeLLM             = "llm"
)

var scopeOrder = []string{
	ScopeRepoConnect, ScopeRunsWrite, ScopeContentRead, ScopeContentPropose, ScopeContentVerbatim,
	ScopeInventoryWrite, ScopeNucleusRead, ScopeNucleusWrite, ScopeLLM,
}

// BaseScopes are carried by every repository token.
var BaseScopes = []string{ScopeRepoConnect, ScopeRunsWrite, ScopeContentRead, ScopeInventoryWrite}

// RequiredScopes returns the scopes a pass of kind with options needs beyond the base set.
func RequiredScopes(kind string, options map[string]any) []string {
	switch kind {
	case KindGuides:
		return []string{ScopeContentPropose, ScopeLLM, ScopeNucleusRead}
	case KindReference:
		if optionBool(options, "prose", false) {
			return []string{ScopeContentPropose, ScopeLLM}
		}
		return []string{ScopeContentPropose}
	case KindVerbatim:
		return []string{ScopeContentVerbatim}
	case KindChangelog:
		return []string{ScopeContentPropose, ScopeLLM}
	case KindNucleus:
		return []string{ScopeNucleusRead, ScopeNucleusWrite, ScopeLLM}
	case KindCheck:
		if optionBool(options, "claims", true) {
			return []string{ScopeContentPropose, ScopeLLM, ScopeNucleusRead}
		}
		return []string{ScopeContentPropose}
	}
	return nil
}

// ScopedPass is the part of a pass that decides its token scopes.
type ScopedPass struct {
	Kind    string
	Options map[string]any
	Enabled bool
}

// UnionScopes returns the base scopes plus every enabled pass's required scopes, in canonical order.
func UnionScopes(passes []ScopedPass) []string {
	set := map[string]bool{}
	for _, s := range BaseScopes {
		set[s] = true
	}
	for _, p := range passes {
		if !p.Enabled {
			continue
		}
		for _, s := range RequiredScopes(p.Kind, p.Options) {
			set[s] = true
		}
	}
	rank := map[string]int{}
	for i, s := range scopeOrder {
		rank[s] = i
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return rank[out[i]] < rank[out[j]] })
	return out
}

func optionBool(options map[string]any, key string, def bool) bool {
	if v, ok := options[key].(bool); ok {
		return v
	}
	return def
}
