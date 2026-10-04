package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type approveData struct {
	Pending  []api.PendingApproval `json:"pending"`
	Approved []api.ApprovalOutcome `json:"approved"`
	Refused  []api.ApprovalOutcome `json:"refused"`
}

func newApproveCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "approve [pass]...",
		Short: "Approve pass targets that wait for approval",
		Long: "A pass writes into a space only once its target is approved. Without arguments, approve lists the pending targets and whether you may approve them; name passes (or --all) to approve them.\n" +
			"Approving needs a user token with the right to write to the target; a repository token cannot approve.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.approve(cmd.Context(), args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "approve every pending target you may approve")
	return cmd
}

func (a *app) approve(ctx context.Context, names []string, all bool) error {
	s, err := a.openSession(ctx)
	if err != nil {
		return err
	}
	repo := repoParam(s.who, s.info)
	d := approveData{Pending: []api.PendingApproval{}, Approved: []api.ApprovalOutcome{}, Refused: []api.ApprovalOutcome{}}
	pending, err := s.client.Approvals(ctx, repo)
	if api.IsUnsupported(err) {
		return a.approveFallback(ctx, s)
	}
	if err != nil {
		return explainAPI(err)
	}
	d.Pending = pending.Pending
	p := a.ui
	if len(names) == 0 && !all {
		if len(d.Pending) == 0 {
			p.Note("", ui.MarkOK, "no target waits for approval")
			return a.ui.Result(d)
		}
		rows := make([][]string, 0, len(d.Pending))
		for _, pa := range d.Pending {
			may := p.Paint(ui.ToneOK, "yes")
			if !pa.MayApprove {
				may = p.Paint(ui.ToneWarn, "no") + " " + p.Dim(pa.Reason)
			}
			rows = append(rows, []string{pa.Pass, pa.Target, may})
		}
		p.Section("Pending targets", "")
		p.Grid("  ", []string{"Pass", "Target", "You may approve"}, rows)
		p.Println("")
		p.Note("", ui.MarkInfo, "approve with %s or %s", p.Bold("gravity approve <pass>"), p.Bold("gravity approve --all"))
		return a.ui.Result(d)
	}
	if s.who.Principal != nil && s.who.Principal.Kind != api.TokenKindUser {
		return &ExitError{Code: CodeError, ErrCode: "user_token_required", Err: errors.New("approving targets needs a user token: run `gravity login` (a repository or organization token cannot approve)")}
	}
	res, err := s.client.Approve(ctx, repo, api.ApproveRequest{Passes: names, All: all})
	if err != nil {
		return explainAPI(err)
	}
	if res.Approved != nil {
		d.Approved = res.Approved
	}
	if res.Refused != nil {
		d.Refused = res.Refused
	}
	for _, o := range d.Approved {
		p.Note("", ui.MarkOK, "%s → %s approved", o.Pass, o.Target)
	}
	for _, o := range d.Refused {
		p.Note("", ui.MarkFail, "%s: %s", o.Pass, firstNonEmpty(o.Reason, "refused"))
	}
	if len(d.Refused) > 0 {
		msg := plural(len(d.Refused), "target was", "targets were") + " not approved"
		if ferr := a.ui.Failure(ui.ErrorInfo{Code: "approval_refused", Message: msg, ExitCode: CodeFindings}, d); ferr != nil {
			return ferr
		}
		return &ExitError{Code: CodeFindings, ErrCode: "approval_refused", Err: errors.New(msg)}
	}
	return a.ui.Result(d)
}

func (a *app) approveFallback(ctx context.Context, s *session) error {
	p := a.ui
	q := api.PlanQuery{Repo: repoParam(s.who, s.info), Trigger: config.TriggerManual, Branch: firstNonEmpty(s.info.branch, s.info.defaultBranch), Mode: api.ModeDry}
	if s.manifest != nil {
		q.ManifestHash = s.manifest.Hash
	}
	plan, err := s.client.Plan(ctx, q)
	if err != nil {
		return explainAPI(err)
	}
	d := approveData{Pending: []api.PendingApproval{}, Approved: []api.ApprovalOutcome{}, Refused: []api.ApprovalOutcome{}}
	var links []ui.Link
	for _, pp := range plan.Passes {
		if pp.Target.Status != api.TargetUnapproved {
			continue
		}
		d.Pending = append(d.Pending, api.PendingApproval{Pass: pp.Name, Target: pp.Target.Ref, Status: pp.Target.Status, ApproveURL: pp.Target.ApproveURL})
		links = append(links, ui.Link{Label: pp.Name, URL: pp.Target.ApproveURL})
	}
	if len(d.Pending) == 0 {
		p.Note("", ui.MarkOK, "no target waits for approval")
		return a.ui.Result(d)
	}
	names := make([]string, 0, len(d.Pending))
	for _, pa := range d.Pending {
		names = append(names, pa.Pass)
	}
	a.ui.Warn("approve_unsupported", "this Gravity server approves targets only in the app")
	p.Card("Approve in the app: "+strings.Join(names, ", "), nil, links)
	return a.ui.Result(d)
}
