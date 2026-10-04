package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func skipLabel(reason string) string {
	switch reason {
	case "":
		return ""
	case api.SkipTriggerMismatch:
		return "not on this trigger"
	case api.SkipBranchMismatch:
		return "not on this branch"
	case api.SkipTargetUnapproved:
		return "target awaits approval"
	case api.SkipTargetMissing:
		return "target missing"
	case api.SkipScopeMissing:
		return "token lacks scopes"
	case api.SkipModuleDisabled:
		return "module not licensed"
	case api.SkipNotSelected:
		return "not selected"
	case api.SkipFirstRunManual:
		return "first run is local: gravity run --dry-run"
	case api.SkipScopeUnchanged, api.SkipNoChanges:
		return "nothing changed in scope"
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func optional(s string, cond bool) string {
	if cond {
		return s
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

func appBaseURL(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "https://app.gravitydocs.io"
	}
	if host, ok := strings.CutPrefix(u.Host, "api."); ok {
		u.Host = host
		if strings.Count(host, ".") == 1 {
			u.Host = "app." + host
		}
	}
	u.Path, u.RawQuery = "", ""
	return strings.TrimSuffix(u.String(), "/")
}
