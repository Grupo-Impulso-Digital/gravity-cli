package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ci"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

const codeQualityFile = "gl-code-quality-report.json"

func reportDoc(res *engine.Result, info *repoInfo, prNumber int) report.Doc {
	d := report.Doc{Repo: info.remoteKey, PR: prNumber}
	if res.Run != nil {
		d.RunURL = res.Run.AppURL
	}
	if res.Trigger != config.TriggerPR {
		where := res.Trigger
		if res.Branch != "" {
			where += " " + res.Branch
		}
		d.Heading = "Gravity · " + where
		d.Applied = res.Mode == api.ModeWrite
		if res.Mode == api.ModeDry {
			d.Heading += " (dry run)"
		}
		if res.Finish != nil && res.Finish.Bundle.Changes > 0 {
			d.BundleURL = firstNonEmpty(res.Finish.Bundle.AppURL, res.Finish.Run.AppURL)
		}
	}
	if res.Range != nil && res.Range.Kind == api.RangeSurvey {
		d.Survey = true
	}
	for _, p := range res.Passes {
		rp := report.Pass{Name: p.Name, Kind: p.Kind, Target: p.Target, Status: p.Status, SkipReason: p.SkipReason, Error: p.Error}
		if p.Report != nil {
			rp.Summary = p.Report.Summary
			rp.Impact = p.Report.Impact
			rp.Findings = p.Report.Findings
			rp.Notes = p.Report.Notes
			rp.Claims = p.Report.Claims
			for _, c := range p.Report.Competing {
				d.Competing = append(d.Competing, competingRow(p.Name, c.Page, c.With, c.Pending))
			}
		}
		d.Passes = append(d.Passes, rp)
	}
	for _, h := range res.Handoffs {
		d.Handoffs = append(d.Handoffs, report.Handoff{UnitKey: h.UnitKey, Role: h.Role, From: h.From, To: h.To, Status: h.Status})
	}
	return d
}

func competingRow(pass string, page api.PageRef, with []api.CompetingChange, pending bool) report.Competing {
	c := report.Competing{Pass: pass, Page: firstNonEmpty(page.Slug, page.Title, page.ID), Pending: pending}
	seenRepo, seenRun, seenKey := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, w := range with {
		if w.Repo != "" && !seenRepo[w.Repo] {
			seenRepo[w.Repo] = true
			c.Repos = append(c.Repos, w.Repo)
		}
		if w.RunID != "" && !seenRun[w.RunID] {
			seenRun[w.RunID] = true
			c.Runs = append(c.Runs, w.RunID)
		}
		for _, k := range w.BlockKeys {
			if !seenKey[k] {
				seenKey[k] = true
				c.Keys = append(c.Keys, k)
			}
		}
	}
	return c
}

func annotationTarget(annotate string, c ci.Context) string {
	if annotate == "auto" {
		return c.Provider
	}
	return annotate
}

func (a *app) logOut() io.Writer {
	if a.ui.JSON() {
		return a.stderr
	}
	return a.stdout
}

func (a *app) publishReport(ctx context.Context, s *pipelineSession, res *engine.Result, comment bool, annotate string) {
	pr := res.Trigger == config.TriggerPR
	prNumber := 0
	if s.opts.PR != nil {
		prNumber = s.opts.PR.Number
	}
	doc := reportDoc(res, s.info, prNumber)
	body := report.Markdown(doc)
	findings := annotatedFindings(res)
	switch annotationTarget(annotate, s.ci) {
	case ci.GitHub:
		for _, line := range report.GitHubAnnotations(findings) {
			fmt.Fprintln(a.logOut(), line)
		}
	case ci.Azure:
		for _, line := range report.AzureAnnotations(findings) {
			fmt.Fprintln(a.logOut(), line)
		}
	case ci.GitLab:
		if s.ci.IsCI() || annotate == ci.GitLab {
			a.writeCodeQuality(s, findings)
		}
	}
	summaryWritten := a.writeStepSummary(s, body)
	if s.ci.Provider == ci.GitHub {
		a.githubOutput("run-url", firstNonEmpty(doc.BundleURL, doc.RunURL))
	}
	posted := false
	if pr && comment && prNumber > 0 {
		posted = a.postComment(ctx, s, prNumber, body, doc.HasImpact())
	}
	if pr && !posted && !summaryWritten && s.ci.IsCI() {
		if err := os.WriteFile(filepath.Join(s.info.root, reportFile), []byte(body), 0o644); err != nil {
			a.ui.Warn("report_file", err.Error())
		} else {
			a.ui.Println("Report written to %s", reportFile)
		}
	}
}

func annotatedFindings(res *engine.Result) []api.Finding {
	off := map[string]bool{}
	if res.Plan != nil {
		for _, pp := range res.Plan.Passes {
			if v, ok := pp.Options["annotate"].(bool); ok && !v {
				off[pp.Name] = true
			}
		}
	}
	var out []api.Finding
	for _, p := range res.Passes {
		if p.Report != nil && (!off[p.Name] || p.Implicit) {
			out = append(out, p.Report.Findings...)
		}
	}
	return out
}

