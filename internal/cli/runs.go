package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type runsData struct {
	Runs []api.RunSummary `json:"runs"`
}

func newRunsCmd(a *app) *cobra.Command {
	var watch bool
	var limit int
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Recent runs of this repository (show, cancel)",
		Long:  "List the recent runs with their status, trigger, branch, changes and cost. --watch keeps the list live until every run has finished.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.listRuns(cmd.Context(), limit, watch)
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "refresh until no run is running")
	cmd.Flags().IntVar(&limit, "limit", 10, "how many runs to list")
	cmd.AddCommand(newRunsShowCmd(a), newRunsCancelCmd(a))
	return cmd
}

func (a *app) fetchRuns(ctx context.Context, s *session, limit int) ([]api.RunSummary, error) {
	repo := repoParam(s.who, s.info)
	runs, err := s.client.RepoRuns(ctx, repo, "", limit)
	if err == nil {
		return runs, nil
	}
	if !api.IsUnsupported(err) {
		return nil, explainAPI(err)
	}
	st, err := s.client.Status(ctx, repo, limit)
	if err != nil {
		return nil, explainAPI(err)
	}
	return st.Runs, nil
}

func (a *app) listRuns(ctx context.Context, limit int, watch bool) error {
	s, err := a.openSession(ctx)
	if err != nil {
		return err
	}
	runs, err := a.fetchRuns(ctx, s, limit)
	if err != nil {
		return err
	}
	if watch && a.ui.Interactive() {
		for {
			renderRuns(a.ui, runs, a.now())
			if !anyRunning(runs) {
				break
			}
			if err := sleepFor(ctx, a, 3*time.Second); err != nil {
				return err
			}
			if runs, err = a.fetchRuns(ctx, s, limit); err != nil {
				return err
			}
		}
		return a.ui.Result(runsData{Runs: runs})
	}
	for watch && anyRunning(runs) {
		if err := sleepFor(ctx, a, 5*time.Second); err != nil {
			return err
		}
		if runs, err = a.fetchRuns(ctx, s, limit); err != nil {
			return err
		}
	}
	renderRuns(a.ui, runs, a.now())
	if runs == nil {
		runs = []api.RunSummary{}
	}
	return a.ui.Result(runsData{Runs: runs})
}

func sleepFor(ctx context.Context, a *app, d time.Duration) error {
	if a.sleep != nil {
		return a.sleep(ctx, d)
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

func anyRunning(runs []api.RunSummary) bool {
	for _, r := range runs {
		if r.Status == api.StatusRunning || r.Status == api.StatusPending {
			return true
		}
	}
	return false
}

func statusMark(status string) string {
	switch status {
	case api.StatusSucceeded:
		return ui.MarkOK
	case api.StatusFailed, api.StatusAbandoned:
		return ui.MarkFail
	case api.StatusPartial:
		return ui.MarkWarn
	case api.StatusCancelled, api.StatusSkipped:
		return ui.MarkSkip
	}
	return ui.MarkInfo
}

func renderRuns(p *ui.Printer, runs []api.RunSummary, now time.Time) {
	p.Section("Runs", plural(len(runs), "run", "runs"))
	if len(runs) == 0 {
		p.Println("  No runs yet. Try %s.", p.Bold("gravity run --dry-run"))
		return
	}
	rows := make([][]string, 0, len(runs))
	for _, r := range runs {
		where := r.Trigger
		if r.Branch != "" {
			where += " · " + r.Branch
		}
		mode := "real run"
		if r.Mode == api.ModeDry {
			mode = "dry run"
		}
		rows = append(rows, []string{p.Mark(statusMark(r.Status)) + " " + r.Status, r.ID, mode, where, ago(now, r.StartedAt), fmt.Sprintf("%d", r.Changes), fmt.Sprintf("$%.2f", r.CostUSD)})
	}
	p.Grid("  ", []string{"Status", "Run", "Mode", "Trigger", "Started", "Changes", "Cost"}, rows)
}

func newRunsShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <runId>",
		Short: "One run: status per pass, progress, lease and the change request",
		Long:  "The authoritative state of a run. A run has finished only when this says succeeded, partial, failed or cancelled.", //nolint:misspell // platform status value
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.openSession(ctx)
			if err != nil {
				return err
			}
			id, err := a.resolveRunID(ctx, s, args[0])
			if err != nil {
				return err
			}
			run, err := s.client.GetRun(ctx, id)
			if err != nil {
				return explainAPI(err)
			}
			renderRunDetail(a.ui, run, a.now())
			return a.ui.Result(run)
		},
	}
}

