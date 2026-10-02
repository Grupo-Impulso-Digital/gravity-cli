// Package passes implements the pass kinds of a pipeline run: guides, reference, verbatim, changelog, nucleus, check and capture.
package passes

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// Pass is one pass kind.
type Pass interface {
	Kind() string
	Run(ctx context.Context, in Input, out Sink) (Report, error)
}

// API is the platform surface passes read.
type API interface {
	agent.DocsAPI
	PageBySlug(ctx context.Context, spaceID, slug string, q api.PageQuery) (*api.PageContent, error)
	GetRun(ctx context.Context, runID string) (*api.Run, error)
}

// RepoInfo identifies the repository a run processes.
type RepoInfo struct {
	ID        string
	RemoteKey string
	Name      string
	WebURL    string
	Provider  string
	Branch    string
}

// Input is everything a pass run needs.
type Input struct {
	Pass         api.PlanPass
	Plan         *api.Plan
	Manifest     *config.Manifest
	Range        changeset.Range
	ChangeSet    *changeset.ChangeSet
	Builder      *changeset.Builder
	RunID        string
	RunPassID    string
	Mode         string
	Trigger      string
	Preview      bool
	Note         string
	FailOn       []string
	Repo         *git.Repo
	Info         RepoInfo
	Client       API
	Harness      *agent.Harness
	Known        *Units
	ProductParam string
	Generator    string
	Log          func(format string, args ...any)
	Sleep        func(ctx context.Context, d time.Duration) error
}

// Dry reports whether the pass must not write.
func (in Input) Dry() bool { return in.Mode == api.ModeDry }

// Head returns the commit the range ends at; empty for the working tree.
func (in Input) Head() string { return in.Range.Head }

// HeadSHA returns the commit to record for writes, HEAD for the working tree.
func (in Input) HeadSHA(ctx context.Context) string {
	if in.Range.Head != "" {
		return in.Range.Head
	}
	sha, _ := in.Repo.ResolveRef(ctx, "HEAD")
	return sha
}

// ReadFile reads a repository file at the range head (the working tree for previews).
func (in Input) ReadFile(ctx context.Context, p string) ([]byte, bool, error) {
	clean, err := pathsafe.Rel(p)
	if err != nil {
		return nil, false, err
	}
	if in.Range.Head == "" {
		full, err := pathsafe.ResolveInRoot(in.Repo.Root, clean)
		if err != nil {
			return nil, false, err
		}
		data, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", p, err)
		}
		return data, true, nil
	}
	return in.Repo.FileAt(ctx, in.Range.Head, clean)
}

// Files lists the repository files at the range head (tracked plus untracked for the working tree).
func (in Input) Files(ctx context.Context) ([]string, error) {
	if in.Range.Head != "" {
		return in.Repo.FilesAt(ctx, in.Range.Head)
	}
	tracked, err := in.Repo.ListFiles(ctx, "")
	if err != nil {
		return nil, err
	}
	untracked, err := in.Repo.UntrackedFiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tracked)+len(untracked))
	out = append(append(out, tracked...), untracked...)
	sort.Strings(out)
	return out, nil
}

// Option returns a pass option or def.
func (in Input) Option(key string, def any) any {
	if v, ok := in.Pass.Options[key]; ok && v != nil {
		return v
	}
	return def
}

