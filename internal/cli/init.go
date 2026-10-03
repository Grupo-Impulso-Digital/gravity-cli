package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/cisetup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/detect"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/setup"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type initOptions struct {
	yes           bool
	product       string
	passesAsCode  bool
	appPasses     bool
	ci            string
	noSecret      bool
	dryRun        bool
	repoID        string
	replaceSecret bool
}

type initData struct {
	DryRun         bool                 `json:"dryRun"`
	Canceled       bool                 `json:"canceled"`
	Questions      int                  `json:"questions"`
	Mode           string               `json:"mode"`
	Detected       *detect.Result       `json:"detected"`
	Product        string               `json:"product"`
	Site           *setup.Site          `json:"site,omitempty"`
	Passes         []initPass           `json:"passes"`
	CreateTargets  []api.CreateTarget   `json:"createTargets"`
	PendingTargets []api.CreateTarget   `json:"pendingTargets,omitempty"`
	Manifest       initManifest         `json:"manifest"`
	Conversion     *config.Conversion   `json:"conversion,omitempty"`
	CI             *cisetup.Plan        `json:"ci,omitempty"`
	Token          *initToken           `json:"token,omitempty"`
	Written        []string             `json:"written"`
	Connect        *api.ConnectResponse `json:"connect"`
}

type initManifest struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Written bool   `json:"written"`
	Action  string `json:"action"`
	Content string `json:"content"`
	Backup  string `json:"backup,omitempty"`
}

type initPass struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Target     string `json:"target,omitempty"`
	Label      string `json:"label,omitempty"`
	Source     string `json:"source"`
	Registered bool   `json:"registered"`
	Status     string `json:"targetStatus,omitempty"`
	ApproveURL string `json:"approveUrl,omitempty"`
	Error      string `json:"error,omitempty"`

	spec *config.Pass
}

type initToken struct {
	Scopes          []string `json:"scopes"`
	KeyHint         string   `json:"keyHint,omitempty"`
	ExpiresAt       *string  `json:"expiresAt"`
	Secret          string   `json:"secret"`
	Via             string   `json:"via,omitempty"`
	Note            string   `json:"note,omitempty"`
	LegacyPipelines []string `json:"legacyPipelines,omitempty"`
}

const (
	permReposManage = "docs.repos.manage"
	permReposTokens = "docs.repos.tokens"

	modeFresh     = "fresh"
	modeConnected = "connected"
	modeConvert   = "convert"

	secretInstalled = "installed"
	secretPrinted   = "printed"
	secretSkipped   = "skipped"

	answerSecret = "secret"
	answerFiles  = "files"
	answerCancel = "cancel"
	answerSite   = "__site__"
	answerNew    = "__new__"

	envSite = "GRAVITY_SITE"
)

func newInitCmd(a *app) *cobra.Command {
	var o initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Connect this repository: detect, suggest passes, write .gravity.yaml and the CI file, install the token",
		Long: "Connect this repository to Gravity in at most three questions: the product, what to keep up to date, and whether to write the files.\n" +
			"init detects your stack (remote, languages, OpenAPI specs, docs folders, UI routes, CI provider), registers the repository, " +
			"mints a repository token with exactly the scopes its passes need and installs it as a CI secret (gh/glab) or prints it once to paste.\n" +
			"A v1 .gravity.yaml is converted to v2 in place (the original is kept as .gravity.v1.yaml.bak). Init never commits or pushes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runInit(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.yes, "yes", "y", false, "accept every suggestion without asking (needed without a terminal)")
	f.StringVar(&o.product, "product", "", "product slug to register this repository under")
	f.BoolVar(&o.passesAsCode, "passes-as-code", false, "declare the chosen passes in .gravity.yaml instead of registering them in the app")
	f.BoolVar(&o.appPasses, "app-passes", false, "when converting a v1 manifest, register its passes in the app and write a minimal .gravity.yaml")
	f.StringVar(&o.ci, "ci", "", "CI provider to wire: github, gitlab, bitbucket, azure, jenkins, circleci or none (default: detected)")
	f.BoolVar(&o.noSecret, "no-secret", false, "never install the CI secret; print the token once to paste instead")
	f.BoolVar(&o.dryRun, "dry-run", false, "show the preview (files, passes, spaces, token) and change nothing")
	f.StringVar(&o.repoID, "repo", "", "adopt a repository pre-registered in the app (its id)")
	f.BoolVar(&o.replaceSecret, "replace-secret", false, "set GRAVITY_TOKEN even when a gravity 0.x pipeline still uses it (0.x refuses repository tokens)")
	return cmd
}

