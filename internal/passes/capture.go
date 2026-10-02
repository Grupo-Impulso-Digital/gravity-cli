package passes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

// Capture polling defaults.
const (
	CapturePoll      = 5 * time.Second
	CaptureStartWait = 2 * time.Minute
	CaptureTimeout   = 30 * time.Minute
)

// Capture runs the server-side Doc Agent with the pass's composed instructions and waits for it.
type Capture struct{}

// Kind is capture.
func (Capture) Kind() string { return config.KindCapture }

var finalCapture = map[string]bool{"succeeded": true, "completed": true, "failed": true, "cancelled": true, "error": true} //nolint:misspell // platform status value

// Run waits for the Doc Agent run the platform starts for this pass run, then reports it.
func (Capture) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	if out.Dry() {
		rep.Summary = "capture runs only in write mode"
		return rep, nil
	}
	sleep := in.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	var waited time.Duration
	for {
		run, err := in.Client.GetRun(ctx, in.RunID)
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return rep, err
			}
			return rep, fmt.Errorf("poll run: %w", err)
		}
		var cr *api.CaptureRun
		for i := range run.CaptureRuns {
			if run.CaptureRuns[i].RunPassID == in.RunPassID {
				cr = &run.CaptureRuns[i]
			}
		}
		switch {
		case cr == nil && waited >= CaptureStartWait:
			return rep, errors.New("the platform did not start a Doc Agent run for this pass (check the agent module and the space's Doc Agent connection)")
		case cr != nil && finalCapture[cr.Status]:
			rep.Summary = fmt.Sprintf("Doc Agent run %s %s", cr.DocAgentRunID, cr.Status)
			if cr.Status != "succeeded" && cr.Status != "completed" {
				return rep, fmt.Errorf("doc agent run %s %s", cr.DocAgentRunID, cr.Status)
			}
			return rep, nil
		case waited >= CaptureTimeout:
			return rep, fmt.Errorf("doc agent run still %s after %s", cr.Status, CaptureTimeout)
		}
		if err := sleep(ctx, CapturePoll); err != nil {
			return rep, err
		}
		waited += CapturePoll
	}
}
