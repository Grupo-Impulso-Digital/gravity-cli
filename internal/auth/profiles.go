// Package auth manages CLI credentials: user profiles, credential resolution and the device login flow.
package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const (
	profilesFile   = "profiles.yaml"
	legacyFile     = "config.yaml"
	profilesV      = 2
	defaultProfile = "default"
)

// Profile is one stored credential.
type Profile struct {
	APIURL    string `yaml:"apiUrl,omitempty" json:"apiUrl,omitempty"`
	Org       string `yaml:"org,omitempty" json:"org,omitempty"`
	TokenKind string `yaml:"tokenKind,omitempty" json:"tokenKind,omitempty"`
	Token     string `yaml:"token" json:"-"`
	User      string `yaml:"user,omitempty" json:"user,omitempty"`
	ExpiresAt string `yaml:"expiresAt,omitempty" json:"expiresAt,omitempty"`
}

// Profiles is the content of ~/.config/gravity/profiles.yaml.
type Profiles struct {
	Version  int                `yaml:"version"`
	Current  string             `yaml:"current,omitempty"`
	Profiles map[string]Profile `yaml:"profiles"`

	path string
}

// ConfigDir returns the CLI's user configuration directory, honoring XDG_CONFIG_HOME.
func ConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gravity"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "gravity"), nil
}

// LegacyConfigPath returns the v0.x user configuration file, which CLI 1.x never writes.
func LegacyConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, legacyFile), nil
}

// TokenKindOf infers the token kind from its prefix.
func TokenKindOf(token string) string {
	switch {
	case strings.HasPrefix(token, "gr_user_"):
		return api.TokenKindUser
	case strings.HasPrefix(token, "gr_repo_"):
		return api.TokenKindRepo
	case strings.HasPrefix(token, "sk_live_"):
		return api.TokenKindOrg
	}
	return ""
}

// LoadProfiles reads profiles.yaml; when it is missing it imports the v0.x config.yaml token as profile "default".
func LoadProfiles() (*Profiles, bool, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, profilesFile)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		p := &Profiles{path: path}
		if err := yaml.Unmarshal(data, p); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
		if p.Profiles == nil {
			p.Profiles = map[string]Profile{}
		}
		return p, false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	p := &Profiles{Version: profilesV, Profiles: map[string]Profile{}, path: path}
	legacy, err := readLegacy(filepath.Join(dir, legacyFile))
	if err != nil || legacy.Token == "" {
		return p, false, err
	}
	p.Profiles[defaultProfile] = Profile{APIURL: legacy.APIURL, Token: legacy.Token, TokenKind: TokenKindOf(legacy.Token)}
	p.Current = defaultProfile
	if err := p.Save(); err != nil {
		return nil, false, err
	}
	return p, true, nil
}

type legacyCredentials struct {
	Token  string `yaml:"token"`
	APIURL string `yaml:"apiUrl"`
}

func readLegacy(path string) (legacyCredentials, error) {
	var c legacyCredentials
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		//nolint:nilerr // an unreadable v0.x file is simply not imported
		return legacyCredentials{}, nil
	}
	return c, nil
}

// Path returns where the profiles are stored.
func (p *Profiles) Path() string { return p.path }

// Get returns the named profile.
func (p *Profiles) Get(name string) (Profile, bool) {
	prof, ok := p.Profiles[name]
	return prof, ok
}

// Names returns the profile names in order.
func (p *Profiles) Names() []string {
	names := make([]string, 0, len(p.Profiles))
	for n := range p.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Put stores a profile and makes it current.
func (p *Profiles) Put(name string, prof Profile) {
	if p.Profiles == nil {
		p.Profiles = map[string]Profile{}
	}
	p.Profiles[name] = prof
	p.Current = name
}

// Remove deletes a profile; the current profile falls back to the first remaining one.
func (p *Profiles) Remove(name string) bool {
	if _, ok := p.Profiles[name]; !ok {
		return false
	}
	delete(p.Profiles, name)
	if p.Current == name {
		p.Current = ""
		if names := p.Names(); len(names) > 0 {
			p.Current = names[0]
		}
	}
	return true
}

// Save writes profiles.yaml with mode 0600.
func (p *Profiles) Save() error {
	if p.path == "" {
		dir, err := ConfigDir()
		if err != nil {
			return err
		}
		p.path = filepath.Join(dir, profilesFile)
	}
	p.Version = profilesV
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".profiles-*.yaml")
	if err != nil {
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return fmt.Errorf("write %s: %w", p.path, err)
	}
	return nil
}
