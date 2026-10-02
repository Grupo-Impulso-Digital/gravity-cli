package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// MaxReportBytes is the pass report size the platform accepts.
const MaxReportBytes = 500 * 1024

// ErrStopped means the platform ended the run (lease lost or run finished) and no finish call was made.
var ErrStopped = errors.New("run stopped by the platform")

type runState struct {
	env      *Env
	opts     Options
	plan     *api.Plan
	prep     *prepared
	started  *api.StartedRun
	known    *passes.Units
	assets   *passes.Assets
	resolver *prompts.Resolver
	ingested []api.IngestUnit
	mu       sync.Mutex
	stopErr  error
	cancel   context.CancelFunc
}

func (s *runState) stop(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopErr == nil {
		s.stopErr = err
		s.cancel()
	}
}

func (s *runState) stopped() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopErr != nil {
		return fmt.Errorf("%w: %w", ErrStopped, s.stopErr)
	}
	return nil
}

func execute(ctx context.Context, env *Env, opts Options, p *api.Plan, prep *prepared, started *api.StartedRun, res *Result) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &runState{
		env: env, opts: opts, plan: p, prep: prep, started: started, known: passes.NewUnits(p.Inventory.Units), assets: passes.NewAssets(), cancel: cancel,
		resolver: &prompts.Resolver{Fetcher: env.Client, Log: env.Log.Debugf},
	}
	hbDone := make(chan struct{})
	go s.heartbeat(runCtx, hbDone)
	defer func() {
		cancel()
		<-hbDone
	}()
	if opts.Mode == api.ModeWrite && (started.Run.Authoritative || opts.Trigger == config.TriggerRelease) && p.Capabilities.Features["product-inventory"] {
		ing, err := s.ingest(runCtx)
		if err != nil {
			if api.StopsRun(err) {
				s.stop(err)
				return s.stopped()
			}
			env.Log.Warn("ingest_failed", fmt.Sprintf("inventory ingest failed; passes use the stored inventory: %v", err))
		}
		res.Ingest = ing
		if ing != nil {
			res.Handoffs = detectedHandoffs(ing)
		}
	}
	results := s.passes(runCtx)
	if err := s.stopped(); err != nil {
		res.Passes = results
		return err
	}
	res.Passes = results
	if res.Ingest != nil {
		if err := s.ingestDocuments(runCtx, results); err != nil {
			if api.StopsRun(err) {
				s.stop(err)
				return s.stopped()
			}
			env.Log.Warn("ingest_failed", fmt.Sprintf("could not record the units this repository documents: %v", err))
		}
	}
	status := finishStatus(results)
	summary := runSummary(results)
	fin, err := env.Client.FinishRun(ctx, started.Run.ID, api.FinishRunRequest{Status: status, Report: &api.FinishReport{Summary: summary}})
	if err != nil {
		if api.StopsRun(err) {
			return fmt.Errorf("%w: %w", ErrStopped, err)
		}
		return fmt.Errorf("finish run: %w", err)
	}
	res.Finish = fin
	return nil
}

func (s *runState) heartbeat(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	secs := s.started.Run.HeartbeatSeconds
	if secs <= 0 {
		secs = s.plan.Capabilities.Limits.HeartbeatSeconds
	}
	if secs <= 0 {
		secs = 60
	}
	for {
		if err := s.env.sleep(ctx, time.Duration(secs)*time.Second); err != nil {
			return
		}
		if _, err := s.env.Client.Heartbeat(ctx, s.started.Run.ID); err != nil {
			if api.StopsRun(err) {
				s.env.Log.Warn("lease_lost", fmt.Sprintf("the run was ended in Gravity (%v); stopping", err))
				s.stop(err)
				return
			}
			if ctx.Err() != nil {
				return
			}
			s.env.Log.Debugf("heartbeat: %v", err)
		}
	}
}

