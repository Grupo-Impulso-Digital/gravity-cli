package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/setup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

const (
	permReposTokens = "docs.repos.tokens"

	secretInstalled = "installed"
	secretPrinted   = "printed"
	secretSkipped   = "skipped"
)

type ciToken struct {
	Scopes    []string `json:"scopes"`
	KeyHint   string   `json:"keyHint,omitempty"`
	ExpiresAt *string  `json:"expiresAt"`
	Secret    string   `json:"secret"`
	Via       string   `json:"via,omitempty"`
	Note      string   `json:"note,omitempty"`
}

type ciSetupData struct {
	Provider string        `json:"provider"`
	Plan     *cisetup.Plan `json:"plan"`
	Written  []string      `json:"written"`
	Token    ciToken       `json:"token"`
	Repo     string        `json:"repo,omitempty"`
}

func newCICmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Wire CI: write the workflow and install the repository token (setup, check)",
	}
	cmd.AddCommand(newCISetupCmd(a), newCICheckCmd(a))
	return cmd
}

func detectProvider(root, remoteProvider string) string {
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	switch {
	case exists(".github/workflows"):
		return cisetup.GitHub
	case exists(".gitlab-ci.yml"):
		return cisetup.GitLab
	case exists("bitbucket-pipelines.yml"):
		return cisetup.Bitbucket
	case exists("azure-pipelines.yml"), exists("azure-pipelines.gravity.yml"):
		return cisetup.Azure
	case exists("Jenkinsfile"):
		return cisetup.Jenkins
	case exists(".circleci/config.yml"):
		return cisetup.CircleCI
	}
	if remoteProvider != "" && cisetup.Valid(remoteProvider) {
		return remoteProvider
	}
	return cisetup.None
}

func ciOptions(info *repoInfo, m *config.Manifest) cisetup.Options {
	apiURL := ""
	if m != nil && m.APIURL != "" && !auth.SameAPIURL(m.APIURL, config.DefaultAPIURL) {
		apiURL = m.APIURL
	}
	var passes []config.Pass
	if m != nil {
		passes = m.Passes
	}
	return cisetup.Options{DefaultBranch: firstNonEmpty(info.defaultBranch, info.branch, "main"), Schedule: setup.HasSchedule(passes, nil), APIURL: apiURL, WebURL: info.webURL}
}

func (a *app) ciState(info *repoInfo, m *config.Manifest) showCI {
	provider := detectProvider(info.root, info.provider)
	st := showCI{Provider: provider, Label: cisetup.Label(provider), Files: []ciFileState{}}
	plan, err := cisetup.Build(info.root, provider, ciOptions(info, m))
	if err != nil {
		return st
	}
	outdated := map[string]string{}
	for _, f := range cisetup.Outdated(info.root) {
		outdated[f.Path] = f.Reason
	}
	for _, f := range plan.Files {
		fs := ciFileState{Path: f.Path}
		switch {
		case outdated[f.Path] != "":
			fs.Status, fs.Note = "outdated", outdated[f.Path]
		case f.Action == cisetup.ActionCreate || f.Action == cisetup.ActionAppend:
			fs.Status = "missing"
		case f.Action == cisetup.ActionUpdate:
			fs.Status, fs.Note = "outdated", f.Note
		default:
			fs.Status = "present"
			if strings.Contains(f.Note, "shared gravity-docs.yml") {
				fs.Note = "shared workflow"
			}
		}
		st.Files = append(st.Files, fs)
	}
	st.Snippet = plan.Snippet != ""
	return st
}

func newCISetupCmd(a *app) *cobra.Command {
	var provider string
	var noSecret bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write the CI file, mint a repository token and install it as GRAVITY_REPO_TOKEN",
		Long: "Write the gravity workflow for your CI provider (an existing file is kept and a snippet printed), register the repository, mint a repository token with exactly the scopes its passes need, and install it as the CI secret GRAVITY_REPO_TOKEN with gh or glab, or print it once to paste.\n" +
			"CI skips the first run of an AI pass (first_run_manual): do it locally with `gravity run --dry-run`, then send it. Nothing is committed or pushed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.ciSetup(cmd.Context(), provider, noSecret)
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "github, gitlab, bitbucket, azure, jenkins, circleci or none (default: detected)")
	cmd.Flags().BoolVar(&noSecret, "no-secret", false, "never install the secret; print the token once to paste instead")
	return cmd
}

