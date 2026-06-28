package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/impulso/gravity-cli/internal/api"
)

// Optional platform feature keys reported by /whoami.
const (
	featureCaptures      = "captures"
	featureNucleus       = "nucleus"
	featureDocsGenerate  = "docs-generate"
	featureBlockAudience = "block-audience"
)

// skippableFeature handles "endpoint not live yet" uniformly. When err is an
// IsUnavailable API error it prints a friendly notice and reports skipped=true;
// with require=true it converts to a hard CodeError instead. Other errors pass
// through unchanged.
func skippableFeature(err error, feature string, w io.Writer, require bool) (skipped bool, out error) {
	if err == nil {
		return false, nil
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.IsUnavailable() {
		if require {
			return false, Failf(CodeError, "%s is not yet available on this platform", feature)
		}
		fmt.Fprintf(w, "note: %s is not yet available on this platform; skipping\n", feature)
		return true, nil
	}
	return false, err
}

// featureAvailable reports whether the platform advertises a feature, so an
// expensive command can bail out before doing work. A nil/absent feature map
// means "not yet available". Network/auth errors are returned.
func featureAvailable(ctx context.Context, client *api.Client, feature string) (bool, error) {
	who, err := client.WhoAmI(ctx)
	if err != nil {
		return false, err
	}
	return who.Features[feature], nil
}
