package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type logoutData struct {
	Removed       []string `json:"removed"`
	Revoked       []string `json:"revoked"`
	LegacyWarning string   `json:"legacyWarning,omitempty"`
}

func newLogoutCmd(a *app) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke the current user token and remove its profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profiles, _, err := auth.LoadProfiles()
			if err != nil {
				return Fail(CodeError, err)
			}
			var names []string
			switch {
			case all:
				names = profiles.Names()
			case a.gf.profile != "":
				names = []string{a.gf.profile}
			case a.env("GRAVITY_PROFILE") != "":
				names = []string{a.env("GRAVITY_PROFILE")}
			case profiles.Current != "":
				names = []string{profiles.Current}
			}
			data := logoutData{Removed: []string{}, Revoked: []string{}}
			if len(names) == 0 {
				a.ui.Println("No stored profile; nothing to sign out of.")
				return a.ui.Result(data)
			}
			for _, name := range names {
				prof, ok := profiles.Get(name)
				if !ok {
					return Failf(CodeError, "profile %q does not exist", name)
				}
				if prof.TokenKind == api.TokenKindUser || auth.TokenKindOf(prof.Token) == api.TokenKindUser {
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
