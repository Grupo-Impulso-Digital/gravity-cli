package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// SchemaVersion is the highest .gravity.yaml schema version this CLI understands.
const SchemaVersion = 1

// Project is the declarative repo manifest loaded from .gravity.yaml.
type Project struct {
	Version int    `yaml:"version"`
	Site    string `yaml:"site"`
	APIURL  string `yaml:"apiUrl"`

	Product      Product      `yaml:"product"`
	Spaces       Spaces       `yaml:"spaces"`
	Sources      []SourceMap  `yaml:"sources"`
	Documents    []DocMap     `yaml:"documents"`
	ReleaseNotes ReleaseNotes `yaml:"releaseNotes"`
	Knowledge    Knowledge    `yaml:"knowledge"`

	Discovery Discovery `yaml:"discovery"`
	I18n      I18n      `yaml:"i18n"`
	Coverage  Coverage  `yaml:"coverage"`

	LegacySpace string `yaml:"space"`
}

// Product identifies the (possibly multi-repo) product this repo belongs to.
type Product struct {
	Slug string `yaml:"slug"`
	Repo string `yaml:"repo"`
	Role string `yaml:"role"`
}

// Unit kinds a repo's documentable things are classified as.
const (
	UnitsAuto      = "auto"
	UnitFeature    = "feature"
	UnitService    = "service"
	UnitSystem     = "system"
	UnitAPI        = "api"
	UnitCapability = "capability"
)

// Product roles recognized by ResolveUnitKind.
const (
	RoleFrontend = "frontend"
	RoleAPI      = "api"
	RoleService  = "service"
	RoleDocs     = "docs"
)

// Discovery configures how `gravity docs generate` surveys the repo.
type Discovery struct {
	Units       string             `yaml:"units"`
	Include     []string           `yaml:"include"`
	Exclude     []string           `yaml:"exclude"`
	Entrypoints []string           `yaml:"entrypoints"`
	Audiences   DiscoveryAudiences `yaml:"audiences"`
}

// DiscoveryAudiences sets the audiences a planned page inherits when the planner names none.
type DiscoveryAudiences struct {
	Default []string `yaml:"default"`
}

// I18n declares the languages this repo's pages should exist in.
type I18n struct {
	Languages []string `yaml:"languages"`
}

// Coverage sets the documentation-coverage bar this repo holds itself to.
type Coverage struct {
	Min     float64  `yaml:"min"`
	Require []string `yaml:"require"`
}

// MaxLanguages caps i18n.languages, mirroring the platform's per-page limit.
const MaxLanguages = 24

// ResolveUnitKind returns the effective default unit kind for this repo.
func (p *Project) ResolveUnitKind() string {
	switch p.Discovery.Units {
	case "", UnitsAuto:
	case UnitFeature, UnitService, UnitSystem, UnitAPI, UnitCapability:
		return p.Discovery.Units
	default:
		return UnitFeature
	}
	switch p.Product.Role {
	case RoleAPI, RoleService:
		return UnitService
	default:
		return UnitFeature
	}
}

// Spaces declares where this repo's pages live.
type Spaces struct {
	Default string   `yaml:"default"`
	Shared  []string `yaml:"shared"`
	Parent  string   `yaml:"parent"`
	Home    string   `yaml:"home"`
}

// SourceMap binds a source artifact (a spec or a code file) to a doc page.
type SourceMap struct {
	Source     string `yaml:"source"`
	Kind       string `yaml:"kind"`
	Space      string `yaml:"space"`
	Page       string `yaml:"page"`
	Title      string `yaml:"title"`
	Generator  string `yaml:"generator"`
	Collection string `yaml:"collection"`
}

// DocMap ingests a human-authored Markdown file into Gravity as native blocks.
type DocMap struct {
	File       string `yaml:"file"`
	Space      string `yaml:"space"`
	Page       string `yaml:"page"`
	Title      string `yaml:"title"`
	Ownership  string `yaml:"ownership"`
	As         string `yaml:"as"`
	Version    string `yaml:"version"`
	Collection string `yaml:"collection"`
}

// ReleaseNotes configures the git-derived release-notes command.
type ReleaseNotes struct {
	Space     string `yaml:"space"`
	Changelog string `yaml:"changelog"`
}

// Knowledge declares the nucleus memory namespace shared across a product's repos.
type Knowledge struct {
	Namespace string `yaml:"namespace"`
	Scope     string `yaml:"scope"`
}

// LoadProject reads and validates the project manifest in projectDir.
func LoadProject(projectDir string) (*Project, error) {
	path := filepath.Join(projectDir, ProjectFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if err := guardNoToken(data, path); err != nil {
		return nil, err
	}

	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	p.applyDefaults(projectDir)
	if err := p.Validate(path); err != nil {
		return nil, err
	}
	return &p, nil
}

func guardNoToken(data []byte, path string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		//nolint:nilerr // malformed YAML is intentionally ignored here; the typed decode in LoadProject reports it.
		return nil
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "token" {
			return fmt.Errorf(
				"%s:%d: a token must not be committed to %s; set %s (CI) or run `gravity auth login` (local)",
				path, root.Content[i].Line, ProjectFileName, EnvToken,
			)
		}
	}
	return nil
}

