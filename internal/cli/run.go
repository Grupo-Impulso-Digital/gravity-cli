package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type runFlags struct {
	pipelineFlags
	yes        bool
	send       string
	allowDirty bool
}

type changePage struct {
	Pass           string   `json:"pass"`
	Op             string   `json:"op"`
	Slug           string   `json:"slug,omitempty"`
	Title          string   `json:"title,omitempty"`
	CollectionPath []string `json:"collectionPath,omitempty"`
	Language       string   `json:"language,omitempty"`
	Source         string   `json:"source,omitempty"`
	Note           string   `json:"note,omitempty"`
}

type runData struct {
	*engine.Result
	Recording string            `json:"recording,omitempty"`
	SentFrom  string            `json:"sentFrom,omitempty"`
	ReviewURL string            `json:"reviewUrl,omitempty"`
	Pages     []changePage      `json:"pages"`
	CostUSD   float64           `json:"costUsd"`
	Issues    []issue           `json:"issues"`
	Plan      *engine.PlanView  `json:"plan,omitempty"`
	Preflight *preflightOutcome `json:"preflight,omitempty"`
}

func newRunCmd(a *app) *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the passes: --dry-run computes everything and writes nothing; a real run opens a change request",
		Long: "A run plans the passes, resolves each pass's commit range, skips passes whose scope did not change and runs the rest inside one Gravity run.\n\n" +
			"DRY RUN (--dry-run): the whole run, model calls and conversion included, but nothing is written to Gravity. The result is shown here and recorded in .gravity/runs/<runId>.json; `gravity run --send <runId>` (or answering yes at the end) sends exactly that result without recomputing.\n" +
			"REAL RUN: from any branch, with the local .gravity.yaml, the changes land in one change request reviewed in the app, like a CI run.\n\n" +
			"Before starting, outside CI: git fetch, and a refusal when the branch is behind or diverged from its upstream or tracked files are uncommitted (--allow-dirty, dry runs only); then the manifest is validated (a dry run reports the same conflicts the real run would hit) and the plan is shown with its estimate. On a terminal you confirm before passes that call a model, unless --yes.\n" +
			"A run reads committed history only. While another run holds the branch, gravity says which one and waits (--lease-timeout); Ctrl-C finishes the run as canceled.\n\n" +
			"In CI the trigger comes from the event; elsewhere it is manual: passes triggered on manual, push or schedule run, and `--pass <name>` runs a pass whatever its triggers.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validAnnotate(f.annotate); err != nil {
				return err
			}
			if f.send != "" {
				return a.sendRun(cmd.Context(), f)
			}
			if f.allowDirty && !f.dryRun {
				return Failf(CodeError, "--allow-dirty works only with --dry-run: a real run cites commits, so commit your changes first")
			}
			return a.runPipeline(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.dryRun, "dry-run", false, "run everything but write nothing; the result is recorded and can be sent later")
	fl.StringSliceVar(&f.passes, "pass", nil, "run only these passes (on a manual run, whatever their triggers)")
	fl.BoolVarP(&f.yes, "yes", "y", false, "do not ask before passes that call a model, or before sending")
	fl.StringVar(&f.send, "send", "", "send a recorded dry run (its run id, or latest) as a real run without recomputing")
	fl.BoolVar(&f.allowDirty, "allow-dirty", false, "dry runs only: run although tracked files have uncommitted changes")
	fl.StringVar(&f.note, "note", "", "a note for reviewers and the AI")
	fl.DurationVar(&f.leaseTimeout, "lease-timeout", 20*time.Minute, "how long to wait while another run holds this branch")
	fl.StringVar(&f.trigger, "trigger", "", "override the detected trigger (pr, push, release, schedule, manual)")
	fl.StringVar(&f.branch, "branch", "", "override the detected branch")
	fl.StringVar(&f.from, "from", "", "start of an explicit range (manual runs)")
	fl.StringVar(&f.to, "to", "", "end of an explicit range (manual runs)")
	fl.IntVar(&f.parallel, "parallel", 1, "run up to N independent passes concurrently (max 4)")
	fl.BoolVar(&f.noComment, "no-comment", false, "do not post the pull request comment")
	fl.BoolVar(&f.strict, "strict", false, "treat unapproved targets, missing scopes and unlicensed modules as failures")
	fl.StringVar(&f.annotate, "annotate", "auto", "finding annotations: auto, github, gitlab, azure or none")
	return cmd
}

