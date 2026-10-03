package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ci"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/report"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

const reportFile = "gravity-report.md"

type pipelineFlags struct {
	passes       []string
	trigger      string
	branch       string
	from         string
	to           string
	note         string
	dryRun       bool
	leaseTimeout time.Duration
	parallel     int
	noComment    bool
	comment      bool
	strict       bool
	failOn       []string
	annotate     string
}

type pipelineMode int

const (
	modeRun pipelineMode = iota
	modePreview
	modeCheck
)

type uiLogger struct{ p *ui.Printer }

func (l uiLogger) Infof(format string, args ...any) { l.p.Println(format, args...) }

func (l uiLogger) Warn(code, message string) { l.p.Warn(code, message) }

func (l uiLogger) Debugf(format string, args ...any) { l.p.Debugf(format, args...) }

type pipelineSession struct {
	info     *repoInfo
	manifest *config.Manifest
	client   *api.Client
	who      *api.WhoAmI
	ci       ci.Context
	opts     engine.Options
}

var errForkPR = errors.New("fork pull request without a token")

func (a *app) pipelineSession(ctx context.Context, mode pipelineMode, f pipelineFlags, committed bool) (*pipelineSession, error) {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return nil, err
	}
	m, err := a.loadManifest(info.root)
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) && errors.Is(ee.Err, config.ErrV1Manifest) {
			ee.ErrCode = "manifest_v1"
			ee.Err = fmt.Errorf("%w; run `gravity init` to convert it to version 2", ee.Err)
		}
		return nil, err
	}
	c, err := a.detectCI(ctx, info.repo)
	if err != nil {
		return nil, err
	}
	s := &pipelineSession{info: info, manifest: m, ci: c}
	opts, err := a.pipelineOptions(ctx, s, mode, f, committed)
	if err != nil {
		return nil, err
	}
	s.opts = opts
	apiURL := ""
	if m != nil {
		apiURL = m.APIURL
	}
	fork := opts.Trigger == config.TriggerPR && c.IsCI() && c.Fork
	creds, err := a.credentials(apiURL)
	if err != nil {
		var ee *ExitError
		if fork && errors.As(err, &ee) && ee.ErrCode == "token_unresolved" {
			return s, errForkPR
		}
		return nil, err
	}
	if fork && creds.Token == "" {
		return s, errForkPR
	}
	if err := a.requireToken(creds); err != nil {
		return nil, err
	}
	s.client = a.client(creds)
	who, err := s.client.WhoAmI(ctx)
	if err != nil {
		return nil, explainAPI(err)
	}
	if err := requirePipelines(who.Features); err != nil {
		return nil, err
	}
	s.who = who
	s.opts.RepoParam = repoParam(who, info)
	return s, nil
}

func (a *app) pipelineOptions(ctx context.Context, s *pipelineSession, mode pipelineMode, f pipelineFlags, committed bool) (engine.Options, error) {
	c := s.ci
	opts := engine.Options{Passes: f.passes, From: f.from, To: f.to, Note: f.note, LeaseTimeout: f.leaseTimeout, Parallel: f.parallel, FailOn: f.failOn, Origin: origin(c)}
	trigger := f.trigger
	switch mode {
	case modePreview:
		trigger = ci.TriggerManual
		opts.Preview = true
		opts.Mode = api.ModeDry
		opts.WorkingTree = !committed
		opts.ConnectContext = api.ContextPreview
	case modeCheck:
		trigger = ci.TriggerPR
		opts.ImplicitCheck = true
	default:
		if trigger == "" {
			trigger = c.Trigger
			if trigger == "" || !c.IsCI() {
				trigger = ci.TriggerManual
			}
		}
	}
	if !ci.ValidTrigger(trigger) {
		return opts, Failf(CodeError, "--trigger %q must be one of pr, push, release, schedule, manual", trigger)
	}
	if f.parallel > 4 {
		return opts, Failf(CodeError, "--parallel is at most 4")
	}
	opts.Trigger = trigger
	if f.dryRun {
		opts.Mode = api.ModeDry
	}
	branch := firstNonEmpty(f.branch, c.Branch, s.info.branch)
	if trigger == ci.TriggerSchedule && f.branch == "" && c.Branch == "" {
		branch = s.info.defaultBranch
	}
	opts.Branch = branch
	opts.Head = c.HeadSHA
	opts.Tag = c.Tag
	if c.IsCI() {
		opts.CI = &api.CIInfo{Provider: c.Provider, RunURL: c.RunURL, Event: c.Event}
	}
	switch trigger {
	case ci.TriggerPR:
		pr := &api.PRInfo{TargetBranch: firstNonEmpty(s.info.defaultBranch, "main")}
		if c.PR != nil {
			pr.Number, pr.URL = c.PR.Number, c.PR.URL
			if c.PR.TargetBranch != "" {
				pr.TargetBranch = c.PR.TargetBranch
			}
			if c.PR.HeadSHA != "" {
				opts.Head = c.PR.HeadSHA
			}
		}
		if f.from != "" {
			opts.PRBase = f.from
		} else {
			opts.PRBase = c.BaseSHA
		}
		if f.to != "" {
			opts.Head = f.to
		}
		opts.PR = pr
		opts.Branch = firstNonEmpty(c.Branch, s.info.branch)
	case ci.TriggerRelease:
		if opts.Tag == "" {
			return opts, Failf(CodeError, "a release run needs its tag: run on a tag in CI or set GRAVITY_TAG")
		}
	default:
		if branch == "" {
			return opts, &ExitError{Code: CodeError, ErrCode: "branch_unknown", Err: errors.New("HEAD is detached and is not on the default branch; pass --branch (or set GRAVITY_BRANCH)")}
		}
	}
	if opts.WorkingTree {
		opts.Head = ""
	}
	if opts.ConnectContext == "" {
		opts.ConnectContext = trigger
	}
	_ = ctx
	return opts, nil
}

