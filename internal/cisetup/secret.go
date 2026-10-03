package cisetup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Runner runs a command with stdin and returns its combined output.
type Runner func(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error)

// ExecRunner runs commands found on PATH.
func ExecRunner(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("%s is not installed: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}

// Installer stores the repository token in the CI provider's secret store through its CLI.
type Installer struct {
	Provider string `json:"provider"`
	Tool     string `json:"tool"`
	Repo     string `json:"repo"`
	run      Runner
}

// FindInstaller returns the installer for provider when its CLI is installed and signed in to the remote's host.
func FindInstaller(ctx context.Context, provider, remoteKey string, run Runner) *Installer {
	if run == nil {
		run = ExecRunner
	}
	host, rest, ok := strings.Cut(remoteKey, "/")
	if !ok || rest == "" {
		return nil
	}
	switch provider {
	case GitHub:
		if _, err := run(ctx, nil, "gh", "auth", "status", "--hostname", host); err != nil {
			return nil
		}
		repo := rest
		if host != "github.com" {
			repo = host + "/" + rest
		}
		return &Installer{Provider: provider, Tool: "gh", Repo: repo, run: run}
	case GitLab:
		if _, err := run(ctx, nil, "glab", "auth", "status", "--hostname", host); err != nil {
			return nil
		}
		repo := rest
		if host != "gitlab.com" {
			repo = "https://" + host + "/" + rest
		}
		return &Installer{Provider: provider, Tool: "glab", Repo: repo, run: run}
	}
	return nil
}

// Install writes the token into the secret named name; the token travels on stdin, never in argv.
func (i *Installer) Install(ctx context.Context, name, token string) error {
	var args []string
	switch i.Tool {
	case "gh":
		args = []string{"secret", "set", name, "--repo", i.Repo}
	case "glab":
		args = []string{"variable", "set", name, "--masked", "--repo", i.Repo}
	default:
		return fmt.Errorf("no secret installer for %s", i.Provider)
	}
	out, err := i.run(ctx, strings.NewReader(token), i.Tool, args...)
	if err != nil {
		msg := strings.TrimSpace(strings.ReplaceAll(string(out), token, "***"))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// Exists reports whether the secret is already set; only names are listed, never values.
func (i *Installer) Exists(ctx context.Context, name string) (bool, error) {
	var args []string
	switch i.Tool {
	case "gh":
		args = []string{"secret", "list", "--repo", i.Repo}
	case "glab":
		args = []string{"variable", "list", "--repo", i.Repo}
	default:
		return false, fmt.Errorf("no secret installer for %s", i.Provider)
	}
	out, err := i.run(ctx, nil, i.Tool, args...)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true, nil
		}
	}
	return false, nil
}

// Describe returns the command shown in the preview.
func (i *Installer) Describe(name string) string {
	switch i.Tool {
	case "gh":
		return "gh secret set " + name + " --repo " + i.Repo
	case "glab":
		return "glab variable set " + name + " --masked --repo " + i.Repo
	}
	return ""
}