func (a *app) writeCodeQuality(s *pipelineSession, findings []api.Finding) {
	data, err := report.GitLabCodeQuality(findings, config.ManifestFileName)
	if err != nil {
		a.ui.Warn("code_quality", err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(s.info.root, codeQualityFile), data, 0o644); err != nil {
		a.ui.Warn("code_quality", err.Error())
		return
	}
	a.ui.Debugf("code quality report written to %s", codeQualityFile)
}

func (a *app) writeStepSummary(s *pipelineSession, body string) bool {
	switch s.ci.Provider {
	case ci.GitHub:
		path := a.env("GITHUB_STEP_SUMMARY")
		if path == "" {
			return false
		}
		if err := report.AppendFile(path, body); err != nil {
			a.ui.Warn("step_summary", err.Error())
			return false
		}
		return true
	case ci.Azure:
		path := filepath.Join(s.info.root, reportFile)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			a.ui.Warn("step_summary", err.Error())
			return false
		}
		fmt.Fprintln(a.logOut(), report.AzureSummary(path))
		return true
	}
	return false
}

func (a *app) githubOutput(key, value string) {
	path := a.env("GITHUB_OUTPUT")
	if path == "" || value == "" || strings.ContainsAny(value, "\r\n") {
		return
	}
	if err := report.AppendFile(path, key+"="+value); err != nil {
		a.ui.Debugf("github output: %v", err)
	}
}

type commentTarget struct {
	commenter report.Commenter
	missing   string
}

func (a *app) commentTarget(s *pipelineSession, pr int) commentTarget {
	prURL := ""
	if s.ci.PR != nil {
		prURL = s.ci.PR.URL
	}
	switch s.ci.Provider {
	case ci.GitHub:
		token, repo := a.env("GITHUB_TOKEN"), a.env("GITHUB_REPOSITORY")
		if token == "" || repo == "" {
			return commentTarget{missing: "GITHUB_TOKEN is not set (pull-requests: write)"}
		}
		return commentTarget{commenter: report.GitHub{API: a.env("GITHUB_API_URL"), Token: token, Repo: repo}}
	case ci.GitLab:
		token, project := a.env("GITLAB_TOKEN"), firstNonEmpty(a.env("CI_PROJECT_ID"), a.env("CI_PROJECT_PATH"))
		if token == "" || project == "" {
			return commentTarget{missing: "GITLAB_TOKEN is not set (a project access token with the api scope; CI_JOB_TOKEN cannot post notes)"}
		}
		return commentTarget{commenter: report.GitLab{API: a.env("CI_API_V4_URL"), Token: token, Project: project, MRURL: prURL}}
	case ci.Bitbucket:
		token, repo := a.env("BITBUCKET_ACCESS_TOKEN"), a.env("BITBUCKET_REPO_FULL_NAME")
		if token == "" || repo == "" {
			return commentTarget{missing: "BITBUCKET_ACCESS_TOKEN is not set (a repository access token with pull request write)"}
		}
		return commentTarget{commenter: report.Bitbucket{Token: token, Repo: repo}}
	case ci.Azure:
		token := a.env("SYSTEM_ACCESSTOKEN")
		coll, project, repoID := a.env("SYSTEM_COLLECTIONURI"), firstNonEmpty(a.env("SYSTEM_TEAMPROJECTID"), a.env("SYSTEM_TEAMPROJECT")), a.env("BUILD_REPOSITORY_ID")
		if token == "" || coll == "" || project == "" || repoID == "" {
			return commentTarget{missing: "SYSTEM_ACCESSTOKEN is not mapped into the step (env: SYSTEM_ACCESSTOKEN: $(System.AccessToken))"}
		}
		if prURL == "" && a.env("BUILD_REPOSITORY_URI") != "" {
			prURL = a.env("BUILD_REPOSITORY_URI") + "/pullrequest/" + strconv.Itoa(pr)
		}
		return commentTarget{commenter: report.Azure{Collection: coll, Project: project, RepoID: repoID, Token: token, PRURL: prURL}}
	}
	return commentTarget{}
}

func (a *app) hasCommentToken(s *pipelineSession) bool {
	return a.commentTarget(s, 0).commenter != nil
}

func (a *app) postComment(ctx context.Context, s *pipelineSession, pr int, body string, impact bool) bool {
	t := a.commentTarget(s, pr)
	if t.commenter == nil {
		if t.missing != "" {
			a.ui.Warn("pr_comment", t.missing+"; the report is in "+reportFile)
		}
		return false
	}
	url, err := t.commenter.Upsert(ctx, pr, report.Marker(s.info.remoteKey), body, impact)
	switch {
	case err != nil:
		a.ui.Warn("pr_comment", "could not post the pull request comment: "+err.Error())
		return false
	case url != "":
		a.ui.Println("Pull request comment: %s", url)
	}
	return true
}
