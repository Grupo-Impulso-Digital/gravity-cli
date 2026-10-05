package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	engine "github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type runView struct {
	a      *app
	yes    bool
	plan   *engine.PlanView
	prog   *ui.Progress
	steps  map[string]int
	mu     sync.Mutex
	lease  int
	seen   map[string]bool
	until  time.Time
	tick   chan struct{}
	ticker sync.WaitGroup
}

func newRunView(a *app, yes bool) *runView {
	return &runView{a: a, yes: yes, steps: map[string]int{}, lease: -1, seen: map[string]bool{}}
}

func (v *runView) attach(env *engine.Env, s *pipelineSession) {
	env.Confirm = v.confirm
	env.OnLease = func(w engine.LeaseWait) { v.onLease(s, w) }
	env.OnStart = v.onStart
	env.OnPass = v.onPass
}

func (v *runView) begin(view *engine.PlanView) {
	if v.prog != nil || !v.a.ui.Interactive() {
		return
	}
	v.prog = v.a.ui.StartProgress("", true)
	if view == nil {
		return
	}
	for _, p := range view.Passes {
		if p.Run {
			v.addStep(p.Name, p.Kind, p.Target)
		}
	}
}

func (v *runView) addStep(name, kind, target string) {
	if v.prog == nil {
		return
	}
	label := name + "  " + v.a.ui.Dim(kind+" → "+target)
	i := v.prog.Add(label)
	v.mu.Lock()
	v.steps[name] = i
	v.mu.Unlock()
}

func (v *runView) stop() {
	v.stopTicker()
	if v.prog != nil {
		v.prog.Stop()
	}
}

func (v *runView) confirm(view engine.PlanView) error {
	v.plan = &view
	p := v.a.ui
	renderPlanView(p, view)
	ai := view.AIPasses()
	if ai > 0 && p.Interactive() && !v.yes {
		label := "Start the real run"
		if view.Mode == api.ModeDry {
			label = "Start the dry run"
		}
		desc := plural(ai, "pass calls", "passes call") + " a model"
		if total, ok := planCost(view); ok {
			desc += fmt.Sprintf(" (about $%.2f)", total)
		}
		answer, err := v.a.prompter().Select(label+"?", desc, []ui.Choice{{Key: "go", Label: label}, {Key: "cancel", Label: "Cancel"}}, "go")
		if err != nil || answer != "go" {
			return engine.ErrDeclined
		}
	}
	v.begin(&view)
	return nil
}

func planCost(view engine.PlanView) (float64, bool) {
	var total float64
	known := false
	for _, p := range view.Passes {
		if p.Run && p.Estimate != nil && p.Estimate.ApproxCostUSD != nil {
			total += *p.Estimate.ApproxCostUSD
			known = true
		}
	}
	return total, known
}

func renderPlanView(p *ui.Printer, view engine.PlanView) {
	mode := "real run"
	if view.Mode == api.ModeDry {
		mode = "dry run"
	}
	sub := mode + " · " + view.Trigger
	if view.Branch != "" {
		sub += " on " + view.Branch
	}
	if view.HeadSHA != "" {
		sub += " @ " + shortSHA(view.HeadSHA)
	}
	if view.Snapshot {
		sub += " · local " + ".gravity.yaml"
	}
	p.Section("Plan", sub)
	rows := make([][]string, 0, len(view.Passes))
	run := 0
	for _, pp := range view.Passes {
		will := p.Paint(ui.ToneOK, "runs")
		if pp.Commits > 0 {
			will += p.Dim(fmt.Sprintf(" · %s", plural(pp.Commits, "commit", "commits")))
		}
		if !pp.Run {
			will = p.Dim("skips: " + skipLabel(pp.Skip))
			if pp.Skip == api.SkipTargetUnapproved {
				will = p.Paint(ui.ToneWarn, "skips: "+skipLabel(pp.Skip)) + p.Dim(" (gravity approve "+pp.Grant+")")
			}
		} else {
			run++
		}
		ai := p.Dim("no")
		if pp.AI {
			ai = p.Paint(ui.ToneAccent, "yes")
		}
		est := "-"
		if pp.Run {
			est = estimateText(pp.Estimate)
		}
		rows = append(rows, []string{pp.Name, pp.Kind, pp.Target, will, ai, est})
	}
	p.Grid("  ", []string{"Pass", "Kind", "Target", "This run", "AI", "Estimate"}, rows)
	total, known := planCost(view)
	line := fmt.Sprintf("%d of %s run", run, plural(len(view.Passes), "pass", "passes"))
	if ai := view.AIPasses(); ai > 0 {
		line += fmt.Sprintf(" · %s a model", plural(ai, "calls", "call"))
		if known {
			line += fmt.Sprintf(" · about $%.2f", total)
		}
	}
	p.Note("  ", ui.MarkInfo, "%s", line)
}