func (a *app) runPipeline(ctx context.Context, f runFlags) error {
	s, err := a.pipelineSession(ctx, modeRun, f.pipelineFlags)
	if errors.Is(err, errForkPR) {
		return a.forkPR(s)
	}
	if err != nil {
		return err
	}
	if s.ci.IsCI() {
		res, err := a.execute(ctx, s)
		if res != nil && res.Plan != nil {
			a.printRun(res, s.info)
		}
		if err != nil {
			return runError(err)
		}
		if s.opts.Trigger == config.TriggerPR || s.ci.IsCI() {
			a.publishReport(ctx, s, res, !f.noComment, f.annotate)
		}
		return a.finishPipeline(res, f.strict, res)
	}
	data := &runData{Pages: []changePage{}, Issues: []issue{}}
	pre, err := a.preflight(ctx, s, f)
	data.Preflight = pre
	if err != nil {
		return a.failWith(err, data)
	}
	m := a.modelFor(ctx, s)
	data.Issues = m.issues
	renderRunIssues(a.ui, m.issues)
	if m.errors() > 0 && s.opts.Mode != api.ModeDry {
		msg := fmt.Sprintf("%s in %s: the run would fail on them; fix them (gravity validate) or look at them in a dry run first", plural(m.errors(), "error", "errors"), config.ManifestFileName)
		return a.failWith(&ExitError{Code: CodeFindings, ErrCode: "validate_failed", Err: errors.New(msg)}, data)
	}
	view := newRunView(a, f.yes)
	res, err := a.executeLocal(ctx, s, view)
	view.stop()
	data.Plan = view.plan
	if res != nil {
		data.Result = res
	}
	if err != nil {
		if errors.Is(err, engine.ErrDeclined) {
			a.ui.Println("Canceled; nothing ran.")
			return a.ui.Result(data)
		}
		if res != nil && res.Plan != nil && res.Run != nil {
			a.renderResult(res, data, s)
		}
		return a.failWith(runError(err), data)
	}
	data.Pages = changePages(res)
	data.CostUSD = runCost(res)
	if res.Mode == api.ModeDry {
		return a.finishDryRun(ctx, s, f, res, data, m.errors())
	}
	data.ReviewURL = reviewURL(res)
	a.renderResult(res, data, s)
	if !f.yes {
		a.offerReview(data.ReviewURL)
	}
	return a.finishPipeline(res, f.strict, data)
}

func (a *app) failWith(err error, data *runData) error {
	var ee *ExitError
	if errors.As(err, &ee) && a.ui.JSON() {
		if ferr := a.ui.Failure(ui.ErrorInfo{Code: errorCode(err), Message: err.Error(), ExitCode: CodeFor(err)}, data); ferr != nil {
			return ferr
		}
	}
	return err
}