func (a *app) runEnv(s *pipelineSession) *engine.Env {
	branch := firstNonEmpty(s.opts.Branch, s.info.defaultBranch)
	return &engine.Env{
		Client: s.client, Repo: s.info.repo, Manifest: s.manifest,
		Info:      passes.RepoInfo{RemoteKey: s.info.remoteKey, Name: s.info.name, WebURL: s.info.webURL, Provider: s.info.provider, Branch: branch, ID: principalRepoID(s.who)},
		Connect:   a.connectRequest(s.info, s.manifest, s.opts.ConnectContext, s.opts.Origin, s.opts.Mode == api.ModeDry),
		Log:       uiLogger{p: a.ui},
		Sleep:     a.sleep,
		Generator: "gravity-cli/" + version.String(),
	}
}

func (a *app) execute(ctx context.Context, s *pipelineSession) (*engine.Result, error) {
	env := a.runEnv(s)
	if !a.ui.Interactive() {
		return engine.Execute(ctx, env, s.opts)
	}
	prog := a.ui.StartProgress("", true)
	defer prog.Stop()
	steps := map[string]int{}
	env.OnPlan = func(p *api.Plan) {
		for _, pp := range p.Passes {
			if _, ok := steps[pp.Name]; ok || !pp.Applies {
				continue
			}
			label := pp.Name + "  " + pp.Kind
			if pp.Target.Ref != "" {
				label += " → " + pp.Target.Ref
			}
			i := prog.Add(label)
			prog.Begin(i)
			steps[pp.Name] = i
		}
	}
	env.OnPass = func(r engine.PassResult) {
		i, ok := steps[r.Name]
		if !ok {
			return
		}
		switch passMark(r) {
		case ui.MarkFail:
			prog.Fail(i, passOutcome(r))
		case ui.MarkWarn:
			prog.Warn(i, passOutcome(r))
		case ui.MarkSkip:
			prog.Skip(i, passOutcome(r))
		default:
			prog.Done(i, passOutcome(r))
		}
	}
	return engine.Execute(ctx, env, s.opts)
}

func principalRepoID(who *api.WhoAmI) string {
	if who != nil && who.Principal != nil && who.Principal.Repo != nil {
		return who.Principal.Repo.ID
	}
	return ""
}

func (a *app) forkPR(s *pipelineSession) error {
	msg := "This pull request comes from a fork, and CI does not share secrets with forks, so there is no Gravity token; skipping the doc check"
	a.ui.Warn("fork_pr_no_token", msg)
	if path := a.env("GITHUB_STEP_SUMMARY"); path != "" && s.ci.Provider == ci.GitHub {
		if err := report.AppendFile(path, "### Gravity\n\n"+msg+"."); err != nil {
			a.ui.Debugf("step summary: %v", err)
		}
	}
	return a.ui.Result(map[string]any{"skipped": "fork_pr_no_token"})
}

func validAnnotate(v string) error {
	switch v {
	case "auto", ci.GitHub, ci.GitLab, ci.Azure, "none":
		return nil
	}
	return Failf(CodeError, "--annotate %q must be auto, github, gitlab, azure or none", v)
}

func runError(err error) error {
	var se *engine.SelectionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se) && se.Reason == engine.SelectionUnknown:
		return &ExitError{Code: CodeError, ErrCode: "pass_unknown", Err: err}
	case errors.As(err, &se):
		return &ExitError{Code: CodeError, ErrCode: "pass_not_applicable", Err: err}
	case errors.Is(err, engine.ErrLeaseTimeout):
		return &ExitError{Code: CodeError, ErrCode: api.CodeLeaseHeld, Err: err}
	case errors.Is(err, engine.ErrStopped) && errors.Is(err, api.ErrLeaseLost):
		return &ExitError{Code: CodeError, ErrCode: api.CodeLeaseLost, Err: err}
	case errors.Is(err, engine.ErrStopped):
		return &ExitError{Code: CodeError, ErrCode: api.CodeRunNotRunning, Err: err}
	}
	return explainAPI(err)
}