func (p *Project) applyDefaults(projectDir string) {
	if p.Version == 0 {
		p.Version = SchemaVersion
	}
	if p.Product.Slug == "" {
		p.Product.Slug = p.Site
	}
	if p.Product.Repo == "" {
		p.Product.Repo = filepath.Base(projectDir)
	}
	if p.Spaces.Default == "" {
		if p.LegacySpace != "" {
			p.Spaces.Default = p.LegacySpace
		} else {
			p.Spaces.Default = p.Product.Repo
		}
	}
	if p.ReleaseNotes.Space == "" {
		if p.LegacySpace != "" {
			p.ReleaseNotes.Space = p.LegacySpace
		} else {
			p.ReleaseNotes.Space = "changelog"
		}
	}
	if p.ReleaseNotes.Changelog == "" {
		p.ReleaseNotes.Changelog = "CHANGELOG.md"
	}
	if p.Knowledge.Namespace == "" {
		p.Knowledge.Namespace = p.Product.Slug
	}
	if p.Knowledge.Scope == "" {
		p.Knowledge.Scope = p.Product.Repo
	}
	if p.Discovery.Units == "" {
		p.Discovery.Units = UnitsAuto
	}
}

// Validate collects load-time errors so the user sees all problems at once.
func (p *Project) Validate(path string) error {
	var errs []string
	if p.Version > SchemaVersion {
		errs = append(errs, fmt.Sprintf("version %d is newer than this gravity CLI supports (max %d); upgrade the CLI", p.Version, SchemaVersion))
	}
	if p.APIURL != "" {
		if u, err := url.Parse(p.APIURL); err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Sprintf("apiUrl %q must be an absolute http(s) URL", p.APIURL))
		}
	}
	for i, s := range p.Sources {
		if strings.TrimSpace(s.Source) == "" {
			errs = append(errs, fmt.Sprintf("sources[%d]: 'source' is required", i))
		}
		if strings.TrimSpace(s.Page) == "" {
			errs = append(errs, fmt.Sprintf("sources[%d]: 'page' is required", i))
		}
		switch s.Kind {
		case "", "openapi":
		case "code":
			errs = append(errs, fmt.Sprintf(
				"sources[%d]: kind \"code\" is no longer supported (it was never implemented). "+
					"Delete this mapping and let `gravity docs generate` discover the file as a "+
					"unit — add its path to discovery.include/discovery.entrypoints. Use kind: "+
					"openapi only for an OpenAPI spec.", i,
			))
		default:
			errs = append(errs, fmt.Sprintf("sources[%d]: kind %q must be openapi", i, s.Kind))
		}
		if msg := checkRepoRelative(s.Source); s.Source != "" && msg != "" {
			errs = append(errs, fmt.Sprintf("sources[%d].source: %s", i, msg))
		}
	}
	for i, d := range p.Documents {
		if strings.TrimSpace(d.File) == "" {
			errs = append(errs, fmt.Sprintf("documents[%d]: 'file' is required", i))
		}
		if msg := checkRepoRelative(d.File); d.File != "" && msg != "" {
			errs = append(errs, fmt.Sprintf("documents[%d].file: %s", i, msg))
		}
		switch d.As {
		case "", "page":
			if strings.TrimSpace(d.Page) == "" {
				errs = append(errs, fmt.Sprintf("documents[%d]: 'page' is required for as=page", i))
			}
		case "release":
		default:
			errs = append(errs, fmt.Sprintf("documents[%d]: as %q must be page|release", i, d.As))
		}
		switch d.Ownership {
		case "", "machine", "hybrid", "human":
		default:
			errs = append(errs, fmt.Sprintf("documents[%d]: ownership %q must be machine|hybrid|human", i, d.Ownership))
		}
	}
	if strings.TrimSpace(p.Spaces.Default) == "" {
		errs = append(errs, "spaces.default could not be derived (set spaces.default or product.repo)")
	}
	if p.Spaces.Parent != "" {
		if msg := checkSlug(p.Spaces.Parent); msg != "" {
			errs = append(errs, "spaces.parent: "+msg)
		}
		if p.Spaces.Parent == p.Spaces.Default {
			errs = append(errs, "spaces.parent must name a different space than spaces.default (a space cannot be its own parent)")
		}
	}
	if p.Spaces.Home != "" {
		if msg := checkSlug(p.Spaces.Home); msg != "" {
			errs = append(errs, "spaces.home: "+msg)
		}
		if (len(p.Sources) > 0 || len(p.Documents) > 0) && !p.mapsHomePage() {
			errs = append(errs, fmt.Sprintf("spaces.home %q matches no source/document page in the default space %q", p.Spaces.Home, p.Spaces.Default))
		}
	}
	for i, s := range p.Sources {
		if s.Collection != "" {
			if msg := checkSlug(s.Collection); msg != "" {
				errs = append(errs, fmt.Sprintf("sources[%d].collection: %s", i, msg))
			}
		}
	}
	for i, d := range p.Documents {
		if d.Collection != "" {
			if msg := checkSlug(d.Collection); msg != "" {
				errs = append(errs, fmt.Sprintf("documents[%d].collection: %s", i, msg))
			}
		}
	}
	errs = append(errs, p.validateDiscovery()...)
	errs = append(errs, p.validateI18n()...)
	errs = append(errs, p.validateCoverage()...)
	if len(errs) > 0 {
		return fmt.Errorf("%s:\n  - %s", path, strings.Join(errs, "\n  - "))
	}
	return nil
}