type initRun struct {
	a        *app
	o        initOptions
	ctx      context.Context
	info     *repoInfo
	path     string
	manifest *config.Manifest
	v1Data   []byte
	conv     *config.Conversion
	creds    auth.Credentials
	client   *api.Client
	who      *api.WhoAmI
	det      *detect.Result
	products []api.ProductSummary
	sites    []api.Site
	conn     *api.ConnectResponse
	prompt   ui.Prompter
	data     initData
	product  setup.ProductOption
	chosen   []setup.Suggestion
	passes   []config.Pass
	inCode   bool
	ip       *initPlan

	previewConn *api.ConnectResponse
}

func (a *app) runInit(ctx context.Context, o initOptions) error {
	r := &initRun{a: a, o: o, ctx: ctx, data: initData{DryRun: o.dryRun, Passes: []initPass{}, CreateTargets: []api.CreateTarget{}, Written: []string{}}}
	if err := r.validateFlags(); err != nil {
		return err
	}
	if err := r.load(); err != nil {
		return err
	}
	if err := r.signIn(); err != nil {
		return err
	}
	if err := r.discover(); err != nil {
		return err
	}
	if err := r.ask(); err != nil {
		return r.canceled(err)
	}
	if err := r.plan(); err != nil {
		return err
	}
	r.printPreview()
	if o.dryRun {
		return a.ui.Result(r.data)
	}
	if err := r.checkBranch(); err != nil {
		return err
	}
	answer, err := r.confirm()
	if err != nil {
		return r.canceled(err)
	}
	if answer == answerCancel {
		return r.canceled(ui.ErrAborted)
	}
	return r.apply(answer)
}

func (r *initRun) validateFlags() error {
	o := r.o
	if o.product != "" && normalize.ProductSlug(o.product) != o.product {
		return Failf(CodeError, "--product %q must be a lowercase slug (try %q)", o.product, normalize.ProductSlug(o.product))
	}
	if o.ci != "" && !cisetup.Valid(o.ci) {
		return Failf(CodeError, "--ci %q must be one of %s", o.ci, strings.Join(cisetup.Providers, ", "))
	}
	if o.passesAsCode && o.appPasses {
		return Failf(CodeError, "--passes-as-code and --app-passes exclude each other")
	}
	if o.replaceSecret && o.noSecret {
		return Failf(CodeError, "--replace-secret and --no-secret exclude each other")
	}
	if !r.a.ui.Interactive() && !o.yes && !o.dryRun {
		return &ExitError{Code: CodeError, ErrCode: "needs_terminal", Err: errors.New("init asks up to three questions and needs a terminal; pass --yes to accept the suggestions, or --dry-run to preview")}
	}
	return nil
}

func (r *initRun) canceled(err error) error {
	if !errors.Is(err, ui.ErrAborted) {
		return err
	}
	r.data.Canceled = true
	r.a.ui.Println("Canceled; nothing was written.")
	return r.a.ui.Result(r.data)
}

