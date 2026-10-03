// Package run orchestrates a pipeline run: connect, plan, ranges, ChangeSets, scope skips, lease, heartbeat, ingest, passes and finish.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/plan"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// API is the platform surface a run uses.
type API interface {
	passes.API
	passes.Writer
	agent.LLM
	prompts.Fetcher
	Connect(ctx context.Context, req api.ConnectRequest) (*api.ConnectResponse, error)
	Plan(ctx context.Context, q api.PlanQuery) (*api.Plan, error)
	StartRun(ctx context.Context, repo string, req api.StartRunRequest) (*api.StartedRun, error)
	Heartbeat(ctx context.Context, runID string) (string, error)
	ReportPass(ctx context.Context, runID, runPassID string, req api.PassReportRequest) (*api.PassRunResult, error)
	FinishRun(ctx context.Context, runID string, req api.FinishRunRequest) (*api.FinishedRun, error)
	IngestInventory(ctx context.Context, req api.IngestRequest) (*api.IngestResult, error)
}

// Logger receives progress.
type Logger interface {
	Infof(format string, args ...any)
	Warn(code, message string)
	Debugf(format string, args ...any)
}

// Options select what a run does.
type Options struct {
	Trigger        string
	Branch         string
	Head           string
	PR             *api.PRInfo
	PRBase         string
	Tag            string
	From           string
	To             string
	Note           string
	Passes         []string
	Mode           string
	WorkingTree    bool
	Preview        bool
	LeaseTimeout   time.Duration
	Parallel       int
	FailOn         []string
	ImplicitCheck  bool
	Origin         string
	CI             *api.CIInfo
	RepoParam      string
	ConnectContext string
	TokenBudget    int
}

// Env carries the collaborators of a run.
type Env struct {
	Client    API
	Repo      *git.Repo
	Info      passes.RepoInfo
	Connect   api.ConnectRequest
	Manifest  *config.Manifest
	Log       Logger
	Sleep     func(ctx context.Context, d time.Duration) error
	Now       func() time.Time
	NewKey    func() string
	Generator string
	OnPlan    func(*api.Plan)
	OnPass    func(PassResult)
}

// PassResult is the outcome of one pass of a run.
type PassResult struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Target     string           `json:"target"`
	RunPassID  string           `json:"runPassId,omitempty"`
	Status     string           `json:"status"`
	SkipReason string           `json:"skipReason,omitempty"`
	Range      *changeset.Range `json:"range,omitempty"`
	Report     *passes.Report   `json:"report,omitempty"`
	Error      string           `json:"error,omitempty"`
	CostUSD    float64          `json:"costUsd"`
	LLMCalls   int              `json:"llmCalls"`
	Implicit   bool             `json:"implicit,omitempty"`
	ApproveURL string           `json:"approveUrl,omitempty"`
	Missing    []string         `json:"missingScopes,omitempty"`
}

// Result is the outcome of a run.
type Result struct {
	Trigger  string               `json:"trigger"`
	Branch   string               `json:"branch,omitempty"`
	Mode     string               `json:"mode"`
	Plan     *api.Plan            `json:"-"`
	Connect  *api.ConnectResponse `json:"-"`
	Run      *api.RunInfo         `json:"run"`
	Finish   *api.FinishedRun     `json:"finish"`
	Passes   []PassResult         `json:"passes"`
	Range    *changeset.Range     `json:"range,omitempty"`
	Commits  int                  `json:"commits"`
	Ingest   *api.IngestResult    `json:"ingest,omitempty"`
	Handoffs []Handoff            `json:"handoffs,omitempty"`
	Warnings []api.Warning        `json:"warnings,omitempty"`
}

// ErrLeaseTimeout means another run held the lease for longer than --lease-timeout.
var ErrLeaseTimeout = errors.New("lease timeout")

// SelectionError reports a pass named with --pass that the plan does not know or will not run.
type SelectionError struct {
	Pass          string
	Reason        string
	Known         []string
	Triggers      []string
	Branches      []string
	Trigger       string
	Branch        string
	DefaultBranch string
}

// SelectionUnknown is the reason of a SelectionError for a pass the plan does not list.
const SelectionUnknown = "unknown"