type preflightOutcome struct {
	Branch   string   `json:"branch"`
	HeadSHA  string   `json:"headSha"`
	Upstream string   `json:"upstream,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Dirty    []string `json:"dirty,omitempty"`
	Fetched  bool     `json:"fetched"`
}

func (a *app) preflight(ctx context.Context, s *pipelineSession, f runFlags) (*preflightOutcome, error) {
	repo := s.info.repo
	out := &preflightOutcome{Branch: s.info.branch, HeadSHA: s.info.head}
	p := a.ui
	branch := firstNonEmpty(s.info.branch, "HEAD")
	if up := repo.Upstream(ctx); up != "" {
		out.Upstream = up
		if err := repo.FetchUpstream(ctx, up); err != nil {
			a.ui.Warn("fetch_failed", "could not fetch "+up+" ("+oneLine(err.Error(), 120)+"); comparing with the last fetched state")
		} else {
			out.Fetched = true
		}
		st, err := repo.Sync(ctx, up)
		if err == nil {
			out.Ahead, out.Behind = st.Ahead, st.Behind
			switch {
			case st.Diverged():
				return out, &ExitError{Code: CodeError, ErrCode: "branch_diverged", Err: fmt.Errorf("%s and %s have diverged (%d local and %d remote commits); reconcile them first:\n    git pull --rebase\nthen run again", branch, up, st.Ahead, st.Behind)}
			case st.Behind > 0:
				return out, &ExitError{Code: CodeError, ErrCode: "branch_behind", Err: fmt.Errorf("%s is %s behind %s; update it first:\n    git pull --ff-only\nthen run again", branch, plural(st.Behind, "commit", "commits"), up)}
			case st.Ahead > 0 && !f.dryRun:
				a.ui.Warn("branch_unpushed", fmt.Sprintf("%s on %s is not pushed; reviewers cannot open the commits the change request cites until you run: git push", plural(st.Ahead, "commit", "commits"), branch))
			}
		}
	} else if s.info.branch != "" {
		a.ui.Warn("branch_no_upstream", fmt.Sprintf("%s has no upstream, so gravity cannot check that it is current; push it with: git push -u origin %s", branch, branch))
	}
	dirty, err := repo.DirtyTracked(ctx)
	if err == nil && len(dirty) > 0 {
		out.Dirty = dirty
		list := strings.Join(dirty, ", ")
		if len(dirty) > 5 {
			list = strings.Join(dirty[:5], ", ") + fmt.Sprintf(" and %d more", len(dirty)-5)
		}
		if !f.allowDirty {
			hint := "commit them (git commit -am \"...\") or stash them (git stash)"
			if f.dryRun {
				hint += ", or pass --allow-dirty to dry-run the committed state anyway"
			}
			return out, &ExitError{Code: CodeError, ErrCode: "worktree_dirty", Err: fmt.Errorf("uncommitted changes in %s; a run reads committed history only: %s", list, hint)}
		}
		a.ui.Warn("uncommitted_changes", "uncommitted changes in "+list+" are not part of this dry run, which reads committed history only")
	}
	state := "no upstream"
	if out.Upstream != "" {
		state = "up to date with " + out.Upstream
		if out.Ahead > 0 {
			state = fmt.Sprintf("%d ahead of %s", out.Ahead, out.Upstream)
		}
	}
	p.Note("", ui.MarkOK, "%s @ %s · %s", p.Bold(branch), shortSHA(s.info.head), state)
	return out, nil
}

func (a *app) modelFor(ctx context.Context, s *pipelineSession) *model {
	m := &model{info: s.info, manifest: s.manifest, server: serverSkipped, client: s.client, who: s.who, signedIn: true}
	if s.manifest == nil {
		return m
	}
	src, err := newWorktreeSource(ctx, s.info)
	if err != nil {
		a.ui.Debugf("files: %v", err)
		return m
	}
	m.mapVerbatim(src)
	m.localRules(src)
	a.serverChecks(ctx, m, modelOptions{server: true})
	m.flagExisting()
	sortIssues(m.issues)
	return m
}

func (a *app) executeLocal(ctx context.Context, s *pipelineSession, v *runView) (*engine.Result, error) {
	env := a.runEnv(s)
	v.attach(env, s)
	return engine.Execute(ctx, env, s.opts)
}

func (a *app) finishDryRun(ctx context.Context, s *pipelineSession, f runFlags, res *engine.Result, data *runData, issues int) error {
	if res.Run != nil && len(res.Passes) > 0 {
		rec := engine.NewRecording(res, s.info.remoteKey, s.manifest.Hash, a.now())
		if rec.Changes() > 0 {
			path, err := engine.SaveRecording(s.info.root, rec)
			if err != nil {
				a.ui.Warn("recording_failed", "could not record the dry run: "+err.Error())
			} else {
				data.Recording = relPath(s.info.root, path)
			}
		}
	}
	a.renderResult(res, data, s)
	if issues > 0 {
		msg := plural(issues, "validation error", "validation errors") + ": sending this run would fail on them"
		if ferr := a.ui.Failure(ui.ErrorInfo{Code: "validate_failed", Message: msg, ExitCode: CodeFindings}, data); ferr != nil {
			return ferr
		}
		return &ExitError{Code: CodeFindings, ErrCode: "validate_failed", Err: errors.New(msg)}
	}
	if code := res.ExitCode(f.strict); code != CodeOK {
		return a.finishPipeline(res, f.strict, data)
	}
	if data.Recording != "" && a.ui.Interactive() && !f.yes && res.Run != nil {
		answer, err := a.prompter().Select("Send this run to Gravity?", "the recorded result becomes a change request; nothing is recomputed", []ui.Choice{
			{Key: "send", Label: "Send it now"},
			{Key: "later", Label: "Not now (gravity run --send " + res.Run.ID + ")"},
		}, "later")
		if err == nil && answer == "send" {
			sf := f
			sf.send = res.Run.ID
			sf.yes = true
			return a.sendRun(ctx, sf)
		}
	}
	return a.ui.Result(data)
}

func (a *app) sendRun(ctx context.Context, f runFlags) error {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return err
	}
	rec, err := engine.LoadRecording(info.root, f.send)
	if err != nil {
		return &ExitError{Code: CodeError, ErrCode: "run_not_found", Err: err}
	}
	f.dryRun = false
	f.passes = nil
	f.branch = firstNonEmpty(f.branch, rec.Branch)
	s, err := a.pipelineSession(ctx, modeRun, f.pipelineFlags)
	if err != nil {
		return err
	}
	data := &runData{Pages: []changePage{}, Issues: []issue{}, SentFrom: rec.RunID}
	if s.info.head != rec.HeadSHA {
		return a.failWith(&ExitError{Code: CodeError, ErrCode: "send_mismatch", Err: fmt.Errorf("dry run %s was computed at %s, but HEAD is now %s; run `gravity run --dry-run` again and send the new result", rec.RunID, shortSHA(rec.HeadSHA), shortSHA(s.info.head))}, data)
	}
	if s.manifest == nil || s.manifest.Hash != rec.ManifestHash {
		return a.failWith(&ExitError{Code: CodeError, ErrCode: "send_mismatch", Err: fmt.Errorf("%s changed since dry run %s; run `gravity run --dry-run` again and send the new result", config.ManifestFileName, rec.RunID)}, data)
	}
	renderRecording(a.ui, rec)
	if a.ui.Interactive() && !f.yes {
		answer, err := a.prompter().Select(fmt.Sprintf("Send %s from dry run %s to Gravity?", plural(rec.Changes(), "change", "changes"), rec.RunID), "they become one change request reviewed in the app", []ui.Choice{{Key: "send", Label: "Send"}, {Key: "cancel", Label: "Cancel"}}, "send")
		if err != nil || answer != "send" {
			a.ui.Println("Canceled; nothing was sent.")
			return a.ui.Result(data)
		}
	}
	v := newRunView(a, true)
	env := a.runEnv(s)
	v.attach(env, s)
	env.Confirm = nil
	v.begin(nil)
	for _, p := range rec.Passes {
		v.addStep(p.Name, p.Kind, p.Target)
	}
	opts := s.opts
	res, err := engine.Replay(ctx, env, opts, rec)
	v.stop()
	if res != nil {
		data.Result = res
	}
	if err != nil {
		return a.failWith(runError(err), data)
	}
	data.ReviewURL = reviewURL(res)
	data.CostUSD = rec.CostUSD
	for _, p := range rec.Passes {
		data.Pages = append(data.Pages, recordedPages(p.Name, p.Recorded)...)
	}
	_ = os.Remove(engine.RecordingPath(info.root, rec.RunID))
	a.renderResult(res, data, s)
	a.offerReview(data.ReviewURL)
	return a.finishPipeline(res, false, data)
}

func (a *app) offerReview(url string) {
	if url == "" || !a.ui.Interactive() || a.openBrowser == nil {
		return
	}
	answer, err := a.prompter().Select("Open the change request?", url, []ui.Choice{{Key: "open", Label: "Open in the browser"}, {Key: "no", Label: "Not now"}}, "open")
	if err == nil && answer == "open" {
		_ = a.openBrowser(url)
	}
}

func reviewURL(res *engine.Result) string {
	if res == nil {
		return ""
	}
	if res.Finish != nil {
		if res.Finish.Bundle.AppURL != "" {
			return res.Finish.Bundle.AppURL
		}
		if res.Finish.Run.AppURL != "" {
			return res.Finish.Run.AppURL
		}
	}
	if res.Run != nil {
		return res.Run.AppURL
	}
	return ""
}

func runCost(res *engine.Result) float64 {
	var c float64
	for _, p := range res.Passes {
		c += p.CostUSD
	}
	return c
}

func changePages(res *engine.Result) []changePage {
	out := []changePage{}
	for _, p := range res.Passes {
		if p.Report == nil || p.Report.Recorded == nil {
			if p.Report != nil {
				for _, im := range p.Report.Impact {
					out = append(out, changePage{Pass: p.Name, Op: im.Action, Slug: im.Page.Slug, Title: im.Page.Title, Note: im.Reason})
				}
			}
			continue
		}
		out = append(out, recordedPages(p.Name, *p.Report.Recorded)...)
	}
	return out
}

func recordedPages(pass string, rec passes.Recorded) []changePage {
	out := []changePage{}
	for _, c := range rec.Changes {
		out = append(out, changePage{Pass: pass, Op: c.Op, Slug: firstNonEmpty(c.Target.Slug, c.Target.PageID), Title: c.Title, CollectionPath: c.Target.CollectionPath, Note: oneLine(c.Summary, 140)})
	}
	for _, v := range rec.Verbatim {
		out = append(out, changePage{Pass: pass, Op: api.OpImport, Slug: v.Page.Slug, Title: v.Page.Title, CollectionPath: v.Page.CollectionPath, Language: v.Language, Source: v.File.Path})
	}
	for _, d := range rec.Deletions {
		out = append(out, changePage{Pass: pass, Op: api.OpDelete, Source: d.Path, Note: d.Reason})
	}
	for _, m := range rec.Memories {
		out = append(out, changePage{Pass: pass, Op: "memory", Title: m.Title, Note: oneLine(m.Body, 140)})
	}
	for _, h := range rec.Hints {
		out = append(out, changePage{Pass: pass, Op: "hint", Slug: firstNonEmpty(h.UnitKey, h.PageID), Note: oneLine(h.Claim, 140)})
	}
	return out
}
