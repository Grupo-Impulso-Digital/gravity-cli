// Package changeset resolves the commit range of a pass and computes what changed in it, once per distinct base.
package changeset

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

// Warning codes raised while resolving a range.
const (
	WarnWatermarkDiverged = "watermark_diverged"
	WarnShallowClone      = "shallow_clone"
	WarnSurvey            = "survey"
)

// Defaults of range resolution.
const (
	DefaultSurveyCommits = 50
	DefaultTagPattern    = "v*"
	DefaultRemote        = "origin"
	deepenStep           = 200
)

// Warning is a non-fatal note about a range or a ChangeSet.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RangeInput describes what to resolve a range for.
type RangeInput struct {
	Trigger     string
	Head        string
	Watermark   string
	PRTarget    string
	PRBase      string
	Tag         string
	TagPattern  string
	From        string
	To          string
	WorkingTree bool
	SurveyMax   int
	Remote      string
}

// Range is a resolved commit range; an empty Base means the root of history and an empty Head the working tree.
type Range struct {
	Kind        string    `json:"rangeKind"`
	Base        string    `json:"baseSha"`
	Head        string    `json:"headSha"`
	MergeBase   string    `json:"mergeBase,omitempty"`
	Skip        string    `json:"skip,omitempty"`
	Tag         string    `json:"tag,omitempty"`
	PreviousTag string    `json:"previousTag,omitempty"`
	Warnings    []Warning `json:"warnings,omitempty"`
}

// Advanceable reports whether finishing a run over this range may move the pass watermark.
func (r Range) Advanceable() bool {
	switch r.Kind {
	case api.RangeWatermark, api.RangeSurvey, api.RangeRelease:
		return r.Skip != api.SkipStaleHead
	}
	return false
}

func (r *Range) warn(code, format string, args ...any) {
	r.Warnings = append(r.Warnings, Warning{Code: code, Message: fmt.Sprintf(format, args...)})
}

// Resolver resolves ranges against a repository, deepening a shallow clone at most once.
type Resolver struct {
	Repo     *git.Repo
	deepened bool
	shallow  bool
}

// NewResolver returns a Resolver for repo.
func NewResolver(repo *git.Repo) *Resolver {
	return &Resolver{Repo: repo}
}

func (rs *Resolver) ensure(ctx context.Context, sha string) bool {
	if rs.Repo.CommitExists(ctx, sha) {
		return true
	}
	rs.deepen(ctx)
	return rs.Repo.CommitExists(ctx, sha)
}

func (rs *Resolver) deepen(ctx context.Context) bool {
	if rs.deepened {
		return false
	}
	rs.deepened = true
	shallow, err := rs.Repo.IsShallow(ctx)
	if err != nil || !shallow {
		return false
	}
	rs.shallow = true
	if rs.Repo.Deepen(ctx, deepenStep) != nil {
		return false
	}
	if still, err := rs.Repo.IsShallow(ctx); err == nil && still {
		_ = rs.Repo.Deepen(ctx, 0)
	}
	return true
}

// Resolve computes the range for one pass.
func (rs *Resolver) Resolve(ctx context.Context, in RangeInput) (Range, error) {
	if in.Remote == "" {
		in.Remote = DefaultRemote
	}
	if in.SurveyMax <= 0 {
		in.SurveyMax = DefaultSurveyCommits
	}
	if in.TagPattern == "" {
		in.TagPattern = DefaultTagPattern
	}
	if in.WorkingTree {
		return rs.workingTree(ctx, in)
	}
	switch in.Trigger {
	case api.RangePR:
		return rs.pullRequest(ctx, in)
	case "release":
		return rs.release(ctx, in)
	case "manual":
		if in.From != "" {
			return rs.explicit(ctx, in)
		}
		if in.To != "" {
			to, err := rs.Repo.ResolveRef(ctx, in.To)
			if err != nil {
				return Range{}, err
			}
			headSHA, err := rs.Repo.ResolveRef(ctx, headRef(in))
			if err != nil {
				return Range{}, err
			}
			if to != headSHA {
				in.Head = to
				r, err := rs.watermark(ctx, in)
				if err != nil {
					return Range{}, err
				}
				if r.Skip == api.SkipStaleHead {
					r, err = rs.survey(ctx, to, in, "")
					if err != nil {
						return Range{}, err
					}
				}
				r.Kind = api.RangeExplicit
				return r, nil
			}
		}
		return rs.watermark(ctx, in)
	default:
		return rs.watermark(ctx, in)
	}
}

func headRef(in RangeInput) string {
	if in.Head != "" {
		return in.Head
	}
	return "HEAD"
}

