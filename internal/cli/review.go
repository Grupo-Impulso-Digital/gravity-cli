package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type reviewData struct {
	RunID string `json:"runId"`
	URL   string `json:"url"`
}

func newReviewCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "review [runId|latest]",
		Short: "Open a run's change request in the app",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.openSession(ctx)
			if err != nil {
				return err
			}
			ref := "latest"
			if len(args) == 1 {
				ref = args[0]
			}
			id, err := a.resolveRunID(ctx, s, ref)
			if err != nil {
				return err
			}
			run, err := s.client.GetRun(ctx, id)
			if err != nil {
				return explainAPI(err)
			}
			url := firstNonEmpty(run.Bundle.AppURL, run.Run.AppURL)
			if url == "" {
				return &ExitError{Code: CodeError, ErrCode: "review_unavailable", Err: fmt.Errorf("run %s has no change request link", id)}
			}
			d := reviewData{RunID: id, URL: url}
			a.ui.Note("", ui.MarkInfo, "%s %s · %s", run.Run.ID, run.Run.Status, strings.TrimSpace(url))
			if a.ui.Interactive() && a.openBrowser != nil {
				_ = a.openBrowser(url)
			}
			return a.ui.Result(d)
		},
	}
}
