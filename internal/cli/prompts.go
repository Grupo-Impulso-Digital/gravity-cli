package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// resolvePrompt returns the server-hosted system prompt for name, falling back
// to the baked-in default on any error. Hosting prompts server-side lets them be
// tuned without a CLI release. Unavailability (the endpoint not being live yet)
// is silent; any other error prints a one-line notice to w. This is best-effort,
// like enrichKickoff — it can never regress a command.
func resolvePrompt(ctx context.Context, client *api.Client, name string, w io.Writer) string {
	text, err := client.Prompt(ctx, name)
	if err == nil && strings.TrimSpace(text) != "" {
		return text
	}
	if err != nil {
		var ae *api.APIError
		if !errors.As(err, &ae) || !ae.IsUnavailable() {
			fmt.Fprintf(w, "note: using built-in %s prompt (%v)\n", name, err)
		}
	}
	return prompts.Lookup(name)
}
