package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/api"
)

func newCaptureCmd(gf *globalFlags) *cobra.Command {
	var (
		urlFlag         string
		paths           []string
		captureKinds    []string
		space           string
		label           string
		releaseProposal string
		attach          string
		async           bool
		timeout         time.Duration
		pollInterval    time.Duration
		require         bool
		ci              bool
		format          string
		dryRun          bool
		authSecret      string
		maxPages        int
	)
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Launch a Gravity runner to navigate and screenshot an app (preview)",
		Long: `Trigger the Gravity platform's agent runner to navigate an application,
capture pages/screenshots, and attach them to the docs site as a draft +
proposal — e.g. after a release, to capture the new UI.

This is a preview: the platform runner endpoint is not live yet. Until it ships,
the command reports that the feature is unavailable and exits 0 (so it is safe to
add to release CI now); pass --require to make absence a hard error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateTextJSON(format); err != nil {
				return err
			}
			if urlFlag == "" {
				return Failf(CodeError, "--url is required (the app to capture)")
			}
			switch attach {
			case "proposal", "none":
			default:
				return Failf(CodeError, "invalid --attach %q (want proposal|none)", attach)
			}

			e, err := resolveEnv(*gf, space)
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			site, err := e.requireSite()
			if err != nil {
				return err
			}

			req := api.CaptureRequest{
				SpaceSlug: e.cfg.Space,
				Label:     label,
				Target:    api.CaptureTarget{URL: urlFlag, Paths: paths, AuthSecretRef: authSecret},
				Capture:   captureKinds,
				Attach:    &api.CaptureAttach{Mode: attach, ReleaseProposalID: releaseProposal},
				Async:     async,
			}
			if maxPages > 0 {
				req.Scope = &api.CaptureScope{MaxPages: maxPages}
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return writeJSON(out, req)
			}

			run, err := e.client.LaunchCapture(cmd.Context(), site, req)
			if err != nil {
				if skipped, ferr := skippableFeature(err, "the Gravity runner (capture)", cmd.ErrOrStderr(), require); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("launch capture: %w", err))
			}

			logw := logWriter(cmd, ci)
			if async {
				fmt.Fprintf(out, "Launched capture %s (status=%s)\n", run.RunID, run.Status)
				if run.StatusURL != "" {
					fmt.Fprintf(out, "  status: %s\n", run.StatusURL)
				}
				fmt.Fprintf(out, "  poll with: gravity capture status %s\n", run.RunID)
				return nil
			}

			if !run.Done() {
				run, err = pollCapture(cmd.Context(), e.client, site, run.RunID, pollInterval, timeout, logw)
				if err != nil {
					return Fail(CodeError, err)
				}
			}
			writeCaptureReport(out, run, format)
			return captureExit(run)
		},
	}
	cmd.Flags().StringVar(&urlFlag, "url", "", "target app URL to navigate (required)")
	cmd.Flags().StringArrayVar(&paths, "path", nil, "specific route to visit (repeatable)")
	cmd.Flags().StringSliceVar(&captureKinds, "capture", []string{"screenshots", "pages"}, "what to capture: screenshots,pages,dom,console")
	cmd.Flags().StringVar(&space, "space", "", "space to attach artifacts to")
	cmd.Flags().StringVar(&label, "label", "", "human label for the run (e.g. a version)")
	cmd.Flags().StringVar(&releaseProposal, "release-proposal", "", "attach artifacts to this release-notes proposal id")
	cmd.Flags().StringVar(&attach, "attach", "proposal", "proposal|none")
	cmd.Flags().BoolVar(&async, "async", false, "launch and return immediately instead of waiting")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "max time to wait for completion")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 5*time.Second, "status poll interval when waiting")
	cmd.Flags().BoolVar(&require, "require", false, "treat an unavailable runner as a hard error (exit 2)")
	cmd.Flags().BoolVar(&ci, "ci", false, "non-interactive, machine-friendly logs")
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request without launching")
	cmd.Flags().StringVar(&authSecret, "auth-secret", "", "platform-stored login secret ref for the app")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, "cap the number of pages crawled (0 = platform default)")
	cmd.AddCommand(newCaptureStatusCmd(gf))
	return cmd
}

func newCaptureStatusCmd(gf *globalFlags) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status <runId>",
		Short: "Show the status and artifacts of a capture run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTextJSON(format); err != nil {
				return err
			}
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			site, err := e.requireSite()
			if err != nil {
				return err
			}
			run, err := e.client.CaptureStatus(cmd.Context(), site, args[0])
			if err != nil {
				if skipped, ferr := skippableFeature(err, "the Gravity runner (capture)", cmd.ErrOrStderr(), false); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("capture status: %w", err))
			}
			writeCaptureReport(cmd.OutOrStdout(), run, format)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	return cmd
}

// pollCapture polls until the run reaches a terminal state, honouring ctx and a
// timeout. The runId is echoed on timeout so a later `capture status` can resume.
func pollCapture(ctx context.Context, client *api.Client, site, runID string, interval, timeout time.Duration, logw io.Writer) (*api.CaptureRun, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		run, err := client.CaptureStatus(ctx, site, runID)
		if err != nil {
			return nil, fmt.Errorf("poll status for %s: %w", runID, err)
		}
		fmt.Fprintf(logw, "capture: %s pages=%d errors=%d\n", run.Status, run.Stats.PagesVisited, run.Stats.Errors)
		if run.Done() {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out after %s waiting for run %s; resume with `gravity capture status %s`", timeout, runID, runID)
		case <-ticker.C:
		}
	}
}

func writeCaptureReport(out io.Writer, run *api.CaptureRun, format string) {
	if format == "json" {
		_ = writeJSON(out, run)
		return
	}
	fmt.Fprintf(out, "Run:    %s\n", run.RunID)
	fmt.Fprintf(out, "Status: %s\n", run.Status)
	fmt.Fprintf(out, "Pages:  %d  Screenshots: %d  Errors: %d\n", run.Stats.PagesVisited, run.Stats.Screenshots, run.Stats.Errors)
	if run.ReviewURL != "" {
		fmt.Fprintf(out, "Review: %s\n", run.ReviewURL)
	}
	for _, a := range run.Artifacts {
		if a.Status == "error" {
			fmt.Fprintf(out, "  ! %s %s — %s\n", a.Type, a.Path, a.Detail)
		}
	}
	if run.Error != "" {
		fmt.Fprintf(out, "Error: %s\n", run.Error)
	}
}

// captureExit maps a terminal run to an exit code: succeeded=0, partial=1
// (navigation errors, like findings), failed/other=2.
func captureExit(run *api.CaptureRun) error {
	switch run.Status {
	case "succeeded":
		return nil
	case "partial":
		return Fail(CodeFindings, nil)
	case "failed":
		return Failf(CodeError, "capture run failed: %s", orNA(run.Error))
	default:
		return Failf(CodeError, "capture ended in non-terminal state %q", run.Status)
	}
}

func validateTextJSON(format string) error {
	switch format {
	case "text", "json":
		return nil
	default:
		return Failf(CodeError, "invalid --format %q (want text|json)", format)
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return Fail(CodeError, err)
	}
	return nil
}

func orNA(s string) string {
	if s == "" {
		return "(no detail)"
	}
	return s
}
