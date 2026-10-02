// Package ci detects the CI provider and the trigger, branch, commits, pull request and tag of the current job.
package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// Providers.
const (
	GitHub    = "github"
	GitLab    = "gitlab"
	Bitbucket = "bitbucket"
	Azure     = "azure"
	Jenkins   = "jenkins"
	CircleCI  = "circleci"
	Generic   = "generic"
	Local     = "local"
)

// Triggers.
const (
	TriggerPR       = "pr"
	TriggerPush     = "push"
	TriggerRelease  = "release"
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Origins.
const (
	OriginCI    = "ci"
	OriginLocal = "local"
)

// PR describes the pull request of a pr trigger.
type PR struct {
	Number       int    `json:"number"`
	URL          string `json:"url,omitempty"`
	HeadSHA      string `json:"headSha,omitempty"`
	TargetBranch string `json:"targetBranch,omitempty"`
}

// Context is what the CLI knows about the job it runs in.
type Context struct {
	Provider string `json:"provider"`
	Origin   string `json:"origin"`
	Trigger  string `json:"trigger,omitempty"`
	Event    string `json:"event,omitempty"`
	Branch   string `json:"branch,omitempty"`
	HeadSHA  string `json:"headSha,omitempty"`
	BaseSHA  string `json:"baseSha,omitempty"`
	PR       *PR    `json:"pr,omitempty"`
	Tag      string `json:"tag,omitempty"`
	RunURL   string `json:"runUrl,omitempty"`
	WebURL   string `json:"webUrl,omitempty"`
	Detached bool   `json:"detached,omitempty"`
}

// IsCI reports whether the job runs on a CI provider.
func (c Context) IsCI() bool { return c.Origin == OriginCI }

// Git resolves local repository facts detection may need.
type Git interface {
	CurrentBranch(ctx context.Context) (string, error)
	ResolveRef(ctx context.Context, ref string) (string, error)
	DefaultBranch(ctx context.Context) string
	RemoteBranchesContaining(ctx context.Context, sha string) ([]string, error)
}

// Env abstracts the process environment and filesystem reads.
type Env struct {
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
}

func (e Env) get(k string) string {
	if e.Getenv == nil {
		return os.Getenv(k)
	}
	return strings.TrimSpace(e.Getenv(k))
}

func (e Env) read(p string) ([]byte, error) {
	if e.ReadFile == nil {
		return os.ReadFile(p)
	}
	return e.ReadFile(p)
}

// ValidTrigger reports whether t is a pipeline trigger.
func ValidTrigger(t string) bool {
	switch t {
	case TriggerPR, TriggerPush, TriggerRelease, TriggerSchedule, TriggerManual:
		return true
	}
	return false
}

// Detect resolves the CI context from the environment, then applies GRAVITY_* overrides.
func Detect(ctx context.Context, env Env, git Git) (Context, error) {
	var c Context
	var err error
	switch {
	case env.get("GITHUB_ACTIONS") == "true":
		c, err = github(env)
	case env.get("GITLAB_CI") == "true":
		c = gitlab(env)
	case env.get("BITBUCKET_BUILD_NUMBER") != "":
		c = bitbucket(env)
	case strings.EqualFold(env.get("TF_BUILD"), "true"):
		c = azure(env)
	case env.get("JENKINS_URL") != "":
		c = jenkins(env)
	case env.get("CIRCLECI") == "true":
		c = circleci(env)
	case truthy(env.get("CI")):
		c = Context{Provider: Generic, Trigger: TriggerPush}
	default:
		c = Context{Provider: Local}
	}
	if err != nil {
		return Context{}, err
	}
	c.Origin = OriginCI
	if c.Provider == Local {
		c.Origin = OriginLocal
	}
	if err := applyOverrides(&c, env); err != nil {
		return Context{}, err
	}
	if c.Provider == Bitbucket && c.BaseSHA != "" && len(c.BaseSHA) < 40 && git != nil {
		if full, err := git.ResolveRef(ctx, c.BaseSHA); err == nil {
			c.BaseSHA = full
		}
	}
	if git != nil {
		if c.HeadSHA == "" {
			if sha, err := git.ResolveRef(ctx, "HEAD"); err == nil {
				c.HeadSHA = sha
			}
		}
		if c.Branch == "" && c.Trigger != TriggerRelease {
			if b, err := git.CurrentBranch(ctx); err == nil {
				if b == "HEAD" {
					c.Detached = true
				} else {
					c.Branch = b
				}
			}
		}
		if c.Detached && c.Branch == "" && c.HeadSHA != "" {
			c.Branch = detachedBranch(ctx, git, c.HeadSHA)
			c.Detached = c.Branch == ""
		}
	}
	if c.PR != nil && c.PR.HeadSHA == "" {
		c.PR.HeadSHA = c.HeadSHA
	}
	return c, nil
}

func detachedBranch(ctx context.Context, git Git, sha string) string {
	def := git.DefaultBranch(ctx)
	if def == "" {
		return ""
	}
	branches, err := git.RemoteBranchesContaining(ctx, sha)
	if err != nil {
		return ""
	}
	for _, b := range branches {
		if b == def {
			return def
		}
	}
	return ""
}

func applyOverrides(c *Context, env Env) error {
	if t := env.get("GRAVITY_TRIGGER"); t != "" {
		if !ValidTrigger(t) {
			return fmt.Errorf("GRAVITY_TRIGGER %q must be one of pr, push, release, schedule, manual", t)
		}
		c.Trigger = t
	}
	if v := env.get("GRAVITY_BRANCH"); v != "" {
		c.Branch = v
	}
	if v := env.get("GRAVITY_HEAD_SHA"); v != "" {
		c.HeadSHA = v
	}
	if v := env.get("GRAVITY_BASE_SHA"); v != "" {
		c.BaseSHA = v
	}
	if v := env.get("GRAVITY_TAG"); v != "" {
		c.Tag = v
	}
	if v := env.get("GRAVITY_RUN_URL"); v != "" {
		c.RunURL = v
	}
	if v := env.get("GRAVITY_PR"); v != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(v, "#"))
		if err != nil || n <= 0 {
			return fmt.Errorf("GRAVITY_PR %q must be a pull request number", v)
		}
		if c.PR == nil {
			c.PR = &PR{}
		}
		c.PR.Number = n
	}
	if v := env.get("GRAVITY_BASE_BRANCH"); v != "" {
		if c.PR == nil {
			c.PR = &PR{}
		}
		c.PR.TargetBranch = v
	}
	if c.Trigger == TriggerPR && c.PR == nil {
		c.PR = &PR{}
	}
	return nil
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func github(env Env) (Context, error) {
	c := Context{Provider: GitHub, Event: env.get("GITHUB_EVENT_NAME"), HeadSHA: env.get("GITHUB_SHA"), Branch: env.get("GITHUB_REF_NAME")}
	if server, repo := env.get("GITHUB_SERVER_URL"), env.get("GITHUB_REPOSITORY"); server != "" && repo != "" {
		c.WebURL = server + "/" + repo
		if id := env.get("GITHUB_RUN_ID"); id != "" {
			c.RunURL = c.WebURL + "/actions/runs/" + id
		}
	}
	var event struct {
		PullRequest *struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"base"`
		} `json:"pull_request"`
		Release *struct {
			TagName string `json:"tag_name"`
		} `json:"release"`
	}
	if p := env.get("GITHUB_EVENT_PATH"); p != "" {
		if data, err := env.read(p); err == nil {
			if err := json.Unmarshal(data, &event); err != nil {
				return Context{}, fmt.Errorf("read GitHub event %s: %w", p, err)
			}
		}
	}
	ref := env.get("GITHUB_REF")
	switch c.Event {
	case "pull_request", "pull_request_target":
		c.Trigger = TriggerPR
		c.PR = &PR{}
		if head := env.get("GITHUB_HEAD_REF"); head != "" {
			c.Branch = head
		}
		if pr := event.PullRequest; pr != nil {
			c.PR = &PR{Number: pr.Number, URL: pr.HTMLURL, HeadSHA: pr.Head.SHA, TargetBranch: pr.Base.Ref}
			if pr.Head.SHA != "" {
				c.HeadSHA = pr.Head.SHA
			}
			if pr.Head.Ref != "" {
				c.Branch = pr.Head.Ref
			}
		}
		if c.PR.TargetBranch == "" {
			c.PR.TargetBranch = env.get("GITHUB_BASE_REF")
		}
	case "push":
		c.Trigger = TriggerPush
		if strings.HasPrefix(ref, "refs/tags/") {
			c.Trigger = TriggerRelease
			c.Tag = strings.TrimPrefix(ref, "refs/tags/")
			c.Branch = ""
		}
	case "release":
		c.Trigger = TriggerRelease
		c.Tag = env.get("GITHUB_REF_NAME")
		if event.Release != nil && event.Release.TagName != "" {
			c.Tag = event.Release.TagName
		}
		c.Branch = ""
	case "schedule":
		c.Trigger = TriggerSchedule
	default:
		c.Trigger = TriggerManual
	}
	return c, nil
}

func gitlab(env Env) Context {
	c := Context{
		Provider: GitLab, Event: env.get("CI_PIPELINE_SOURCE"),
		Branch: env.get("CI_COMMIT_BRANCH"), HeadSHA: env.get("CI_COMMIT_SHA"),
		RunURL: env.get("CI_PIPELINE_URL"), WebURL: env.get("CI_PROJECT_URL"),
	}
	tag := env.get("CI_COMMIT_TAG")
	switch c.Event {
	case "merge_request_event":
		c.Trigger = TriggerPR
		n, _ := strconv.Atoi(env.get("CI_MERGE_REQUEST_IID"))
		c.PR = &PR{Number: n, HeadSHA: c.HeadSHA, TargetBranch: env.get("CI_MERGE_REQUEST_TARGET_BRANCH_NAME")}
		if c.WebURL != "" && n > 0 {
			c.PR.URL = c.WebURL + "/-/merge_requests/" + strconv.Itoa(n)
		}
		c.BaseSHA = env.get("CI_MERGE_REQUEST_DIFF_BASE_SHA")
		if src := env.get("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"); src != "" {
			c.Branch = src
		}
	case "schedule":
		c.Trigger = TriggerSchedule
	case "web", "api", "trigger", "pipeline", "chat":
		c.Trigger = TriggerManual
	default:
		c.Trigger = TriggerPush
	}
	if tag != "" && c.Trigger != TriggerPR {
		c.Trigger = TriggerRelease
		c.Tag = tag
		c.Branch = ""
	}
	return c
}

func bitbucket(env Env) Context {
	full := env.get("BITBUCKET_REPO_FULL_NAME")
	c := Context{Provider: Bitbucket, Branch: env.get("BITBUCKET_BRANCH"), HeadSHA: env.get("BITBUCKET_COMMIT"), Trigger: TriggerPush}
	if full != "" {
		c.WebURL = "https://bitbucket.org/" + full
		c.RunURL = c.WebURL + "/pipelines/results/" + env.get("BITBUCKET_BUILD_NUMBER")
	}
	switch {
	case env.get("BITBUCKET_PR_ID") != "":
		c.Trigger = TriggerPR
		n, _ := strconv.Atoi(env.get("BITBUCKET_PR_ID"))
		c.PR = &PR{Number: n, HeadSHA: c.HeadSHA, TargetBranch: env.get("BITBUCKET_PR_DESTINATION_BRANCH")}
		if c.WebURL != "" && n > 0 {
			c.PR.URL = c.WebURL + "/pull-requests/" + strconv.Itoa(n)
		}
		c.BaseSHA = env.get("BITBUCKET_PR_DESTINATION_COMMIT")
	case env.get("BITBUCKET_TAG") != "":
		c.Trigger = TriggerRelease
		c.Tag = env.get("BITBUCKET_TAG")
		c.Branch = ""
	}
	return c
}

func azure(env Env) Context {
	c := Context{
		Provider: Azure, Event: env.get("BUILD_REASON"),
		Branch: env.get("BUILD_SOURCEBRANCHNAME"), HeadSHA: env.get("BUILD_SOURCEVERSION"),
		WebURL: env.get("BUILD_REPOSITORY_URI"),
	}
	if coll, project, id := env.get("SYSTEM_COLLECTIONURI"), env.get("SYSTEM_TEAMPROJECT"), env.get("BUILD_BUILDID"); coll != "" && id != "" {
		c.RunURL = coll + project + "/_build/results?buildId=" + id
	}
	source := env.get("BUILD_SOURCEBRANCH")
	switch c.Event {
	case "PullRequest":
		c.Trigger = TriggerPR
		num := env.get("SYSTEM_PULLREQUEST_PULLREQUESTNUMBER")
		if num == "" {
			num = env.get("SYSTEM_PULLREQUEST_PULLREQUESTID")
		}
		n, _ := strconv.Atoi(num)
		c.PR = &PR{Number: n, TargetBranch: strings.TrimPrefix(env.get("SYSTEM_PULLREQUEST_TARGETBRANCH"), "refs/heads/")}
		if sha := env.get("SYSTEM_PULLREQUEST_SOURCECOMMITID"); sha != "" {
			c.HeadSHA = sha
			c.PR.HeadSHA = sha
		}
		if src := env.get("SYSTEM_PULLREQUEST_SOURCEBRANCH"); src != "" {
			c.Branch = strings.TrimPrefix(src, "refs/heads/")
		}
	case "Schedule":
		c.Trigger = TriggerSchedule
	case "Manual":
		c.Trigger = TriggerManual
	default:
		c.Trigger = TriggerPush
	}
	if strings.HasPrefix(source, "refs/tags/") && c.Trigger == TriggerPush {
		c.Trigger = TriggerRelease
		c.Tag = strings.TrimPrefix(source, "refs/tags/")
		c.Branch = ""
	} else if strings.HasPrefix(source, "refs/heads/") && c.Trigger != TriggerPR {
		c.Branch = strings.TrimPrefix(source, "refs/heads/")
	}
	return c
}

func jenkins(env Env) Context {
	c := Context{Provider: Jenkins, HeadSHA: env.get("GIT_COMMIT"), RunURL: env.get("BUILD_URL"), Trigger: TriggerPush}
	c.Branch = env.get("BRANCH_NAME")
	if c.Branch == "" {
		c.Branch = strings.TrimPrefix(env.get("GIT_BRANCH"), "origin/")
	}
	switch {
	case env.get("CHANGE_ID") != "":
		c.Trigger = TriggerPR
		n, _ := strconv.Atoi(env.get("CHANGE_ID"))
		c.PR = &PR{Number: n, URL: env.get("CHANGE_URL"), HeadSHA: c.HeadSHA, TargetBranch: env.get("CHANGE_TARGET")}
		if src := env.get("CHANGE_BRANCH"); src != "" {
			c.Branch = src
		}
	case env.get("TAG_NAME") != "":
		c.Trigger = TriggerRelease
		c.Tag = env.get("TAG_NAME")
		c.Branch = ""
	}
	return c
}

func circleci(env Env) Context {
	c := Context{
		Provider: CircleCI, Branch: env.get("CIRCLE_BRANCH"), HeadSHA: env.get("CIRCLE_SHA1"),
		RunURL: env.get("CIRCLE_BUILD_URL"), Trigger: TriggerPush,
	}
	switch {
	case env.get("CIRCLE_PULL_REQUEST") != "":
		c.Trigger = TriggerPR
		u := env.get("CIRCLE_PULL_REQUEST")
		n, _ := strconv.Atoi(path.Base(strings.TrimRight(u, "/")))
		c.PR = &PR{Number: n, URL: u, HeadSHA: c.HeadSHA}
	case env.get("CIRCLE_TAG") != "":
		c.Trigger = TriggerRelease
		c.Tag = env.get("CIRCLE_TAG")
		c.Branch = ""
	}
	return c
}