func (rs *Resolver) workingTree(ctx context.Context, in RangeInput) (Range, error) {
	base := "HEAD"
	if in.From != "" {
		base = in.From
	}
	sha, err := rs.Repo.ResolveRef(ctx, base)
	if err != nil {
		return Range{}, err
	}
	return Range{Kind: api.RangeWorkingTree, Base: sha}, nil
}

func (rs *Resolver) explicit(ctx context.Context, in RangeInput) (Range, error) {
	base, err := rs.Repo.ResolveRef(ctx, in.From)
	if err != nil {
		return Range{}, err
	}
	to := in.To
	if to == "" {
		to = headRef(in)
	}
	head, err := rs.Repo.ResolveRef(ctx, to)
	if err != nil {
		return Range{}, err
	}
	r := Range{Kind: api.RangeExplicit, Base: base, Head: head}
	if base == head {
		r.Skip = api.SkipNoChanges
	}
	return r, nil
}

func (rs *Resolver) watermark(ctx context.Context, in RangeInput) (Range, error) {
	head, err := rs.Repo.ResolveRef(ctx, headRef(in))
	if err != nil {
		return Range{}, err
	}
	w := in.Watermark
	if w == "" {
		return rs.survey(ctx, head, in, "no watermark yet: surveying the last %d commits")
	}
	if strings.EqualFold(w, head) {
		return Range{Kind: api.RangeWatermark, Base: head, Head: head, Skip: api.SkipNoChanges}, nil
	}
	if !rs.ensure(ctx, w) {
		r, err := rs.survey(ctx, head, in, "")
		if err != nil {
			return Range{}, err
		}
		if rs.shallow {
			r.warn(WarnShallowClone, "watermark %s is not in this clone even after fetching more history; surveyed the last %d commits instead (use fetch-depth: 0)", short(w), in.SurveyMax)
		} else {
			r.warn(WarnWatermarkDiverged, "watermark %s no longer exists in this repository (force-push?); surveyed the last %d commits instead", short(w), in.SurveyMax)
		}
		return r, nil
	}
	behind, err := rs.Repo.IsAncestor(ctx, head, w)
	if err != nil {
		return Range{}, err
	}
	if behind {
		return Range{Kind: api.RangeWatermark, Base: w, Head: head, Skip: api.SkipStaleHead}, nil
	}
	ahead, err := rs.Repo.IsAncestor(ctx, w, head)
	if err != nil {
		return Range{}, err
	}
	if ahead {
		return Range{Kind: api.RangeWatermark, Base: w, Head: head}, nil
	}
	mb, err := rs.Repo.MergeBase(ctx, w, head)
	if err != nil || mb == "" {
		r, serr := rs.survey(ctx, head, in, "")
		if serr != nil {
			return Range{}, serr
		}
		r.warn(WarnWatermarkDiverged, "watermark %s shares no history with HEAD; surveyed the last %d commits instead", short(w), in.SurveyMax)
		return r, nil
	}
	r := Range{Kind: api.RangeWatermark, Base: mb, Head: head, MergeBase: mb}
	r.warn(WarnWatermarkDiverged, "watermark %s is not an ancestor of HEAD (force-push or rebase); using the merge base %s", short(w), short(mb))
	return r, nil
}

func (rs *Resolver) survey(ctx context.Context, head string, in RangeInput, note string) (Range, error) {
	if shallow, err := rs.Repo.IsShallow(ctx); err == nil && shallow {
		rs.deepen(ctx)
	}
	base, err := rs.Repo.AncestorAt(ctx, head, in.SurveyMax)
	if err != nil {
		return Range{}, err
	}
	tag, err := rs.latestTagBefore(ctx, head, in.TagPattern)
	if err != nil {
		return Range{}, err
	}
	if tag != nil {
		n, err := rs.Repo.CountCommits(ctx, tag.Commit, head)
		if err == nil && n > 0 && n < in.SurveyMax {
			base = tag.Commit
		}
	}
	r := Range{Kind: api.RangeSurvey, Base: base, Head: head}
	if note != "" {
		r.warn(WarnSurvey, note, in.SurveyMax)
	}
	return r, nil
}

func (rs *Resolver) latestTagBefore(ctx context.Context, head, pattern string) (*git.TagInfo, error) {
	tags, err := rs.Repo.Tags(ctx, pattern)
	if err != nil {
		return nil, err
	}
	var best *git.TagInfo
	for i := range tags {
		t := tags[i]
		if t.Commit == head {
			continue
		}
		ok, err := rs.Repo.IsAncestor(ctx, t.Commit, head)
		if err != nil || !ok {
			continue
		}
		if best == nil || tagLess(*best, t) {
			best = &tags[i]
		}
	}
	return best, nil
}

