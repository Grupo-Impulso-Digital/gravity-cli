package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

const (
	defaultInterval = 5 * time.Second
	slowDownStep    = 5 * time.Second
)

// DeviceLoginOptions configures the device flow.
type DeviceLoginOptions struct {
	Org      string
	Hostname string
	Prompt   func(start *api.DeviceStart)
	Sleep    func(ctx context.Context, d time.Duration) error
}

// DeviceLogin runs the device authorization flow: start, show the code, poll until approved.
func DeviceLogin(ctx context.Context, client *api.Client, opts DeviceLoginOptions) (*api.DevicePoll, error) {
	host := opts.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	start, err := client.StartDeviceLogin(ctx, api.DeviceStartRequest{
		ClientName:    "gravity-cli",
		ClientVersion: version.String(),
		OS:            runtime.GOOS,
		Hostname:      host,
		Org:           opts.Org,
	})
	if err != nil {
		return nil, fmt.Errorf("start device login: %w", err)
	}
	if opts.Prompt != nil {
		opts.Prompt(start)
	}
	sleep := opts.Sleep
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
	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	lifetime := time.Duration(start.ExpiresIn) * time.Second
	var waited time.Duration
	for {
		if lifetime > 0 && waited >= lifetime {
			return nil, api.ErrDeviceExpired
		}
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
		waited += interval
		poll, err := client.PollDeviceLogin(ctx, start.DeviceCode)
		if err != nil {
			if errors.Is(err, api.ErrDeviceDenied) || errors.Is(err, api.ErrDeviceExpired) {
				return nil, err
			}
			return nil, fmt.Errorf("poll device login: %w", err)
		}
		switch poll.Status {
		case api.DeviceApproved:
			if poll.Token == "" {
				return nil, errors.New("the platform approved the login but returned no token")
			}
			return poll, nil
		case api.DeviceSlowDown:
			if poll.Interval > 0 {
				interval = time.Duration(poll.Interval) * time.Second
			} else {
				interval += slowDownStep
			}
		case api.DevicePending:
		default:
			return nil, fmt.Errorf("unexpected device login status %q", poll.Status)
		}
	}
}
