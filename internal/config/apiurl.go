// Package config loads and validates the v2 repository manifest and holds the shared configuration constants.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultAPIURL is the Gravity API base URL used when nothing else is configured.
const DefaultAPIURL = "https://api.gravitydocs.io"

// Environment variables recognized by the CLI.
const (
	EnvToken    = "GRAVITY_TOKEN"
	EnvAPIURL   = "GRAVITY_API_URL"
	EnvProfile  = "GRAVITY_PROFILE"
	EnvManifest = "GRAVITY_MANIFEST"
)

// ManifestFileName is the default manifest path, relative to the repository root.
const ManifestFileName = ".gravity.yaml"

var marketingHosts = map[string]bool{
	"gravitydocs.io":     true,
	"www.gravitydocs.io": true,
}

// CheckAPIURL reports whether raw is usable as the Gravity API base URL.
func CheckAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid API URL %q: must be an absolute http(s) URL", raw)
	}
	if marketingHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("invalid API URL %q: %s is the marketing site and serves no API; use %s", raw, u.Hostname(), DefaultAPIURL)
	}
	return nil
}
