package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type approveData struct {
	Pending  []api.PendingApproval `json:"pending"`
	Granted  []api.Grant           `json:"granted"`
	Approved []api.ApprovalOutcome `json:"approved"`
	Refused  []api.ApprovalOutcome `json:"refused"`
}

func newApproveCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "approve [site/space]...",
		Short: "Allow this repository to write where a human must say yes",
		Long: "The repository token used in CI may only write where a person allowed it, because anyone who can push could otherwise point .gravity.yaml at any space. " +
			"The allowance is a grant per site/space and covers every pass of the repository that targets that space or a collection in it.\n" +
			"A grant is needed only when a pass would put content in front of readers without review (verbatim imports, auto-accepting passes), writes to the product memory (nucleus), or targets a space that is not public. " +
			"Passes that land as change requests in a public space need none. gravity setup and your own runs grant automatically where you have write access.\n" +
			"Without arguments, approve lists the spaces that wait for a grant and why; name site/space targets (or --all) to grant them. A pass name is accepted too and grants that pass's space. Granting needs a user login with write access to the space.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.approve(cmd.Context(), args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "grant every pending space you may grant")
	return cmd
}

func pendingWhy(pa api.PendingApproval) string {
	if pa.Why != "" {
		return pa.Why
	}
	return api.ApprovalWhy("", pa.Reasons)
}

func grantLine(repo string, pa api.PendingApproval) string {
	line := fmt.Sprintf("Allow %s to write to %s", repo, pa.Target)
	if why := pendingWhy(pa); why != "" {
		line += " — " + why
	}
	return line
}

func (a *app) approve(ctx context.Context, args []string, all bool) error {
	s, err := a.openSession(ctx)
	if err != nil {
		return err
	}
	repo := repoParam(s.who, s.info)
	d := approveData{Pending: []api.PendingApproval{}, Granted: []api.Grant{}, Approved: []api.ApprovalOutcome{}, Refused: []api.ApprovalOutcome{}}
	pending, err := s.client.Approvals(ctx, repo)
	if api.IsUnsupported(err) {
		return a.approveFallback(ctx, s)
	}
	if err != nil {
		return explainAPI(err)
	}
	if pending.Pending != nil {
		d.Pending = pending.Pending
	}
	if pending.Granted != nil {
		d.Granted = pending.Granted
	}
	repoName := firstNonEmpty(pending.Repo.Name, s.info.name)
	p := a.ui
	if len(args) == 0 && !all {
		if len(d.Pending) == 0 {
			p.Note("", ui.MarkOK, "no space waits for a grant; passes that land as change requests in public spaces need none")
			return a.ui.Result(d)
		}
		p.Section("Waiting for a grant", plural(len(d.Pending), "space", "spaces"))
		for _, pa := range d.Pending {
			p.Println("  %s %s", p.Mark(ui.MarkWarn), grantLine(repoName, pa))
			p.Println("      %s %s", p.Dim("passes"), strings.Join(pa.Passes, ", "))
			if !pa.MayApprove {
				p.Println("      %s %s", p.Dim("you   "), p.Paint(ui.ToneWarn, firstNonEmpty(pa.Reason, "you may not grant this")))
			}
		}
		p.Println("")
		p.Note("", ui.MarkInfo, "grant with %s or %s", p.Bold("gravity approve <site/space>"), p.Bold("gravity approve --all"))
		return a.ui.Result(d)
	}
	if s.who.Principal != nil && s.who.Principal.Kind != api.TokenKindUser {
		return &ExitError{Code: CodeError, ErrCode: "user_token_required", Err: errors.New("granting needs a user token: run `gravity login` (a repository or organization token cannot grant)")}
	}
	req := api.ApproveRequest{All: all}
	targets := map[string]bool{}
	for _, pa := range d.Pending {
		targets[pa.Target] = true
	}
	for _, arg := range args {
		if strings.Contains(arg, "/") || targets[arg] {
			req.Spaces = append(req.Spaces, strings.Trim(arg, "/"))
		} else {
			req.Passes = append(req.Passes, arg)
		}
	}
	res, err := s.client.Approve(ctx, repo, req)
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
		p.Note("", ui.MarkOK, "%s may now write to %s (%s)", repoName, o.Target, orDash(strings.Join(o.Passes, ", ")))
	}
	for _, o := range d.Refused {
		p.Note("", ui.MarkFail, "%s: %s", firstNonEmpty(o.Target, o.Pass), firstNonEmpty(o.Message, o.Code, "refused"))
	}
	if len(d.Refused) > 0 {
		msg := plural(len(d.Refused), "grant was", "grants were") + " not made"
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
	d := approveData{Pending: []api.PendingApproval{}, Granted: []api.Grant{}, Approved: []api.ApprovalOutcome{}, Refused: []api.ApprovalOutcome{}}
	var links []ui.Link
	for _, pp := range plan.Passes {
		if pp.Target.Status != api.TargetUnapproved {
			continue
		}
		d.Pending = append(d.Pending, api.PendingApproval{Target: pp.Target.GrantKey(), Passes: []string{pp.Name}, Reasons: pp.Target.Reasons, ApproveURL: pp.Target.ApproveURL})
		links = append(links, ui.Link{Label: pp.Name, URL: pp.Target.ApproveURL})
	}
	if len(d.Pending) == 0 {
		p.Note("", ui.MarkOK, "no target waits for approval")
		return a.ui.Result(d)
	}
	names := make([]string, 0, len(d.Pending))
	for _, pa := range d.Pending {
		names = append(names, pa.Passes...)
	}
	a.ui.Warn("approve_unsupported", "this Gravity server approves targets only in the app")
	p.Card("Approve in the app: "+strings.Join(names, ", "), nil, links)
	return a.ui.Result(d)
}
