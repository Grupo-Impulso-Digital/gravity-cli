package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func newPingCmd(gf *globalFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Announce this repo + CLI to the platform (setup handshake)",
		Long: `Send a one-shot setup handshake to POST /api/v1/setup/ping so the Gravity web
app — which just walked you through install and ` + "`gravity init`" + ` — can confirm the
token works and surface this repo's resolved configuration.

The ping reports the CLI version/platform, the .gravity.yaml connection config
(API URL, site, space), and the repo's name/remote/branch. Nothing is written to
the platform: the server echoes back your organization, the token's key hint, and
the default site. Exits 2 on an auth or network failure.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			req := buildPingRequest(cmd.Context(), e)
			return runPing(cmd.Context(), e.client, req, jsonOut, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the ping request + response as JSON")
	return cmd
}

// runPing posts the handshake and renders the result. It is split from the
// command so the network round-trip is testable with a pre-built request.
func runPing(ctx context.Context, client *api.Client, req api.SetupPingRequest, jsonOut bool, out io.Writer) error {
	resp, err := client.SetupPing(ctx, req)
	if err != nil {
		return Fail(CodeError, fmt.Errorf("setup ping failed: %w", classifyAuthErr(err)))
	}
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Request  api.SetupPingRequest   `json:"request"`
			Response *api.SetupPingResponse `json:"response"`
		}{req, resp})
	}
	printPing(out, req, resp)
	return nil
}

// buildPingRequest assembles the handshake from the running binary, the resolved
// config, and best-effort git facts about the current repo.
func buildPingRequest(ctx context.Context, e *env) api.SetupPingRequest {
	return api.SetupPingRequest{
		CLI: api.PingCLI{
			Version: version,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
		},
		Config: api.PingConfig{
			APIURL: e.cfg.APIURL,
			Site:   e.cfg.Site,
			Space:  e.cfg.Space,
		},
		Repo: detectRepo(ctx, e.proj),
	}
}

// detectRepo reads the current repo's remote and branch best-effort — a missing
// git binary, no repo, or no remote all degrade to empty fields rather than
// failing the handshake.
func detectRepo(ctx context.Context, proj *config.Project) api.PingRepo {
	var repo api.PingRepo
	cwd, err := os.Getwd()
	if err != nil {
		return repo
	}
	if r, err := git.Open(ctx, cwd); err == nil {
		if remote, _ := r.RemoteURL(ctx); remote != "" {
			repo.Remote = normalizeRemote(remote)
		}
		if branch, _ := r.CurrentBranch(ctx); branch != "" {
			repo.Branch = branch
		}
	}
	repo.Name = repoName(proj, repo.Remote, cwd)
	return repo
}

// repoName prefers the manifest's declared repo identity (its unique name within
// the product), falling back to the remote's last path segment, then the working
// directory's base name.
func repoName(proj *config.Project, remote, cwd string) string {
	if proj != nil && proj.Product.Repo != "" {
		return proj.Product.Repo
	}
	if remote != "" {
		if i := strings.LastIndex(remote, "/"); i >= 0 && i+1 < len(remote) {
			return remote[i+1:]
		}
	}
	return filepath.Base(cwd)
}

// normalizeRemote reduces a git remote URL to a stable host/path identity so the
// same repo pings identically whether it was cloned over SSH or HTTPS: the
// scheme, any credentials, and a trailing ".git" are stripped. A value it can't
// parse is returned trimmed but otherwise untouched.
func normalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			s = u.Host + u.Path
		}
	} else if i := strings.Index(s, "@"); i >= 0 {
		// scp-like syntax: git@github.com:acme/api.git
		s = strings.Replace(s[i+1:], ":", "/", 1)
	}
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}

// printPing renders the handshake as a human-readable summary.
func printPing(out io.Writer, req api.SetupPingRequest, resp *api.SetupPingResponse) {
	fmt.Fprintf(out, "CLI:    gravity %s (%s/%s)\n", req.CLI.Version, req.CLI.OS, req.CLI.Arch)
	fmt.Fprintf(out, "API:    %s\n", req.Config.APIURL)
	fmt.Fprintf(out, "Site:   %s\n", orUnset(req.Config.Site))
	fmt.Fprintf(out, "Space:  %s\n", orUnset(req.Config.Space))
	fmt.Fprintf(out, "Repo:   %s\n", orUnset(req.Repo.Name))
	if req.Repo.Remote != "" {
		fmt.Fprintf(out, "Remote: %s\n", req.Repo.Remote)
	}
	if req.Repo.Branch != "" {
		fmt.Fprintf(out, "Branch: %s\n", req.Repo.Branch)
	}

	fmt.Fprintf(out, "\nOrganization: %s\n", orUnset(resp.OrganizationName))
	if resp.KeyHint != "" {
		fmt.Fprintf(out, "Key:          …%s\n", resp.KeyHint)
	}
	if resp.DefaultSiteSlug != "" {
		fmt.Fprintf(out, "Default site: %s\n", resp.DefaultSiteSlug)
	}
	fmt.Fprintln(out, "\nHandshake OK — the Gravity web app can now pick this repo up.")
}
