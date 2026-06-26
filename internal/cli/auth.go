package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/config"
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
		Long:  "Persist an API token (mode 0600) so local runs are authenticated.\nThe token is stored only in the user-level credentials file — never in .gravity.yaml.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if token == "" {
				token = os.Getenv(config.EnvToken)
			}
			if token == "" {
				return Failf(CodeError, "--token is required (or set %s)", config.EnvToken)
			}
			if apiURL == "" {
				apiURL = config.DefaultAPIURL
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
	cmd.Flags().StringVar(&token, "token", "", "API token (sk_live_...); falls back to "+config.EnvToken)
	cmd.Flags().StringVar(&apiURL, "api-url", "", fmt.Sprintf("Gravity API base URL (default %s)", config.DefaultAPIURL))
	return cmd
}
