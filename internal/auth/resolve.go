package auth

import (
	"fmt"
	"os"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

// Sources of a resolved value.
const (
	SourceFlag     = "flag"
	SourceEnv      = "env"
	SourceManifest = "manifest"
	SourceProfile  = "profile"
	SourceDefault  = "default"
	SourceNone     = "none"
)

// Inputs are the credential sources outside the profiles file.
type Inputs struct {
	FlagToken      string
	FlagAPIURL     string
	FlagProfile    string
	ManifestAPIURL string
	Getenv         func(string) string
}

// Credentials are the resolved token and API URL of an invocation.
type Credentials struct {
	Token        string
	TokenSource  string
	TokenKind    string
	APIURL       string
	APIURLSource string
	ProfileName  string
	Profile      *Profile
}

// Resolve applies the token (flag, env, profile) and API URL (flag, env, manifest, profile, default) precedence.
func Resolve(in Inputs, profiles *Profiles) (Credentials, error) {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var c Credentials
	name := in.FlagProfile
	if name == "" {
		name = getenv(config.EnvProfile)
	}
	explicit := name != ""
	if name == "" && profiles != nil {
		name = profiles.Current
	}
	if name != "" && profiles != nil {
		if prof, ok := profiles.Get(name); ok {
			c.ProfileName, c.Profile = name, &prof
		}
	}
	if explicit && c.Profile == nil {
		return Credentials{}, fmt.Errorf("profile %q does not exist (run `gravity login --profile %s`)", name, name)
	}
	switch {
	case in.FlagToken != "":
		c.Token, c.TokenSource = in.FlagToken, SourceFlag
	case getenv(config.EnvToken) != "":
		c.Token, c.TokenSource = getenv(config.EnvToken), SourceEnv
	case c.Profile != nil && c.Profile.Token != "":
		c.Token, c.TokenSource = c.Profile.Token, SourceProfile
	default:
		c.TokenSource = SourceNone
	}
	c.TokenKind = TokenKindOf(c.Token)
	switch {
	case in.FlagAPIURL != "":
		c.APIURL, c.APIURLSource = in.FlagAPIURL, SourceFlag
	case getenv(config.EnvAPIURL) != "":
		c.APIURL, c.APIURLSource = getenv(config.EnvAPIURL), SourceEnv
	case in.ManifestAPIURL != "":
		c.APIURL, c.APIURLSource = in.ManifestAPIURL, SourceManifest
	case c.Profile != nil && c.Profile.APIURL != "":
		c.APIURL, c.APIURLSource = c.Profile.APIURL, SourceProfile
	default:
		c.APIURL, c.APIURLSource = config.DefaultAPIURL, SourceDefault
	}
	if err := config.CheckAPIURL(c.APIURL); err != nil {
		return Credentials{}, err
	}
	return c, nil
}

// HostMismatch returns the profile's API URL when the token came from a profile issued for another host.
func (c Credentials) HostMismatch() string {
	if c.TokenSource != SourceProfile || c.Profile == nil || c.Profile.APIURL == "" || c.Profile.APIURL == c.APIURL {
		return ""
	}
	return c.Profile.APIURL
}
