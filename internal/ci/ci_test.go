package ci

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type fakeGit struct {
	branch     string
	refs       map[string]string
	defBranch  string
	containing []string
}

func (f fakeGit) CurrentBranch(context.Context) (string, error) { return f.branch, nil }

func (f fakeGit) DefaultBranch(context.Context) string { return f.defBranch }

func (f fakeGit) RemoteBranchesContaining(context.Context, string) ([]string, error) {
	return f.containing, nil
}

func (f fakeGit) ResolveRef(_ context.Context, ref string) (string, error) {
	if sha, ok := f.refs[ref]; ok {
		return sha, nil
	}
	return "", errors.New("unknown ref")
}

const head = "1111111111111111111111111111111111111111"

func detect(t *testing.T, vars, files map[string]string, git Git) Context {
	t.Helper()
	env := Env{
		Getenv: func(k string) string { return vars[k] },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
	}
	c, err := Detect(context.Background(), env, git)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDetectProviders(t *testing.T) {
	ghBase := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "acme/billing-api", "GITHUB_RUN_ID": "123", "GITHUB_SHA": "mergesha"}
	with := func(base, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	prEvent := `{"pull_request":{"number":42,"html_url":"https://github.com/acme/billing-api/pull/42","head":{"sha":"prhead","ref":"feat/x"},"base":{"ref":"main","sha":"basesha"}}}`
	cases := []struct {
		name  string
		vars  map[string]string
		files map[string]string
		want  Context
	}{
		{
			"github pr uses head sha not merge sha", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": "/ev.json", "GITHUB_REF_NAME": "42/merge"}),
			map[string]string{"/ev.json": prEvent},
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerPR, Event: "pull_request", Branch: "feat/x", HeadSHA: "prhead", PR: &PR{Number: 42, URL: "https://github.com/acme/billing-api/pull/42", HeadSHA: "prhead", TargetBranch: "main"}, RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"github push", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main", "GITHUB_REF_NAME": "main"}), nil,
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerPush, Event: "push", Branch: "main", HeadSHA: "mergesha", RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"github tag push is a release", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/tags/v1.4.0", "GITHUB_REF_NAME": "v1.4.0"}), nil,
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerRelease, Event: "push", Tag: "v1.4.0", HeadSHA: "mergesha", RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"github release event", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "release", "GITHUB_EVENT_PATH": "/ev.json", "GITHUB_REF_NAME": "v1.4.0"}),
			map[string]string{"/ev.json": `{"release":{"tag_name":"v1.4.0"}}`},
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerRelease, Event: "release", Tag: "v1.4.0", HeadSHA: "mergesha", RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"github schedule", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "schedule", "GITHUB_REF_NAME": "main"}), nil,
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerSchedule, Event: "schedule", Branch: "main", HeadSHA: "mergesha", RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"github dispatch", with(ghBase, map[string]string{"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF_NAME": "main"}), nil,
			Context{Provider: GitHub, Origin: OriginCI, Trigger: TriggerManual, Event: "workflow_dispatch", Branch: "main", HeadSHA: "mergesha", RunURL: "https://github.com/acme/billing-api/actions/runs/123", WebURL: "https://github.com/acme/billing-api"},
		},
		{
			"gitlab mr",
			map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "merge_request_event", "CI_COMMIT_SHA": "mrhead", "CI_MERGE_REQUEST_IID": "7", "CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "main", "CI_MERGE_REQUEST_DIFF_BASE_SHA": "basesha", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feat/y", "CI_PIPELINE_URL": "https://gitlab.com/acme/x/-/pipelines/9", "CI_PROJECT_URL": "https://gitlab.com/acme/x"},
			nil,
			Context{Provider: GitLab, Origin: OriginCI, Trigger: TriggerPR, Event: "merge_request_event", Branch: "feat/y", HeadSHA: "mrhead", BaseSHA: "basesha", PR: &PR{Number: 7, URL: "https://gitlab.com/acme/x/-/merge_requests/7", HeadSHA: "mrhead", TargetBranch: "main"}, RunURL: "https://gitlab.com/acme/x/-/pipelines/9", WebURL: "https://gitlab.com/acme/x"},
		},
		{
			"gitlab push",
			map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "push", "CI_COMMIT_BRANCH": "main", "CI_COMMIT_SHA": head},
			nil,
			Context{Provider: GitLab, Origin: OriginCI, Trigger: TriggerPush, Event: "push", Branch: "main", HeadSHA: head},
		},
		{
			"gitlab tag",
			map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "push", "CI_COMMIT_TAG": "v2.0.0", "CI_COMMIT_SHA": head},
			nil,
			Context{Provider: GitLab, Origin: OriginCI, Trigger: TriggerRelease, Event: "push", Tag: "v2.0.0", HeadSHA: head},
		},
		{
			"gitlab schedule",
			map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "schedule", "CI_COMMIT_BRANCH": "main", "CI_COMMIT_SHA": head},
			nil,
			Context{Provider: GitLab, Origin: OriginCI, Trigger: TriggerSchedule, Event: "schedule", Branch: "main", HeadSHA: head},
		},
		{
			"gitlab web",
			map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "web", "CI_COMMIT_BRANCH": "main", "CI_COMMIT_SHA": head},
			nil,
			Context{Provider: GitLab, Origin: OriginCI, Trigger: TriggerManual, Event: "web", Branch: "main", HeadSHA: head},
		},
		{
			"bitbucket pr expands short destination sha",
			map[string]string{"BITBUCKET_BUILD_NUMBER": "5", "BITBUCKET_REPO_FULL_NAME": "acme/x", "BITBUCKET_PR_ID": "3", "BITBUCKET_COMMIT": head, "BITBUCKET_BRANCH": "feat/z", "BITBUCKET_PR_DESTINATION_BRANCH": "main", "BITBUCKET_PR_DESTINATION_COMMIT": "abc1234"},
			nil,
			Context{Provider: Bitbucket, Origin: OriginCI, Trigger: TriggerPR, Branch: "feat/z", HeadSHA: head, BaseSHA: "abc1234000000000000000000000000000000000", PR: &PR{Number: 3, URL: "https://bitbucket.org/acme/x/pull-requests/3", HeadSHA: head, TargetBranch: "main"}, RunURL: "https://bitbucket.org/acme/x/pipelines/results/5", WebURL: "https://bitbucket.org/acme/x"},
		},
		{
			"bitbucket tag",
			map[string]string{"BITBUCKET_BUILD_NUMBER": "5", "BITBUCKET_TAG": "v1.0.0", "BITBUCKET_COMMIT": head},
			nil,
			Context{Provider: Bitbucket, Origin: OriginCI, Trigger: TriggerRelease, Tag: "v1.0.0", HeadSHA: head},
		},
		{
			"bitbucket push",
			map[string]string{"BITBUCKET_BUILD_NUMBER": "5", "BITBUCKET_BRANCH": "main", "BITBUCKET_COMMIT": head},
			nil,
			Context{Provider: Bitbucket, Origin: OriginCI, Trigger: TriggerPush, Branch: "main", HeadSHA: head},
		},
		{
			"azure pr",
			map[string]string{"TF_BUILD": "True", "BUILD_REASON": "PullRequest", "BUILD_SOURCEVERSION": "merge", "SYSTEM_PULLREQUEST_PULLREQUESTID": "99", "SYSTEM_PULLREQUEST_SOURCECOMMITID": "prhead", "SYSTEM_PULLREQUEST_TARGETBRANCH": "refs/heads/main", "SYSTEM_PULLREQUEST_SOURCEBRANCH": "refs/heads/feat/a", "SYSTEM_COLLECTIONURI": "https://dev.azure.com/acme/", "SYSTEM_TEAMPROJECT": "proj", "BUILD_BUILDID": "77"},
			nil,
			Context{Provider: Azure, Origin: OriginCI, Trigger: TriggerPR, Event: "PullRequest", Branch: "feat/a", HeadSHA: "prhead", PR: &PR{Number: 99, HeadSHA: "prhead", TargetBranch: "main"}, RunURL: "https://dev.azure.com/acme/proj/_build/results?buildId=77"},
		},
		{
			"azure tag ci is release",
			map[string]string{"TF_BUILD": "True", "BUILD_REASON": "IndividualCI", "BUILD_SOURCEBRANCH": "refs/tags/v3.1.0", "BUILD_SOURCEBRANCHNAME": "v3.1.0", "BUILD_SOURCEVERSION": head},
			nil,
			Context{Provider: Azure, Origin: OriginCI, Trigger: TriggerRelease, Event: "IndividualCI", Tag: "v3.1.0", HeadSHA: head},
		},
		{
			"azure nested branch push",
			map[string]string{"TF_BUILD": "True", "BUILD_REASON": "BatchedCI", "BUILD_SOURCEBRANCH": "refs/heads/release/2026", "BUILD_SOURCEBRANCHNAME": "2026", "BUILD_SOURCEVERSION": head},
			nil,
			Context{Provider: Azure, Origin: OriginCI, Trigger: TriggerPush, Event: "BatchedCI", Branch: "release/2026", HeadSHA: head},
		},
		{
			"azure schedule",
			map[string]string{"TF_BUILD": "True", "BUILD_REASON": "Schedule", "BUILD_SOURCEBRANCH": "refs/heads/main", "BUILD_SOURCEVERSION": head},
			nil,
			Context{Provider: Azure, Origin: OriginCI, Trigger: TriggerSchedule, Event: "Schedule", Branch: "main", HeadSHA: head},
		},
		{
			"jenkins pr",
			map[string]string{"JENKINS_URL": "https://ci", "CHANGE_ID": "12", "CHANGE_TARGET": "main", "CHANGE_BRANCH": "feat/j", "CHANGE_URL": "https://github.com/acme/x/pull/12", "GIT_COMMIT": head, "BUILD_URL": "https://ci/job/1"},
			nil,
			Context{Provider: Jenkins, Origin: OriginCI, Trigger: TriggerPR, Branch: "feat/j", HeadSHA: head, PR: &PR{Number: 12, URL: "https://github.com/acme/x/pull/12", HeadSHA: head, TargetBranch: "main"}, RunURL: "https://ci/job/1"},
		},
		{
			"jenkins push strips origin",
			map[string]string{"JENKINS_URL": "https://ci", "GIT_BRANCH": "origin/main", "GIT_COMMIT": head},
			nil,
			Context{Provider: Jenkins, Origin: OriginCI, Trigger: TriggerPush, Branch: "main", HeadSHA: head},
		},
		{
			"jenkins tag",
			map[string]string{"JENKINS_URL": "https://ci", "TAG_NAME": "v1.0.0", "GIT_COMMIT": head},
			nil,
			Context{Provider: Jenkins, Origin: OriginCI, Trigger: TriggerRelease, Tag: "v1.0.0", HeadSHA: head},
		},
		{
			"circleci pr",
			map[string]string{"CIRCLECI": "true", "CIRCLE_PULL_REQUEST": "https://github.com/acme/x/pull/8", "CIRCLE_SHA1": head, "CIRCLE_BRANCH": "feat/c", "CIRCLE_BUILD_URL": "https://circleci.com/b/1"},
			nil,
			Context{Provider: CircleCI, Origin: OriginCI, Trigger: TriggerPR, Branch: "feat/c", HeadSHA: head, PR: &PR{Number: 8, URL: "https://github.com/acme/x/pull/8", HeadSHA: head}, RunURL: "https://circleci.com/b/1"},
		},
		{
			"circleci tag",
			map[string]string{"CIRCLECI": "true", "CIRCLE_TAG": "v9.0.0", "CIRCLE_SHA1": head},
			nil,
			Context{Provider: CircleCI, Origin: OriginCI, Trigger: TriggerRelease, Tag: "v9.0.0", HeadSHA: head},
		},
		{
			"generic defaults to push and git",
			map[string]string{"CI": "true"},
			nil,
			Context{Provider: Generic, Origin: OriginCI, Trigger: TriggerPush, Branch: "work", HeadSHA: head},
		},
		{
			"generic overrides",
			map[string]string{"CI": "1", "GRAVITY_TRIGGER": "pr", "GRAVITY_PR": "#5", "GRAVITY_BASE_BRANCH": "develop", "GRAVITY_BRANCH": "feat/g", "GRAVITY_RUN_URL": "https://ci/run"},
			nil,
			Context{Provider: Generic, Origin: OriginCI, Trigger: TriggerPR, Branch: "feat/g", HeadSHA: head, PR: &PR{Number: 5, HeadSHA: head, TargetBranch: "develop"}, RunURL: "https://ci/run"},
		},
		{
			"local",
			map[string]string{},
			nil,
			Context{Provider: Local, Origin: OriginLocal, Branch: "work", HeadSHA: head},
		},
	}
	git := fakeGit{branch: "work", refs: map[string]string{"HEAD": head, "abc1234": "abc1234000000000000000000000000000000000"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detect(t, tc.vars, tc.files, git)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v pr=%+v\nwant %+v pr=%+v", got, got.PR, tc.want, tc.want.PR)
			}
		})
	}
}