func finishStatus(results []PassResult) string {
	ran, failed := 0, 0
	for _, r := range results {
		switch r.Status {
		case api.StatusFailed:
			failed++
			ran++
		case api.StatusSucceeded:
			ran++
		}
	}
	switch {
	case failed > 0 && failed == ran:
		return api.StatusFailed
	case failed > 0:
		return api.StatusPartial
	}
	return api.StatusSucceeded
}

func runSummary(results []PassResult) string {
	ran, skipped, failed := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case api.StatusSucceeded:
			ran++
		case api.StatusFailed:
			failed++
		default:
			skipped++
		}
	}
	s := fmt.Sprintf("%d passes ran, %d skipped", ran, skipped)
	if failed > 0 {
		s += fmt.Sprintf(", %d failed", failed)
	}
	return s
}

func (s *runState) passes(ctx context.Context) []PassResult {
	server := map[string]api.RunPass{}
	for _, rp := range s.started.Passes {
		server[rp.Name] = rp
	}
	results := make([]PassResult, len(s.prep.passes))
	parallel := min(max(s.opts.Parallel, 1), 4)
	sem := make(chan struct{}, parallel)
	locks := map[string]*sync.Mutex{}
	var wg sync.WaitGroup
	for i, pp := range s.prep.passes {
		rp, ok := server[pp.pass.Name]
		base := PassResult{Name: pp.pass.Name, Kind: pp.pass.Kind, Target: passes.TargetLabel(pp.pass), RunPassID: rp.RunPassID, ApproveURL: pp.pass.Target.ApproveURL, Missing: pp.pass.MissingScopes}
		if pp.ranged {
			rng := pp.rng
			base.Range = &rng
		}
		if !ok || rp.Status == api.StatusSkipped || !pp.decision.Run {
			base.Status = api.StatusSkipped
			base.SkipReason = firstOf(rp.SkipReason, pp.decision.Skip)
			results[i] = base
			s.emit(base)
			continue
		}
		key := ""
		if pp.pass.Target.Space != nil {
			key = pp.pass.Target.Space.ID
		}
		if _, ok := locks[key]; !ok {
			locks[key] = &sync.Mutex{}
		}
		lock := locks[key]
		wg.Add(1)
		go func(i int, pp preparedPass, rp api.RunPass, base PassResult) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if key != "" {
				lock.Lock()
				defer lock.Unlock()
			}
			if ctx.Err() != nil {
				base.Status = api.StatusFailed
				base.Error = "run stopped"
				results[i] = base
				s.emit(base)
				return
			}
			results[i] = s.runPass(ctx, pp, rp, base)
			s.emit(results[i])
		}(i, pp, rp, base)
		if parallel == 1 {
			wg.Wait()
		}
	}
	wg.Wait()
	return results
}

func (s *runState) emit(r PassResult) {
	if s.env.OnPass != nil {
		s.env.OnPass(r)
	}
}

func (s *runState) input(pp preparedPass, rp api.RunPass) passes.Input {
	return passes.Input{
		Pass: pp.pass, Plan: s.plan, Manifest: s.env.Manifest, Range: pp.rng, ChangeSet: pp.cs, Builder: s.prep.builder,
		RunID: s.started.Run.ID, RunPassID: rp.RunPassID, Mode: s.opts.Mode, Trigger: s.opts.Trigger, Preview: s.opts.Preview, Note: s.opts.Note,
		FailOn: s.opts.FailOn, Repo: s.env.Repo, Info: s.env.Info, Client: s.env.Client, Known: s.known, ProductParam: productParam(s.opts, s.plan),
		Generator: s.env.Generator, Log: s.env.Log.Debugf, Sleep: s.env.Sleep,
	}
}

func productParam(opts Options, p *api.Plan) string {
	if opts.RepoParam == "" {
		return ""
	}
	return p.Product.Slug
}

func (s *runState) harness(rp api.RunPass) *agent.Harness {
	return &agent.Harness{LLM: s.env.Client, Prompts: s.resolver, RunID: s.started.Run.ID, RunPassID: rp.RunPassID, TokenBudget: s.opts.TokenBudget, Log: debugWriter(s.env.Log)}
}

