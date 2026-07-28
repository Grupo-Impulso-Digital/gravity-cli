package docs

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// MaxChangedPaths caps how many changed paths are listed verbatim to the planner.
const MaxChangedPaths = 400

// Scope is the survey window declared by discovery.include / discovery.exclude.
type Scope struct {
	Include []string
	Exclude []string
}

// IsZero reports whether the scope declares no window at all.
func (s Scope) IsZero() bool {
	return len(s.Include) == 0 && len(s.Exclude) == 0
}

// Allows reports whether a repo-relative path is inside the survey window.
func (s Scope) Allows(p string) bool {
	clean, err := pathsafe.Rel(p)
	if err != nil {
		return false
	}
	if len(s.Include) > 0 && !matchAny(s.Include, clean) {
		return false
	}
	return !matchAny(s.Exclude, clean)
}

// Filter returns the paths inside the survey window, deduped and sorted.
func (s Scope) Filter(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean, err := pathsafe.Rel(strings.TrimSpace(p))
		if err != nil || clean == "" || clean == "." || seen[clean] || !s.Allows(clean) {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

func matchAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if matchGlob(strings.TrimSpace(pat), p) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, p string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == p || strings.HasPrefix(p, pattern+"/") {
		return true
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, seg []string) bool {
	switch {
	case len(pat) == 0:
		return len(seg) == 0
	case pat[0] == "**":
		if len(pat) == 1 {
			return true
		}
		for i := 0; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	case len(seg) == 0:
		return false
	default:
		ok, err := path.Match(pat[0], seg[0])
		if err != nil || !ok {
			return false
		}
		return matchSegments(pat[1:], seg[1:])
	}
}

// ChangeSet is the file universe of a change-scoped (`--since`) run.
type ChangeSet struct {
	Ref   string
	paths map[string]bool
	list  []string
}

// NewChangeSet builds a change set from an already-filtered path list.
func NewChangeSet(ref string, paths []string) *ChangeSet {
	c := &ChangeSet{Ref: ref, paths: make(map[string]bool, len(paths)), list: paths}
	for _, p := range paths {
		c.paths[p] = true
	}
	return c
}

// Len reports how many paths changed.
func (c *ChangeSet) Len() int {
	if c == nil {
		return 0
	}
	return len(c.list)
}

// Paths returns the changed paths, sorted.
func (c *ChangeSet) Paths() []string {
	if c == nil {
		return nil
	}
	return c.list
}

// Contains reports whether a repo-relative path changed.
func (c *ChangeSet) Contains(p string) bool {
	if c == nil {
		return false
	}
	clean, err := pathsafe.Rel(strings.TrimSpace(p))
	if err != nil || clean == "" || clean == "." {
		return false
	}
	if c.paths[clean] {
		return true
	}
	prefix := clean + "/"
	for _, x := range c.list {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

// ContainsAny reports whether any of the refs changed.
func (c *ChangeSet) ContainsAny(refs []string) bool {
	for _, ref := range refs {
		if c.Contains(ref) {
			return true
		}
	}
	return false
}

// Digest renders the change set for the planner's kickoff.
func (c *ChangeSet) Digest() string {
	if c == nil || len(c.list) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CHANGE SET — %d file(s) changed since %s. Survey only these files (plus the entrypoints listed above) and mark the units they touch as changed:\n", len(c.list), c.Ref)
	if len(c.list) <= MaxChangedPaths {
		for _, p := range c.list {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
		return b.String()
	}
	counts := map[string]int{}
	for _, p := range c.list {
		dir := path.Dir(p)
		if dir == "." {
			dir = "(root)"
		}
		counts[dir]++
	}
	dirs := make([]string, 0, len(counts))
	for d := range counts {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	fmt.Fprintf(&b, "(too many to list; summarized by directory)\n")
	for _, d := range dirs {
		fmt.Fprintf(&b, "- %s (%d file(s))\n", d, counts[d])
	}
	b.WriteString("\n")
	return b.String()
}
