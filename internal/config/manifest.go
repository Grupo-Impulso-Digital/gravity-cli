package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// ManifestVersion is the manifest schema version CLI 1.x reads.
const ManifestVersion = 2

// Manifest is a parsed, validated v2 .gravity.yaml.
type Manifest struct {
	Version   int    `yaml:"version" json:"version"`
	Product   string `yaml:"product,omitempty" json:"product,omitempty"`
	APIURL    string `yaml:"apiUrl,omitempty" json:"apiUrl,omitempty"`
	AppPasses string `yaml:"appPasses,omitempty" json:"appPasses,omitempty"`
	Code      *Code  `yaml:"code,omitempty" json:"code,omitempty"`
	Docs      *Docs  `yaml:"docs,omitempty" json:"docs,omitempty"`
	Passes    []Pass `yaml:"passes,omitempty" json:"passes,omitempty"`

	Path string          `yaml:"-" json:"-"`
	YAML []byte          `yaml:"-" json:"-"`
	Doc  json.RawMessage `yaml:"-" json:"-"`
	Hash string          `yaml:"-" json:"-"`
}

// Code holds the repository's code facts.
type Code struct {
	OpenAPI     []string `yaml:"openapi,omitempty" json:"openapi,omitempty"`
	Entrypoints []string `yaml:"entrypoints,omitempty" json:"entrypoints,omitempty"`
	Include     []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude     []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Units       *Units   `yaml:"units,omitempty" json:"units,omitempty"`
}

// Units sets the default unit kind and contributor roles of the repository.
type Units struct {
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Role Roles  `yaml:"role,omitempty" json:"role,omitempty"`
}

// Roles is one contributor role or a list of them.
type Roles []string

// UnmarshalYAML accepts a scalar role or a sequence of roles.
func (r *Roles) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*r = Roles{node.Value}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return fmt.Errorf("decode role: %w", err)
	}
	*r = list
	return nil
}

// MarshalYAML writes a single role as a scalar.
func (r Roles) MarshalYAML() (any, error) {
	if len(r) == 1 {
		return r[0], nil
	}
	return []string(r), nil
}

// Docs lists human-written documents passes may read as context.
type Docs struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// Pass is one pass declared in the manifest.
type Pass struct {
	Name         string         `yaml:"name" json:"name"`
	Title        string         `yaml:"title,omitempty" json:"title,omitempty"`
	Kind         string         `yaml:"kind" json:"kind"`
	Template     string         `yaml:"template,omitempty" json:"template,omitempty"`
	Target       string         `yaml:"target,omitempty" json:"target,omitempty"`
	Triggers     []string       `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Branches     []string       `yaml:"branches,omitempty" json:"branches,omitempty"`
	Scope        *Scope         `yaml:"scope,omitempty" json:"scope,omitempty"`
	Audiences    []string       `yaml:"audiences,omitempty" json:"audiences,omitempty"`
	Instructions string         `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	Publish      string         `yaml:"publish,omitempty" json:"publish,omitempty"`
	Enabled      *bool          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Options      map[string]any `yaml:"options,omitempty" json:"options,omitempty"`
}

// Scope narrows the changes a pass reacts to.
type Scope struct {
	Paths   []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Units   []string `yaml:"units,omitempty" json:"units,omitempty"`
}

// IsEnabled reports whether the pass is enabled (the default).
func (p Pass) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// PassByName returns the declared pass with that name.
func (m *Manifest) PassByName(name string) (Pass, bool) {
	if m == nil {
		return Pass{}, false
	}
	for _, p := range m.Passes {
		if p.Name == name {
			return p, true
		}
	}
	return Pass{}, false
}

// CodeExclude returns code.exclude, or nil.
func (m *Manifest) CodeExclude() []string {
	if m == nil || m.Code == nil {
		return nil
	}
	return m.Code.Exclude
}

// CodeInclude returns code.include, or nil.
func (m *Manifest) CodeInclude() []string {
	if m == nil || m.Code == nil {
		return nil
	}
	return m.Code.Include
}

// DefaultScope returns the paths a pass without scope.paths reacts to: code.include plus code.openapi, else everything.
func (m *Manifest) DefaultScope() []string {
	include := m.CodeInclude()
	if len(include) == 0 {
		return []string{"**"}
	}
	return append(append([]string{}, include...), m.OpenAPIFiles()...)
}

// OpenAPIFiles returns code.openapi, or nil.
func (m *Manifest) OpenAPIFiles() []string {
	if m == nil || m.Code == nil {
		return nil
	}
	return m.Code.OpenAPI
}

// ManifestPath returns the manifest location: override if set, else .gravity.yaml under dir.
func ManifestPath(dir, override string) string {
	if override == "" {
		return filepath.Join(dir, ManifestFileName)
	}
	if filepath.IsAbs(override) {
		return override
	}
	return filepath.Join(dir, override)
}

// Load reads and validates the manifest at path; a missing file returns (nil, nil).
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	m, err := Parse(data)
	if err != nil {
		var me *ManifestError
		if errors.As(err, &me) {
			me.File = path
		}
		var ve *V1Error
		if errors.As(err, &ve) {
			ve.File = path
		}
		return nil, err
	}
	m.Path = path
	return m, nil
}

// DetectV1 reports whether manifest bytes are a v1 manifest.
func DetectV1(data []byte) bool {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	if doc == nil {
		return true
	}
	root, ok := doc.(map[string]any)
	return ok && IsV1(root)
}

// Parse decodes and validates manifest bytes.
func Parse(data []byte) (*Manifest, error) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &ManifestError{Issues: []Issue{{Message: fmt.Sprintf("invalid YAML: %v", err)}}}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, &ManifestError{Issues: []Issue{{Message: "the manifest must be a YAML mapping"}}}
	}
	if issues := tokenIssues(root, nil); len(issues) > 0 {
		return nil, &ManifestError{Issues: issues}
	}
	if IsV1(root) {
		return nil, &V1Error{}
	}
	jsonDoc, err := json.Marshal(jsonCompatible(root))
	if err != nil {
		return nil, &ManifestError{Issues: []Issue{{Message: fmt.Sprintf("manifest is not representable as JSON: %v", err)}}}
	}
	if issues := validate(jsonDoc); len(issues) > 0 {
		return nil, &ManifestError{Issues: issues}
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, &ManifestError{Issues: []Issue{{Message: err.Error()}}}
	}
	canon, err := normalize.CanonicalJSON(json.RawMessage(jsonDoc))
	if err != nil {
		return nil, fmt.Errorf("hash manifest: %w", err)
	}
	m.YAML = data
	m.Doc = canon
	m.Hash = normalize.SHA256(canon)
	return &m, nil
}

func jsonCompatible(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = jsonCompatible(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = jsonCompatible(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = jsonCompatible(e)
		}
		return out
	default:
		return t
	}
}
