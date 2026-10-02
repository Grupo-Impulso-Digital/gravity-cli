package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ci"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/plan"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

type globalFlags struct {
	profile  string
	apiURL   string
	token    string
	manifest string
	dir      string
	json     bool
	noColor  bool
	quiet    bool
	verbose  bool
}

type app struct {
	gf          globalFlags
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	getenv      func(string) string
	terminal    bool
	openBrowser func(string) error
	configure   func(*api.Client)
	sleep       func(context.Context, time.Duration) error
	ui          *ui.Printer
}

func newApp() *app {
	return &app{
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		getenv:      os.Getenv,
		terminal:    ui.IsTerminal(os.Stdin) && ui.IsTerminal(os.Stderr),
		openBrowser: openURL,
	}
}

func (a *app) sleeper() func(context.Context, time.Duration) error {
	return a.sleep
}

func (a *app) env(k string) string {
	if a.getenv == nil {
		return os.Getenv(k)
	}
	return a.getenv(k)
}

func (a *app) isCI() bool {
	switch strings.ToLower(a.env("CI")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func (a *app) setupUI(command string) {
	a.ui = ui.New(a.stdout, a.stderr, ui.Options{
		JSON:     a.gf.json,
		NoColor:  a.gf.noColor || a.env("NO_COLOR") != "",
		Quiet:    a.gf.quiet,
		Verbose:  a.gf.verbose,
		CI:       a.isCI(),
		Terminal: a.terminal,
	})
	a.ui.SetCommand(command)
}

func (a *app) workdir() (string, error) {
	dir := a.gf.dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", Fail(CodeError, fmt.Errorf("get working dir: %w", err))
		}
		return wd, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", Fail(CodeError, fmt.Errorf("resolve -C %s: %w", dir, err))
	}
	return abs, nil
}

func (a *app) manifestOverride() string {
	if a.gf.manifest != "" {
		return a.gf.manifest
	}
	return a.env(config.EnvManifest)
}

func (a *app) loadManifest(root string) (*config.Manifest, error) {
	m, err := config.Load(config.ManifestPath(root, a.manifestOverride()))
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	return m, nil
}

func (a *app) repoAPIURL(ctx context.Context) string {
	dir, err := a.workdir()
	if err != nil {
		return ""
	}
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return ""
	}
	m, err := config.Load(config.ManifestPath(repo.Root, a.manifestOverride()))
	if err != nil || m == nil {
		return ""
	}
	return m.APIURL
}

func (a *app) credentials(manifestAPIURL string) (auth.Credentials, error) {
	profiles, imported, err := auth.LoadProfiles()
	if err != nil {
		return auth.Credentials{}, Fail(CodeError, err)
	}
	if imported {
		a.ui.Warn("profile_imported", "Imported the token of ~/.config/gravity/config.yaml (gravity v0.x) as profile \"default\"; that file is left untouched")
	}
	creds, err := auth.Resolve(auth.Inputs{
		FlagToken:      a.gf.token,
		FlagAPIURL:     a.gf.apiURL,
		FlagProfile:    a.gf.profile,
		ManifestAPIURL: manifestAPIURL,
		Getenv:         a.env,
	}, profiles)
	var hm *auth.HostMismatchError
	if errors.As(err, &hm) {
		return auth.Credentials{}, &ExitError{Code: CodeError, ErrCode: "token_host_mismatch", Err: err}
	}
	if err != nil {
		return auth.Credentials{}, Fail(CodeError, err)
	}
	if (creds.TokenSource == auth.SourceEnv || creds.TokenSource == auth.SourceFlag) && creds.APIURLSource == auth.SourceManifest && !auth.SameAPIURL(creds.APIURL, config.DefaultAPIURL) {
		from := config.EnvToken
		if creds.TokenSource == auth.SourceFlag {
			from = "--token"
		}
		a.ui.Warn("token_to_manifest_host", fmt.Sprintf("sending the %s token to %s, taken from %s apiUrl; set %s to pin the host (in CI, and when the repository is not yours)", from, creds.APIURL, config.ManifestFileName, config.EnvAPIURL))
	}
	a.ui.Debugf("api %s (%s), token from %s, profile %q", creds.APIURL, creds.APIURLSource, creds.TokenSource, creds.ProfileName)
	return creds, nil
}

