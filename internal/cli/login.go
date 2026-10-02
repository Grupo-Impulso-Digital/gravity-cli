package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type loginOptions struct {
	org       string
	noBrowser bool
	withToken bool
}

type loginData struct {
	Profile      string      `json:"profile"`
	ProfilesPath string      `json:"profilesPath"`
	APIURL       string      `json:"apiUrl"`
	TokenKind    string      `json:"tokenKind"`
	Organization *api.OrgRef `json:"organization,omitempty"`
	User         string      `json:"user,omitempty"`
	ExpiresAt    string      `json:"expiresAt,omitempty"`
}

func newLoginCmd(a *app) *cobra.Command {
	var o loginOptions
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your browser (device flow) and store a profile",
		Long:  "Sign in with your browser: gravity shows a code, you approve it in the Gravity app, and the token is stored in ~/.config/gravity/profiles.yaml.\nUse --with-token to store a token read from stdin instead (for example a repository token on a dev machine).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := a.login(cmd.Context(), o)
			if err != nil {
				return err
			}
			return a.ui.Result(data)
		},
	}
	cmd.Flags().StringVar(&o.org, "org", "", "organization slug to sign in to")
	cmd.Flags().BoolVar(&o.noBrowser, "no-browser", false, "print the approval link instead of opening a browser")
	cmd.Flags().BoolVar(&o.withToken, "with-token", false, "read a token from stdin instead of signing in with the browser")
	return cmd
}

func (a *app) login(ctx context.Context, o loginOptions) (*loginData, error) {
	profiles, _, err := auth.LoadProfiles()
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	creds, err := auth.Resolve(auth.Inputs{FlagAPIURL: a.gf.apiURL, Getenv: func(k string) string {
		if k == "GRAVITY_TOKEN" || k == "GRAVITY_PROFILE" {
			return ""
		}
		return a.env(k)
	}}, &auth.Profiles{})
	if err != nil {
		return nil, Fail(CodeError, err)
	}
	var prof auth.Profile
	var org *api.OrgRef
	if o.withToken {
		prof, org, err = a.loginWithToken(ctx, creds)
	} else {
		prof, org, err = a.deviceLogin(ctx, creds, o)
	}
	if err != nil {
		return nil, err
	}
	name := a.gf.profile
	if name == "" && org != nil {
		name = org.Slug
	}
	if name == "" {
		name = "default"
	}
	profiles.Put(name, prof)
	if err := profiles.Save(); err != nil {
		return nil, Fail(CodeError, err)
	}
	orgLabel := ""
	if org != nil {
		orgLabel = " · " + firstNonEmpty(org.Name, org.Slug)
	}
	who := prof.User
	if who == "" {
		who = prof.TokenKind + " token"
	}
	a.ui.Println("%s Signed in as %s%s (profile %s)", a.ui.Mark(ui.MarkOK), who, orgLabel, name)
	return &loginData{
		Profile: name, ProfilesPath: profiles.Path(), APIURL: prof.APIURL, TokenKind: prof.TokenKind,
		Organization: org, User: prof.User, ExpiresAt: prof.ExpiresAt,
	}, nil
}

func (a *app) loginWithToken(ctx context.Context, creds auth.Credentials) (auth.Profile, *api.OrgRef, error) {
	token, err := readToken(a.stdin)
	if err != nil {
		return auth.Profile{}, nil, Fail(CodeError, err)
	}
	creds.Token = token
	who, err := a.client(creds).WhoAmI(ctx)
	if err != nil {
		return auth.Profile{}, nil, explainAPI(fmt.Errorf("verify token: %w", err))
	}
	prof := auth.Profile{APIURL: creds.APIURL, Token: token, TokenKind: auth.TokenKindOf(token)}
	if who.Token != nil {
		if who.Token.Kind != "" {
			prof.TokenKind = who.Token.Kind
		}
		if who.Token.ExpiresAt != nil {
			prof.ExpiresAt = *who.Token.ExpiresAt
		}
	}
	if who.Principal != nil && who.Principal.User != nil {
		prof.User = who.Principal.User.Email
	}
	org := who.Organization
	if org == nil {
		org = &api.OrgRef{ID: who.OrganizationID, Name: who.OrganizationName}
	}
	prof.Org = org.Slug
	return prof, org, nil
}

func readToken(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("--with-token reads the token from stdin, but stdin is empty")
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read token from stdin: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("--with-token reads the token from stdin, but stdin is empty")
	}
	if auth.TokenKindOf(token) == "" {
		return "", errors.New("that does not look like a Gravity token (gr_user_, gr_repo_ or sk_live_)")
	}
	return token, nil
}

func (a *app) deviceLogin(ctx context.Context, creds auth.Credentials, o loginOptions) (auth.Profile, *api.OrgRef, error) {
	creds.Token = ""
	client := a.client(creds)
	poll, err := auth.DeviceLogin(ctx, client, auth.DeviceLoginOptions{
		Org: o.org,
		Prompt: func(start *api.DeviceStart) {
			url := start.VerificationURIComplete
			if url == "" {
				url = start.VerificationURI
			}
			a.ui.Always("Your code: %s", a.ui.Bold(start.UserCode))
			a.ui.Always("Approve it at %s", url)
			if !o.noBrowser && a.ui.Interactive() && a.openBrowser != nil {
				if err := a.openBrowser(url); err == nil {
					a.ui.Always("Opened your browser. Waiting for approval…")
					return
				}
			}
			a.ui.Always("Waiting for approval…")
		},
		Sleep: a.sleeper(),
	})
	if err != nil {
		if errors.Is(err, api.ErrDeviceDenied) || errors.Is(err, api.ErrDeviceExpired) {
			return auth.Profile{}, nil, Fail(CodeError, err)
		}
		return auth.Profile{}, nil, explainAPI(err)
	}
	prof := auth.Profile{
		APIURL:    firstNonEmpty(poll.APIURL, creds.APIURL),
		Token:     poll.Token,
		TokenKind: firstNonEmpty(poll.TokenKind, auth.TokenKindOf(poll.Token)),
		ExpiresAt: poll.ExpiresAt,
	}
	if poll.User != nil {
		prof.User = poll.User.Email
	}
	if poll.Organization != nil {
		prof.Org = poll.Organization.Slug
	}
	return prof, poll.Organization, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
