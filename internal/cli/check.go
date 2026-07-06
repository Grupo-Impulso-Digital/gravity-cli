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

// validateFormat returns an error for an unrecognised --format value.
func validateFormat(format string) error {
	if !output.ValidFormat(format) {
		return Failf(CodeError, "invalid --format %q (want text|json|github)", format)
	}
	return nil
}
