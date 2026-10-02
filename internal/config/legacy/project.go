// Package legacy decodes v1 .gravity.yaml manifests so they can be detected and converted to v2.
package legacy

// Project is a v1 repository manifest.
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

// Product identifies the product a v1 repository belonged to.
type Product struct {
	Slug string `yaml:"slug"`
	Repo string `yaml:"repo"`
	Role string `yaml:"role"`
}

// Discovery configured how v0.x surveyed the repository.
type Discovery struct {
	Units       string             `yaml:"units"`
	Include     []string           `yaml:"include"`
	Exclude     []string           `yaml:"exclude"`
	Entrypoints []string           `yaml:"entrypoints"`
	Audiences   DiscoveryAudiences `yaml:"audiences"`
}

// DiscoveryAudiences holds the default audiences of v0.x planned pages.
type DiscoveryAudiences struct {
	Default []string `yaml:"default"`
}

// I18n declares the languages pages should exist in.
type I18n struct {
	Languages []string `yaml:"languages"`
}

// Coverage is the v1 documentation-coverage bar.
type Coverage struct {
	Min     float64  `yaml:"min"`
	Require []string `yaml:"require"`
}

// Spaces declares where v1 pages lived.
type Spaces struct {
	Default string      `yaml:"default"`
	Shared  []string    `yaml:"shared"`
	Parent  string      `yaml:"parent"`
	Home    string      `yaml:"home"`
	Declare []SpaceDecl `yaml:"declare"`
}

// SpaceDecl is a space a v1 manifest declared.
type SpaceDecl struct {
	Slug       string   `yaml:"slug"`
	Name       string   `yaml:"name"`
	Parent     string   `yaml:"parent"`
	Type       string   `yaml:"type"`
	Visibility string   `yaml:"visibility"`
	Audiences  []string `yaml:"audiences"`
}

// SourceMap binds an OpenAPI document to a page.
type SourceMap struct {
	Source     string `yaml:"source"`
	Kind       string `yaml:"kind"`
	Space      string `yaml:"space"`
	Page       string `yaml:"page"`
	Title      string `yaml:"title"`
	Collection string `yaml:"collection"`
	Generator  string `yaml:"generator"`
}

// DocMap ingested a Markdown file as a page.
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

// ReleaseNotes configured the v0.x release-notes command.
type ReleaseNotes struct {
	Space     string `yaml:"space"`
	Changelog string `yaml:"changelog"`
}

// Knowledge declared the Nucleus namespace.
type Knowledge struct {
	Namespace string `yaml:"namespace"`
	Scope     string `yaml:"scope"`
}