func (r *initRun) load() error {
	info, err := r.a.inspectRepo(r.ctx)
	if err != nil {
		return err
	}
	r.info = info
	r.path = config.ManifestPath(info.root, r.a.manifestOverride())
	r.data.Manifest.Path = relPath(info.root, r.path)
	m, err := config.Load(r.path)
	switch {
	case errors.Is(err, config.ErrV1Manifest):
		data, rerr := os.ReadFile(r.path)
		if rerr != nil {
			return Fail(CodeError, rerr)
		}
		conv, cerr := config.ConvertV1(data, config.ConvertOptions{RepoName: info.name, Site: r.a.env(envSite)})
		if cerr != nil {
			return &ExitError{Code: CodeError, ErrCode: "manifest_v1", Err: fmt.Errorf("%s is a v1 manifest and cannot be converted: %w", r.data.Manifest.Path, cerr)}
		}
		r.v1Data, r.conv = data, conv
		r.data.Conversion = conv
		r.data.Manifest.Exists = true
	case err != nil:
		return Fail(CodeError, err)
	default:
		r.manifest = m
		r.data.Manifest.Exists = m != nil
	}
	if r.o.product != "" && r.manifestProduct() != "" && r.manifestProduct() != r.o.product {
		return Failf(CodeError, "%s declares product %s; edit it or drop --product", r.data.Manifest.Path, r.manifestProduct())
	}
	return nil
}

func (r *initRun) convertWithDefaultSite() error {
	if r.conv == nil || r.conv.Site != "" || r.who.DefaultSiteSlug == nil || *r.who.DefaultSiteSlug == "" {
		return nil
	}
	conv, err := config.ConvertV1(r.v1Data, config.ConvertOptions{RepoName: r.info.name, Site: *r.who.DefaultSiteSlug})
	if err != nil {
		return &ExitError{Code: CodeError, ErrCode: "manifest_v1", Err: fmt.Errorf("%s is a v1 manifest and cannot be converted: %w", r.data.Manifest.Path, err)}
	}
	r.conv = conv
	r.data.Conversion = conv
	return nil
}

func (r *initRun) manifestProduct() string {
	switch {
	case r.manifest != nil:
		return r.manifest.Product
	case r.conv != nil:
		return r.conv.Product
	}
	return ""
}

func (r *initRun) manifestAPIURL() string {
	switch {
	case r.manifest != nil:
		return r.manifest.APIURL
	case r.conv != nil:
		return r.conv.Manifest.APIURL
	}
	return ""
}

func (r *initRun) signIn() error {
	a := r.a
	apiURL := r.manifestAPIURL()
	creds, err := a.credentials(apiURL)
	if err != nil {
		return err
	}
	if creds.Token == "" {
		if !a.ui.Interactive() {
			return a.requireToken(creds)
		}
		if creds.APIURLSource == auth.SourceManifest && !auth.SameAPIURL(creds.APIURL, config.DefaultAPIURL) {
			return &ExitError{Code: CodeError, ErrCode: "manifest_api_url", Err: fmt.Errorf("not signed in, and %s points at %s; gravity only signs you in to a host you name yourself: run `gravity login --api-url %s` if you trust it, then `gravity init` again", r.data.Manifest.Path, creds.APIURL, creds.APIURL)}
		}
		if _, err := a.login(r.ctx, loginOptions{}); err != nil {
			return err
		}
		if creds, err = a.credentials(apiURL); err != nil {
			return err
		}
	}
	r.creds = creds
	r.client = a.client(creds)
	who, err := r.client.WhoAmI(r.ctx)
	if err != nil {
		return explainAPI(err)
	}
	if err := requirePipelines(who.Features); err != nil {
		return err
	}
	r.who = who
	org := r.orgName()
	switch {
	case who.Principal != nil && who.Principal.User != nil:
		a.ui.Println("%s Signed in as %s · %s", a.ui.Mark(ui.MarkOK), who.Principal.User.Email, org)
	case r.isRepoPrincipal():
		a.ui.Println("%s Repository token · %s", a.ui.Mark(ui.MarkOK), org)
	default:
		a.ui.Println("%s Organization token · %s", a.ui.Mark(ui.MarkOK), org)
	}
	return r.checkPermissions()
}