func passMark(p engine.PassResult) string {
	switch {
	case p.Status == api.StatusFailed:
		return ui.MarkFail
	case p.Report != nil && p.Report.Failing:
		return ui.MarkFail
	case p.SkipReason == api.SkipTargetMissing:
		return ui.MarkFail
	case p.SkipReason == api.SkipTargetUnapproved || p.SkipReason == api.SkipScopeMissing:
		return ui.MarkWarn
	case p.Status == api.StatusSkipped:
		return ui.MarkSkip
	}
	return ui.MarkOK
}

func passOutcome(p engine.PassResult) string {
	switch {
	case p.Status == api.StatusFailed:
		return "failed: " + p.Error
	case p.Status == api.StatusSkipped:
		reason := "skipped: " + skipLabel(p.SkipReason)
		if p.ApproveURL != "" && p.SkipReason == api.SkipTargetUnapproved {
			reason += " (" + p.ApproveURL + ")"
		}
		if len(p.Missing) > 0 {
			reason += " (" + strings.Join(p.Missing, ", ") + ")"
		}
		return reason
	case p.Report == nil:
		return ""
	}
	if line := p.Report.Counts.Line(); line != "" && p.Kind != "check" {
		return line
	}
	return p.Report.Summary
}

func (a *app) printRun(res *engine.Result, info *repoInfo) {
	p := a.ui
	where := res.Trigger
	if res.Branch != "" {
		where += " " + res.Branch
	}
	if res.Range != nil {
		head := shortSHA(res.Range.Head)
		if head == "" {
			head = "working tree"
		}
		where += fmt.Sprintf(" (%s..%s, %d commits)", firstNonEmpty(shortSHA(res.Range.Base), "root"), head, res.Commits)
	}
	product := "-"
	if res.Plan != nil {
		product = firstNonEmpty(res.Plan.Product.Name, res.Plan.Product.Slug, "-")
	}
	mode := ""
	if res.Mode == api.ModeDry {
		mode = " · dry run"
	}
	p.Println("%s %s → %s · %s%s", p.Bold("Gravity ·"), info.name, product, where, mode)
	rows := make([][]string, 0, len(res.Passes))
	for _, ps := range res.Passes {
		cost := ""
		if ps.RunPassID != "" && (ps.Status == api.StatusSucceeded || ps.Status == api.StatusFailed) {
			cost = fmt.Sprintf("$%.2f", ps.CostUSD)
		}
		rows = append(rows, []string{p.Mark(passMark(ps)), ps.Name, ps.Kind, ps.Target, passOutcome(ps), cost})
	}
	p.Table("", rows)
	for _, ps := range res.Passes {
		if ps.Report == nil {
			continue
		}
		for _, w := range ps.Report.Warnings {
			p.Println("  %s %s: %s", p.Mark(ui.MarkWarn), ps.Name, w)
		}
		for _, f := range ps.Report.Findings {
			loc := ""
			if f.File != "" {
				loc = " (" + f.File
				if f.Line > 0 {
					loc += fmt.Sprintf(":%d", f.Line)
				}
				loc += ")"
			}
			p.Println("  %s %s: %s%s", p.Mark(findingMark(f)), ps.Name, f.Title, loc)
		}
		for _, n := range ps.Report.Notes {
			p.Println("  %s %s: %s", p.Mark(ui.MarkInfo), ps.Name, n.Title)
		}
		for _, c := range ps.Report.Competing {
			row := competingRow(ps.Name, c.Page, c.With, c.Pending)
			who := firstNonEmpty(strings.Join(row.Repos, ", "), strings.Join(row.Runs, ", "))
			if c.Pending {
				p.Println("  %s %s: %s already has an open change from %s; the review shows both", p.Mark(ui.MarkWarn), ps.Name, row.Page, who)
				continue
			}
			p.Println("  %s %s: %s also changed by %s; the review shows both versions", p.Mark(ui.MarkWarn), ps.Name, row.Page, who)
		}
	}
	for _, h := range res.Handoffs {
		when := "detected"
		if h.Status == engine.HandoffExpected {
			when = "once merged"
		}
		p.Println("  %s handoff: %s %s moves from %s to %s (%s)", p.Mark(ui.MarkInfo), h.UnitKey, h.Role, h.From, h.To, when)
	}
	a.manualHint(res)
	if p.Interactive() {
		a.runCard(res)
		return
	}
	if res.Mode == api.ModeWrite && res.Finish != nil && res.Finish.Bundle.Changes > 0 {
		p.Println("Bundle: %d changes awaiting review → %s", res.Finish.Bundle.Changes, firstNonEmpty(res.Finish.Bundle.AppURL, res.Finish.Run.AppURL))
	} else if res.Run != nil && res.Run.AppURL != "" {
		p.Println("Run: %s", res.Run.AppURL)
	}
}

