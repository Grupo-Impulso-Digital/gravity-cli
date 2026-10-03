// Package cisetup renders the CI files gravity init writes and installs the repository token as a CI secret.
package cisetup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// CI providers init can wire.
const (
	GitHub    = "github"
	GitLab    = "gitlab"
	Bitbucket = "bitbucket"
	Azure     = "azure"
	Jenkins   = "jenkins"
	CircleCI  = "circleci"
	None      = "none"
)

// Providers lists every value --ci accepts.
var Providers = []string{GitHub, GitLab, Bitbucket, Azure, Jenkins, CircleCI, None}

// SecretName is the CI secret the templates read.
const SecretName = "GRAVITY_REPO_TOKEN"

// DefaultInstallURL is the app route that serves install.sh.
const DefaultInstallURL = "https://app.gravitydocs.io/install.sh"

// File actions.
const (
	ActionCreate = "create"
	ActionAppend = "append"
	ActionKeep   = "keep"
)

// Options shape the generated CI files.
type Options struct {
	DefaultBranch string
	Schedule      bool
	APIURL        string
	InstallURL    string
	WebURL        string
}

// File is one CI file init writes or leaves alone.
type File struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content"`
	Note    string `json:"note,omitempty"`
}

// Plan is what init does for one CI provider.
type Plan struct {
	Provider      string `json:"provider"`
	Label         string `json:"label"`
	Files         []File `json:"files"`
	Snippet       string `json:"snippet,omitempty"`
	SnippetTarget string `json:"snippetTarget,omitempty"`
	PasteHint     string `json:"pasteHint"`
	CommentToken  string `json:"commentToken,omitempty"`
	CommentHint   string `json:"commentHint,omitempty"`
}

// Valid reports whether p is a provider --ci accepts.
func Valid(p string) bool {
	for _, v := range Providers {
		if v == p {
			return true
		}
	}
	return false
}

// Label returns the human name of a provider.
func Label(p string) string {
	switch p {
	case GitHub:
		return "GitHub Actions"
	case GitLab:
		return "GitLab CI"
	case Bitbucket:
		return "Bitbucket Pipelines"
	case Azure:
		return "Azure Pipelines"
	case Jenkins:
		return "Jenkins"
	case CircleCI:
		return "CircleCI"
	}
	return "CI"
}

type data struct {
	Branch     string
	Schedule   bool
	APIURL     string
	InstallURL string
}