func (r *initRun) orgName() string {
	if r.who.Organization != nil {
		return firstNonEmpty(r.who.Organization.Name, r.who.Organization.Slug)
	}
	return firstNonEmpty(r.who.OrganizationName, "this organization")
}

func (r *initRun) isUser() bool {
	return r.who.Principal != nil && r.who.Principal.Kind == api.TokenKindUser
}

func (r *initRun) isRepoPrincipal() bool {
	return r.who.Principal != nil && r.who.Principal.Kind == api.TokenKindRepo
}

func (r *initRun) canManage() bool {
	return !r.isUser() || r.who.HasPermission(permReposManage)
}

func (r *initRun) canMint() bool {
	return !r.isRepoPrincipal() && (!r.isUser() || r.who.HasPermission(permReposTokens))
}

func (r *initRun) checkPermissions() error {
	if r.isUser() && !r.o.dryRun && !r.who.HasPermission(permReposTokens) && (r.canManage() || r.o.repoID != "") {
		r.a.ui.Warn("token_permission_missing", "you can't mint repository tokens in "+r.orgName()+"; init connects the repository, and an admin mints its token in the app")
	}
	if r.canManage() || r.o.dryRun || r.o.repoID != "" {
		return nil
	}
	role := "-"
	if r.who.Principal != nil {
		role = orDash(r.who.Principal.Role)
	}
	app := appBaseURL(r.creds.APIURL)
	return &ExitError{Code: CodeError, ErrCode: "forbidden", Err: fmt.Errorf("you can't connect repositories in %s (role: %s). Ask an admin to run `gravity init` here, or to pre-register this repository at %s/app/repos/connect and send you `gravity init --repo <id>`; `gravity init --dry-run` shows what would be set up", r.orgName(), role, app)}
}

func appBaseURL(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "https://app.gravitydocs.io"
	}
	if host, ok := strings.CutPrefix(u.Host, "api."); ok {
		u.Host = host
		if strings.Count(host, ".") == 1 {
			u.Host = "app." + host
		}
	}
	u.Path, u.RawQuery = "", ""
	return strings.TrimSuffix(u.String(), "/")
}

func (r *initRun) discover() error {
	a := r.a
	if err := r.convertWithDefaultSite(); err != nil {
		return err
	}
	files, err := r.info.repo.ListFiles(r.ctx, "")
	if err != nil {
		return Fail(CodeError, err)
	}
	if untracked, err := r.info.repo.UntrackedFiles(r.ctx); err == nil {
		files = append(files, untracked...)
	}
	var tags []string
	if list, err := r.info.repo.Tags(r.ctx, "v*"); err == nil {
		for _, t := range list {
			tags = append(tags, t.Name)
		}
	}
	det, err := detect.Run(detect.Input{Root: r.info.root, Files: files, Tags: tags})
	if err != nil {
		return Fail(CodeError, err)
	}
	r.det = det
	r.data.Detected = det
	a.ui.Println("%s %s", a.ui.Mark(ui.MarkOK), detectionLine(r.info.remoteKey, det))
	if !r.isRepoPrincipal() {
		if r.products, err = r.client.Products(r.ctx); err != nil {
			if api.IsLicenseError(err) {
				return explainAPI(err)
			}
			a.ui.Debugf("products: %v", err)
		}
		if r.sites, err = r.client.Sites(r.ctx); err != nil {
			if api.IsLicenseError(err) {
				return explainAPI(err)
			}
			a.ui.Debugf("sites: %v", err)
		}
	}
	req := r.connectRequest(r.currentManifest(), true)
	conn, err := r.client.Connect(r.ctx, req)
	if err != nil {
		return explainAPI(fmt.Errorf("connect: %w", err))
	}
	r.conn = conn
	if r.o.repoID != "" {
		if conn.Repo.ID == "" || conn.Repo.Created {
			return Failf(CodeError, "%s is not registered in Gravity yet, so --repo %s cannot adopt it; check the remote, or ask the admin who pre-registered it", r.info.remoteKey, r.o.repoID)
		}
		if conn.Repo.ID != r.o.repoID {
			return Failf(CodeError, "%s is registered as %s, not %s", r.info.remoteKey, conn.Repo.ID, r.o.repoID)
		}
	}
	return nil
}