func (v *runView) onLease(s *pipelineSession, w engine.LeaseWait) {
	p := v.a.ui
	id := ""
	if w.Holder != nil {
		id = w.Holder.RunID
	}
	v.mu.Lock()
	first := !v.seen[id]
	v.seen[id] = true
	v.until = w.Deadline
	v.mu.Unlock()
	if first {
		desc := "another run"
		if id != "" {
			desc = "run " + id
			if run, err := s.client.GetRun(context.Background(), id); err == nil {
				where := run.Run.Trigger
				if run.Run.Branch != nil && *run.Run.Branch != "" {
					where += " on " + *run.Run.Branch
				}
				desc += " (" + where + optional(", started "+ago(v.a.now(), run.Run.StartedAt), run.Run.StartedAt != "") + ")"
			}
		}
		p.Always("%s waiting for %s, which holds %s · timeout %s", p.Mark(ui.MarkWarn), desc, firstNonEmpty(s.opts.Branch, "this branch"), w.Timeout.Round(time.Second))
		if id != "" {
			p.Always("  %s if it is stuck: gravity runs cancel %s", p.Dim("hint"), id)
		}
	}
	if v.prog == nil {
		v.begin(v.plan)
	}
	v.mu.Lock()
	if v.lease < 0 && v.prog != nil {
		v.lease = v.prog.Add("Waiting for " + firstNonEmpty(id, "the lease"))
		v.prog.Begin(v.lease)
	}
	v.mu.Unlock()
	if p.Interactive() {
		v.startTicker()
	} else if !first {
		p.Always("  still waiting · %s left", leftText(time.Now(), w.Deadline))
	}
	v.updateLease()
}

func (v *runView) updateLease() {
	v.mu.Lock()
	i, until := v.lease, v.until
	v.mu.Unlock()
	if i >= 0 && v.prog != nil {
		v.prog.Update(i, leftText(time.Now(), until)+" left")
	}
}

func (v *runView) startTicker() {
	v.mu.Lock()
	if v.tick != nil {
		v.mu.Unlock()
		return
	}
	v.tick = make(chan struct{})
	done := v.tick
	v.mu.Unlock()
	v.ticker.Add(1)
	go func() {
		defer v.ticker.Done()
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				v.updateLease()
			}
		}
	}()
}

func (v *runView) stopTicker() {
	v.mu.Lock()
	if v.tick != nil {
		close(v.tick)
		v.tick = nil
	}
	v.mu.Unlock()
	v.ticker.Wait()
}

func (v *runView) onStart(started *api.StartedRun) {
	v.stopTicker()
	v.mu.Lock()
	i := v.lease
	v.lease = -1
	v.mu.Unlock()
	if i >= 0 && v.prog != nil {
		v.prog.Done(i, "lease acquired")
	}
	v.a.ui.Debugf("run %s started", started.Run.ID)
	v.mu.Lock()
	for _, idx := range v.steps {
		if v.prog != nil {
			v.prog.Begin(idx)
		}
	}
	v.mu.Unlock()
}

func (v *runView) onPass(r engine.PassResult) {
	v.mu.Lock()
	i, ok := v.steps[r.Name]
	v.mu.Unlock()
	if !ok || v.prog == nil {
		return
	}
	switch passMark(r) {
	case ui.MarkFail:
		v.prog.Fail(i, passOutcome(r))
	case ui.MarkWarn:
		v.prog.Warn(i, passOutcome(r))
	case ui.MarkSkip:
		v.prog.Skip(i, passOutcome(r))
	default:
		v.prog.Done(i, passOutcome(r))
	}
}

func leftText(now, until time.Time) string {
	d := until.Sub(now).Round(time.Second)
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	return fmt.Sprintf("%d:%02d", m, int(d.Seconds())-60*m)
}

