package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

var retiredCaptureFlags = map[string]string{
	"url":              "the Doc Agent takes its target from the space's connection — use --connection <label>",
	"path":             "the Doc Agent takes its route list from the brief — use --brief/--brief-file",
	"capture":          "the Doc Agent decides what to capture from the brief — use --brief/--brief-file",
	"max-pages":        "scope the run through the brief — use --brief/--brief-file",
	"auth-secret":      "credentials live on the platform connection — use --connection <label>",
	"attach":           "every run lands as a draft + open proposal; there is nothing to configure",
	"release-proposal": "every run lands as a draft + open proposal; there is nothing to configure",
}

func newCaptureCmd(gf *globalFlags) *cobra.Command {
	var (
		space        string
		label        string
		connection   string
		brief        string
		briefFile    string
		async        bool
		timeout      time.Duration
		pollInterval time.Duration
		require      bool
		format       string
		dryRun       bool
		retired      struct {
			url             string
			paths           []string
			captureKinds    []string
			attach          string
			releaseProposal string
			authSecret      string
			maxPages        int
		}
	)
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Trigger a Doc Agent run against a connected app",
		Long: `Trigger the Gravity platform's Doc Agent to navigate a connected application,
capture what it finds, and write it back as a draft + open proposal — e.g. after
a release, to refresh the documented UI.

The run's target comes from the space's platform-stored connection (--connection)
and its brief (--brief / --brief-file); credentials never leave the platform.

Where the platform has no Doc Agent configured, the command reports it and exits
0 (pass --require to make absence a hard error).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateTextJSON(format); err != nil {
				return err
			}
			for name, replacement := range retiredCaptureFlags {
				if cmd.Flags().Changed(name) {
					return Failf(CodeError, "--%s was removed with the capture contract it described: %s", name, replacement)
				}
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
			if e.cfg.Space == "" {
				return Failf(CodeError, "no space configured: pass --space or set spaces.default in .gravity.yaml")
			}
			text, err := resolveBrief(brief, briefFile)
			if err != nil {
				return Fail(CodeError, err)
			}
			if connection == "" && label != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: --label is deprecated; it is being sent as the connection label — prefer --connection")
				connection = label
			}

			req := api.DocAgentRunRequest{
				SpaceSlug:       e.cfg.Space,
				ConnectionLabel: connection,
				Brief:           text,
				Async:           async,
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return writeJSON(out, req)
			}
			if skip, gerr := e.gateFeature(cmd.Context(), featureDocAgentRuns, "the Doc Agent", cmd.ErrOrStderr(), require); skip || gerr != nil {
				return gerr
			}
			req.Repo = attributedRepo(cmd.Context(), e)

			run, err := e.client.StartDocAgentRun(cmd.Context(), site, req)
			if err != nil {
				if skipped, ferr := skippableFeature(err, "the Doc Agent", cmd.ErrOrStderr(), require); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("start doc agent run: %w", err))
			}

			logw := logWriter(cmd)
			if async {
				fmt.Fprintf(out, "Launched Doc Agent run %s (status=%s)\n", run.RunID, run.Status)
				if run.StatusURL != "" {
					fmt.Fprintf(out, "  status: %s\n", run.StatusURL)
				}
				fmt.Fprintf(out, "  poll with: gravity capture status %s\n", run.RunID)
				return nil
			}

			if !run.Done() {
				run, err = pollDocAgentRun(cmd.Context(), e.client, site, run.RunID, pollInterval, timeout, logw)
				if err != nil {
					return Fail(CodeError, err)
				}
			}
			writeDocAgentReport(out, run, format)
			return docAgentExit(run)
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "space the run writes into")
	cmd.Flags().StringVar(&connection, "connection", "", "label of the platform connection the agent navigates")
	cmd.Flags().StringVar(&brief, "brief", "", "extra instructions merged over the space's brief")
	cmd.Flags().StringVar(&briefFile, "brief-file", "", "repo-relative file whose contents are used as the brief")
	cmd.Flags().StringVar(&label, "label", "", "deprecated: sent as --connection")
	cmd.Flags().BoolVar(&async, "async", false, "launch and return immediately instead of waiting")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "max time to wait for completion")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 5*time.Second, "status poll interval when waiting")
	cmd.Flags().BoolVar(&require, "require", false, "treat an unavailable Doc Agent as a hard error (exit 2)")
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the request without launching")

	cmd.Flags().StringVar(&retired.url, "url", "", "removed")
	cmd.Flags().StringArrayVar(&retired.paths, "path", nil, "removed")
	cmd.Flags().StringSliceVar(&retired.captureKinds, "capture", nil, "removed")
	cmd.Flags().StringVar(&retired.attach, "attach", "", "removed")
	cmd.Flags().StringVar(&retired.releaseProposal, "release-proposal", "", "removed")
	cmd.Flags().StringVar(&retired.authSecret, "auth-secret", "", "removed")
	cmd.Flags().IntVar(&retired.maxPages, "max-pages", 0, "removed")
	for name := range retiredCaptureFlags {
		_ = cmd.Flags().MarkHidden(name)
	}

	cmd.AddCommand(newCaptureStatusCmd(gf))
	return cmd
}

func attributedRepo(ctx context.Context, e *env) *api.RepoRef {
	feats, err := e.features(ctx)
	if err != nil || !feats[featureRepos] {
		return nil
	}
	return localRepoRef(ctx, e.proj)
}

func resolveBrief(brief, briefFile string) (string, error) {
	if brief != "" && briefFile != "" {
		return "", errors.New("--brief and --brief-file are mutually exclusive")
	}
	if briefFile == "" {
		return brief, nil
	}
	clean, err := pathsafe.Rel(briefFile)
	if err != nil {
		return "", fmt.Errorf("--brief-file %q: %w", briefFile, err)
	}
	b, err := os.ReadFile(clean)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", clean, err)
	}
	return string(b), nil
}

func newCaptureStatusCmd(gf *globalFlags) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status <runId>",
		Short: "Show the status and artifacts of a Doc Agent run",
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
			if skip, gerr := e.gateFeature(cmd.Context(), featureDocAgentRuns, "the Doc Agent", cmd.ErrOrStderr(), false); skip || gerr != nil {
				return gerr
			}
			run, err := e.client.DocAgentRunStatus(cmd.Context(), site, args[0])
			if err != nil {
				if skipped, ferr := skippableFeature(err, "the Doc Agent", cmd.ErrOrStderr(), false); skipped || ferr != nil {
					return ferr
				}
				return Fail(CodeError, fmt.Errorf("doc agent run status: %w", err))
			}
			writeDocAgentReport(cmd.OutOrStdout(), run, format)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text|json")
	return cmd
}

func pollDocAgentRun(ctx context.Context, client *api.Client, site, runID string, interval, timeout time.Duration, logw io.Writer) (*api.DocAgentRun, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		run, err := client.DocAgentRunStatus(ctx, site, runID)
		if err != nil {
			return nil, fmt.Errorf("poll status for %s: %w", runID, err)
		}
		fmt.Fprintf(logw, "doc agent: %s artifacts=%d errors=%d\n", run.Status, runStats(run).Artifacts, runStats(run).Errors)
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

func runStats(run *api.DocAgentRun) api.DocAgentStats {
	if run.Stats == nil {
		return api.DocAgentStats{}
	}
	return *run.Stats
}

func writeDocAgentReport(out io.Writer, run *api.DocAgentRun, format string) {
	if format == "json" {
		_ = writeJSON(out, run)
		return
	}
	stats := runStats(run)
	fmt.Fprintf(out, "Run:    %s\n", run.RunID)
	fmt.Fprintf(out, "Status: %s\n", run.Status)
	if run.SpaceSlug != "" {
		fmt.Fprintf(out, "Space:  %s\n", run.SpaceSlug)
	}
	if run.ConnectionLabel != "" {
		fmt.Fprintf(out, "Connection: %s\n", run.ConnectionLabel)
	}
	fmt.Fprintf(out, "Artifacts: %d  Screenshots: %d  Errors: %d\n", stats.Artifacts, stats.Screenshots, stats.Errors)
	if run.ReviewURL != "" {
		fmt.Fprintf(out, "Review: %s\n", run.ReviewURL)
	}
	if run.Error != "" {
		fmt.Fprintf(out, "Error: %s\n", run.Error)
	}
}

func docAgentExit(run *api.DocAgentRun) error {
	switch run.Status {
	case api.RunStatusSucceeded:
		return nil
	case api.RunStatusFailed:
		return Failf(CodeFindings, "doc agent run failed: %s", orNA(run.Error))
	case api.RunStatusCancelled:
		return Failf(CodeFindings, "doc agent run ended as %s", run.Status)
	default:
		return Failf(CodeError, "doc agent run ended in non-terminal state %q", run.Status)
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
