package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/impulso/gravity-cli/internal/pathsafe"
	yaml "go.yaml.in/yaml/v3"
)

// SchemaVersion is the highest .gravity.yaml schema version this CLI
// understands. A file declaring a higher version is rejected with a clear
// "upgrade gravity" error.
const SchemaVersion = 1

// Project is the declarative repo manifest loaded from .gravity.yaml. It
// carries NO secrets — there is structurally no Token field, and LoadProject
// additionally rejects any committed `token:` key.
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

	// LegacySpace preserves today's top-level `space:` key for back-compat; it
	// is folded into Spaces.Default / ReleaseNotes.Space when those are unset.
	LegacySpace string `yaml:"space"`
}

// Product identifies the (possibly multi-repo) product this repo belongs to.
type Product struct {
	Slug string `yaml:"slug"` // logical product id = shared knowledge namespace
	Repo string `yaml:"repo"` // this repo's unique name within the product
	Role string `yaml:"role"` // informational: api | service | frontend | docs | ...
}

// Spaces declares where this repo's pages live and which spaces are co-owned
// with sibling repos (and therefore slug-namespaced to avoid clobbering).
type Spaces struct {
	Default string   `yaml:"default"`
	Shared  []string `yaml:"shared"`
}

// SourceMap binds a source artifact (a spec or a code file) to a doc page made
// of machine-owned, drift-locked blocks.
type SourceMap struct {
	Source    string `yaml:"source"` // repo-relative file (hashed into the binding)
	Kind      string `yaml:"kind"`   // openapi | code
	Space     string `yaml:"space"`  // optional; default Spaces.Default
	Page      string `yaml:"page"`   // target page slug
	Title     string `yaml:"title"`
	Generator string `yaml:"generator"`
}

// DocMap ingests a human-authored Markdown file into Gravity as native blocks,
// either as a standalone page or as a versioned release.
type DocMap struct {
	File      string `yaml:"file"`      // repo-relative .md file
	Space     string `yaml:"space"`     // optional; default Spaces.Default
	Page      string `yaml:"page"`      // target page slug (required when as=page)
	Title     string `yaml:"title"`     // optional; default first H1 or filename
	Ownership string `yaml:"ownership"` // machine | hybrid | human (default machine)
	As        string `yaml:"as"`        // page | release (default page)
	Version   string `yaml:"version"`   // explicit version for as=release
}

// ReleaseNotes configures the git-derived release-notes command.
type ReleaseNotes struct {
	Space     string `yaml:"space"`
	Changelog string `yaml:"changelog"`
}

// Knowledge declares the nucleus memory namespace shared across a product's
// repos and this repo's atom scope within it.
type Knowledge struct {
	Namespace string `yaml:"namespace"`
	Scope     string `yaml:"scope"`
}

// LoadProject reads and validates the project manifest in projectDir. A missing
// file is not an error (returns nil, nil). A committed token is rejected.
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

// guardNoToken turns the silent committed-token footgun into a loud, actionable
// error pointing at the offending line.
func guardNoToken(data []byte, path string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // malformed YAML is reported by the typed decode in LoadProject
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

// applyDefaults fills derived defaults so downstream code can rely on them.
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
		case "", "openapi", "code":
		default:
			errs = append(errs, fmt.Sprintf("sources[%d]: kind %q must be openapi|code", i, s.Kind))
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
	if len(errs) > 0 {
		return fmt.Errorf("%s:\n  - %s", path, strings.Join(errs, "\n  - "))
	}
	return nil
}

// checkRepoRelative returns a non-empty message when ref is not a safe
// repo-relative path.
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

// PageTarget resolves the effective (space, slug) for a page, prefixing the
// slug with product.repo when the space is shared so sibling repos in the same
// product don't clobber each other.
func (p *Project) PageTarget(space, slug string) (string, string) {
	if space == "" {
		space = p.Spaces.Default
	}
	if p.isShared(space) && p.Product.Repo != "" {
		return space, p.Product.Repo + "/" + slug
	}
	return space, slug
}

func (p *Project) isShared(space string) bool {
	for _, s := range p.Spaces.Shared {
		if s == space {
			return true
		}
	}
	return false
}