func (s *runState) runPass(ctx context.Context, pp preparedPass, rp api.RunPass, base PassResult) PassResult {
	impl, ok := passes.For(pp.pass.Kind)
	if !ok {
		base.Status = api.StatusFailed
		base.Error = "unknown pass kind " + pp.pass.Kind
		s.report(ctx, rp, api.StatusFailed, nil, base.Error)
		return base
	}
	if _, err := s.env.Client.ReportPass(ctx, s.started.Run.ID, rp.RunPassID, api.PassReportRequest{Status: api.StatusRunning}); err != nil {
		if api.StopsRun(err) {
			s.stop(err)
			base.Status, base.Error = api.StatusFailed, err.Error()
			return base
		}
		s.env.Log.Debugf("report %s running: %v", pp.pass.Name, err)
	}
	in := s.input(pp, rp)
	h := s.harness(rp)
	in.Harness = h
	var sink passes.Sink
	var rec *passes.Recorder
	if s.opts.Mode == api.ModeDry {
		rec = &passes.Recorder{}
		sink = rec
	} else {
		sink = &passes.PlatformSink{W: s.env.Client, RunID: s.started.Run.ID, RunPassID: rp.RunPassID, Assets: s.assets}
	}
	started := s.env.now()
	rep, err := impl.Run(ctx, in, sink)
	rep.DurationMs = s.env.now().Sub(started).Milliseconds()
	rep.Usage = h.Usage()
	if rec != nil {
		r := rec.Recorded()
		rep.Recorded = &r
	}
	if err == nil && rep.Truncated(pp.cs) {
		rep.Warnings = append(rep.Warnings, "the change set was truncated")
	}
	base.Report = &rep
	if err != nil {
		if api.StopsRun(err) {
			s.stop(err)
		}
		if api.StopsRun(err) || s.stopped() != nil {
			base.Status, base.Error = api.StatusFailed, err.Error()
			return base
		}
		base.Status, base.Error = api.StatusFailed, err.Error()
		if pr := s.report(ctx, rp, api.StatusFailed, &rep, err.Error()); pr != nil {
			base.CostUSD, base.LLMCalls = pr.CostUSD, pr.LLMCalls
		}
		return base
	}
	base.Status = api.StatusSucceeded
	if pr := s.report(ctx, rp, api.StatusSucceeded, &rep, ""); pr != nil {
		base.CostUSD, base.LLMCalls = pr.CostUSD, pr.LLMCalls
	}
	return base
}

func (s *runState) report(ctx context.Context, rp api.RunPass, status string, rep *passes.Report, msg string) *api.PassRunResult {
	req := api.PassReportRequest{Status: status}
	if msg != "" {
		m := msg
		req.Error = &m
	}
	if rep != nil {
		pr := fitReport(rep.PassReport)
		req.Report = &pr
	}
	out, err := s.env.Client.ReportPass(ctx, s.started.Run.ID, rp.RunPassID, req)
	if err != nil {
		if api.StopsRun(err) {
			s.stop(err)
			return nil
		}
		s.env.Log.Warn("report_failed", fmt.Sprintf("could not report pass %s: %v", rp.Name, err))
		return nil
	}
	return out
}

func fitReport(r api.PassReport) api.PassReport {
	for range 8 {
		data, err := json.Marshal(r)
		if err != nil || len(data) <= MaxReportBytes {
			return r
		}
		r.Impact = half(r.Impact)
		r.Findings = half(r.Findings)
		r.Notes = half(r.Notes)
		if !strings.Contains(r.Summary, "(report truncated)") {
			r.Summary += " (report truncated)"
		}
	}
	return r
}

func half[T any](list []T) []T {
	if len(list) <= 1 {
		return list
	}
	return list[:len(list)/2]
}