func TestOverridesWinOverProvider(t *testing.T) {
	c := detect(t, map[string]string{
		"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_REF_NAME": "main", "GITHUB_SHA": head,
		"GRAVITY_TRIGGER": "schedule", "GRAVITY_BRANCH": "nightly", "GRAVITY_HEAD_SHA": "override", "GRAVITY_BASE_SHA": "base", "GRAVITY_TAG": "v0",
	}, nil, nil)
	if c.Trigger != TriggerSchedule || c.Branch != "nightly" || c.HeadSHA != "override" || c.BaseSHA != "base" || c.Tag != "v0" {
		t.Fatalf("c = %+v", c)
	}
}

func TestDetachedHead(t *testing.T) {
	c := detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}})
	if !c.Detached || c.Branch != "" {
		t.Fatalf("c = %+v", c)
	}
	c = detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}, defBranch: "main", containing: []string{"feature", "main"}})
	if c.Detached || c.Branch != "main" {
		t.Fatalf("c = %+v", c)
	}
	c = detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}, defBranch: "main", containing: []string{"feature"}})
	if !c.Detached || c.Branch != "" {
		t.Fatalf("c = %+v", c)
	}
	c = detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}, containing: []string{"feature", "master", "main"}})
	if c.Detached || c.Branch != "main" {
		t.Fatalf("c = %+v", c)
	}
	c = detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}, containing: []string{"master"}})
	if c.Detached || c.Branch != "master" {
		t.Fatalf("c = %+v", c)
	}
	c = detect(t, map[string]string{"CI": "true"}, nil, fakeGit{branch: "HEAD", refs: map[string]string{"HEAD": head}, defBranch: "develop", containing: []string{"main"}})
	if !c.Detached || c.Branch != "" {
		t.Fatalf("c = %+v", c)
	}
}