func (rs *Resolver) pullRequest(ctx context.Context, in RangeInput) (Range, error) {
	head, err := rs.Repo.ResolveRef(ctx, headRef(in))
	if err != nil {
		return Range{}, err
	}
	if in.PRTarget == "" {
		r, err := rs.survey(ctx, head, in, "")
		if err != nil {
			return Range{}, err
		}
		r.Kind = api.RangePR
		r.warn(WarnSurvey, "the pull request target branch is unknown; surveyed the last %d commits", in.SurveyMax)
		return r, nil
	}
	target := rs.resolveTarget(ctx, in)
	if target != "" {
		mb, err := rs.Repo.MergeBase(ctx, target, head)
		if err != nil && rs.deepen(ctx) {
			mb, err = rs.Repo.MergeBase(ctx, target, head)
		}
		if err == nil && mb != "" {
			return Range{Kind: api.RangePR, Base: mb, Head: head, MergeBase: mb}, nil
		}
	}
	if in.PRBase != "" && rs.ensure(ctx, in.PRBase) {
		return Range{Kind: api.RangePR, Base: in.PRBase, Head: head}, nil
	}
	r, err := rs.survey(ctx, head, in, "")
	if err != nil {
		return Range{}, err
	}
	r.Kind = api.RangePR
	r.warn(WarnShallowClone, "could not find the merge base with %s; surveyed the last %d commits (use fetch-depth: 0)", in.PRTarget, in.SurveyMax)
	return r, nil
}

func (rs *Resolver) resolveTarget(ctx context.Context, in RangeInput) string {
	candidates := []string{in.Remote + "/" + in.PRTarget, "refs/remotes/" + in.Remote + "/" + in.PRTarget, in.PRTarget}
	for _, c := range candidates {
		if sha, err := rs.Repo.ResolveRef(ctx, c); err == nil {
			return sha
		}
	}
	if rs.Repo.FetchBranch(ctx, in.Remote, in.PRTarget) == nil {
		if sha, err := rs.Repo.ResolveRef(ctx, in.Remote+"/"+in.PRTarget); err == nil {
			return sha
		}
	}
	return ""
}

func (rs *Resolver) release(ctx context.Context, in RangeInput) (Range, error) {
	if in.Tag == "" {
		return Range{}, fmt.Errorf("a release run needs the release tag (set GRAVITY_TAG or run on a tag)")
	}
	head, err := rs.Repo.ResolveRef(ctx, in.Tag)
	if err != nil {
		return Range{}, fmt.Errorf("release tag %s: %w", in.Tag, err)
	}
	tags, err := rs.Repo.Tags(ctx, in.TagPattern)
	if err != nil {
		return Range{}, err
	}
	self := git.TagInfo{Name: in.Tag, Commit: head}
	for _, t := range tags {
		if t.Name == in.Tag {
			self = t
		}
	}
	var prev *git.TagInfo
	for i := range tags {
		t := tags[i]
		if t.Name == in.Tag || t.Commit == head || !tagLess(t, self) {
			continue
		}
		ok, err := rs.Repo.IsAncestor(ctx, t.Commit, head)
		if err != nil || !ok {
			continue
		}
		if prev == nil || tagLess(*prev, t) {
			prev = &tags[i]
		}
	}
	if prev == nil {
		base, err := rs.Repo.AncestorAt(ctx, head, in.SurveyMax)
		if err != nil {
			return Range{}, err
		}
		r := Range{Kind: api.RangeSurvey, Base: base, Head: head, Tag: in.Tag}
		r.warn(WarnSurvey, "first release: covering up to the last %d commits", in.SurveyMax)
		return r, nil
	}
	return Range{Kind: api.RangeRelease, Base: prev.Commit, Head: head, Tag: in.Tag, PreviousTag: prev.Name}, nil
}

func tagLess(a, b git.TagInfo) bool {
	va, okA := parseSemver(a.Name)
	vb, okB := parseSemver(b.Name)
	if okA && okB {
		if c := compareSemver(va, vb); c != 0 {
			return c < 0
		}
		return a.Name < b.Name
	}
	if a.CreatorDate != b.CreatorDate {
		return a.CreatorDate < b.CreatorDate
	}
	return a.Name < b.Name
}

// SortTags orders tags oldest release first (semver when both parse, else creator date).
func SortTags(tags []git.TagInfo) {
	sort.SliceStable(tags, func(i, j int) bool { return tagLess(tags[i], tags[j]) })
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