var (
	plainYAML   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._/-]*$`)
	plainShell  = regexp.MustCompile(`^[A-Za-z0-9._/:@%+,=-]+$`)
	yamlKeyword = regexp.MustCompile(`^(?i:y|n|yes|no|on|off|true|false|null)$`)
)

func yamlQuote(s string) string {
	if plainYAML.MatchString(s) && !yamlKeyword.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func shellQuote(s string) string {
	if plainShell.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func groovyQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(s) + "'"
}

var funcs = template.FuncMap{"yaml": yamlQuote, "sh": shellQuote, "groovy": groovyQuote}

func render(name, text string, d data) string {
	t := template.Must(template.New(name).Funcs(funcs).Parse(text))
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return ""
	}
	return buf.String()
}

// Build plans the CI files for provider in the repository at root.
func Build(root, provider string, o Options) (*Plan, error) {
	if !Valid(provider) {
		return nil, fmt.Errorf("unknown CI provider %q (one of %s)", provider, strings.Join(Providers, ", "))
	}
	d := data{Branch: o.DefaultBranch, Schedule: o.Schedule, APIURL: o.APIURL, InstallURL: o.InstallURL}
	if d.Branch == "" {
		d.Branch = "main"
	}
	if d.InstallURL == "" {
		d.InstallURL = DefaultInstallURL
	}
	p := &Plan{Provider: provider, Label: Label(provider), Files: []File{}, PasteHint: pasteHint(provider, o.WebURL)}
	p.CommentToken, p.CommentHint = commentToken(provider, o.WebURL)
	switch provider {
	case GitHub:
		f, err := planFile(root, ".github/workflows/gravity.yml", render("github", githubTemplate, d))
		if err != nil {
			return nil, err
		}
		if caller := SharedWorkflowCaller(root); caller != "" && f.Action != ActionKeep {
			f = File{Path: f.Path, Action: ActionKeep, Note: caller + " calls the shared gravity-docs.yml workflow, which runs gravity 1.x for a version 2 manifest with " + SecretName}
		}
		p.Files = append(p.Files, f)
	case GitLab:
		if err := planGitLab(root, p, d); err != nil {
			return nil, err
		}
	case Bitbucket:
		content := render("bitbucket", bitbucketTemplate, d)
		existing, err := readOptional(root, "bitbucket-pipelines.yml")
		if err != nil {
			return nil, err
		}
		if existing == nil {
			p.Files = append(p.Files, File{Path: "bitbucket-pipelines.yml", Action: ActionCreate, Content: content})
		} else {
			p.Snippet = render("bitbucket-snippet", bitbucketSnippet, d)
			p.SnippetTarget = "bitbucket-pipelines.yml (merge the gravity step into your pipelines)"
		}
	case Azure:
		f, err := planFile(root, "azure-pipelines.gravity.yml", render("azure", azureTemplate, d))
		if err != nil {
			return nil, err
		}
		p.Files = append(p.Files, f)
	case Jenkins:
		p.Snippet = render("jenkins", jenkinsSnippet, d)
		p.SnippetTarget = "Jenkinsfile (add this stage)"
	case CircleCI:
		p.Snippet = render("circleci", circleSnippet, d)
		p.SnippetTarget = ".circleci/config.yml (add this job and workflow)"
	case None:
		p.Snippet = render("generic", genericSnippet, d)
		p.SnippetTarget = "your CI (run this on pushes, pull requests and release tags)"
	}
	return p, nil
}

func readOptional(root, rel string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, nil
}

func planFile(root, rel, content string) (File, error) {
	existing, err := readOptional(root, rel)
	if err != nil {
		return File{}, err
	}
	switch {
	case existing == nil:
		return File{Path: rel, Action: ActionCreate, Content: content}, nil
	case string(existing) == content:
		return File{Path: rel, Action: ActionKeep, Content: content, Note: "already up to date"}, nil
	}
	return File{Path: rel, Action: ActionKeep, Content: string(existing), Note: "exists; left as is (delete it and run gravity init again to regenerate it)"}, nil
}

var topLevelInclude = regexp.MustCompile(`(?m)^include:`)

func planGitLab(root string, p *Plan, d data) error {
	f, err := planFile(root, ".gitlab/gravity.yml", render("gitlab", gitlabTemplate, d))
	if err != nil {
		return err
	}
	p.Files = append(p.Files, f)
	existing, err := readOptional(root, ".gitlab-ci.yml")
	if err != nil {
		return err
	}
	include := "include:\n  - local: .gitlab/gravity.yml\n"
	switch {
	case existing == nil:
		p.Files = append(p.Files, File{Path: ".gitlab-ci.yml", Action: ActionCreate, Content: include})
	case bytes.Contains(existing, []byte(".gitlab/gravity.yml")):
		p.Files = append(p.Files, File{Path: ".gitlab-ci.yml", Action: ActionKeep, Content: string(existing), Note: "already includes .gitlab/gravity.yml"})
	case topLevelInclude.Match(existing):
		p.Snippet = "  - local: .gitlab/gravity.yml\n"
		p.SnippetTarget = ".gitlab-ci.yml (add this entry to your include: list)"
	default:
		sep := "\n"
		if !bytes.HasSuffix(existing, []byte("\n")) {
			sep = "\n\n"
		}
		p.Files = append(p.Files, File{Path: ".gitlab-ci.yml", Action: ActionAppend, Content: sep + include})
	}
	return nil
}

// Write applies the plan's create and append actions under root and returns the paths it touched.
func Write(root string, p *Plan) ([]string, error) {
	var written []string
	for _, f := range p.Files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		switch f.Action {
		case ActionCreate:
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return written, fmt.Errorf("create %s: %w", filepath.Dir(f.Path), err)
			}
			if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
				return written, fmt.Errorf("write %s: %w", f.Path, err)
			}
		case ActionAppend:
			fh, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return written, fmt.Errorf("append to %s: %w", f.Path, err)
			}
			_, werr := fh.WriteString(f.Content)
			cerr := fh.Close()
			if werr != nil {
				return written, fmt.Errorf("append to %s: %w", f.Path, werr)
			}
			if cerr != nil {
				return written, fmt.Errorf("append to %s: %w", f.Path, cerr)
			}
		default:
			continue
		}
		written = append(written, f.Path)
	}
	return written, nil
}

func pasteHint(provider, webURL string) string {
	switch provider {
	case GitHub:
		if webURL != "" {
			return "add it as the repository secret " + SecretName + " at " + webURL + "/settings/secrets/actions/new"
		}
		return "add it as the repository secret " + SecretName + " (Settings > Secrets and variables > Actions)"
	case GitLab:
		if webURL != "" {
			return "add it as the masked CI/CD variable " + SecretName + " at " + webURL + "/-/settings/ci_cd (Variables)"
		}
		return "add it as the masked CI/CD variable " + SecretName + " (Settings > CI/CD > Variables)"
	case Bitbucket:
		return "add it as the secured repository variable " + SecretName + " (Repository settings > Pipelines > Repository variables)"
	case Azure:
		return "add it as the secret pipeline variable " + SecretName + " (Pipelines > Edit > Variables, keep this value secret)"
	case Jenkins:
		return "add it as a Secret text credential with ID gravity-repo-token (Manage Jenkins > Credentials)"
	case CircleCI:
		return "add it as the project environment variable " + SecretName + " (Project Settings > Environment Variables)"
	}
	return "store it in your CI's secret store as " + SecretName
}

const commentReport = "gravity-report.md"

func commentToken(provider, webURL string) (name, hint string) {
	switch provider {
	case GitLab:
		where := "Settings > Access tokens"
		vars := "Settings > CI/CD > Variables"
		if webURL != "" {
			where = webURL + "/-/settings/access_tokens"
			vars = webURL + "/-/settings/ci_cd"
		}
		return "GITLAB_TOKEN", "create a project access token with the api scope (" + where + ") and add it as the masked CI/CD variable GITLAB_TOKEN (" + vars + "); CI_JOB_TOKEN cannot post merge request notes, so without it the report is only kept as the job artifact " + commentReport
	case Bitbucket:
		return "BITBUCKET_ACCESS_TOKEN", "create a repository access token with Pull requests: Write (Repository settings > Security > Access tokens) and add it as the secured repository variable BITBUCKET_ACCESS_TOKEN; without it the report is only kept as the step artifact " + commentReport
	}
	return "", ""
}

const githubTemplate = `name: Gravity
on:
  push:
    branches: [{{yaml .Branch}}]
  pull_request:
  release:
    types: [published]
{{- if .Schedule}}
  schedule:
    - cron: '0 6 * * 1'
{{- end}}
  workflow_dispatch:
