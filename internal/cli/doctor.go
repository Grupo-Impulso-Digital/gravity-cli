package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/impulso/gravity-cli/internal/api"
)

func newDoctorCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check token validity and gateway configuration",
		Long:  "Calls /api/v1/whoami and /api/llm/v1/config and reports token validity, org, model, tone, and whether a provider key is configured.\nExits 2 on auth or network failure.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "API URL: %s\n", e.cfg.APIURL)

			who, err := e.client.WhoAmI(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("whoami failed: %w", classifyDoctorErr(err)))
			}
			fmt.Fprintf(out, "Token:   valid (%s)\n", who.KeyHint)
			fmt.Fprintf(out, "Org:     %s (%s)\n", who.OrganizationName, who.OrganizationID)
			if who.DefaultSiteSlug != nil {
				fmt.Fprintf(out, "Default site: %s\n", *who.DefaultSiteSlug)
			} else {
				fmt.Fprintln(out, "Default site: (none)")
			}

			cfg, err := e.client.LLMConfig(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("llm config failed: %w", classifyDoctorErr(err)))
			}
			fmt.Fprintf(out, "Provider: %s\n", cfg.Provider)
			fmt.Fprintf(out, "Model:    %s\n", cfg.Model)
			fmt.Fprintf(out, "Tone:     %s\n", cfg.Tone)
			fmt.Fprintf(out, "Has key:  %v\n", cfg.HasKey)
			if len(cfg.AvailableProviders) > 0 {
				fmt.Fprintf(out, "Providers: %v\n", cfg.AvailableProviders)
			}
			if !cfg.HasKey {
				fmt.Fprintln(out, "\nwarning: no provider key is configured for this organization; AI features will fail until one is added.")
			}
			fmt.Fprintln(out, "\nAll checks passed.")
			return nil
		},
	}
	return cmd
}

// classifyDoctorErr keeps auth errors readable for the doctor output.
func classifyDoctorErr(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.IsAuth() {
		return fmt.Errorf("token rejected (%d): %s", apiErr.StatusCode, apiErr.Message)
	}
	return err
}
