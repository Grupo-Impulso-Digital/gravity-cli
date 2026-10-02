package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

func versionLine() string {
	b := version.Build()
	var extra []string
	if b.Commit != "" {
		extra = append(extra, "commit "+shortSHA(b.Commit)+strings.TrimPrefix(b.Commit, shortCommitPrefix(b.Commit)))
	}
	if b.BuildDate != "" {
		extra = append(extra, "built "+b.BuildDate)
	}
	extra = append(extra, b.GoVersion, b.Platform)
	return fmt.Sprintf("gravity %s (%s)", b.Version, strings.Join(extra, ", "))
}

func shortCommitPrefix(commit string) string {
	sha, _, _ := strings.Cut(commit, "-")
	return sha
}

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, build date, Go version and platform",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			a.ui.Always("%s", versionLine())
			return a.ui.Result(version.Build())
		},
	}
}