permissions:
  contents: read
  pull-requests: write
jobs:
  gravity:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: Grupo-Impulso-Digital/gravity-cli/ci/github@v1
        with:
          token: ${{"{{"}} secrets.GRAVITY_REPO_TOKEN || secrets.GRAVITY_TOKEN {{"}}"}}
{{- if .APIURL}}
          api-url: {{yaml .APIURL}}
{{- end}}
`

const gitlabTemplate = `gravity:
  image: alpine:3.20
  variables:
    GIT_DEPTH: '0'
{{- if .APIURL}}
    GRAVITY_API_URL: {{yaml .APIURL}}
{{- end}}
  before_script:
    - apk add --no-cache git curl
    - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
  script:
    - gravity run
  artifacts:
    when: always
    paths:
      - gravity-report.md
    reports:
      codequality: gl-code-quality-report.json
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
    - if: $CI_COMMIT_TAG
    - if: $CI_PIPELINE_SOURCE == "schedule"
`

const bitbucketTemplate = `image: alpine:3.20
clone:
  depth: full
definitions:
  steps:
    - step: &gravity
        name: Gravity
        script:
          - apk add --no-cache git curl
          - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
          - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
          - gravity run
        artifacts:
          - gravity-report.md
pipelines:
  branches:
    {{yaml .Branch}}:
      - step: *gravity
  pull-requests:
    '**':
      - step: *gravity
  tags:
    'v*':
      - step: *gravity
  custom:
    gravity-schedule:
      - step:
          name: Gravity (schedule)
          script:
            - apk add --no-cache git curl
            - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
            - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
            - GRAVITY_TRIGGER=schedule gravity run
    gravity-manual:
      - step:
          name: Gravity (manual)
          script:
            - apk add --no-cache git curl
            - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
            - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
            - GRAVITY_TRIGGER=manual gravity run