func (a *app) manualHint(res *engine.Result) {
	if res.Trigger != ci.TriggerManual || res.Mode != api.ModeWrite || res.Plan == nil {
		return
	}
	var ran, other []string
	for _, pp := range res.Plan.Passes {
		switch {
		case pp.Applies:
			ran = append(ran, pp.Name)
		case pp.SkipReason == api.SkipTriggerMismatch:
			other = append(other, pp.Name+" ("+firstNonEmpty(strings.Join(pp.Triggers, ", "), "no triggers")+")")
		}
	}
	if len(other) == 0 {
		return
	}
	p := a.ui
	if len(ran) == 0 {
		p.Println("%s Nothing ran: no pass runs on a manual run here. Not on manual runs: %s. Run one by name with `gravity run --pass <name>`.", p.Mark(ui.MarkInfo), strings.Join(other, ", "))
		return
	}
	p.Println("%s Manual run: %s ran. Not on manual runs: %s; run one by name with `gravity run --pass <name>`.", p.Mark(ui.MarkInfo), strings.Join(ran, ", "), strings.Join(other, ", "))
}

func (a *app) runCard(res *engine.Result) {
	ran, skipped, failed := 0, 0, 0
	var cost float64
	for _, ps := range res.Passes {
		cost += ps.CostUSD
		switch ps.Status {
		case api.StatusFailed:
			failed++
		case api.StatusSkipped:
			skipped++
		default:
			ran++
		}
	}
	title := "Gravity run finished"
	if res.Mode == api.ModeDry {
		title = "Gravity dry run finished"
	}
	lines := []string{fmt.Sprintf("%s, %d skipped, %d failed · $%.2f", plural(ran, "pass ran", "passes ran"), skipped, failed, cost)}
	var links []ui.Link
	if res.Mode == api.ModeWrite && res.Finish != nil && res.Finish.Bundle.Changes > 0 {
		lines = append(lines, fmt.Sprintf("%d changes awaiting review", res.Finish.Bundle.Changes))
		links = append(links, ui.Link{Label: "Review bundle", URL: firstNonEmpty(res.Finish.Bundle.AppURL, res.Finish.Run.AppURL)})
	}
	if res.Run != nil && res.Run.AppURL != "" {
		links = append(links, ui.Link{Label: "Run", URL: res.Run.AppURL})
	}
	for _, ps := range res.Passes {
		if ps.ApproveURL != "" && ps.SkipReason == api.SkipTargetUnapproved {
			links = append(links, ui.Link{Label: "Approve " + ps.Name, URL: ps.ApproveURL})
		}
	}
	a.ui.Card(title, lines, links)
}

func findingMark(f api.Finding) string {
	switch f.Severity {
	case api.SeverityError:
		return ui.MarkFail
	case api.SeverityWarning:
		return ui.MarkWarn
	}
	return ui.MarkInfo
}

func (a *app) finishPipeline(res *engine.Result, strict bool, extra error) error {
	if extra != nil {
		return extra
	}
	code := res.ExitCode(strict)
	if code == CodeOK {
		return a.ui.Result(res)
	}
	errCode := "pass_failed"
	msg := "one or more passes failed"
	switch code {
	case CodeFindings:
		errCode, msg = "findings", findingsMessage(res)
	case CodeLicense:
		errCode, msg = api.CodeModuleDisabled, "a pass needs a module this organization does not have"
	default:
		for _, p := range res.Passes {
			if p.SkipReason == api.SkipTargetMissing {
				msg = fmt.Sprintf("pass %s targets %s, which does not exist (fix it in the app or %s)", p.Name, p.Target, config.ManifestFileName)
			}
		}
	}
	ee := &ExitError{Code: code, ErrCode: errCode, Err: errors.New(msg)}
	if err := a.ui.Failure(ui.ErrorInfo{Code: errCode, Message: msg, ExitCode: code}, res); err != nil {
		return err
	}
	return ee
}

func findingsMessage(res *engine.Result) string {
	n := 0
	for _, p := range res.Passes {
		if p.Report == nil || !p.Report.Failing {
			continue
		}
		want := map[string]bool{}
		for _, c := range p.Report.FailOn {
			want[c] = true
		}
		for _, f := range p.Report.Findings {
			if f.Severity == api.SeverityError && want[passes.Category(f.Code)] {
				n++
			}
		}
	}
	if n == 1 {
		return "1 finding fails the check"
	}
	return fmt.Sprintf("%d findings fail the check", n)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
