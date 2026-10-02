package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type initOptions struct {
	product string
	dryRun  bool
}

type initData struct {
	Manifest initManifest         `json:"manifest"`
	DryRun   bool                 `json:"dryRun"`
	Connect  *api.ConnectResponse `json:"connect"`
}

type initManifest struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Written bool   `json:"written"`
	Content string `json:"content"`
}

const permReposManage = "docs.repos.manage"

func newInitCmd(a *app) *cobra.Command {
	var o initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Connect this repository to Gravity and write a minimal .gravity.yaml",
		Long:  "Connect this repository to Gravity: registers it under a product, writes a minimal .gravity.yaml when there is none, and shows the passes the app already has for it.\nInit never commits or pushes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runInit(cmd.Context(), o)
		},
	}
	cmd.Flags().StringVar(&o.product, "product", "", "product slug to register this repository under")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would be written and registered, change nothing")
	return cmd
}

func (a *app) runInit(ctx context.Context, o initOptions) error {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return err
	}
	path := config.ManifestPath(info.root, a.manifestOverride())
	m, err := config.Load(path)
	if errors.Is(err, config.ErrV1Manifest) {
		return &ExitError{Code: CodeError, ErrCode: "manifest_v1", Err: fmt.Errorf("%s is a v1 manifest; converting it to v2 is not available in this build yet, keep using gravity v0.3 for this repository", relPath(info.root, path))}
	}
	if err != nil {
		return Fail(CodeError, err)
	}
	if o.product != "" && normalize.ProductSlug(o.product) != o.product {
		return Failf(CodeError, "--product %q must be a lowercase slug (try %q)", o.product, normalize.ProductSlug(o.product))
	}
	data := initData{DryRun: o.dryRun, Manifest: initManifest{Path: relPath(info.root, path), Exists: m != nil}}
	if m == nil {
		data.Manifest.Content = minimalManifest(o.product)
		draft, err := config.Parse([]byte(data.Manifest.Content))
		if err != nil {
			return Fail(CodeError, err)
		}
		m = draft
		m.Path = path
	} else {
		data.Manifest.Content = string(m.YAML)
		if o.product != "" && m.Product != "" && m.Product != o.product {
			return Failf(CodeError, "%s declares product %s; edit it or drop --product", data.Manifest.Path, m.Product)
		}
	}
	apiURL := m.APIURL
	creds, err := a.credentials(apiURL)
	if err != nil {
		return err
	}
	if creds.Token == "" {
		if !a.ui.Interactive() {
			return requireToken(creds)
		}
		if _, err := a.login(ctx, loginOptions{apiURL: apiURL}); err != nil {
			return err
		}
		if creds, err = a.credentials(apiURL); err != nil {
			return err
		}
	}
	client := a.client(creds)
	who, err := client.WhoAmI(ctx)
	if err != nil {
		return explainAPI(err)
	}
	if err := requirePipelines(who.Features); err != nil {
		return err
	}
	if who.Principal != nil && who.Principal.Kind == api.TokenKindUser && !who.HasPermission(permReposManage) && !o.dryRun {
		org := "this organization"
		if who.Organization != nil {
			org = firstNonEmpty(who.Organization.Name, who.Organization.Slug)
		}
		return &ExitError{Code: CodeError, ErrCode: "forbidden", Err: fmt.Errorf("you can't connect repositories in %s (role: %s); ask an admin to run `gravity init` here, or to pre-register this repository in the app; `gravity init --dry-run` shows what would be set up", org, orDash(who.Principal.Role))}
	}
	product := firstNonEmpty(o.product, m.Product)
	c, err := a.detectCI(ctx, info.repo)
	if err != nil {
		return err
	}
	req := a.connectRequest(info, m, api.ContextInit, origin(c), true)
	req.Product = product
	preview, err := client.Connect(ctx, req)
	if err != nil {
		return explainAPI(fmt.Errorf("connect: %w", err))
	}
	a.printInitPreview(info, data, preview, product)
	if o.dryRun {
		data.Connect = preview
		return a.ui.Result(data)
	}
	req.DryRun = false
	conn, err := client.Connect(ctx, req)
	if err != nil {
		return explainAPI(fmt.Errorf("connect: %w", err))
	}
	data.Connect = conn
	if !data.Manifest.Exists {
		if err := os.WriteFile(path, []byte(data.Manifest.Content), 0o644); err != nil {
			return Fail(CodeError, fmt.Errorf("connected, but writing %s failed: %w", data.Manifest.Path, err))
		}
		data.Manifest.Written = true
	}
	for _, w := range conn.Manifest.Warnings {
		a.ui.Warn(w.Code, w.Message)
	}
	a.printInitDone(conn, data, creds)
	return a.ui.Result(data)
}

func minimalManifest(product string) string {
	var b strings.Builder
	b.WriteString("version: 2\n")
	if product != "" {
		b.WriteString("product: " + product + "\n")
	}
	return b.String()
}

func (a *app) printInitPreview(info *repoInfo, data initData, conn *api.ConnectResponse, product string) {
	p := a.ui
	p.Println("%s %s · branch %s", p.Mark(ui.MarkOK), info.remoteKey, orDash(info.branch))
	p.Println("%s", p.Bold("Preview"))
	if data.Manifest.Exists {
		p.Println("  = %s (kept as is)", data.Manifest.Path)
	} else {
		lines := strings.Count(data.Manifest.Content, "\n")
		p.Println("  + %s (%d lines)", data.Manifest.Path, lines)
		for _, l := range strings.Split(strings.TrimRight(data.Manifest.Content, "\n"), "\n") {
			p.Println("      %s", l)
		}
	}
	productLabel := firstNonEmpty(conn.Repo.Product.Name, conn.Repo.Product.Slug, product)
	if productLabel == "" {
		productLabel = normalize.ProductSlug(info.name) + " (new)"
	}
	if conn.Repo.Created || conn.Repo.ID == "" {
		p.Println("  + register %s under product %s", info.name, productLabel)
	} else {
		p.Println("  = %s is already connected (product %s)", info.name, productLabel)
	}
	if n := len(conn.Effective.Passes); n > 0 {
		names := make([]string, 0, n)
		for _, ps := range conn.Effective.Passes {
			names = append(names, ps.Name)
		}
		p.Println("  passes: %s", strings.Join(names, ", "))
	} else {
		p.Println("  passes: none yet; add them in the app once connected")
	}
}

func (a *app) printInitDone(conn *api.ConnectResponse, data initData, creds auth.Credentials) {
	p := a.ui
	if data.Manifest.Written {
		p.Println("%s Wrote %s", p.Mark(ui.MarkOK), data.Manifest.Path)
	}
	product := firstNonEmpty(conn.Repo.Product.Name, conn.Repo.Product.Slug)
	p.Println("%s Connected %s to %s with %d passes", p.Mark(ui.MarkOK), conn.Repo.Name, orDash(product), len(conn.Effective.Passes))
	if !conn.Manifest.Persisted && conn.Manifest.Reason != nil && *conn.Manifest.Reason == "branch_not_authoritative" {
		p.Println("%s %s is stored when it reaches %s", p.Mark(ui.MarkWarn), data.Manifest.Path, orDash(conn.Manifest.AuthoritativeBranch))
	}
	if conn.Repo.AppURL != "" {
		p.Println("Manage passes: %s", conn.Repo.AppURL)
	}
	next := "Next: commit " + data.Manifest.Path + ", then `gravity status`"
	if creds.TokenKind == api.TokenKindUser {
		next += "; CI needs a repository token (GRAVITY_TOKEN) minted in the app"
	}
	p.Println("%s", next)
}