`

const bitbucketSnippet = `clone:
  depth: full
pipelines:
  branches:
    {{yaml .Branch}}:
      - step:
          name: Gravity
          image: alpine:3.20
          script:
            - apk add --no-cache git curl
            - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
            - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
            - gravity run
  pull-requests:
    '**':
      - step:
          name: Gravity
          image: alpine:3.20
          script:
            - apk add --no-cache git curl
            - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
            - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
            - gravity run
          artifacts:
            - gravity-report.md
  tags:
    'v*':
      - step:
          name: Gravity
          image: alpine:3.20
          script:
            - apk add --no-cache git curl
            - curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
{{- if .APIURL}}
            - {{yaml (print "export GRAVITY_API_URL=" (sh .APIURL))}}
{{- end}}
            - gravity run
`

const azureTemplate = `trigger:
  branches:
    include: [{{yaml .Branch}}]
  tags:
    include: ['v*']
pr:
  branches:
    include: ['*']
{{- if .Schedule}}
schedules:
  - cron: '0 6 * * 1'
    displayName: Gravity weekly
    branches:
      include: [{{yaml .Branch}}]
    always: true
{{- end}}
pool:
  vmImage: ubuntu-latest
steps:
  - checkout: self
    fetchDepth: 0
  - script: |
      curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 GRAVITY_INSTALL_DIR="$HOME/.local/bin" sh
      "$HOME/.local/bin/gravity" run
    displayName: Gravity
    env:
      GRAVITY_REPO_TOKEN: $(GRAVITY_REPO_TOKEN)
      GRAVITY_TOKEN: $(GRAVITY_TOKEN)
      SYSTEM_ACCESSTOKEN: $(System.AccessToken)
{{- if .APIURL}}
      GRAVITY_API_URL: {{yaml .APIURL}}
{{- end}}
`

const jenkinsSnippet = `stage('Gravity') {
  environment {
    GRAVITY_REPO_TOKEN = credentials('gravity-repo-token')
{{- if .APIURL}}
    GRAVITY_API_URL = {{groovy .APIURL}}
{{- end}}
  }
  steps {
    sh 'curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 GRAVITY_INSTALL_DIR="$WORKSPACE/.gravity-bin" sh'
    sh '"$WORKSPACE/.gravity-bin/gravity" run'
  }
}
`

const circleSnippet = `jobs:
  gravity:
    docker:
      - image: cimg/base:stable
    steps:
      - checkout
      - run: curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 GRAVITY_INSTALL_DIR="$HOME/.local/bin" sh
      - run:
          name: Gravity
          command: $HOME/.local/bin/gravity run
          environment:
            CIRCLE_PIPELINE_TRIGGER_SOURCE: << pipeline.trigger_source >>
{{- if .APIURL}}
            GRAVITY_API_URL: {{yaml .APIURL}}
{{- end}}
workflows:
  gravity:
    jobs: [gravity]
`

const genericSnippet = `curl -fsSL {{.InstallURL}} | GRAVITY_VERSION=1 sh
GRAVITY_REPO_TOKEN=<secret>{{if .APIURL}} GRAVITY_API_URL={{sh .APIURL}}{{end}} gravity run
`
