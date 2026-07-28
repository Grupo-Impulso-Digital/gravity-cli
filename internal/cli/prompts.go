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