func ago(now time.Time, ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func opMark(p *ui.Printer, op string) string {
	switch op {
	case api.OpCreate:
		return p.Paint(ui.ToneOK, "+ new   ")
	case api.OpUpdate:
		return p.Paint(ui.ToneInfo, "~ update")
	case api.OpImport:
		return p.Paint(ui.ToneAccent, p.Glyph("↓", "v")+" import")
	case api.OpDelete:
		return p.Paint(ui.ToneFail, "- delete")
	case "memory":
		return p.Dim("* memory")
	case "hint":
		return p.Dim("? hint  ")
	}
	return op
}

func pageLabel(p *ui.Printer, pg changePage) string {
	where := strings.Join(append(append([]string{}, pg.CollectionPath...), pg.Slug), "/")
	where = strings.Trim(where, "/")
	label := firstNonEmpty(where, pg.Source)
	if pg.Language != "" {
		label += " [" + pg.Language + "]"
	}
	if pg.Title != "" && pg.Title != pg.Slug {
		label += "  " + pg.Title
	}
	if pg.Source != "" && where != "" {
		label += "  " + p.Dim(p.Glyph("←", "<-")+" "+pg.Source)
	}
	if pg.Note != "" {
		label += "  " + p.Dim(pg.Note)
	}
	return label
}

func (a *app) renderResult(res *engine.Result, data *runData, _ *pipelineSession) {
	renderRunResult(a.ui, res, data, func() { a.manualHint(res) })
}

func renderRunResult(p *ui.Printer, res *engine.Result, data *runData, before func()) {
	dry := res.Mode == api.ModeDry
	title := "Result"
	sub := "nothing was written to Gravity"
	if !dry {
		sub = "sent to Gravity"
	}
	if data.SentFrom != "" {
		sub = "sent from dry run " + data.SentFrom
	}
	p.Section(title, sub)
	byPass := map[string][]changePage{}
	for _, pg := range data.Pages {
		byPass[pg.Pass] = append(byPass[pg.Pass], pg)
	}
	for _, ps := range res.Passes {
		head := p.Bold(ps.Name) + "  " + p.Dim(ps.Kind+" → "+ps.Target)
		outcome := passOutcome(ps)
		mark := passMark(ps)
		p.Println("  %s %s  %s", p.Mark(mark), head, outcome)
		for _, pg := range byPass[ps.Name] {
			p.Println("      %s %s", opMark(p, pg.Op), pageLabel(p, pg))
		}
		if ps.Report != nil {
			for _, w := range ps.Report.Warnings {
				p.Println("      %s %s", p.Mark(ui.MarkWarn), w)
			}
			for _, f := range ps.Report.Findings {
				loc := ""
				if f.File != "" {
					loc = " (" + f.File + optional(fmt.Sprintf(":%d", f.Line), f.Line > 0) + ")"
				}
				p.Println("      %s %s%s", p.Mark(findingMark(f)), f.Title, loc)
			}
			for _, e := range ps.Report.Errors {
				p.Println("      %s %s", p.Mark(ui.MarkFail), e)
			}
		}
	}
	errs := 0
	for _, is := range data.Issues {
		if is.Severity == severityError {
			errs++
			p.Println("  %s %s  %s", p.Mark(ui.MarkFail), p.Paint(ui.ToneFail, is.Code), is.Message)
		}
	}
	ran, skipped, failed, changes := 0, 0, 0, len(data.Pages)
	for _, ps := range res.Passes {
		switch ps.Status {
		case api.StatusFailed:
			failed++
		case api.StatusSkipped:
			skipped++
		default:
			ran++
		}
	}
	lines := []string{fmt.Sprintf("%s ran, %d skipped, %d failed · %s · $%.2f", plural(ran, "pass", "passes"), skipped, failed, plural(changes, "change", "changes"), data.CostUSD)}
	var links []ui.Link
	runID := ""
	if res.Run != nil {
		runID = res.Run.ID
	}
	switch {
	case dry && data.Recording != "":
		lines = append(lines, "Recorded in "+data.Recording)
		if errs > 0 {
			lines = append(lines, p.Paint(ui.ToneFail, fmt.Sprintf("%s would make sending fail; fix them and dry-run again", plural(errs, "validation error", "validation errors"))))
		} else {
			lines = append(lines, "Send it:  gravity run --send "+runID)
		}
	case dry:
		lines = append(lines, "No change to send")
	default:
		if res.Finish != nil && res.Finish.Bundle.Changes > 0 {
			lines = append(lines, fmt.Sprintf("%s awaiting review", plural(res.Finish.Bundle.Changes, "change", "changes")))
		}
		if runID != "" {
			lines = append(lines, "Follow it:  gravity runs show "+runID)
		}
		if data.ReviewURL != "" {
			links = append(links, ui.Link{Label: "Change request", URL: data.ReviewURL})
		}
	}
	for _, ps := range res.Passes {
		if ps.SkipReason == api.SkipTargetUnapproved && ps.Grant != "" {
			line := "Grant " + ps.Grant + " for " + ps.Name + ":  gravity approve " + ps.Grant
			if ps.Why != "" {
				line += "  (" + ps.Why + ")"
			}
			lines = append(lines, line)
		}
	}
	title = "Dry run finished"
	if !dry {
		title = "Run finished"
		if res.Finish != nil && res.Finish.Run.Status != "" && res.Finish.Run.Status != api.StatusSucceeded {
			title = "Run " + res.Finish.Run.Status
		}
	}
	if before != nil {
		before()
	}
	p.Println("")
	p.Card(title, lines, links)
}

func renderRecording(p *ui.Printer, rec *engine.Recording) {
	p.Section("Dry run "+rec.RunID, fmt.Sprintf("recorded %s on %s @ %s · $%.2f spent", rec.CreatedAt, rec.Branch, shortSHA(rec.HeadSHA), rec.CostUSD))
	for _, ps := range rec.Passes {
		pages := recordedPages(ps.Name, ps.Recorded)
		p.Println("  %s %s  %s", p.Mark(ui.MarkOK), p.Bold(ps.Name), p.Dim(ps.Kind+" → "+ps.Target+" · "+plural(len(pages), "change", "changes")))
		for _, pg := range pages {
			p.Println("      %s %s", opMark(p, pg.Op), pageLabel(p, pg))
		}
	}
}
