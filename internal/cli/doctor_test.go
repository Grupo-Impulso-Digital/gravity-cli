package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func TestPrintConfigSummaryNeverDoublesUnset(t *testing.T) {
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvSpace, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	printConfigSummary(&buf, &globalFlags{}, &env{cfg: config.Config{APIURL: config.DefaultAPIURL}})
	out := buf.String()
	if strings.Contains(out, "(unset)") {
		t.Errorf("summary still prints (unset):\n%s", out)
	}
	for _, want := range []string{
		"Site:    not set — pass --site",
		"Space:   not set — set GRAVITY_SPACE",
		"Token:   not set — set GRAVITY_TOKEN",
		"API URL: " + config.DefaultAPIURL + "  (default)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestPrintConfigSummaryNamesSources(t *testing.T) {
	t.Setenv(config.EnvToken, "sk_live_x")
	t.Setenv(config.EnvSite, "")
	t.Setenv(config.EnvSpace, "")

	proj := &config.Project{Site: "acme", Spaces: config.Spaces{Default: "guides"}}
	var buf bytes.Buffer
	printConfigSummary(&buf, &globalFlags{}, &env{
		cfg:  config.Config{APIURL: config.DefaultAPIURL, Site: "acme", Space: "guides", Token: "sk_live_x"},
		proj: proj,
	})
	out := buf.String()
	for _, want := range []string{
		"Site:    acme  (project (.gravity.yaml))",
		"Space:   guides  (project (.gravity.yaml))",
		"Token:   set  (env GRAVITY_TOKEN)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}