func detectionLine(remoteKey string, d *detect.Result) string {
	parts := []string{remoteKey}
	for i, l := range d.Languages {
		if i >= 2 {
			break
		}
		parts = append(parts, l.Name)
	}
	if n := len(d.OpenAPI); n > 0 {
		doc := d.OpenAPI[0]
		label := "OpenAPI"
		if strings.HasPrefix(doc.Version, "swagger") {
			label = "Swagger"
		} else if doc.Version != "" {
			label += " " + doc.Version
		}
		ops := 0
		for _, o := range d.OpenAPI {
			ops += o.Operations
		}
		if n > 1 {
			label = fmt.Sprintf("%d OpenAPI documents", n)
		}
		parts = append(parts, fmt.Sprintf("%s (%d operations)", label, ops))
	}
	if d.UIRoutes > 0 {
		parts = append(parts, fmt.Sprintf("%s UI (%d routes)", d.UIFramework, d.UIRoutes))
	}
	if d.ServerRoutes > 0 {
		parts = append(parts, fmt.Sprintf("%d server routes", d.ServerRoutes))
	}
	if d.CLICommands > 0 {
		parts = append(parts, fmt.Sprintf("%d CLI commands", d.CLICommands))
	}
	if d.MarkdownFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d Markdown docs", d.MarkdownFiles))
	}
	if d.ReleaseTags > 0 {
		parts = append(parts, plural(d.ReleaseTags, "release", "releases"))
	}
	for _, c := range d.CI {
		parts = append(parts, cisetup.Label(c))
	}
	return strings.Join(parts, " · ")
}

func (r *initRun) currentManifest() *config.Manifest {
	return r.manifest
}

func (r *initRun) connectRequest(m *config.Manifest, dryRun bool) api.ConnectRequest {
	c, _ := r.a.detectCI(r.ctx, r.info.repo)
	req := r.a.connectRequest(r.info, m, api.ContextInit, origin(c), dryRun)
	req.Repo.DefaultBranch = firstNonEmpty(req.Repo.DefaultBranch, r.info.branch)
	if p := firstNonEmpty(r.o.product, r.product.Slug); p != "" && (m == nil || m.Product == "") {
		req.Product = p
	}
	d := r.det
	det := &api.Detected{Languages: d.LanguageKeys(), UIRoutes: d.UIRoutes, MarkdownFiles: d.MarkdownFiles}
	for _, o := range d.OpenAPI {
		det.OpenAPI = append(det.OpenAPI, api.DetectedOpenAPI{Path: o.Path, Operations: o.Operations})
	}
	if len(d.CI) > 0 {
		det.CI = r.ciProvider()
	}
	req.Detected = det
	return req
}

func (r *initRun) mode() string {
	switch {
	case r.conv != nil:
		return modeConvert
	case r.o.repoID != "", r.isRepoPrincipal():
		return modeConnected
	case r.manifest != nil && len(r.manifest.Passes) > 0:
		return modeConnected
	case r.conn.Repo.ID != "" && !r.conn.Repo.Created && len(r.conn.Effective.Passes) > 0:
		return modeConnected
	}
	return modeFresh
}

func (r *initRun) ciProvider() string {
	if r.o.ci != "" {
		return r.o.ci
	}
	for _, c := range r.det.CI {
		if c == r.info.provider {
			return c
		}
	}
	if len(r.det.CI) > 0 {
		return r.det.CI[0]
	}
	if r.info.provider != "" {
		return r.info.provider
	}
	return cisetup.None
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
