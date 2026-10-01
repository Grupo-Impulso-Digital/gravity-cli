// Package config resolves runtime configuration from flags, env, and config files.
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

// DefaultAPIURL is the single source of the default Gravity API base URL.
const DefaultAPIURL = "https://api.gravitydocs.io"

// LegacyAPIURL is the app host, which still proxies the API for older clients.
const LegacyAPIURL = "https://app.gravitydocs.io"

// Environment variable names recognized by the CLI.
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

// Flags carries the per-invocation flag overrides.
type Flags struct {
	Token  string
	APIURL string
	Site   string
	Space  string
}

// ProjectFileName is the project-local config file written by `gravity init`.
const ProjectFileName = ".gravity.yaml"

// UserConfigPath returns the path to the user-level config file.
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

// Resolve merges all configuration sources with the documented precedence.
func Resolve(flags Flags, projectDir string) (Config, error) {
	cfg, _, err := ResolveWithProject(flags, projectDir)
	return cfg, err
}

// ResolveWithProject merges all configuration sources with the documented precedence.
func ResolveWithProject(flags Flags, projectDir string) (Config, *Project, error) {
	var cfg Config

	uc, err := loadUserCredentials()
	if err != nil {
		return Config{}, nil, err
	}
	if uc != nil {
		cfg.Token = uc.Token
		cfg.APIURL = uc.APIURL
	}

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

	setIf(&cfg.Token, os.Getenv(EnvToken))
	setIf(&cfg.APIURL, os.Getenv(EnvAPIURL))
	setIf(&cfg.Site, os.Getenv(EnvSite))
	setIf(&cfg.Space, os.Getenv(EnvSpace))
	setIf(&cfg.Namespace, os.Getenv(EnvNamespace))

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

func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func (c Config) validate() error {
	if c.APIURL != "" {
		u, err := url.Parse(c.APIURL)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid API URL %q: must be an absolute http(s) URL", c.APIURL)
		}
	}
	return nil
}

// UserCredentials is the subset persisted by `gravity auth login`.
type UserCredentials struct {
	Token  string `yaml:"token"`
	APIURL string `yaml:"apiUrl"`
}

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

// LoadUserCredentials reads the user-level credentials file.
func LoadUserCredentials() (*UserCredentials, error) {
	return loadUserCredentials()
}

// RemoveUserCredentials deletes the user-level credentials file.
func RemoveUserCredentials() (path string, existed bool, err error) {
	path, err = UserConfigPath()
	if err != nil {
		return "", false, err
	}
	if err = os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return path, false, nil
		}
		return path, false, fmt.Errorf("remove %s: %w", path, err)
	}
	return path, true, nil
}

// WriteUserCredentials writes the user-level config with 0600 permissions.
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
