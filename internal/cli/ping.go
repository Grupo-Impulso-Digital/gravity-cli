package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

func newPingCmd(gf *globalFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Register this repo with the platform (setup handshake)",
		Long: `Send a setup handshake to POST /api/v1/setup/ping so the Gravity web app —
which just walked you through install and ` + "`gravity init`" + ` — can confirm the token
works, and so the platform registers this repo against the site it publishes to.

The ping reports the CLI version/platform, the .gravity.yaml connection config,
the repo's name/remote/branch/commit, the fully-resolved manifest (which carries
no token: ` + "`.gravity.yaml`" + ` structurally cannot hold one) and a summary of what
this repo documents. No documentation is written: the server echoes back your
organization, the token's key hint, the default site, this repo's registration,
and the sibling repos publishing to the same site. Exits 2 on an auth or network
failure.`,
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

func buildPingRequest(ctx context.Context, e *env) api.SetupPingRequest {
	req := api.SetupPingRequest{
		CLI: api.PingCLI{
			Version: version.String(),
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
	if e.proj != nil {
		req.ConfigFull = projectConfigJSON(e.proj)
		req.ConfigYAML = readProjectYAML()
		req.DocSources = docSourcesSummary(e.proj)
	}
	return req
}

func detectRepo(ctx context.Context, proj *config.Project) api.PingRepo {
	var repo api.PingRepo
	cwd, err := os.Getwd()
	if err != nil {
		return repo
	}
	if r, err := git.Open(ctx, cwd); err == nil {
		if remote, _ := r.RemoteURL(ctx); remote != "" {
			repo.Remote = git.NormalizeRemoteKey(remote)
			repo.RemoteKeySource = api.RemoteKeySourceRemote
		}
		if branch, _ := r.CurrentBranch(ctx); branch != "" {
			repo.Branch = branch
		}
		if commits, err := r.Log(ctx, "", "HEAD", 1); err == nil && len(commits) > 0 {
			repo.Commit = commits[0].Hash
		}
	}
	if repo.Remote == "" {
		if key := configRemoteKey(proj); key != "" {
			repo.Remote = key
			repo.RemoteKeySource = api.RemoteKeySourceConfig
		}
	}
	repo.Name = repoName(proj, repo.Remote, cwd)
	return repo
}

func configRemoteKey(proj *config.Project) string {
	if proj == nil || proj.Product.Slug == "" || proj.Product.Repo == "" {
		return ""
	}
	return proj.Product.Slug + "/" + proj.Product.Repo
}

func localRepoRef(ctx context.Context, proj *config.Project) *api.RepoRef {
	pr := detectRepo(ctx, proj)
	if pr.Remote == "" {
		return nil
	}
	return &api.RepoRef{RemoteKey: pr.Remote, Name: pr.Name}
}

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

func projectConfigJSON(proj *config.Project) map[string]any {
	if proj == nil {
		return nil
	}
	data, err := yaml.Marshal(proj)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func readProjectYAML() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(cwd, config.ProjectFileName))
	if err != nil {
		return ""
	}
	return string(data)
}

func docSourcesSummary(proj *config.Project) *api.PingDocSources {
	if proj == nil {
		return nil
	}
	spaces := map[string]bool{}
	kinds := map[string]bool{}
	add := func(s string) {
		if s != "" {
			spaces[s] = true
		}
	}
	add(proj.Spaces.Default)
	add(proj.Spaces.Parent)
	add(proj.ReleaseNotes.Space)
	for _, s := range proj.Spaces.Shared {
		add(s)
	}
	for _, d := range proj.Spaces.Declare {
		add(d.Slug)
	}
	for _, s := range proj.Sources {
		add(s.Space)
		kinds[firstNonEmpty(s.Kind, "openapi")] = true
	}
	for _, d := range proj.Documents {
		add(d.Space)
	}
	return &api.PingDocSources{
		Sources:   len(proj.Sources),
		Documents: len(proj.Documents),
		Spaces:    sortedKeys(spaces),
		Kinds:     sortedKeys(kinds),
		Languages: proj.I18n.Languages,
		Units:     proj.ResolveUnitKind(),
	}
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func printPing(out io.Writer, req api.SetupPingRequest, resp *api.SetupPingResponse) {
	fmt.Fprintf(out, "CLI:    gravity %s (%s/%s)\n", req.CLI.Version, req.CLI.OS, req.CLI.Arch)
	fmt.Fprintf(out, "API:    %s\n", req.Config.APIURL)
	fmt.Fprintf(out, "Site:   %s\n", orUnset(req.Config.Site))
	fmt.Fprintf(out, "Space:  %s\n", orUnset(req.Config.Space))
	fmt.Fprintf(out, "Repo:   %s\n", orUnset(req.Repo.Name))
	if req.Repo.Remote != "" {
		fmt.Fprintf(out, "Remote: %s", req.Repo.Remote)
		if req.Repo.RemoteKeySource == api.RemoteKeySourceConfig {
			fmt.Fprint(out, "  (derived from product.slug/product.repo — this repo has no git remote)")
		}
		fmt.Fprintln(out)
	}
	if req.Repo.Branch != "" {
		fmt.Fprintf(out, "Branch: %s\n", req.Repo.Branch)
	}
	if req.Repo.Commit != "" {
		fmt.Fprintf(out, "Commit: %s\n", shortHash(req.Repo.Commit))
	}
	if req.DocSources != nil {
		fmt.Fprintf(out, "Docs:   %d source(s), %d document(s), units %s\n",
			req.DocSources.Sources, req.DocSources.Documents, orUnset(req.DocSources.Units))
	}

	fmt.Fprintf(out, "\nOrganization: %s\n", orUnset(resp.OrganizationName))
	if resp.KeyHint != "" {
		fmt.Fprintf(out, "Key:          …%s\n", resp.KeyHint)
	}
	if resp.DefaultSiteSlug != "" {
		fmt.Fprintf(out, "Default site: %s\n", resp.DefaultSiteSlug)
	}
	if resp.Repo != nil {
		fmt.Fprintf(out, "Registered:   %s (first seen %s)\n", resp.Repo.ID, dateOr(resp.Repo.FirstSeenAt, "just now"))
	}
	if feats := enabledFeatures(resp.ServerFeatures); len(feats) > 0 {
		fmt.Fprintf(out, "Features:     %s\n", strings.Join(feats, " "))
	}
	if len(resp.Siblings) > 0 {
		fmt.Fprintf(out, "\nSibling repos on this site (%d):\n", len(resp.Siblings))
		printSiblings(out, resp.Siblings)
	}
	fmt.Fprintln(out, "\nHandshake OK — the Gravity web app can now pick this repo up.")
}

func enabledFeatures(features map[string]bool) []string {
	var out []string
	for name, on := range features {
		if on {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
