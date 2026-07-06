package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication credentials",
	}
	cmd.AddCommand(newAuthLoginCmd())
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if token == "" {
				return Failf(CodeError, "--token is required")
			}
			if apiURL == "" {
				return Failf(CodeError, "--api-url is required")
			}
			path, err := config.WriteUserCredentials(config.UserCredentials{
				Token:  token,
				APIURL: apiURL,
			})
			if err != nil {
				return Fail(CodeError, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved credentials to %s (mode 0600)\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "API token (sk_live_...)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Gravity API base URL")
	return cmd
}
