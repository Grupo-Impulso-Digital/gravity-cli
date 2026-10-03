package cli

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type logoutData struct {
	Removed       []string     `json:"removed"`
	Revoked       []string     `json:"revoked"`
	Token         *tokenLogout `json:"token,omitempty"`
	LegacyWarning string       `json:"legacyWarning,omitempty"`
}

type tokenLogout struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	APIURL  string `json:"apiUrl,omitempty"`
	Revoked bool   `json:"revoked"`
}

func newLogoutCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke the current user token and remove its profile",
		Long:  "Revoke the current user token and remove its profile. With --token, GRAVITY_REPO_TOKEN or GRAVITY_TOKEN holding a user token (gr_user_), that token is revoked on the host it is used against and any profile holding it is removed; repository and organization tokens are revoked in the app.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profiles, _, err := auth.LoadProfiles()
			if err != nil {
				return Fail(CodeError, err)
			}
			data := logoutData{Removed: []string{}, Revoked: []string{}}
			explicit, source := strings.TrimSpace(a.gf.token), "--token"
			if explicit == "" {
				var unresolved *auth.UnresolvedTokenError
				explicit, source, unresolved = auth.EnvToken(a.env)
				if unresolved != nil && explicit == "" {
					a.ui.Warn("token_unresolved_ignored", unresolved.Error())
				}
			}
			if explicit != "" {
				tl, err := a.revokeExplicitToken(cmd.Context(), explicit, source)
				if err != nil {
					return err
				}
				data.Token = tl
			}
			var names []string
			switch {
			case all:
				names = profiles.Names()
			case a.gf.profile != "":
				names = []string{a.gf.profile}
			case a.env("GRAVITY_PROFILE") != "":
				names = []string{a.env("GRAVITY_PROFILE")}
			case explicit != "":
				for _, n := range profiles.Names() {
					if prof, ok := profiles.Get(n); ok && prof.Token == explicit {
						names = append(names, n)
					}
				}
			case profiles.Current != "":
				names = []string{profiles.Current}
			}
			if len(names) == 0 {
				if data.Token == nil {
					a.ui.Println("No stored profile; nothing to sign out of.")
				}
				return a.ui.Result(data)
			}
			for _, name := range names {
				prof, ok := profiles.Get(name)
				if !ok {
					return Failf(CodeError, "profile %q does not exist", name)
				}
				switch {
				case explicit != "" && prof.Token == explicit:
					if data.Token != nil && data.Token.Revoked {
						data.Revoked = append(data.Revoked, name)
					}
				case prof.TokenKind == api.TokenKindUser || auth.TokenKindOf(prof.Token) == api.TokenKindUser:
					c := a.client(auth.Credentials{Token: prof.Token, APIURL: firstNonEmpty(prof.APIURL, config.DefaultAPIURL)})
					if err := c.Logout(cmd.Context()); err != nil {
						if !errors.Is(err, api.ErrUnauthorized) {
							a.ui.Warn("revoke_failed", "could not revoke the token of profile "+name+" on the server: "+err.Error())
						}
					} else {
						data.Revoked = append(data.Revoked, name)
					}
				}
				profiles.Remove(name)
				data.Removed = append(data.Removed, name)
				a.ui.Println("%s Signed out of profile %s", a.ui.Mark(ui.MarkOK), name)
			}
			if err := profiles.Save(); err != nil {
				return Fail(CodeError, err)
			}
			if legacy, err := auth.LegacyConfigPath(); err == nil {
				for _, n := range data.Removed {
					if n != "default" {
						continue
					}
					if _, statErr := os.Stat(legacy); statErr == nil {
						data.LegacyWarning = "the gravity v0.x file " + legacy + " still holds a token; rm " + legacy + " to remove it"
						a.ui.Warn("legacy_config", data.LegacyWarning)
					}
				}
			}
			return a.ui.Result(data)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "sign out of every profile")
	return cmd
}

func (a *app) revokeExplicitToken(ctx context.Context, token, source string) (*tokenLogout, error) {
	tl := &tokenLogout{Source: source, Kind: auth.TokenKindOf(token)}
	if tl.Kind != api.TokenKindUser {
		app := appBaseURL(firstNonEmpty(a.gf.apiURL, a.env(config.EnvAPIURL), a.repoAPIURL(ctx), config.DefaultAPIURL))
		a.ui.Warn("token_kind_unsupported", "the "+source+" token is not a user token (gr_user_); repository and organization tokens are revoked in the app under Settings › CLI & machines ("+app+"/app/settings/tokens), not by gravity logout")
		return tl, nil
	}
	creds, err := a.credentials(a.repoAPIURL(ctx))
	if err != nil {
		return nil, err
	}
	tl.APIURL = creds.APIURL
	if err := a.client(creds).Logout(ctx); err != nil {
		if !errors.Is(err, api.ErrUnauthorized) {
			return nil, explainAPI(err)
		}
		a.ui.Println("%s The %s token was already revoked or expired", a.ui.Mark(ui.MarkOK), source)
		return tl, nil
	}
	tl.Revoked = true
	a.ui.Println("%s Revoked the %s token on %s", a.ui.Mark(ui.MarkOK), source, creds.APIURL)
	return tl, nil
}
