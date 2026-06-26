// Package config resolves runtime configuration from (highest precedence
// first): explicit flags, environment variables, a project-local
// .gravity.yaml, and the user-level ~/.config/gravity/config.yaml.
//
// Secrets are deliberately segregated: a token may come ONLY from the
// environment (CI) or the user-level credentials file (local `gravity auth
// login`). The committable project file (.gravity.yaml) must never carry a
// token — LoadProject rejects one outright (see project.go).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	yaml "go.yaml.in/yaml/v3"
)

// DefaultAPIURL is the single source of the default Gravity API base URL. Both
// `gravity init`/`auth login` defaults and the resolution fallback use it, so
// changing the target host is a one-line change.
const DefaultAPIURL = "https://gravity.dave-vermette-1.workers.dev"

// Environment variable names recognised by the CLI.
const (
	EnvToken     = "GRAVITY_TOKEN"
	EnvAPIURL    = "GRAVITY_API_URL"
	EnvSite      = "GRAVITY_SITE"
	EnvSpace     = "GRAVITY_SPACE"
	EnvNamespace = "GRAVITY_KNOWLEDGE_NAMESPACE"
)

// Config is the fully-resolved configuration for a command invocation.
type Config struct {
	Token     string
	APIURL    string
	Site      string
	Space     string
	Namespace string
}

// Flags carries the per-invocation flag overrides. Empty strings mean "unset".
type Flags struct {
	Token  string
	APIURL string
	Site   string
	Space  string
}

// ProjectFileName is the project-local config file written by `gravity init`.
const ProjectFileName = ".gravity.yaml"

// UserConfigPath returns the path to the user-level config file, honouring
// XDG_CONFIG_HOME when set.
func UserConfigPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gravity", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "gravity", "config.yaml"), nil
}

// Resolve merges all configuration sources with the documented precedence and
// returns only the flat runtime Config. It is a thin wrapper over
// ResolveWithProject for callers that don't need the typed manifest.
func Resolve(flags Flags, projectDir string) (Config, error) {
	cfg, _, err := ResolveWithProject(flags, projectDir)
	return cfg, err
}

// ResolveWithProject merges all configuration sources with the precedence:
//
//	flags > env > ./.gravity.yaml > ~/.config/gravity/config.yaml
//
// and also returns the typed project manifest (nil when no .gravity.yaml
// exists). The token is NEVER read from the project file. projectDir is the
// directory to look for .gravity.yaml in (usually the cwd).
func ResolveWithProject(flags Flags, projectDir string) (Config, *Project, error) {
	var cfg Config

	// Lowest precedence: user-level credentials file (token + apiUrl only).
	uc, err := loadUserCredentials()
	if err != nil {
		return Config{}, nil, err
	}
	if uc != nil {
		cfg.Token = uc.Token
		cfg.APIURL = uc.APIURL
	}

	// Next: the project manifest contributes site / apiUrl / default space /
	// knowledge namespace — but never a token.
	proj, err := LoadProject(projectDir)
	if err != nil {
		return Config{}, nil, err
	}
	if proj != nil {
		setIf(&cfg.Site, proj.Site)
		setIf(&cfg.APIURL, proj.APIURL)
		setIf(&cfg.Space, proj.Spaces.Default)
		setIf(&cfg.Namespace, proj.Knowledge.Namespace)
	}

	// Next: environment variables win over any file.
	setIf(&cfg.Token, os.Getenv(EnvToken))
	setIf(&cfg.APIURL, os.Getenv(EnvAPIURL))
	setIf(&cfg.Site, os.Getenv(EnvSite))
	setIf(&cfg.Space, os.Getenv(EnvSpace))
	setIf(&cfg.Namespace, os.Getenv(EnvNamespace))

	// Highest precedence: explicit flags.
	setIf(&cfg.Token, flags.Token)
	setIf(&cfg.APIURL, flags.APIURL)
	setIf(&cfg.Site, flags.Site)
	setIf(&cfg.Space, flags.Space)

	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	if err := cfg.validate(); err != nil {
		return Config{}, nil, err
	}
	return cfg, proj, nil
}

// setIf copies v into *dst only when v is non-empty, preserving any
// lower-precedence value otherwise.
func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// validate checks the resolved values that every command relies on.
func (c Config) validate() error {
	if c.APIURL != "" {
		u, err := url.Parse(c.APIURL)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid API URL %q: must be an absolute http(s) URL", c.APIURL)
		}
	}
	return nil
}

// ProjectConfig is the minimal subset written programmatically (e.g. the
// round-trip writer and migration). The rich, commented scaffold emitted by
// `gravity init` is produced from a template, not this struct.
type ProjectConfig struct {
	Version int    `yaml:"version"`
	Site    string `yaml:"site"`
	APIURL  string `yaml:"apiUrl"`
	Space   string `yaml:"space,omitempty"`
}

// WriteProjectConfig writes a minimal .gravity.yaml in dir. It never writes a
// token.
func WriteProjectConfig(dir string, pc ProjectConfig) (string, error) {
	if pc.Version == 0 {
		pc.Version = SchemaVersion
	}
	data, err := yaml.Marshal(pc)
	if err != nil {
		return "", fmt.Errorf("marshal project config: %w", err)
	}
	path := filepath.Join(dir, ProjectFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// UserCredentials is the subset persisted by `gravity auth login`.
type UserCredentials struct {
	Token  string `yaml:"token"`
	APIURL string `yaml:"apiUrl"`
}

// loadUserCredentials reads the user-level credentials file, returning (nil,
// nil) when it does not exist.
func loadUserCredentials() (*UserCredentials, error) {
	path, err := UserConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user config %s: %w", path, err)
	}
	var uc UserCredentials
	if err := yaml.Unmarshal(data, &uc); err != nil {
		return nil, fmt.Errorf("parse user config %s: %w", path, err)
	}
	return &uc, nil
}

// WriteUserCredentials writes the user-level config with 0600 permissions,
// creating the parent directory if necessary.
func WriteUserCredentials(creds UserCredentials) (string, error) {
	path, err := UserConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(creds)
	if err != nil {
		return "", fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