func (s *runState) ingest(ctx context.Context) (*api.IngestResult, error) {
	units := append([]api.IngestUnit{}, s.prep.inventory...)
	defer func() { s.ingested = units }()
	complete := true
	if s.env.Manifest != nil && s.env.Manifest.Code != nil && len(s.env.Manifest.Code.Entrypoints) > 0 {
		mapped, ran, err := s.mapUnits(ctx)
		switch {
		case err != nil && api.StopsRun(err):
			return nil, err
		case err != nil:
			s.env.Log.Warn("map_units_failed", fmt.Sprintf("unit mapping failed: %v", err))
			complete = false
		case !ran:
			complete = false
		}
		units = mergeUnits(units, mapped)
	}
	units = keepDocuments(units, s.plan, s.env.Info)
	if len(units) == 0 && !complete {
		return nil, nil
	}
	const batch = 2000
	head := s.prep.headSHA
	var res *api.IngestResult
	var err error
	if len(units) <= batch {
		res, err = s.env.Client.IngestInventory(ctx, api.IngestRequest{Product: productParam(s.opts, s.plan), RunID: s.started.Run.ID, HeadSHA: head, Complete: complete, Units: units})
	} else {
		for i := 0; i < len(units); i += batch {
			end := min(i+batch, len(units))
			if res, err = s.env.Client.IngestInventory(ctx, api.IngestRequest{Product: productParam(s.opts, s.plan), RunID: s.started.Run.ID, HeadSHA: head, Complete: false, Units: units[i:end]}); err != nil {
				break
			}
		}
		if err == nil && complete {
			entries := make([]api.IngestEntry, 0, len(units))
			for _, u := range units {
				entries = append(entries, api.IngestEntry{Key: u.Key, Roles: u.Roles})
			}
			res, err = s.env.Client.IngestInventory(ctx, api.IngestRequest{Product: productParam(s.opts, s.plan), RunID: s.started.Run.ID, HeadSHA: head, Complete: true, Entries: entries})
		}
	}
	if err != nil {
		if api.HasCode(err, api.CodeBranchNotAuthority) {
			s.env.Log.Infof("Inventory not ingested: this branch does not define ownership")
			return nil, nil
		}
		return nil, err
	}
	for _, u := range units {
		s.known.Add(u.Key)
	}
	for _, c := range res.Conflicts {
		s.env.Log.Warn("unit_conflict", fmt.Sprintf("unit %s not ingested: %s (existing kind %s)", c.Key, c.Reason, c.ExistingKind))
	}
	return res, nil
}

func mergeUnits(base, extra []api.IngestUnit) []api.IngestUnit {
	seen := map[string]bool{}
	for _, u := range base {
		seen[u.Key] = true
	}
	for _, u := range extra {
		if !seen[u.Key] {
			seen[u.Key] = true
			base = append(base, u)
		}
	}
	return base
}

func (s *runState) mapUnits(ctx context.Context) ([]api.IngestUnit, bool, error) {
	m := s.env.Manifest
	var host *preparedPass
	var hostRP api.RunPass
	for i, pp := range s.prep.passes {
		if !pp.decision.Run || !passes.AIKinds[pp.pass.Kind] {
			continue
		}
		for _, rp := range s.started.Passes {
			if rp.Name == pp.pass.Name && rp.Status != api.StatusSkipped {
				host, hostRP = &s.prep.passes[i], rp
			}
		}
		if host != nil {
			break
		}
	}
	if host == nil {
		return nil, false, nil
	}
	survey := host.rng.Kind == api.RangeSurvey
	changed := false
	if host.cs != nil {
		for _, f := range host.cs.Files {
			if matchesAny(m.Code.Entrypoints, f.Path) {
				changed = true
			}
		}
	}
	if !survey && !changed {
		return nil, false, nil
	}
	files, err := s.env.Repo.FilesAt(ctx, s.prep.headSHA)
	if err != nil {
		return nil, false, err
	}
	var entry []string
	for _, f := range files {
		if matchesAny(m.Code.Entrypoints, f) && !changeset.Excluded(f, m.CodeExclude()) {
			entry = append(entry, f)
			if len(entry) == 400 {
				break
			}
		}
	}
	var existing strings.Builder
	for _, u := range s.plan.Inventory.Units {
		if u.Kind != api.UnitKindAPI {
			fmt.Fprintf(&existing, "- %s (%s) %s\n", u.Key, u.Kind, u.Title)
		}
	}
	kickoff := fmt.Sprintf("Repository %s. Entrypoints: %s.\n\n## Existing product units (reuse their keys)\n%s\n## Entrypoint files\n%s\n",
		s.env.Info.RemoteKey, strings.Join(m.Code.Entrypoints, ", "), firstOf(existing.String(), "(none)\n"), strings.Join(entry, "\n"))
	if kind := unitKind(m); kind != "" {
		kickoff += "\nDefault unit kind: " + kind + "\n"
	}
	h := s.harness(hostRP)
	tools := agent.RepoTools(s.env.Repo, agent.Range{Head: s.prep.headSHA})
	out, err := agent.Submit[agent.UnitsInput](ctx, h, agent.Task{Prompt: prompts.MapUnits, Purpose: api.PurposeMapUnits, Kickoff: kickoff, Tools: tools, Submit: agent.SubmitUnitsTool()})
	if err != nil {
		return nil, false, err
	}
	roles := Roles(m)
	units := make([]api.IngestUnit, 0, len(out.Units))
	for _, u := range out.Units {
		units = append(units, api.IngestUnit{Key: u.Key, Kind: u.Kind, Title: firstOf(u.Title, u.Key), Summary: u.Summary, Audiences: u.Audiences, Roles: roles, SourceRefs: u.SourceRefs, Aliases: u.Aliases})
	}
	return units, true, nil
}

