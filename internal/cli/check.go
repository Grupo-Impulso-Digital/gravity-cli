package cli

import (
	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/output"
)

func newCheckCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run drift and completeness checks against the docs",
	}
	cmd.AddCommand(newCheckAPICmd(gf), newCheckDocsCmd(gf))
	return cmd
}

func validateFormat(format string) error {
	if !output.ValidFormat(format) {
		return Failf(CodeError, "invalid --format %q (want text|json|github)", format)
	}
	return nil
}

func jsonFormat(format string, jsonOut bool) string {
	if jsonOut {
		return output.FormatJSON
	}
	return format
}
