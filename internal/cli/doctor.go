package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func newDoctorCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate configuration, token, and gateway",
		Long:  "Loads and validates .gravity.yaml, prints the resolved configuration (and where each value came from), then calls /api/v1/whoami and /api/llm/v1/config to verify the token and gateway.\nExits 2 on a config, auth, or network failure.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printConfigSummary(out, gf, e)

			if err := e.requireAuth(); err != nil {
				return err
			}

			who, err := e.client.WhoAmI(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("whoami failed: %w", classifyAuthErr(err)))
			}
			fmt.Fprintf(out, "\nToken:   valid (%s)\n", who.KeyHint)
			fmt.Fprintf(out, "Org:     %s (%s)\n", who.OrganizationName, who.OrganizationID)
			if who.DefaultSiteSlug != nil {
				fmt.Fprintf(out, "Default site: %s\n", *who.DefaultSiteSlug)
			} else {
				fmt.Fprintln(out, "Default site: (none)")
			}
			if who.APIURL != "" && strings.TrimRight(e.cfg.APIURL, "/") == config.LegacyAPIURL && who.APIURL != config.LegacyAPIURL {
				fmt.Fprintf(out, "note: %s is the legacy API host; set apiUrl to %s (the platform's advertised API host)\n", config.LegacyAPIURL, who.APIURL)
			}
			fmt.Fprintf(out, "Space hierarchy (subspaces/home pages/collections): %s\n", featureState(who.Features[featureSpaceHierarchy]))
			fmt.Fprintf(out, "Space metadata (declared space type/visibility): %s\n", featureState(who.Features[featureSpaceMetadata]))
			fmt.Fprintf(out, "Connected repos (registration + write attribution): %s\n", featureState(who.Features[featureRepos]))
			fmt.Fprintf(out, "Feature inventory: %s\n", featureState(who.Features[featureInventory]))
			fmt.Fprintf(out, "Coverage reporting: %s\n", featureState(who.Features[featureCoverage]))
			fmt.Fprintf(out, "Doc Agent runs (CLI-triggered): %s\n", featureState(who.Features[featureDocAgentRuns]))
			fmt.Fprintf(out, "Page languages (i18n translation requests): %s\n", featureState(who.Features[featurePageLanguages]))

			cfg, err := e.client.LLMConfig(cmd.Context())
			if err != nil {
				return Fail(CodeError, fmt.Errorf("llm config failed: %w", classifyAuthErr(err)))
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

func printConfigSummary(out io.Writer, gf *globalFlags, e *env) {
	var projSite, projAPIURL, projSpace string
	if e.proj != nil {
		projSite = e.proj.Site
		projAPIURL = e.proj.APIURL
		projSpace = e.proj.Spaces.Default
	}

	apiSrc := fieldSource(gf.apiURL, config.EnvAPIURL, projAPIURL)
	if apiSrc == "" {
		if e.cfg.APIURL == config.DefaultAPIURL {
			apiSrc = "default"
		} else {
			apiSrc = "user file"
		}
	}
	fmt.Fprintf(out, "API URL: %s  (%s)\n", e.cfg.APIURL, apiSrc)
	fmt.Fprintf(out, "Site:    %s  (%s)\n", orUnset(e.cfg.Site), srcOrUnset(fieldSource(gf.site, config.EnvSite, projSite)))
	fmt.Fprintf(out, "Space:   %s  (%s)\n", orUnset(e.cfg.Space), srcOrUnset(fieldSource("", config.EnvSpace, projSpace)))
	fmt.Fprintf(out, "Token:   %s  (%s)\n", tokenState(e.cfg.Token), tokenSource(gf.token))

	if e.proj != nil {
		fmt.Fprintf(out, "\nProject: %s\n", config.ProjectFileName)
		if e.proj.Product.Repo != "" {
			fmt.Fprintf(out, "Product: %s / %s", orUnset(e.proj.Product.Slug), e.proj.Product.Repo)
			if e.proj.Product.Role != "" {
				fmt.Fprintf(out, " (%s)", e.proj.Product.Role)
			}
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "Mappings: %d source(s), %d document(s)\n", len(e.proj.Sources), len(e.proj.Documents))
		if ns := e.knowledgeNamespace(); ns != "" {
			fmt.Fprintf(out, "Knowledge namespace: %s\n", ns)
		}
	} else {
		fmt.Fprintf(out, "\nProject: no %s found (run `gravity init`)\n", config.ProjectFileName)
	}
}

func fieldSource(flagVal, envName, projVal string) string {
	if flagVal != "" {
		return "flag"
	}
	if os.Getenv(envName) != "" {
		return "env " + envName
	}
	if projVal != "" {
		return "project (" + config.ProjectFileName + ")"
	}
	return ""
}

func tokenSource(flagVal string) string {
	if flagVal != "" {
		return "flag"
	}
	if os.Getenv(config.EnvToken) != "" {
		return "env " + config.EnvToken
	}
	if path, err := config.UserConfigPath(); err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return "user file"
		}
	}
	return "unset"
}

func tokenState(token string) string {
	if token == "" {
		return "(unset)"
	}
	return "set"
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func srcOrUnset(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

func featureState(available bool) string {
	if available {
		return "available"
	}
	return "not yet available"
}

func classifyAuthErr(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.IsAuth() {
		return fmt.Errorf("token rejected (%d): %s", apiErr.StatusCode, apiErr.Message)
	}
	return err
}