func unitKind(m *config.Manifest) string {
	if m.Code != nil && m.Code.Units != nil && m.Code.Units.Kind != "auto" {
		return m.Code.Units.Kind
	}
	return ""
}

func matchesAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSuffix(strings.TrimPrefix(pat, "./"), "/")
		if p == pat || strings.HasPrefix(p, pat+"/") || changeset.MatchSourceRef(pat, p) {
			return true
		}
	}
	return false
}

func implicitCheck(ctx context.Context, env *Env, opts Options, p *api.Plan, prep *prepared, res *Result) error {
	if !opts.ImplicitCheck {
		return nil
	}
	for _, pp := range p.Passes {
		if pp.Kind == config.KindCheck && pp.Applies {
			return nil
		}
	}
	rng, err := prep.resolver.Resolve(ctx, changeset.RangeInput{Trigger: opts.Trigger, Head: opts.Head, PRTarget: prTarget(opts), PRBase: opts.PRBase, From: opts.From, To: opts.To, WorkingTree: opts.WorkingTree})
	if err != nil {
		return fmt.Errorf("range of the implicit check: %w", err)
	}
	cs, err := prep.builder.For(ctx, rng)
	if err != nil {
		return err
	}
	pp := api.PlanPass{Name: "check", Kind: config.KindCheck, Applies: true, Enabled: true, Target: api.PassTarget{Status: api.TargetNone}, Options: map[string]any{}}
	in := passes.Input{
		Pass: pp, Plan: p, Manifest: env.Manifest, Range: rng, ChangeSet: cs, Builder: prep.builder, Mode: api.ModeDry, Trigger: opts.Trigger,
		FailOn: opts.FailOn, Repo: env.Repo, Info: env.Info, Client: env.Client, Known: passes.NewUnits(p.Inventory.Units), Generator: env.Generator, Log: env.Log.Debugf,
	}
	rep, err := passes.Check{}.Run(ctx, in, &passes.Recorder{})
	r := PassResult{Name: "check", Kind: config.KindCheck, Target: "-", Status: api.StatusSucceeded, Implicit: true, Range: &rng, Report: &rep}
	if err != nil {
		if api.StopsRun(err) || api.IsLicenseError(err) {
			return err
		}
		r.Status, r.Error = api.StatusFailed, err.Error()
	}
	res.Passes = append(res.Passes, r)
	if env.OnPass != nil {
		env.OnPass(r)
	}
	return nil
}

func prTarget(opts Options) string {
	if opts.PR != nil {
		return opts.PR.TargetBranch
	}
	return ""
}