func (e *SelectionError) Error() string {
	switch e.Reason {
	case SelectionUnknown:
		known := strings.Join(e.Known, ", ")
		if known == "" {
			known = "none"
		}
		return fmt.Sprintf("no pass named %q (passes: %s)", e.Pass, known)
	case api.SkipDisabled:
		return fmt.Sprintf("pass %s is disabled; enable it in the app (or %s) to run it", e.Pass, config.ManifestFileName)
	case api.SkipTriggerMismatch:
		return fmt.Sprintf("pass %s does not run on %s runs (its triggers: %s), and this Gravity server applies triggers even to a pass named with --pass; add %q to its triggers in the app (or %s), or run it with --trigger %s", e.Pass, e.Trigger, firstOf(strings.Join(e.Triggers, ", "), "none"), e.Trigger, config.ManifestFileName, firstOf(firstWriteTrigger(e.Triggers), config.TriggerPush))
	case api.SkipBranchMismatch:
		where := "the default branch"
		if len(e.Branches) > 0 {
			where = strings.Join(e.Branches, ", ")
		} else if e.DefaultBranch != "" {
			where = "the default branch " + e.DefaultBranch
		}
		return fmt.Sprintf("pass %s runs only on %s, not on %s; run it from there, or widen its branches in the app (or %s)", e.Pass, where, firstOf(e.Branch, "this branch"), config.ManifestFileName)
	}
	return fmt.Sprintf("pass %s does not run: %s", e.Pass, strings.ReplaceAll(e.Reason, "_", " "))
}

func firstWriteTrigger(triggers []string) string {
	for _, t := range []string{config.TriggerPush, config.TriggerSchedule} {
		for _, have := range triggers {
			if have == t {
				return t
			}
		}
	}
	return ""
}

func checkSelection(p *api.Plan, opts Options) error {
	if len(opts.Passes) == 0 {
		return nil
	}
	byName := map[string]api.PlanPass{}
	known := make([]string, 0, len(p.Passes))
	for _, pp := range p.Passes {
		byName[pp.Name] = pp
		known = append(known, pp.Name)
	}
	sort.Strings(known)
	for _, name := range opts.Passes {
		pp, ok := byName[name]
		if !ok {
			return &SelectionError{Pass: name, Reason: SelectionUnknown, Known: known}
		}
		if pp.Applies || opts.Trigger != config.TriggerManual {
			continue
		}
		switch pp.SkipReason {
		case api.SkipDisabled, api.SkipTriggerMismatch, api.SkipBranchMismatch:
			return &SelectionError{Pass: name, Reason: pp.SkipReason, Triggers: pp.Triggers, Branches: pp.Branches, Trigger: opts.Trigger, Branch: opts.Branch, DefaultBranch: p.Repo.DefaultBranch}
		}
	}
	return nil
}

// Exit codes a run maps to.
const (
	ExitOK       = 0
	ExitFindings = 1
	ExitError    = 2
	ExitLicense  = 3
)

// ExitCode applies the exit-code contract to the pass outcomes.
func (r *Result) ExitCode(strict bool) int {
	code := ExitOK
	raise := func(c int) {
		if c > code {
			code = c
		}
	}
	for _, p := range r.Passes {
		switch {
		case p.Status == api.StatusFailed:
			raise(ExitError)
		case p.SkipReason == api.SkipTargetMissing:
			raise(ExitError)
		case strict && p.SkipReason == api.SkipModuleDisabled:
			raise(ExitLicense)
		case strict && (p.SkipReason == api.SkipTargetUnapproved || p.SkipReason == api.SkipScopeMissing):
			raise(ExitError)
		}
		if p.Report != nil && p.Report.Failing {
			raise(ExitFindings)
		}
	}
	return code
}