func (a *app) client(creds auth.Credentials) *api.Client {
	c := api.New(creds.APIURL, creds.Token)
	if a.configure != nil {
		a.configure(c)
	}
	return c
}

func requireToken(creds auth.Credentials) error {
	if creds.Token == "" {
		return Failf(CodeError, "not signed in: run `gravity login`, or set %s (CI secret)", config.EnvToken)
	}
	return nil
}

func explainAPI(err error) error {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return err
	}
	return Fail(CodeError, err)
}

func requirePipelines(features map[string]bool) error {
	if err := plan.RequirePipelines(features); err != nil {
		return &ExitError{Code: CodeError, Err: err, ErrCode: "pipelines_unsupported"}
	}
	return nil
}

type repoInfo struct {
	repo          *git.Repo
	root          string
	remote        string
	remoteKey     string
	name          string
	branch        string
	head          string
	defaultBranch string
	webURL        string
	provider      string
}

func (a *app) inspectRepo(ctx context.Context) (*repoInfo, error) {
	dir, err := a.workdir()
	if err != nil {
		return nil, err
	}
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	info := &repoInfo{repo: repo, root: repo.Root}
	info.remote, _ = repo.RemoteURL(ctx)
	if info.remote == "" {
		return nil, Failf(CodeError, "this repository has no git remote; add one (git remote add origin <url>) so Gravity can identify it")
	}
	info.remoteKey = git.NormalizeRemoteKey(info.remote)
	info.name = info.remoteKey[strings.LastIndex(info.remoteKey, "/")+1:]
	if b, err := repo.CurrentBranch(ctx); err == nil && b != "HEAD" {
		info.branch = b
	}
	info.head, _ = repo.ResolveRef(ctx, "HEAD")
	info.defaultBranch = repo.DefaultBranch(ctx)
	info.webURL, info.provider = webURL(info.remoteKey)
	return info, nil
}

func webURL(remoteKey string) (string, string) {
	host, _, _ := strings.Cut(remoteKey, "/")
	provider := ""
	switch {
	case host == "github.com" || strings.Contains(host, "github"):
		provider = ci.GitHub
	case host == "gitlab.com" || strings.Contains(host, "gitlab"):
		provider = ci.GitLab
	case host == "bitbucket.org":
		provider = ci.Bitbucket
	case strings.HasSuffix(host, "dev.azure.com") || strings.HasSuffix(host, "visualstudio.com"):
		provider = ci.Azure
	}
	if remoteKey == "" || !strings.Contains(host, ".") {
		return "", provider
	}
	return "https://" + remoteKey, provider
}

func (a *app) detectCI(ctx context.Context, repo *git.Repo) (ci.Context, error) {
	c, err := ci.Detect(ctx, ci.Env{Getenv: a.env}, repo)
	if err != nil {
		return ci.Context{}, Fail(CodeError, err)
	}
	return c, nil
}

func (a *app) connectRequest(info *repoInfo, m *config.Manifest, trigger, origin string, dryRun bool) api.ConnectRequest {
	req := api.ConnectRequest{
		CLI: api.CLIInfo{Version: version.String(), OS: runtime.GOOS, Arch: runtime.GOARCH},
		Repo: api.ConnectRepo{
			Remote:        info.remote,
			Name:          info.name,
			Provider:      info.provider,
			WebURL:        info.webURL,
			DefaultBranch: info.defaultBranch,
			Branch:        info.branch,
			Commit:        info.head,
		},
		Context: api.ConnectContext{Trigger: trigger, Origin: origin},
		DryRun:  dryRun,
	}
	if m != nil {
		req.Manifest = m.Doc
		req.ManifestYAML = string(m.YAML)
		req.ManifestHash = m.Hash
		req.Product = m.Product
	}
	return req
}

func origin(c ci.Context) string {
	if c.IsCI() {
		return api.OriginCI
	}
	return api.OriginLocal
}

func repoParam(who *api.WhoAmI, info *repoInfo) string {
	if who != nil && who.Principal != nil && who.Principal.Kind == api.TokenKindRepo {
		return ""
	}
	return info.remoteKey
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