func TestInvalidOverrides(t *testing.T) {
	for _, vars := range []map[string]string{{"GRAVITY_TRIGGER": "nightly"}, {"GRAVITY_PR": "abc"}} {
		_, err := Detect(context.Background(), Env{Getenv: func(k string) string { return vars[k] }}, nil)
		if err == nil || !strings.Contains(err.Error(), "GRAVITY_") {
			t.Fatalf("vars %v: err = %v", vars, err)
		}
	}
}

func TestBrokenGitHubEventFile(t *testing.T) {
	_, err := Detect(context.Background(), Env{
		Getenv: func(k string) string {
			return map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": "/e"}[k]
		},
		ReadFile: func(string) ([]byte, error) { return []byte("{"), nil },
	}, nil)
	if err == nil {
		t.Fatal("want error for a corrupt event file")
	}
}

func TestDefaultBranchFromProviders(t *testing.T) {
	read := func(string) ([]byte, error) {
		return []byte(`{"repository":{"default_branch":"trunk"}}`), nil
	}
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"github event":  {map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_PATH": "/e"}, "trunk"},
		"gitlab":        {map[string]string{"GITLAB_CI": "true", "CI_DEFAULT_BRANCH": "develop"}, "develop"},
		"override wins": {map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_PATH": "/e", "GRAVITY_DEFAULT_BRANCH": "main"}, "main"},
		"unknown":       {map[string]string{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			env := Env{Getenv: func(k string) string { return tc.env[k] }, ReadFile: read}
			if got := DefaultBranch(env); got != tc.want {
				t.Fatalf("DefaultBranch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestForkDetection(t *testing.T) {
	gh := func(event string) (map[string]string, map[string]string) {
		return map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": "/ev.json"}, map[string]string{"/ev.json": event}
	}
	type forkCase struct {
		name  string
		vars  map[string]string
		files map[string]string
		fork  bool
	}
	var cases []forkCase
	add := func(name string, vars, files map[string]string, fork bool) {
		cases = append(cases, forkCase{name, vars, files, fork})
	}
	v, f := gh(`{"pull_request":{"number":1,"head":{"sha":"a","repo":{"full_name":"someone/billing-api"}},"base":{"ref":"main","repo":{"full_name":"acme/billing-api"}}}}`)
	add("github fork", v, f, true)
	v, f = gh(`{"pull_request":{"number":1,"head":{"sha":"a","repo":{"full_name":"Acme/Billing-API"}},"base":{"ref":"main","repo":{"full_name":"acme/billing-api"}}}}`)
	add("github same repository", v, f, false)
	v, f = gh(`{"pull_request":{"number":1,"head":{"sha":"a","repo":null},"base":{"ref":"main","repo":{"full_name":"acme/billing-api"}}}}`)
	add("github deleted fork", v, f, true)
	v, f = gh(`{"pull_request":{"number":1,"head":{"sha":"a"},"base":{"ref":"main"}}}`)
	add("github event without repositories", v, f, false)
	add("gitlab fork", map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "merge_request_event", "CI_MERGE_REQUEST_SOURCE_PROJECT_ID": "7", "CI_MERGE_REQUEST_PROJECT_ID": "3"}, nil, true)
	add("gitlab same project", map[string]string{"GITLAB_CI": "true", "CI_PIPELINE_SOURCE": "merge_request_event", "CI_MERGE_REQUEST_SOURCE_PROJECT_ID": "3", "CI_MERGE_REQUEST_PROJECT_ID": "3"}, nil, false)
	add("azure fork", map[string]string{"TF_BUILD": "True", "BUILD_REASON": "PullRequest", "SYSTEM_PULLREQUEST_ISFORK": "True"}, nil, true)
	add("azure same repository", map[string]string{"TF_BUILD": "True", "BUILD_REASON": "PullRequest", "SYSTEM_PULLREQUEST_ISFORK": "False"}, nil, false)
	add("jenkins fork", map[string]string{"JENKINS_URL": "https://ci", "CHANGE_ID": "4", "CHANGE_FORK": "someone"}, nil, true)
	add("jenkins same repository", map[string]string{"JENKINS_URL": "https://ci", "CHANGE_ID": "4"}, nil, false)
	add("circleci fork", map[string]string{"CIRCLECI": "true", "CIRCLE_PULL_REQUEST": "https://github.com/acme/x/pull/9", "CIRCLE_PR_USERNAME": "someone"}, nil, true)
	add("circleci same repository", map[string]string{"CIRCLECI": "true", "CIRCLE_PULL_REQUEST": "https://github.com/acme/x/pull/9"}, nil, false)
	add("bitbucket never reports a fork", map[string]string{"BITBUCKET_BUILD_NUMBER": "1", "BITBUCKET_PR_ID": "2"}, nil, false)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := detect(t, tc.vars, tc.files, nil)
			if c.Trigger != TriggerPR {
				t.Fatalf("trigger = %q, want pr", c.Trigger)
			}
			if c.Fork != tc.fork {
				t.Fatalf("fork = %v, want %v", c.Fork, tc.fork)
			}
		})
	}
}

func TestDependabotAndManualStarts(t *testing.T) {
	dep := detect(t, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": "/ev.json"},
		map[string]string{"/ev.json": `{"pull_request":{"number":3,"user":{"login":"dependabot[bot]"},"head":{"sha":"a","repo":{"full_name":"acme/x"}},"base":{"ref":"main","repo":{"full_name":"acme/x"}}}}`}, nil)
	if dep.Bot != BotDependabot || dep.Fork || !dep.NoSecrets() {
		t.Fatalf("dependabot pull request = %+v", dep)
	}
	push := detect(t, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_ACTOR": "dependabot[bot]"}, nil, nil)
	if push.Bot != BotDependabot {
		t.Fatalf("dependabot push = %+v", push)
	}
	human := detect(t, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_NAME": "push", "GITHUB_ACTOR": "dave"}, nil, nil)
	if human.NoSecrets() {
		t.Fatalf("a human push has secrets: %+v", human)
	}
	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"jenkins user cause", map[string]string{"JENKINS_URL": "https://ci", "BUILD_CAUSE": "USERIDCAUSE"}, TriggerManual},
		{"jenkins root cause", map[string]string{"JENKINS_URL": "https://ci", "ROOT_BUILD_CAUSE": "MANUALTRIGGER"}, TriggerManual},
		{"jenkins build user", map[string]string{"JENKINS_URL": "https://ci", "BUILD_USER_ID": "dave"}, TriggerManual},
		{"jenkins timer", map[string]string{"JENKINS_URL": "https://ci", "BUILD_CAUSE": "TIMERTRIGGER"}, TriggerSchedule},
		{"jenkins scm", map[string]string{"JENKINS_URL": "https://ci", "BUILD_CAUSE": "SCMTRIGGER"}, TriggerPush},
		{"jenkins pull request stays pr", map[string]string{"JENKINS_URL": "https://ci", "CHANGE_ID": "4", "BUILD_USER_ID": "dave"}, TriggerPR},
		{"circleci api", map[string]string{"CIRCLECI": "true", "CIRCLE_PIPELINE_TRIGGER_SOURCE": "api"}, TriggerManual},
		{"circleci schedule", map[string]string{"CIRCLECI": "true", "CIRCLE_PIPELINE_TRIGGER_SOURCE": "scheduled_pipeline"}, TriggerSchedule},
		{"circleci webhook", map[string]string{"CIRCLECI": "true", "CIRCLE_PIPELINE_TRIGGER_SOURCE": "webhook"}, TriggerPush},
		{"bitbucket override", map[string]string{"BITBUCKET_BUILD_NUMBER": "1", "GRAVITY_TRIGGER": "manual"}, TriggerManual},
	}
	for _, tc := range cases {
		if got := detect(t, tc.vars, nil, nil).Trigger; got != tc.want {
			t.Errorf("%s: trigger = %q, want %q", tc.name, got, tc.want)
		}
	}
}