func (p *Project) validateDiscovery() []string {
	var errs []string
	switch p.Discovery.Units {
	case "", UnitsAuto, UnitFeature, UnitService, UnitSystem, UnitAPI, UnitCapability:
	default:
		errs = append(errs, fmt.Sprintf("discovery.units %q must be auto|feature|service|system|api|capability", p.Discovery.Units))
	}
	globs := []struct {
		field string
		vals  []string
	}{
		{"discovery.include", p.Discovery.Include},
		{"discovery.exclude", p.Discovery.Exclude},
		{"discovery.entrypoints", p.Discovery.Entrypoints},
	}
	for _, g := range globs {
		for i, v := range g.vals {
			if v == "" {
				errs = append(errs, fmt.Sprintf("%s[%d]: must not be empty", g.field, i))
				continue
			}
			if msg := checkRepoRelative(v); msg != "" {
				errs = append(errs, fmt.Sprintf("%s[%d]: %s", g.field, i, msg))
			}
		}
	}
	for i, a := range p.Discovery.Audiences.Default {
		switch a {
		case "public", "users", "developers":
		default:
			errs = append(errs, fmt.Sprintf("discovery.audiences.default[%d]: %q must be public|users|developers", i, a))
		}
	}
	return errs
}

func (p *Project) validateI18n() []string {
	var errs []string
	if len(p.I18n.Languages) > MaxLanguages {
		errs = append(errs, fmt.Sprintf("i18n.languages: at most %d languages", MaxLanguages))
	}
	for i, l := range p.I18n.Languages {
		if !languageCodeRE.MatchString(l) {
			errs = append(errs, fmt.Sprintf("i18n.languages[%d]: %q is not a valid language code", i, l))
		}
	}
	return errs
}

func (p *Project) validateCoverage() []string {
	var errs []string
	if p.Coverage.Min < 0 || p.Coverage.Min > 1 {
		errs = append(errs, fmt.Sprintf("coverage.min %.2f must be between 0 and 1", p.Coverage.Min))
	}
	for i, slug := range p.Coverage.Require {
		if strings.TrimSpace(slug) == "" {
			errs = append(errs, fmt.Sprintf("coverage.require[%d]: must not be empty", i))
			continue
		}
		if msg := checkSlug(slug); msg != "" {
			errs = append(errs, fmt.Sprintf("coverage.require[%d]: %s", i, msg))
		}
	}
	return errs
}

var languageCodeRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

func (p *Project) mapsHomePage() bool {
	inDefault := func(space string) bool { return space == "" || space == p.Spaces.Default }
	for _, s := range p.Sources {
		if s.Page == p.Spaces.Home && inDefault(s.Space) {
			return true
		}
	}
	for _, d := range p.Documents {
		if (d.As == "" || d.As == "page") && d.Page == p.Spaces.Home && inDefault(d.Space) {
			return true
		}
	}
	return false
}

func checkSlug(s string) string {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Sprintf("%q must be a lowercase slug (a-z, 0-9, dashes)", s)
		}
	}
	return ""
}

func checkRepoRelative(ref string) string {
	_, err := pathsafe.Rel(ref)
	switch {
	case errors.Is(err, pathsafe.ErrAbsolute):
		return "must be a repo-relative path, not absolute"
	case errors.Is(err, pathsafe.ErrEscape):
		return "must not escape the repo root"
	}
	return ""
}

// PageTarget resolves the effective (space, slug, collection) for a page.
func (p *Project) PageTarget(space, slug, collection string) (string, string, string) {
	if space == "" {
		space = p.Spaces.Default
	}
	if p.isHomePage(space, slug) {
		return space, slug, ""
	}
	if p.IsShared(space) && p.Product.Repo != "" {
		if collection == "" {
			collection = p.Product.Repo
		}
		return space, p.Product.Repo + "/" + slug, collection
	}
	return space, slug, collection
}

func (p *Project) isHomePage(space, slug string) bool {
	return p.Spaces.Home != "" && slug == p.Spaces.Home && space == p.Spaces.Default
}

// IsShared reports whether space is co-owned with sibling repos.
func (p *Project) IsShared(space string) bool {
	for _, s := range p.Spaces.Shared {
		if s == space {
			return true
		}
	}
	return false
}