func (a *app) ciSetup(ctx context.Context, provider string, noSecret bool) error {
	if provider != "" && !cisetup.Valid(provider) {
		return Failf(CodeError, "--provider %q must be one of %s", provider, strings.Join(cisetup.Providers, ", "))
	}
	s, err := a.openSession(ctx)
	if err != nil {
		return err
	}
	if provider == "" {
		provider = detectProvider(s.info.root, s.info.provider)
	}
	plan, err := cisetup.Build(s.info.root, provider, ciOptions(s.info, s.manifest))
	if err != nil {
		return Fail(CodeError, err)
	}
	d := ciSetupData{Provider: provider, Plan: plan, Written: []string{}}
	p := a.ui
	c, _ := a.detectCI(ctx, s.info.repo)
	conn, err := s.client.Connect(ctx, a.connectRequest(s.info, s.manifest, api.ContextInit, origin(c), false))
	if err != nil {
		return explainAPI(fmt.Errorf("connect: %w", err))
	}
	d.Repo = conn.Repo.AppURL
	p.Note("", ui.MarkOK, "%s registered under %s", s.info.name, firstNonEmpty(conn.Repo.Product.Name, conn.Repo.Product.Slug, "its product"))
	written, werr := cisetup.Write(s.info.root, plan)
	d.Written = append(d.Written, written...)
	if werr != nil {
		return Fail(CodeError, werr)
	}
	for _, f := range plan.Files {
		switch f.Action {
		case cisetup.ActionKeep:
			p.Note("", ui.MarkInfo, "%s kept (%s)", f.Path, f.Note)
		default:
			p.Note("", ui.MarkOK, "%s %s", f.Path, f.Action+"d")
		}
	}
	if plan.Snippet != "" {
		p.Note("", ui.MarkInfo, "add this to %s:", plan.SnippetTarget)
		p.Print(plan.Snippet)
	}
	var passes []config.Pass
	if s.manifest != nil {
		passes = s.manifest.Passes
	}
	d.Token = ciToken{Scopes: setup.Scopes(passes), Secret: secretSkipped}
	token := a.mintCIToken(ctx, s, conn, plan, &d)
	if token != "" {
		installed := false
		if !noSecret && provider == s.info.provider && (provider == cisetup.GitHub || provider == cisetup.GitLab) {
			if inst := cisetup.FindInstaller(ctx, provider, s.info.remoteKey, a.secretRunner); inst != nil {
				if err := inst.Install(ctx, cisetup.SecretName, token); err != nil {
					a.ui.Warn("secret_install_failed", "could not set "+cisetup.SecretName+" with "+inst.Tool+": "+err.Error())
				} else {
					installed = true
					d.Token.Secret, d.Token.Via = secretInstalled, inst.Tool
					p.Note("", ui.MarkOK, "%s set on %s with %s", cisetup.SecretName, inst.Repo, inst.Tool)
				}
			}
		}
		if !installed {
			if !a.terminal && !noSecret {
				d.Token.Note = "not shown: there is no terminal and the token must not land in logs; rerun with --no-secret to print it"
				a.ui.Warn("token_not_shown", d.Token.Note)
			} else {
				d.Token.Secret = secretPrinted
				w := a.stderr
				fmt.Fprintln(w, "")
				fmt.Fprintln(w, "-------- "+cisetup.SecretName+" (shown once) --------")
				fmt.Fprintln(w, token)
				fmt.Fprintln(w, "--------------------------------------------")
				fmt.Fprintln(w, "Copy it now and "+plan.PasteHint+".")
				fmt.Fprintln(w, "")
			}
		}
	}
	lines := []string{"Provider: " + plan.Label}
	if len(d.Written) > 0 {
		lines = append(lines, "Commit: "+strings.Join(d.Written, ", "))
	}
	switch d.Token.Secret {
	case secretInstalled:
		lines = append(lines, "Token: "+cisetup.SecretName+" set with "+d.Token.Via)
	case secretPrinted:
		lines = append(lines, "Token: printed above; store it as "+cisetup.SecretName)
	default:
		if d.Token.Note != "" {
			lines = append(lines, "Token: "+d.Token.Note)
		}
	}
	if plan.CommentToken != "" {
		lines = append(lines, "Comments: "+plan.CommentHint)
	}
	lines = append(lines, "CI skips first runs of AI passes: do them here with gravity run --dry-run")
	p.Card("CI wired", lines, []ui.Link{{Label: "Repository", URL: conn.Repo.AppURL}})
	return a.ui.Result(d)
}

