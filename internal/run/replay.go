package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

// RecordingVersion is the format version of a dry-run recording.
const RecordingVersion = 1

// RecordingDir is where dry runs are recorded, relative to the repository root.
const RecordingDir = ".gravity/runs"

// RecordedPass is one pass of a recorded dry run.
type RecordedPass struct {
	Name     string           `json:"name"`
	Kind     string           `json:"kind"`
	Target   string           `json:"target"`
	Range    *changeset.Range `json:"range,omitempty"`
	Summary  string           `json:"summary,omitempty"`
	Counts   passes.Counts    `json:"counts"`
	Warnings []string         `json:"warnings,omitempty"`
	CostUSD  float64          `json:"costUsd"`
	Recorded passes.Recorded  `json:"recorded"`
}

// Recording is a dry run saved so it can be sent without recomputing.
type Recording struct {
	Version      int            `json:"version"`
	RunID        string         `json:"runId"`
	CreatedAt    string         `json:"createdAt"`
	Repo         string         `json:"repo"`
	Branch       string         `json:"branch"`
	HeadSHA      string         `json:"headSha"`
	ManifestHash string         `json:"manifestHash"`
	Trigger      string         `json:"trigger"`
	Product      string         `json:"product,omitempty"`
	CostUSD      float64        `json:"costUsd"`
	Passes       []RecordedPass `json:"passes"`
}

// Changes counts the writes a recording would send.
func (r *Recording) Changes() int {
	n := 0
	for _, p := range r.Passes {
		n += len(p.Recorded.Changes) + len(p.Recorded.Verbatim) + len(p.Recorded.Deletions) + len(p.Recorded.Memories) + len(p.Recorded.Hints)
	}
	return n
}

// NewRecording captures what a finished dry run would have written.
func NewRecording(res *Result, remoteKey, manifestHash string, now time.Time) *Recording {
	rec := &Recording{Version: RecordingVersion, CreatedAt: now.UTC().Format(time.RFC3339), Repo: remoteKey, Branch: res.Branch, HeadSHA: res.HeadSHA, ManifestHash: manifestHash, Trigger: res.Trigger, Passes: []RecordedPass{}}
	if res.Run != nil {
		rec.RunID = res.Run.ID
	}
	if res.Plan != nil {
		rec.Product = res.Plan.Product.Slug
	}
	for _, p := range res.Passes {
		if p.Status != api.StatusSucceeded || p.Report == nil || p.Report.Recorded == nil {
			continue
		}
		rp := RecordedPass{Name: p.Name, Kind: p.Kind, Target: p.Target, Range: p.Range, Summary: p.Report.Summary, Counts: p.Report.Counts, Warnings: p.Report.Warnings, CostUSD: p.CostUSD, Recorded: *p.Report.Recorded}
		rec.CostUSD += p.CostUSD
		rec.Passes = append(rec.Passes, rp)
	}
	return rec
}

// RecordingPath is the file of a recorded dry run.
func RecordingPath(root, runID string) string {
	return filepath.Join(root, filepath.FromSlash(RecordingDir), runID+".json")
}

// SaveRecording writes the recording under the repository root and returns its path.
func SaveRecording(root string, rec *Recording) (string, error) {
	if rec.RunID == "" {
		return "", errors.New("the dry run has no run id")
	}
	p := RecordingPath(root, rec.RunID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(p), err)
	}
	ignore := filepath.Join(root, ".gravity", ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(ignore, []byte("*\n"), 0o644)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode recording: %w", err)
	}
	if err := os.WriteFile(p, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}