// IntOption returns an integer pass option.
func (in Input) IntOption(key string, def int) int {
	switch v := in.Option(key, def).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

// BoolOption returns a boolean pass option.
func (in Input) BoolOption(key string, def bool) bool {
	if v, ok := in.Option(key, def).(bool); ok {
		return v
	}
	return def
}

// StringOption returns a string pass option.
func (in Input) StringOption(key, def string) string {
	if v, ok := in.Option(key, def).(string); ok && v != "" {
		return v
	}
	return def
}

// StringsOption returns a list-of-strings pass option.
func (in Input) StringsOption(key string) []string {
	raw, _ := in.Pass.Options[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SpaceID is the target space id, or empty for site-only and targetless passes.
func (in Input) SpaceID() string {
	if in.Pass.Target.Space != nil {
		return in.Pass.Target.Space.ID
	}
	return ""
}

// SpaceSlug is the target space slug.
func (in Input) SpaceSlug() string {
	if in.Pass.Target.Space != nil && in.Pass.Target.Space.Slug != "" {
		return in.Pass.Target.Space.Slug
	}
	if in.Pass.Target.SpaceSlug != "" {
		return in.Pass.Target.SpaceSlug
	}
	parts := strings.Split(in.Pass.Target.Ref, "/")
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

// SiteSlug is the target site slug.
func (in Input) SiteSlug() string {
	if in.Pass.Target.Site != nil && in.Pass.Target.Site.Slug != "" {
		return in.Pass.Target.Site.Slug
	}
	if in.Pass.Target.SiteSlug != "" {
		return in.Pass.Target.SiteSlug
	}
	site, _, _ := strings.Cut(in.Pass.Target.Ref, "/")
	return site
}

// CollectionPrefix is the collection path of the target below its space.
func (in Input) CollectionPrefix() []string {
	if c := in.Pass.Target.Collection; c != nil && len(c.Path) > 0 {
		return append([]string{}, c.Path...)
	}
	if len(in.Pass.Target.CollectionPath) > 0 {
		return append([]string{}, in.Pass.Target.CollectionPath...)
	}
	parts := strings.Split(in.Pass.Target.Ref, "/")
	if len(parts) > 2 {
		return append([]string{}, parts[2:]...)
	}
	return []string{}
}

// TargetLabel is the human name of the pass target.
func (in Input) TargetLabel() string {
	return TargetLabel(in.Pass)
}

// TargetLabel renders a pass target as "Site › Space".
func TargetLabel(pp api.PlanPass) string {
	t := pp.Target
	switch {
	case t.Site != nil && t.Space != nil:
		return firstOf(t.Site.Name, t.Site.Slug) + " › " + firstOf(t.Space.Name, t.Space.Slug)
	case t.Ref != "":
		return t.Ref
	}
	return "-"
}

// Audiences returns the pass audiences.
func (in Input) Audiences() []string { return in.Pass.Audiences }

// Languages returns the translation languages requested by the pass.
func (in Input) Languages() []string { return in.StringsOption("languages") }

// Units is the set of product unit keys the platform knows, safe for concurrent passes.
type Units struct {
	mu   sync.Mutex
	keys map[string]bool
}

// NewUnits seeds the known unit keys from the plan inventory.
func NewUnits(units []api.Unit) *Units {
	u := &Units{keys: map[string]bool{}}
	for _, unit := range units {
		u.keys[unit.Key] = true
		for _, a := range unit.Aliases {
			u.keys[a] = true
		}
	}
	return u
}

// Add records keys the run ingested.
func (u *Units) Add(keys ...string) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, k := range keys {
		u.keys[k] = true
	}
}

// Filter keeps the keys the platform knows, so a write never fails on an unknown unit.
func (u *Units) Filter(keys []string) []string {
	if u == nil || len(keys) == 0 {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		if u.keys[k] && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// For returns the implementation of a pass kind.
func For(kind string) (Pass, bool) {
	switch kind {
	case config.KindGuides:
		return Guides{}, true
	case config.KindReference:
		return Reference{}, true
	case config.KindVerbatim:
		return Verbatim{}, true
	case config.KindChangelog:
		return Changelog{}, true
	case config.KindNucleus:
		return Nucleus{}, true
	case config.KindCheck:
		return Check{}, true
	case config.KindCapture:
		return Capture{}, true
	}
	return nil, false
}

// AIKinds are the pass kinds that call the LLM gateway.
var AIKinds = map[string]bool{config.KindGuides: true, config.KindChangelog: true, config.KindNucleus: true, config.KindCheck: true}