// Findings returns every finding of the run.
func (r *Result) Findings() []api.Finding {
	var out []api.Finding
	for _, p := range r.Passes {
		if p.Report != nil {
			out = append(out, p.Report.Findings...)
		}
	}
	return out
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *Env) key() string {
	if e.NewKey != nil {
		return e.NewKey()
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "gr" + hex.EncodeToString(b[:])
}

type logWriter struct{ log Logger }

func (w logWriter) Write(p []byte) (int, error) {
	if w.log != nil {
		w.log.Debugf("%s", strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func debugWriter(l Logger) io.Writer { return logWriter{log: l} }

// Execute runs the pipeline.
func Execute(ctx context.Context, env *Env, opts Options) (*Result, error) {
	if opts.Mode == "" {
		opts.Mode = api.ModeWrite
	}
	if opts.Trigger == config.TriggerPR {
		opts.Mode = api.ModeDry
	}
	res := &Result{Trigger: opts.Trigger, Branch: opts.Branch, Mode: opts.Mode}
	conn := env.Connect
	conn.Context = api.ConnectContext{Trigger: firstOf(opts.ConnectContext, opts.Trigger), Origin: firstOf(opts.Origin, api.OriginLocal)}
	conn.DryRun = opts.Mode == api.ModeDry
	connected, err := env.Client.Connect(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	res.Connect = connected
	for _, w := range connected.Manifest.Warnings {
		env.Log.Warn(w.Code, w.Message)
	}
	timeout := opts.LeaseTimeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	deadline := env.now().Add(timeout)
	stale := 0
	for {
		p, err := fetchPlan(ctx, env, opts)
		if err != nil {
			return res, err
		}
		res.Plan = p
		if err := checkSelection(p, opts); err != nil {
			return res, err
		}
		if env.OnPlan != nil {
			env.OnPlan(p)
		}
		if env.Info.ID == "" {
			env.Info.ID = p.Repo.ID
		}
		prep, err := prepare(ctx, env, opts, p)
		if err != nil {
			return res, err
		}
		res.Range, res.Commits = prep.runRange, prep.commits
		res.Handoffs = expectedHandoffs(p, env.Info, Roles(env.Manifest), prep)
		decisions := make([]plan.Decision, 0, len(prep.passes))
		for _, pp := range prep.passes {
			decisions = append(decisions, pp.decision)
		}
		if !plan.NeedsRun(decisions, opts.Mode == api.ModeWrite) {
			res.Passes = skippedResults(prep)
			if err := implicitCheck(ctx, env, opts, p, prep, res); err != nil {
				return res, err
			}
			return res, nil
		}
		started, err := env.Client.StartRun(ctx, opts.RepoParam, startRequest(env, opts, p, prep))
		switch {
		case errors.Is(err, api.ErrLeaseHeld):
			wait := leaseWait(err)
			if env.now().Add(wait).After(deadline) {
				return res, fmt.Errorf("%w: %s", ErrLeaseTimeout, holderLine(err, opts.Branch, timeout))
			}
			env.Log.Infof("Another Gravity run holds %s; retrying in %s", firstOf(opts.Branch, opts.Tag), wait)
			if err := env.sleep(ctx, wait); err != nil {
				return res, err
			}
			continue
		case errors.Is(err, api.ErrPlanStale) && stale < 3:
			stale++
			env.Log.Infof("The watermark moved since the plan; planning again")
			continue
		case err != nil:
			return res, fmt.Errorf("start run: %w", err)
		}
		res.Run = &started.Run
		if err := execute(ctx, env, opts, p, prep, started, res); err != nil {
			return res, err
		}
		if err := implicitCheck(ctx, env, opts, p, prep, res); err != nil {
			return res, err
		}
		return res, nil
	}
}

func fetchPlan(ctx context.Context, env *Env, opts Options) (*api.Plan, error) {
	q := api.PlanQuery{Repo: opts.RepoParam, Trigger: opts.Trigger, Branch: opts.Branch, Passes: opts.Passes, Mode: opts.Mode}
	switch opts.Trigger {
	case config.TriggerPR:
		if opts.PR != nil && opts.PR.TargetBranch != "" {
			q.Branch = opts.PR.TargetBranch
		}
	case config.TriggerRelease:
		q.Branch = ""
	}
	if opts.Mode == api.ModeDry && env.Manifest != nil {
		q.ManifestHash = env.Manifest.Hash
	}
	p, err := env.Client.Plan(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if err := plan.RequirePipelines(p.Capabilities.Features); err != nil && len(p.Capabilities.Features) > 0 {
		return nil, err
	}
	for _, w := range p.Warnings {
		env.Log.Warn(w.Code, w.Message)
	}
	if opts.Mode == api.ModeWrite && env.Manifest != nil && p.Repo.ManifestHash != "" && p.Repo.ManifestHash != env.Manifest.Hash && opts.Branch != firstOf(p.Repo.AuthoritativeBranch, p.Repo.DefaultBranch) {
		env.Log.Warn("manifest_not_authoritative", fmt.Sprintf("%s differs from the configuration stored in Gravity; this write run uses the stored passes (changes take effect when the file reaches %s)", config.ManifestFileName, firstOf(p.Repo.AuthoritativeBranch, p.Repo.DefaultBranch, "the default branch")))
	}
	return p, nil
}

func leaseWait(err error) time.Duration {
	var ae *api.APIError
	secs := 5
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		secs = ae.RetryAfter
	}
	secs = min(max(secs, 5), 60)
	return time.Duration(secs) * time.Second
}

func holderLine(err error, branch string, timeout time.Duration) string {
	var ae *api.APIError
	if errors.As(err, &ae) && ae.Holder != nil {
		return fmt.Sprintf("another Gravity run (%s, %s) holds %s; giving up after %s", ae.Holder.RunID, shortSHA(ae.Holder.HeadSHA), firstOf(branch, "this lease"), timeout)
	}
	return fmt.Sprintf("another Gravity run holds %s; giving up after %s", firstOf(branch, "this lease"), timeout)
}

func skippedResults(prep *prepared) []PassResult {
	out := make([]PassResult, 0, len(prep.passes))
	for _, pp := range prep.passes {
		r := PassResult{Name: pp.pass.Name, Kind: pp.pass.Kind, Target: passes.TargetLabel(pp.pass), Status: api.StatusSkipped, SkipReason: pp.decision.Skip, ApproveURL: pp.pass.Target.ApproveURL, Missing: pp.pass.MissingScopes}
		if pp.ranged {
			rng := pp.rng
			r.Range = &rng
		}
		out = append(out, r)
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

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
