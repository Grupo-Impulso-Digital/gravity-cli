package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func newAuthCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication credentials",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthStatusCmd(gf))
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var (
		token  string
		apiURL string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API token and URL in ~/.config/gravity/config.yaml",
		Long: "Persist an API token (mode 0600) so local runs are authenticated.\n" +
			"With no --token on an interactive terminal it prompts for the token with hidden\n" +
			"input, so the secret never lands in your shell history. Re-run it to change the\n" +
			"stored token. The token lives only in the user-level credentials file — never in\n" +
			".gravity.yaml.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			existing, _ := config.LoadUserCredentials()
			interactive := isInteractive(cmd.InOrStdin())

			if existing != nil && token == "" && os.Getenv(config.EnvToken) == "" && interactive {
				replace := false
				if err := huh.NewForm(huh.NewGroup(
					huh.NewConfirm().
						Title("Replace the stored credentials?").
						Description("A token is already saved for this user.").
						Value(&replace),
				)).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr()).Run(); err != nil {
					return Fail(CodeError, err)
				}
				if !replace {
					fmt.Fprintln(out, "Kept the existing credentials.")
					return nil
				}
			}

			if token == "" {
				token = os.Getenv(config.EnvToken)
			}
			if token == "" && interactive {
				if err := huh.NewForm(huh.NewGroup(
					huh.NewInput().
						Title("API token").
						Description("Paste your sk_live_… token (input hidden).").
						EchoMode(huh.EchoModePassword).
						Value(&token).Validate(requiredField),
				)).WithInput(cmd.InOrStdin()).WithOutput(cmd.ErrOrStderr()).Run(); err != nil {
					return Fail(CodeError, err)
				}
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return Failf(CodeError, "--token is required (or set %s)", config.EnvToken)
			}

			if apiURL == "" {
				if existing != nil && existing.APIURL != "" {
					apiURL = existing.APIURL
				} else {
					apiURL = config.DefaultAPIURL
				}
			}

			path, err := config.WriteUserCredentials(config.UserCredentials{Token: token, APIURL: apiURL})
			if err != nil {
				return Fail(CodeError, err)
			}
			fmt.Fprintf(out, "Saved credentials to %s (mode 0600)\n", path)

			who, verr := api.New(apiURL, token).WhoAmI(cmd.Context())
			if verr != nil {
				fmt.Fprintf(out, "warning: saved, but could not verify the token: %v\n", classifyAuthErr(verr))
				return nil
			}
			fmt.Fprintf(out, "Verified: org %s (%s), key %s\n", who.OrganizationName, who.OrganizationID, who.KeyHint)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "API token (sk_live_...); falls back to "+config.EnvToken+" or an interactive prompt")
	cmd.Flags().StringVar(&apiURL, "api-url", "", fmt.Sprintf("Gravity API base URL (default %s)", config.DefaultAPIURL))
	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credentials (~/.config/gravity/config.yaml)",
		Long:  "Delete the user-level credentials file to clear or rotate the stored token.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			path, existed, err := config.RemoveUserCredentials()
			if err != nil {
				return Fail(CodeError, err)
			}
			if existed {
				fmt.Fprintf(out, "Removed %s\n", path)
			} else {
				fmt.Fprintf(out, "No stored credentials at %s — nothing to remove.\n", path)
			}
			if os.Getenv(config.EnvToken) != "" {
				fmt.Fprintf(out, "note: %s is still set in this environment and will be used until you unset it.\n", config.EnvToken)
			}
			return nil
		},
	}
	return cmd
}

func newAuthStatusCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active token, where it resolved from, and verify it",
		Long: "Report whether a token is configured and from which source (flag / env / user\n" +
			"file), then verify it against /whoami. Exits 2 when a configured token is\n" +
			"rejected or the gateway is unreachable; exits 0 when verified or when no token\n" +
			"is configured (a valid 'logged out' answer).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			path, _ := config.UserConfigPath()
			fmt.Fprintf(out, "Config file: %s\n", path)
			fmt.Fprintf(out, "API URL:     %s\n", e.cfg.APIURL)
			fmt.Fprintf(out, "Token:       %s  (%s)\n", tokenState(e.cfg.Token), tokenSource(gf.token))

			if e.cfg.Token == "" {
				fmt.Fprintln(out, "\nNot signed in. Run `gravity auth login` to store a token.")
				return nil
			}
			who, verr := e.client.WhoAmI(cmd.Context())
			if verr != nil {
				return Fail(CodeError, fmt.Errorf("token not verified: %w", classifyAuthErr(verr)))
			}
			fmt.Fprintf(out, "\nVerified: org %s (%s), key %s\n", who.OrganizationName, who.OrganizationID, who.KeyHint)
			if who.DefaultSiteSlug != nil {
				fmt.Fprintf(out, "Default site: %s\n", *who.DefaultSiteSlug)
			}
			return nil
		},
	}
	return cmd
}