func (a *app) resolveRunID(ctx context.Context, s *session, ref string) (string, error) {
	if ref != "latest" && ref != "" {
		return ref, nil
	}
	runs, err := a.fetchRuns(ctx, s, 1)
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", &ExitError{Code: CodeError, ErrCode: "run_not_found", Err: errors.New("this repository has no runs yet")}
	}
	return runs[0].ID, nil
}

func renderRunDetail(p *ui.Printer, run *api.Run, now time.Time) {
	r := run.Run
	where := r.Trigger
	if r.Branch != nil && *r.Branch != "" {
		where += " on " + *r.Branch
	}
	mode := "real run"
	if r.Mode == api.ModeDry {
		mode = "dry run"
	}
	lines := []string{
		fmt.Sprintf("%s %s · %s · %s @ %s", p.Mark(statusMark(r.Status)), p.Bold(r.Status), mode, where, shortSHA(r.HeadSHA)),
		"Started " + ago(now, r.StartedAt) + optional(" · finished "+ago(now, deref(r.FinishedAt)), r.FinishedAt != nil),
		fmt.Sprintf("%s · %s · $%.2f", plural(r.ChangesCount, "change", "changes"), plural(r.LLMCalls, "model call", "model calls"), r.CostUSD),
	}
	if r.Error != nil && *r.Error != "" {
		lines = append(lines, p.Paint(ui.ToneFail, "Error: "+*r.Error))
	}
	if r.Lease != nil && r.Status == api.StatusRunning {
		lines = append(lines, "Holds lease "+r.Lease.Key+optional(" until "+r.Lease.ExpiresAt, r.Lease.ExpiresAt != ""))
	}
	if r.Progress != nil {
		lines = append(lines, progressText(r.Progress))
	}
	var links []ui.Link
	if run.Bundle.AppURL != "" && run.Bundle.Changes > 0 {
		lines = append(lines, fmt.Sprintf("Change request: %d pending, %d accepted, %d declined", run.Bundle.Changes-run.Bundle.Accepted-run.Bundle.Declined, run.Bundle.Accepted, run.Bundle.Declined))
		links = append(links, ui.Link{Label: "Change request", URL: run.Bundle.AppURL})
	}
	if r.AppURL != "" {
		links = append(links, ui.Link{Label: "Run", URL: r.AppURL})
	}
	p.Card("Run "+r.ID, lines, links)
	rows := make([][]string, 0, len(run.Passes))
	for _, ps := range run.Passes {
		state := p.Mark(statusMark(ps.Status)) + " " + ps.Status
		if ps.SkipReason != nil && *ps.SkipReason != "" {
			state += " " + p.Dim("("+skipLabel(*ps.SkipReason)+")")
		}
		detail := ""
		switch {
		case ps.Progress != nil && ps.Status == api.StatusRunning:
			detail = progressText(ps.Progress)
		case ps.Error != nil && *ps.Error != "":
			detail = oneLine(*ps.Error, 80)
		case ps.WatermarkNote != nil && *ps.WatermarkNote != "":
			detail = *ps.WatermarkNote
		}
		rows = append(rows, []string{ps.Name, ps.Kind, state, fmt.Sprintf("%d", ps.ChangesCount), fmt.Sprintf("$%.2f", ps.CostUSD), detail})
	}
	p.Grid("  ", []string{"Pass", "Kind", "Status", "Changes", "Cost", "Detail"}, rows)
	if r.Status == api.StatusRunning || r.Status == api.StatusPending {
		p.Note("  ", ui.MarkInfo, "still running: gravity runs --watch, or cancel it with gravity runs cancel %s", r.ID)
	}
}

func progressText(pr *api.Progress) string {
	if pr.Total > 0 {
		return fmt.Sprintf("%s (%d/%d)", pr.Step, pr.Done, pr.Total)
	}
	return pr.Step
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func newRunsCancelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <runId>",
		Short: "Cancel a run: its lease is released and its held changes declined",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.openSession(ctx)
			if err != nil {
				return err
			}
			res, err := s.client.CancelRun(ctx, args[0])
			if api.IsUnsupported(err) {
				return &ExitError{Code: CodeError, ErrCode: "server_unsupported", Err: errors.New("this Gravity server cannot cancel runs from the CLI; cancel it in the app (its lease expires on its own)")}
			}
			if err != nil {
				return explainAPI(err)
			}
			a.ui.Note("", ui.MarkOK, "run %s %s", args[0], firstNonEmpty(res.Run.Status, api.StatusCancelled))
			return a.ui.Result(res)
		},
	}
}