func (a *app) mintCIToken(ctx context.Context, s *session, conn *api.ConnectResponse, plan *cisetup.Plan, d *ciSetupData) string {
	who := s.who
	switch {
	case who.Principal != nil && who.Principal.Kind == api.TokenKindRepo:
		d.Token.Note = "you are using a repository token; CI can use it as " + cisetup.SecretName
		return ""
	case who.Principal != nil && who.Principal.Kind == api.TokenKindUser && !who.HasPermission(permReposTokens):
		d.Token.Note = "you can't mint repository tokens here; ask an admin to mint one in the app"
		a.ui.Warn("token_permission_missing", d.Token.Note)
		return ""
	case !who.Features["machine-tokens"]:
		d.Token.Note = "this Gravity server cannot mint repository tokens; create one in the app"
		return ""
	}
	minted, err := s.client.MintRepoToken(ctx, conn.Repo.ID, api.MintTokenRequest{Name: plan.Label + " · " + s.info.name, Scopes: d.Token.Scopes})
	if err != nil {
		d.Token.Note = "minting failed: " + err.Error()
		a.ui.Warn("token_mint_failed", "could not mint a repository token ("+err.Error()+"); mint one in the app and store it as "+cisetup.SecretName)
		return ""
	}
	d.Token.KeyHint = minted.Key.KeyHint
	d.Token.ExpiresAt = minted.Key.ExpiresAt
	if len(minted.Key.Scopes) > 0 {
		d.Token.Scopes = minted.Key.Scopes
	}
	a.ui.Note("", ui.MarkOK, "repository token minted (…%s, %s)", minted.Key.KeyHint, strings.Join(d.Token.Scopes, ", "))
	return minted.Token
}

type ciCheckData struct {
	CI     showCI `json:"ci"`
	Secret string `json:"secret"`
	OK     bool   `json:"ok"`
}

func newCICheckCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check that the CI file is present and current and that the secret exists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			info, err := a.inspectRepo(ctx)
			if err != nil {
				return err
			}
			m, err := a.loadManifest(info.root)
			if err != nil {
				return err
			}
			st := a.ciState(info, m)
			d := ciCheckData{CI: st, Secret: "unknown", OK: true}
			if inst := cisetup.FindInstaller(ctx, st.Provider, info.remoteKey, a.secretRunner); inst != nil {
				if ok, err := inst.Exists(ctx, cisetup.SecretName); err == nil {
					d.Secret = "missing"
					if ok {
						d.Secret = "present"
					}
				}
			}
			renderCI(a.ui, st)
			switch d.Secret {
			case "present":
				a.ui.Note("  ", ui.MarkOK, "secret %s present", cisetup.SecretName)
			case "missing":
				a.ui.Note("  ", ui.MarkFail, "secret %s missing: run gravity ci setup", cisetup.SecretName)
				d.OK = false
			default:
				a.ui.Note("  ", ui.MarkInfo, "secret %s: cannot check without gh/glab", cisetup.SecretName)
			}
			for _, f := range st.Files {
				if f.Status != "present" {
					d.OK = false
				}
			}
			if len(st.Files) == 0 && !st.Snippet {
				d.OK = false
			}
			if d.OK {
				return a.ui.Result(d)
			}
			msg := "CI is not fully wired; run `gravity ci setup`"
			if ferr := a.ui.Failure(ui.ErrorInfo{Code: "ci_incomplete", Message: msg, ExitCode: CodeFindings}, d); ferr != nil {
				return ferr
			}
			return &ExitError{Code: CodeFindings, ErrCode: "ci_incomplete", Err: errors.New(msg)}
		},
	}
}
