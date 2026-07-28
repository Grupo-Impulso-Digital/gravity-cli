package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func newReposCmd(gf *globalFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List the repos that publish to this site",
		Long: `Show every repo registered against this site: this one, plus its siblings —
the other repos in the organization whose docs land on the same site.

The listing comes from the setup handshake, so running it also refreshes this
repo's registration (the same call ` + "`gravity ping`" + ` makes). For each sibling it
reports the spaces it declares, the collections its pages are filed under, when
it last pinged and last wrote, and the CLI version it used.

Nothing is published. Exits 2 on an auth or network failure.`,
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
			return runRepos(cmd.Context(), e.client, req, jsonOut, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the repo registry as JSON")
	return cmd
}

type reposView struct {
	Site       string            `json:"site,omitempty"`
	Repo       repoSelfView      `json:"repo"`
	Siblings   []api.SiblingRepo `json:"siblings"`
	Registered bool              `json:"registered"`
}

type repoSelfView struct {
	Name            string `json:"name,omitempty"`
	RemoteKey       string `json:"remoteKey,omitempty"`
	RemoteKeySource string `json:"remoteKeySource,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Commit          string `json:"commit,omitempty"`
	ID              string `json:"id,omitempty"`
	FirstSeenAt     string `json:"firstSeenAt,omitempty"`
}

func runRepos(ctx context.Context, client *api.Client, req api.SetupPingRequest, jsonOut bool, out io.Writer) error {
	resp, err := client.SetupPing(ctx, req)
	if err != nil {
		return Fail(CodeError, fmt.Errorf("read repos failed: %w", classifyAuthErr(err)))
	}
	view := buildReposView(req, resp)
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}
	printRepos(out, view)
	return nil
}

func buildReposView(req api.SetupPingRequest, resp *api.SetupPingResponse) reposView {
	view := reposView{
		Site: req.Config.Site,
		Repo: repoSelfView{
			Name:            req.Repo.Name,
			RemoteKey:       req.Repo.Remote,
			RemoteKeySource: req.Repo.RemoteKeySource,
			Branch:          req.Repo.Branch,
			Commit:          req.Repo.Commit,
		},
		Siblings: resp.Siblings,
	}
	if resp.Repo != nil {
		view.Repo.ID = resp.Repo.ID
		view.Repo.FirstSeenAt = resp.Repo.FirstSeenAt
		view.Registered = true
	}
	if view.Siblings == nil {
		view.Siblings = []api.SiblingRepo{}
	}
	return view
}

func printRepos(out io.Writer, view reposView) {
	fmt.Fprintf(out, "Site:      %s\n", orUnset(view.Site))
	fmt.Fprintf(out, "This repo: %s", orUnset(view.Repo.Name))
	if view.Repo.RemoteKey != "" {
		fmt.Fprintf(out, "  (%s)", view.Repo.RemoteKey)
	}
	fmt.Fprintln(out)

	var facts []string
	if view.Repo.ID != "" {
		facts = append(facts, "registered "+view.Repo.ID)
	}
	if view.Repo.FirstSeenAt != "" {
		facts = append(facts, "first seen "+dateOr(view.Repo.FirstSeenAt, "just now"))
	}
	if view.Repo.Branch != "" {
		facts = append(facts, "branch "+view.Repo.Branch)
	}
	if len(facts) > 0 {
		fmt.Fprintf(out, "           %s\n", strings.Join(facts, " · "))
	}

	if !view.Registered {
		fmt.Fprintln(out, "\nnote: this platform does not register repos yet (or could not derive an identity"+
			" for this checkout); sibling repos are unavailable")
		return
	}
	if len(view.Siblings) == 0 {
		fmt.Fprintf(out, "\nNo sibling repos — this is the only repo publishing to %s.\n", orUnset(view.Site))
		return
	}
	fmt.Fprintf(out, "\nSibling repos (%d):\n", len(view.Siblings))
	printSiblings(out, view.Siblings)
}

func printSiblings(out io.Writer, siblings []api.SiblingRepo) {
	width := 0
	for _, s := range siblings {
		if len(s.Name) > width {
			width = len(s.Name)
		}
	}
	for _, s := range siblings {
		parts := []string{"spaces " + listOr(s.Spaces, "(none)")}
		if len(s.Collections) > 0 {
			parts = append(parts, "collections "+listOr(s.Collections, ""))
		}
		parts = append(
			parts,
			"pinged "+dateOr(s.LastPingAt, "never"),
			"wrote "+dateOr(s.LastWriteAt, "never"),
			"cli "+orUnset(s.CLIVersion),
		)
		fmt.Fprintf(out, "  %-*s  %s\n", width, s.Name, strings.Join(parts, "   "))
	}
}

func listOr(vals []string, fallback string) string {
	if len(vals) == 0 {
		return fallback
	}
	return strings.Join(vals, ",")
}

func dateOr(ts, fallback string) string {
	if ts == "" {
		return fallback
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return ts
}
