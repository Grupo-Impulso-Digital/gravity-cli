package changeset

import (
	"context"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func resolve(t *testing.T, repo *git.Repo, in RangeInput) Range {
	t.Helper()
	r, err := NewResolver(repo).Resolve(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hasWarning(r Range, code string) bool {
	for _, w := range r.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestPushWithWatermark(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(4)
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", Watermark: shas[1]})
	if r.Kind != api.RangeWatermark || r.Base != shas[1] || r.Head != shas[3] || r.Skip != "" || !r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
}

func TestPushEqualHeadIsNoChanges(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(2)
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", Watermark: shas[1]})
	if r.Skip != api.SkipNoChanges || !r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
}

func TestStaleHeadNeverAdvances(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	s.git("checkout", "-q", shas[1])
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", Watermark: shas[2]})
	if r.Skip != api.SkipStaleHead || r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
}

func TestOutOfOrderPushes(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	c1, c2 := shas[1], shas[2]
	repo := s.repo()
	late := resolve(t, repo, RangeInput{Trigger: "push", Head: c1, Watermark: c2})
	if late.Skip != api.SkipStaleHead {
		t.Fatalf("late job = %+v", late)
	}
	early := resolve(t, repo, RangeInput{Trigger: "push", Head: c2, Watermark: shas[0]})
	if early.Skip != "" || early.Base != shas[0] || early.Head != c2 {
		t.Fatalf("winning job = %+v", early)
	}
}

func TestNoWatermarkSurveyIsBounded(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(8)
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", SurveyMax: 3})
	if r.Kind != api.RangeSurvey || r.Base != shas[4] || r.Head != shas[7] || !hasWarning(r, WarnSurvey) || !r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
	short := resolve(t, s.repo(), RangeInput{Trigger: "push", SurveyMax: 50})
	if short.Kind != api.RangeSurvey || short.Base != "" {
		t.Fatalf("short history survey = %+v", short)
	}
}

func TestSurveyStopsAtCloserReleaseTag(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(6)
	s.git("tag", "v1.0.0", shas[3])
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", SurveyMax: 50})
	if r.Base != shas[3] {
		t.Fatalf("range = %+v want base %s", r, shas[3])
	}
}

func TestForcePushUsesMergeBase(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	s.git("checkout", "-q", "-b", "old", shas[2])
	orphaned := s.commit("old tip", map[string]string{"old.txt": "x"})
	s.git("checkout", "-q", "main")
	s.git("reset", "-q", "--hard", shas[1])
	newHead := s.commit("rewritten", map[string]string{"new.txt": "y"})
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", Watermark: orphaned})
	if r.Kind != api.RangeWatermark || r.Base != shas[1] || r.Head != newHead || r.MergeBase != shas[1] || !hasWarning(r, WarnWatermarkDiverged) {
		t.Fatalf("range = %+v", r)
	}
}

func TestMissingWatermarkFallsBackToSurvey(t *testing.T) {
	s := newScripted(t)
	s.linear(3)
	r := resolve(t, s.repo(), RangeInput{Trigger: "push", Watermark: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	if r.Kind != api.RangeSurvey || !hasWarning(r, WarnWatermarkDiverged) {
		t.Fatalf("range = %+v", r)
	}
}

func TestShallowCloneDeepensForWatermark(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(6)
	clone := t.TempDir()
	runGit(t, clone, "clone", "-q", "--depth", "1", "file://"+s.dir, ".")
	repo, err := git.Open(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	if repo.CommitExists(context.Background(), shas[2]) {
		t.Fatal("clone should be shallow")
	}
	r := resolve(t, repo, RangeInput{Trigger: "push", Watermark: shas[2]})
	if r.Kind != api.RangeWatermark || r.Base != shas[2] || r.Head != shas[5] {
		t.Fatalf("range = %+v", r)
	}
}

func TestPullRequestMergeBase(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	s.git("checkout", "-q", "-b", "feat")
	prHead := s.commit("feat: x", map[string]string{"x.txt": "x"})
	s.git("checkout", "-q", "main")
	s.commit("main moves on", map[string]string{"m.txt": "m"})
	r := resolve(t, s.repo(), RangeInput{Trigger: "pr", Head: prHead, PRTarget: "main"})
	if r.Kind != api.RangePR || r.Base != shas[2] || r.Head != prHead || r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
}

func TestPullRequestFallsBackToProviderBase(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	r := resolve(t, s.repo(), RangeInput{Trigger: "pr", PRTarget: "does-not-exist", PRBase: shas[0]})
	if r.Kind != api.RangePR || r.Base != shas[0] {
		t.Fatalf("range = %+v", r)
	}
}

func TestReleaseUsesPreviousSemverTag(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(5)
	s.git("tag", "v1.9.0", shas[0])
	s.git("tag", "v1.10.0-rc.1", shas[1])
	s.git("tag", "v1.10.0", shas[2])
	s.git("tag", "-a", "v1.11.0", "-m", "release", shas[4])
	s.git("tag", "nightly", shas[3])
	r := resolve(t, s.repo(), RangeInput{Trigger: "release", Tag: "v1.11.0"})
	if r.Kind != api.RangeRelease || r.Base != shas[2] || r.Head != shas[4] || r.PreviousTag != "v1.10.0" || !r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
	rc := resolve(t, s.repo(), RangeInput{Trigger: "release", Tag: "v1.10.0"})
	if rc.PreviousTag != "v1.10.0-rc.1" {
		t.Fatalf("v1.10.0 previous = %+v", rc)
	}
}

func TestFirstReleaseIsSurvey(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(3)
	s.git("tag", "v0.1.0", shas[2])
	r := resolve(t, s.repo(), RangeInput{Trigger: "release", Tag: "v0.1.0"})
	if r.Kind != api.RangeSurvey || r.Head != shas[2] || r.Base != "" || r.Tag != "v0.1.0" {
		t.Fatalf("range = %+v", r)
	}
	if _, err := NewResolver(s.repo()).Resolve(context.Background(), RangeInput{Trigger: "release"}); err == nil {
		t.Fatal("release without a tag must fail")
	}
}

func TestManualRanges(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(4)
	repo := s.repo()
	r := resolve(t, repo, RangeInput{Trigger: "manual", From: shas[0], To: shas[2]})
	if r.Kind != api.RangeExplicit || r.Base != shas[0] || r.Head != shas[2] || r.Advanceable() {
		t.Fatalf("explicit = %+v", r)
	}
	r = resolve(t, repo, RangeInput{Trigger: "manual", To: shas[2], Watermark: shas[1]})
	if r.Kind != api.RangeExplicit || r.Base != shas[1] || r.Head != shas[2] || r.Advanceable() {
		t.Fatalf("--to before HEAD = %+v", r)
	}
	r = resolve(t, repo, RangeInput{Trigger: "manual", To: "HEAD", Watermark: shas[1]})
	if r.Kind != api.RangeWatermark || r.Base != shas[1] {
		t.Fatalf("--to HEAD = %+v", r)
	}
	r = resolve(t, repo, RangeInput{Trigger: "schedule", Watermark: shas[2]})
	if r.Kind != api.RangeWatermark || r.Base != shas[2] {
		t.Fatalf("schedule = %+v", r)
	}
}

func TestWorkingTreeRange(t *testing.T) {
	s := newScripted(t)
	shas := s.linear(2)
	r := resolve(t, s.repo(), RangeInput{Trigger: "manual", WorkingTree: true})
	if r.Kind != api.RangeWorkingTree || r.Base != shas[1] || r.Head != "" || r.Advanceable() {
		t.Fatalf("range = %+v", r)
	}
}

func TestSemverOrdering(t *testing.T) {
	ordered := []string{"v0.9.0", "v1.0.0-alpha", "v1.0.0-alpha.2", "v1.0.0-beta", "v1.0.0", "v1.2", "v1.10.0", "release-2.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		a, ok1 := parseSemver(ordered[i])
		b, ok2 := parseSemver(ordered[i+1])
		if !ok1 || !ok2 || compareSemver(a, b) >= 0 {
			t.Fatalf("%s should sort before %s", ordered[i], ordered[i+1])
		}
	}
	if _, ok := parseSemver("nightly"); ok {
		t.Fatal("nightly is not semver")
	}
	tags := []git.TagInfo{{Name: "b", CreatorDate: "2026-02-01"}, {Name: "a", CreatorDate: "2026-03-01"}, {Name: "v2.0.0"}, {Name: "v1.0.0"}}
	SortTags(tags)
	if tags[0].Name != "b" && tags[0].Name != "v1.0.0" {
		t.Fatalf("sorted = %+v", tags)
	}
}
