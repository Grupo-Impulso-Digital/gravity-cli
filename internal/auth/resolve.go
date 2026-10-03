package auth

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

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
	TokenEnv     string
	TokenKind    string
	APIURL       string
	APIURLSource string
	ProfileName  string
	Profile      *Profile
}

// HostMismatchError refuses to send a profile token to a host other than the one that issued it.
type HostMismatchError struct {
	Profile string
	Issuer  string
	APIURL  string
	Source  string
}

func (e *HostMismatchError) Error() string {
	return fmt.Sprintf("refusing to send the token of profile %q to %s (API URL from %s): token was issued by %s; use --token or %s for that host, or sign in to it with `gravity login --api-url %s --profile <name>`",
		e.Profile, e.APIURL, sourceLabel(e.Source), e.Issuer, config.EnvToken, e.APIURL)
}

func sourceLabel(source string) string {
	switch source {
	case SourceFlag:
		return "--api-url"
	case SourceEnv:
		return config.EnvAPIURL
	case SourceManifest:
		return config.ManifestFileName + " apiUrl"
	}
	return source
}

// Resolve applies the token (flag, env, profile) and API URL (flag, env, manifest, profile, default) precedence; a profile token only ever goes to the host that issued it.
func Resolve(in Inputs, profiles *Profiles) (Credentials, error) {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var c Credentials
	var unresolved *UnresolvedTokenError
	if in.FlagToken != "" {
		c.Token, c.TokenSource = in.FlagToken, SourceFlag
	} else {
		for _, name := range TokenEnvs {
			v := strings.TrimSpace(getenv(name))
			if v == "" {
				continue
			}
			if shape := UnresolvedShape(v); shape != "" {
				if unresolved == nil {
					unresolved = &UnresolvedTokenError{Shape: shape}
				}
				unresolved.Vars = append(unresolved.Vars, name)
				continue
			}
			c.Token, c.TokenSource, c.TokenEnv = v, SourceEnv, name
			break
		}
		if c.Token == "" && unresolved != nil {
			return Credentials{}, unresolved
		}
	}
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
	if explicit && c.Profile == nil && c.Token == "" {
		return Credentials{}, fmt.Errorf("profile %q does not exist (run `gravity login --profile %s`)", name, name)
	}
	if c.Token == "" {
		if c.Profile != nil && c.Profile.Token != "" {
			c.Token, c.TokenSource = c.Profile.Token, SourceProfile
		} else {
			c.TokenSource = SourceNone
		}
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
	if c.TokenSource == SourceProfile {
		issuer := c.Profile.APIURL
		if issuer == "" {
			issuer = config.DefaultAPIURL
		}
		if !SameAPIURL(issuer, c.APIURL) {
			return Credentials{}, &HostMismatchError{Profile: c.ProfileName, Issuer: issuer, APIURL: c.APIURL, Source: c.APIURLSource}
		}
	}
	return c, nil
}

// TokenEnvs are the environment variables a token is read from, in order: the repository token first.
var TokenEnvs = []string{config.EnvRepoToken, config.EnvToken}

// UnresolvedTokenError reports token variables that hold an unexpanded CI variable reference instead of a token.
type UnresolvedTokenError struct {
	Vars  []string
	Shape string
}

func (e *UnresolvedTokenError) Error() string {
	return fmt.Sprintf("%s holds an unexpanded variable reference (%s) instead of a token: the CI secret is not defined for this job, or is not passed to it; store the repository token as %s", strings.Join(e.Vars, " and "), e.Shape, config.EnvRepoToken)
}

var unresolvedShapes = []struct {
	re    *regexp.Regexp
	shape string
}{
	{regexp.MustCompile(`^\$\([^()]*\)$`), "$(NAME), an undefined Azure Pipelines variable"},
	{regexp.MustCompile(`^\$\{\{.*\}\}$`), "${{ ... }}, an unevaluated workflow expression"},
	{regexp.MustCompile(`^\$\{?[A-Za-z_][A-Za-z0-9_]*\}?$`), "$NAME, an unexpanded shell variable"},
	{regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_]*%$`), "%NAME%, an unexpanded Windows variable"},
}

// UnresolvedShape describes the unexpanded variable reference a token value is, or returns "".
func UnresolvedShape(token string) string {
	t := strings.TrimSpace(token)
	for _, u := range unresolvedShapes {
		if u.re.MatchString(t) {
			return u.shape
		}
	}
	return ""
}

// TokenFrom names where the token came from: --token, the variable, or the profile.
func (c Credentials) TokenFrom() string {
	switch c.TokenSource {
	case SourceFlag:
		return "--token"
	case SourceEnv:
		return c.TokenEnv
	case SourceProfile:
		return "profile " + c.ProfileName
	}
	return c.TokenSource
}

// SameAPIURL reports whether two API base URLs address the same origin and path.
func SameAPIURL(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Host, ub.Host) &&
		strings.TrimRight(ua.Path, "/") == strings.TrimRight(ub.Path, "/")
}