// LoadRecording reads a recorded dry run; "latest" picks the newest recording.
func LoadRecording(root, runID string) (*Recording, error) {
	if runID == "latest" || runID == "" {
		dir := filepath.Join(root, filepath.FromSlash(RecordingDir))
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("no recorded dry run in %s: run `gravity run --dry-run` first", RecordingDir)
		}
		type item struct {
			id  string
			mod time.Time
		}
		var items []item
		for _, e := range entries {
			if e.IsDir() || path.Ext(e.Name()) != ".json" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			items = append(items, item{id: strings.TrimSuffix(e.Name(), ".json"), mod: info.ModTime()})
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("no recorded dry run in %s: run `gravity run --dry-run` first", RecordingDir)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
		runID = items[0].id
	}
	if strings.ContainsAny(runID, `/\`) || strings.Contains(runID, "..") {
		return nil, fmt.Errorf("%q is not a run id", runID)
	}
	data, err := os.ReadFile(RecordingPath(root, runID))
	if err != nil {
		return nil, fmt.Errorf("no recorded dry run %s in %s: send only runs recorded on this machine with `gravity run --dry-run`", runID, RecordingDir)
	}
	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("read recording %s: %w", runID, err)
	}
	if rec.Version != RecordingVersion {
		return nil, fmt.Errorf("recording %s has format %d; this gravity reads %d: run the dry run again", runID, rec.Version, RecordingVersion)
	}
	return &rec, nil
}

// Replay sends a recorded dry run as a write run without recomputing anything.
func Replay(ctx context.Context, env *Env, opts Options, rec *Recording) (*Result, error) {
	opts.Mode = api.ModeWrite
	opts.Trigger = firstOf(rec.Trigger, config.TriggerManual)
	opts.Branch = firstOf(opts.Branch, rec.Branch)
	res := &Result{Trigger: opts.Trigger, Branch: opts.Branch, Mode: opts.Mode, HeadSHA: rec.HeadSHA}
	conn := env.Connect
	conn.Context = api.ConnectContext{Trigger: firstOf(opts.ConnectContext, opts.Trigger), Origin: firstOf(opts.Origin, api.OriginLocal)}
	conn.DryRun = false
	connected, err := env.Client.Connect(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	res.Connect = connected
	names := make([]string, 0, len(rec.Passes))
	byName := map[string]RecordedPass{}
	for _, p := range rec.Passes {
		names = append(names, p.Name)
		byName[p.Name] = p
	}
	opts.Passes = names
	snapshot := env.Manifest != nil && !opts.NoSnapshot
	timeout := opts.LeaseTimeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	deadline := env.now().Add(timeout)
	for {
		p, err := fetchPlan(ctx, env, opts, snapshot)
		if err != nil {
			return res, err
		}
		res.Plan = p
		req := replayRequest(env, opts, p, rec)
		if snapshot {
			h := env.Manifest.Hash
			req.ManifestHash = &h
		}
		started, err := env.Client.StartRun(ctx, opts.RepoParam, req)
		switch {
		case snapshot && api.RejectsWriteManifest(err):
			snapshot = false
			env.Log.Warn("manifest_snapshot_unsupported", fmt.Sprintf("this Gravity server runs write runs with the passes stored from %s only", firstOf(p.Repo.AuthoritativeBranch, p.Repo.DefaultBranch, "the default branch")))
			continue
		case errors.Is(err, api.ErrLeaseHeld):
			wait := leaseWait(err)
			if env.now().Add(wait).After(deadline) {
				return res, fmt.Errorf("%w: %s", ErrLeaseTimeout, holderLine(err, opts.Branch, timeout))
			}
			if env.OnLease != nil {
				env.OnLease(LeaseWait{Holder: leaseHolder(err), Wait: wait, Deadline: deadline, Timeout: timeout})
			}
			if err := env.sleep(ctx, wait); err != nil {
				return res, err
			}
			continue
		case err != nil:
			return res, fmt.Errorf("start run: %w", err)
		}
		res.Run = &started.Run
		res.Snapshot = snapshot
		if env.OnStart != nil {
			env.OnStart(started)
		}
		return res, replay(ctx, env, started, byName, rec, res)
	}
}

func replayRequest(env *Env, opts Options, p *api.Plan, rec *Recording) api.StartRunRequest {
	req := api.StartRunRequest{
		ClientKey: env.key(), Trigger: opts.Trigger, Mode: api.ModeWrite, Origin: firstOf(opts.Origin, api.OriginLocal),
		Branch: opts.Branch, HeadSHA: rec.HeadSHA, CI: opts.CI, Note: firstOf(opts.Note, "sent from dry run "+rec.RunID),
		CLI: api.CLIInfo{Version: strings.TrimPrefix(env.Generator, "gravity-cli/")}, PlanHash: p.PlanHash,
	}
	recorded := map[string]RecordedPass{}
	for _, rp := range rec.Passes {
		recorded[rp.Name] = rp
	}
	wmBranch := WatermarkBranch(opts.Trigger, opts.Branch)
	for _, pp := range p.Passes {
		entry := api.RunPassStart{Name: pp.Name}
		rp, ok := recorded[pp.Name]
		switch {
		case !ok:
			entry.Skip = api.SkipNotSelected
		case rp.Range != nil:
			entry.RangeKind, entry.BaseSHA = rp.Range.Kind, rp.Range.Base
			if pp.Watermark != nil && pp.Watermark.Branch == wmBranch {
				seen := pp.Watermark.CommitSHA
				entry.WatermarkSeen = &seen
			}
			if req.BaseSHA == "" {
				req.BaseSHA, req.RangeKind = rp.Range.Base, rp.Range.Kind
			}
		}
		req.Passes = append(req.Passes, entry)
	}
	return req
}

func replay(ctx context.Context, env *Env, started *api.StartedRun, byName map[string]RecordedPass, rec *Recording, res *Result) error {
	assets := passes.NewAssets()
	for _, rp := range started.Passes {
		recd, ok := byName[rp.Name]
		if !ok {
			continue
		}
		base := PassResult{Name: recd.Name, Kind: recd.Kind, Target: recd.Target, RunPassID: rp.RunPassID, Range: recd.Range}
		if rp.Status == api.StatusSkipped {
			base.Status, base.SkipReason = api.StatusSkipped, rp.SkipReason
			res.Passes = append(res.Passes, base)
			emit(env, base)
			continue
		}
		if ctx.Err() != nil {
			return cancelRun(ctx, env, started.Run.ID, res)
		}
		out := replayPass(ctx, env, started.Run.ID, rp, recd, rec.HeadSHA, assets)
		out.Name, out.Kind, out.Target, out.RunPassID, out.Range = base.Name, base.Kind, base.Target, base.RunPassID, base.Range
		res.Passes = append(res.Passes, out)
		emit(env, out)
	}
	if ctx.Err() != nil {
		return cancelRun(ctx, env, started.Run.ID, res)
	}
	fin, err := env.Client.FinishRun(ctx, started.Run.ID, api.FinishRunRequest{Status: finishStatus(res.Passes), Report: &api.FinishReport{Summary: runSummary(res.Passes) + " (sent from dry run " + rec.RunID + ")"}})
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	res.Finish = fin
	return nil
}

func emit(env *Env, r PassResult) {
	if env.OnPass != nil {
		env.OnPass(r)
	}
}

func replayPass(ctx context.Context, env *Env, runID string, rp api.RunPass, recd RecordedPass, head string, assets *passes.Assets) PassResult {
	out := PassResult{Status: api.StatusSucceeded}
	if _, err := env.Client.ReportPass(ctx, runID, rp.RunPassID, api.PassReportRequest{Status: api.StatusRunning}); err != nil {
		env.Log.Debugf("report %s running: %v", recd.Name, err)
	}
	sink := &passes.PlatformSink{W: env.Client, RunID: runID, RunPassID: rp.RunPassID, Assets: assets}
	rep := passes.Report{}
	rep.Summary = recd.Summary
	rep.Counts = recd.Counts
	rep.Warnings = append(rep.Warnings, recd.Warnings...)
	recorded, err := uploadRecordedAssets(ctx, env, sink, recd.Recorded, head)
	var failures []string
	if err != nil {
		failures = append(failures, err.Error())
	}
	for _, c := range recorded.Changes {
		if _, err := sink.Change(ctx, c); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", firstOf(c.Target.Slug, c.Target.PageID, c.Title), err))
		}
	}
	for _, v := range recorded.Verbatim {
		if _, err := sink.Verbatim(ctx, v); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", v.File.Path, err))
		}
	}
	for _, d := range recorded.Deletions {
		if _, err := sink.DeleteVerbatim(ctx, d); err != nil && !api.HasCode(err, api.CodeNotFound) {
			failures = append(failures, fmt.Sprintf("delete %s: %v", d.Path, err))
		}
	}
	if len(recorded.Hints) > 0 {
		if _, err := sink.Hints(ctx, recorded.Hints); err != nil {
			failures = append(failures, fmt.Sprintf("hints: %v", err))
		}
	}
	for _, m := range recorded.Memories {
		if _, err := sink.Memory(ctx, m); err != nil {
			failures = append(failures, fmt.Sprintf("memory %s: %v", m.Title, err))
		}
	}
	status := api.StatusSucceeded
	var msg *string
	if len(failures) > 0 {
		status = api.StatusFailed
		rep.Errors = failures
		m := fmt.Sprintf("%d write(s) failed: %s", len(failures), strings.Join(failures, "; "))
		msg = &m
		out.Error = m
	}
	out.Status = status
	out.Report = &rep
	pr := fitReport(rep.PassReport)
	r, err := env.Client.ReportPass(ctx, runID, rp.RunPassID, api.PassReportRequest{Status: status, Error: msg, Report: &pr})
	if err == nil && r != nil {
		out.CostUSD, out.LLMCalls = recd.CostUSD, r.LLMCalls
	}
	return out
}

var assetTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml"}

func uploadRecordedAssets(ctx context.Context, env *Env, sink *passes.PlatformSink, rec passes.Recorded, head string) (passes.Recorded, error) {
	if len(rec.Assets) == 0 {
		return rec, nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return rec, fmt.Errorf("encode recorded writes: %w", err)
	}
	text := string(data)
	var failed []string
	for _, p := range rec.Assets {
		content, ok, err := env.Repo.FileAt(ctx, head, p)
		if err != nil || !ok {
			failed = append(failed, p)
			continue
		}
		a, err := sink.Asset(ctx, p, assetTypes[strings.ToLower(path.Ext(p))], content)
		if err != nil {
			failed = append(failed, p)
			continue
		}
		quoted, _ := json.Marshal(a.URL)
		text = strings.ReplaceAll(text, "repo:"+p, strings.Trim(string(quoted), `"`))
	}
	var out passes.Recorded
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return rec, fmt.Errorf("decode recorded writes: %w", err)
	}
	if len(failed) > 0 {
		return out, fmt.Errorf("could not upload %s", strings.Join(failed, ", "))
	}
	return out, nil
}
