// Package config resolves runtime configuration from (highest precedence
// first): explicit flags, environment variables, a project-local
// .gravity.yaml, and the user-level ~/.config/gravity/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Environment variable names recognised by the CLI.
const (
	EnvToken  = "GRAVITY_TOKEN"
	EnvAPIURL = "GRAVITY_API_URL"
	EnvSite   = "GRAVITY_SITE"
	EnvSpace  = "GRAVITY_SPACE"
)

// Config is the fully-resolved configuration for a command invocation.
type Config struct {
	Token  string
	APIURL string
	Site   string
	Space  string
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

// Resolve merges all configuration sources with the documented precedence:
//
//	flags > env > ./.gravity.yaml > ~/.config/gravity/config.yaml
//
// projectDir is the directory to look for .gravity.yaml in (usually the cwd).
func Resolve(flags Flags, projectDir string) (Config, error) {
	v := viper.New()
	v.SetDefault("token", "")
	v.SetDefault("apiUrl", "")
	v.SetDefault("site", "")
	v.SetDefault("space", "")

	// Lowest precedence: user-level config file.
	if userPath, err := UserConfigPath(); err == nil {
		if _, statErr := os.Stat(userPath); statErr == nil {
			v.SetConfigFile(userPath)
			if err := v.ReadInConfig(); err != nil {
				return Config{}, fmt.Errorf("read user config %s: %w", userPath, err)
			}
		}
	}

	// Next: project-local config file overlays the user config.
	projectPath := filepath.Join(projectDir, ProjectFileName)
	if _, statErr := os.Stat(projectPath); statErr == nil {
		pv := viper.New()
		pv.SetConfigFile(projectPath)
		if err := pv.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read project config %s: %w", projectPath, err)
		}
		mergeIfSet(v, pv, "token", "apiUrl", "site", "space")
	}

	cfg := Config{
		Token:  v.GetString("token"),
		APIURL: v.GetString("apiUrl"),
		Site:   v.GetString("site"),
		Space:  v.GetString("space"),
	}

	// Next: environment variables win over any file.
	if val := os.Getenv(EnvToken); val != "" {
		cfg.Token = val
	}
	if val := os.Getenv(EnvAPIURL); val != "" {
		cfg.APIURL = val
	}
	if val := os.Getenv(EnvSite); val != "" {
		cfg.Site = val
	}
	if val := os.Getenv(EnvSpace); val != "" {
		cfg.Space = val
	}

	// Highest precedence: explicit flags.
	if flags.Token != "" {
		cfg.Token = flags.Token
	}
	if flags.APIURL != "" {
		cfg.APIURL = flags.APIURL
	}
	if flags.Site != "" {
		cfg.Site = flags.Site
	}
	if flags.Space != "" {
		cfg.Space = flags.Space
	}

	return cfg, nil
}

// mergeIfSet copies keys from src into dst only when src actually holds a
// non-empty value for them, preserving lower-precedence values otherwise.
func mergeIfSet(dst, src *viper.Viper, keys ...string) {
	for _, k := range keys {
		if src.IsSet(k) {
			if val := src.GetString(k); val != "" {
				dst.Set(k, val)
			}
		}
	}
}

// ProjectConfig is the subset persisted by `gravity init`.
type ProjectConfig struct {
	Site   string `yaml:"site"`
	APIURL string `yaml:"apiUrl"`
	Space  string `yaml:"space,omitempty"`
}

// WriteProjectConfig writes .gravity.yaml in dir.
func WriteProjectConfig(dir string, pc ProjectConfig) (string, error) {
	v := viper.New()
	v.Set("site", pc.Site)
	v.Set("apiUrl", pc.APIURL)
	if pc.Space != "" {
		v.Set("space", pc.Space)
	}
	path := filepath.Join(dir, ProjectFileName)
	if err := v.WriteConfigAs(path); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// UserCredentials is the subset persisted by `gravity auth login`.
type UserCredentials struct {
	Token  string `yaml:"token"`
	APIURL string `yaml:"apiUrl"`
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
	v := viper.New()
	v.Set("token", creds.Token)
	v.Set("apiUrl", creds.APIURL)
	if err := v.WriteConfigAs(path); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("chmod %s: %w", path, err)
	}
	return path, nil
}
