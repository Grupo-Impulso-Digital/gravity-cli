package changeset

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/pathsafe"
)

// Size caps of a ChangeSet.
const (
	MaxCommits = 2000
	MaxFiles   = 5000
)

// Commit is one commit of the range.
type Commit struct {
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Files   int    `json:"files"`
	PR      *int   `json:"pr"`
}

// File is one changed file of the range.
type File struct {
	Path      string  `json:"path"`
	Status    string  `json:"status"`
	OldPath   *string `json:"oldPath"`
	Additions int     `json:"additions"`
	Deletions int     `json:"deletions"`
	Binary    bool    `json:"binary"`
}

// TouchedUnit is a product unit whose source this range changed.
type TouchedUnit struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Change      string   `json:"change"`
	MatchedRefs []string `json:"matchedRefs"`
}

// UnitRef identifies a unit discovered or lost at head.
type UnitRef struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

// Units are the unit-level effects of the range.
type Units struct {
	Touched []TouchedUnit `json:"touched"`
	Added   []UnitRef     `json:"added"`
	Removed []UnitRef     `json:"removed"`
}

// Docs lists changed human-written documents.
type Docs struct {
	Changed []string `json:"changed"`
}

// ChangeSet is what changed in a range, computed once and shared by every pass.
type ChangeSet struct {
	Trigger   string     `json:"trigger"`
	Branch    string     `json:"branch,omitempty"`
	RangeKind string     `json:"rangeKind"`
	BaseSHA   string     `json:"baseSha"`
	HeadSHA   string     `json:"headSha"`
	MergeBase *string    `json:"mergeBase"`
	Commits   []Commit   `json:"commits"`
	Files     []File     `json:"files"`
	Units     Units      `json:"units"`
	OpenAPI   []SpecDiff `json:"openapi"`
	Symbols   Symbols    `json:"symbols"`
	Docs      Docs       `json:"docs"`
	Truncated bool       `json:"truncated"`
	Warnings  []Warning  `json:"warnings,omitempty"`
}

// Paths returns the changed paths, including the old side of renames.
func (c *ChangeSet) Paths() []string {
	out := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		out = append(out, f.Path)
		if f.OldPath != nil {
			out = append(out, *f.OldPath)
		}
	}
	return out
}

// Options configure ChangeSet computation.
type Options struct {
	Trigger     string
	Branch      string
	CodeExclude []string
	CodeInclude []string
	OpenAPI     []string
	DocsInclude []string
	DocsExclude []string
	Inventory   []api.Unit
	RepoKey     string
	RepoName    string
	MaxCommits  int
	MaxFiles    int
}

var alwaysExcluded = []string{
	"**/node_modules/**", "**/vendor/**", "**/dist/**", "**/*.min.*",
	"**/package-lock.json", "**/yarn.lock", "**/pnpm-lock.yaml", "**/npm-shrinkwrap.json", "**/bun.lockb",
	"**/go.sum", "**/Cargo.lock", "**/Gemfile.lock", "**/poetry.lock", "**/Pipfile.lock",
	"**/composer.lock", "**/mix.lock", "**/pubspec.lock", "**/Podfile.lock", "**/packages.lock.json", "**/uv.lock",
}

var defaultDocs = []string{"**/*.md", "**/*.mdx", "**/*.markdown"}

// Excluded reports whether a path never takes part in a ChangeSet.
func Excluded(p string, codeExclude []string) bool {
	return glob.MatchAny(alwaysExcluded, p) || glob.MatchAny(codeExclude, p)
}

// Builder computes ChangeSets and caches them per distinct range.
type Builder struct {
	Repo *git.Repo
	Opts Options

	mu    sync.Mutex
	cache map[string]*ChangeSet
}

// NewBuilder returns a Builder for repo.
func NewBuilder(repo *git.Repo, opts Options) *Builder {
	return &Builder{Repo: repo, Opts: opts, cache: map[string]*ChangeSet{}}
}

// For returns the ChangeSet of rng, computing it once per (base, head).
func (b *Builder) For(ctx context.Context, rng Range) (*ChangeSet, error) {
	key := rng.Base + ".." + rng.Head
	b.mu.Lock()
	defer b.mu.Unlock()
	if cs, ok := b.cache[key]; ok {
		return cs, nil
	}
	cs, err := Build(ctx, b.Repo, rng, b.Opts)
	if err != nil {
		return nil, err
	}
	b.cache[key] = cs
	return cs, nil
}

// Build computes the ChangeSet of rng.
func Build(ctx context.Context, repo *git.Repo, rng Range, opts Options) (*ChangeSet, error) {
	maxCommits, maxFiles := opts.MaxCommits, opts.MaxFiles
	if maxCommits <= 0 {
		maxCommits = MaxCommits
	}
	if maxFiles <= 0 {
		maxFiles = MaxFiles
	}
	cs := &ChangeSet{
		Trigger: opts.Trigger, Branch: opts.Branch, RangeKind: rng.Kind, BaseSHA: rng.Base, HeadSHA: rng.Head,
		Commits: []Commit{}, Files: []File{}, OpenAPI: []SpecDiff{}, Docs: Docs{Changed: []string{}},
		Units:    Units{Touched: []TouchedUnit{}, Added: []UnitRef{}, Removed: []UnitRef{}},
		Symbols:  Symbols{Removed: []Symbol{}, Renamed: []RenamedSymbol{}},
		Warnings: append([]Warning{}, rng.Warnings...),
	}
	if rng.MergeBase != "" {
		mb := rng.MergeBase
		cs.MergeBase = &mb
	}
	if rng.Skip == api.SkipNoChanges || rng.Skip == api.SkipStaleHead {
		return cs, nil
	}
	base := rng.Base
	if base == "" {
		tree, err := repo.EmptyTree(ctx)
		if err != nil {
			return nil, err
		}
		base = tree
	}
	if rng.Head != "" {
		commits, err := repo.Commits(ctx, rng.Base, rng.Head, maxCommits+1)
		if err != nil {
			return nil, err
		}
		if len(commits) > maxCommits {
			commits = commits[:maxCommits]
			cs.Truncated = true
		}
		for _, c := range commits {
			cs.Commits = append(cs.Commits, Commit{SHA: c.SHA, Author: c.Author, Date: c.Date, Subject: c.Subject, Body: c.Body, Files: c.Files, PR: prNumber(c.Subject)})
		}
	}
	changes, err := repo.ChangedFilesDetailed(ctx, base, rng.Head)
	if err != nil {
		return nil, err
	}
	for _, f := range changes {
		if Excluded(f.Path, opts.CodeExclude) && (f.OldPath == "" || Excluded(f.OldPath, opts.CodeExclude)) {
			continue
		}
		if len(cs.Files) >= maxFiles {
			cs.Truncated = true
			break
		}
		file := File{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary}
		if f.OldPath != "" {
			old := f.OldPath
			file.OldPath = &old
		}
		cs.Files = append(cs.Files, file)
	}
	docsInclude := opts.DocsInclude
	if len(docsInclude) == 0 {
		docsInclude = defaultDocs
	}
	for _, f := range cs.Files {
		if f.Status != "D" && glob.MatchAny(docsInclude, f.Path) && !glob.MatchAny(opts.DocsExclude, f.Path) {
			cs.Docs.Changed = append(cs.Docs.Changed, f.Path)
		}
	}
	cs.Units.Touched = touchedUnits(cs.Files, opts)
	if err := diffSpecFiles(ctx, repo, cs, base, rng.Head, opts); err != nil {
		return nil, err
	}
	diff, err := repo.DiffZeroContext(ctx, base, rng.Head)
	if err != nil {
		return nil, err
	}
	cs.Symbols = ParseSymbols(diff, func(p string) bool {
		return !Excluded(p, opts.CodeExclude) && (len(opts.CodeInclude) == 0 || glob.MatchAny(opts.CodeInclude, p))
	})
	return cs, nil
}

var prRE = regexp.MustCompile(`(?:\(#(\d+)\)\s*$|^Merge pull request #(\d+)|^Merged in .*\(pull request #(\d+)\)|See merge request .*!(\d+))`)

func prNumber(subject string) *int {
	m := prRE.FindStringSubmatch(subject)
	if m == nil {
		return nil
	}
	for _, g := range m[1:] {
		if g != "" {
			if n, err := strconv.Atoi(g); err == nil {
				return &n
			}
		}
	}
	return nil
}

func ownedBy(c api.Contributor, opts Options) bool {
	if opts.RepoKey != "" && c.Repo.RemoteKey == opts.RepoKey {
		return true
	}
	return opts.RepoKey == "" && opts.RepoName != "" && c.Repo.Name == opts.RepoName
}

// MatchSourceRef reports whether a changed file matches a unit source ref.
func MatchSourceRef(ref, file string) bool {
	p, _, _ := strings.Cut(ref, "#")
	if p == "" {
		return false
	}
	if glob.HasMeta(p) {
		return glob.Match(p, file)
	}
	if strings.HasSuffix(p, "/") {
		return strings.HasPrefix(file, p)
	}
	return p == file
}

func touchedUnits(files []File, opts Options) []TouchedUnit {
	out := []TouchedUnit{}
	for _, u := range opts.Inventory {
		var refs []string
		statuses := map[string]bool{}
		for _, c := range u.Contributors {
			if !ownedBy(c, opts) {
				continue
			}
			for _, ref := range c.SourceRefs {
				hit := false
				for _, f := range files {
					if MatchSourceRef(ref, f.Path) || (f.OldPath != nil && MatchSourceRef(ref, *f.OldPath)) {
						hit = true
						statuses[f.Status] = true
					}
				}
				if hit {
					refs = append(refs, ref)
				}
			}
		}
		if len(refs) == 0 {
			continue
		}
		change := "modified"
		switch {
		case len(statuses) == 1 && statuses["D"]:
			change = "removed"
		case len(statuses) == 1 && statuses["A"]:
			change = "added"
		}
		out = append(out, TouchedUnit{Key: u.Key, Kind: u.Kind, Change: change, MatchedRefs: refs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func specPaths(ctx context.Context, repo *git.Repo, ref string, patterns []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(patterns) == 0 {
		return out, nil
	}
	var files []string
	var err error
	if ref == "" {
		files, err = repo.ListFiles(ctx, "")
	} else {
		files, err = repo.FilesAt(ctx, ref)
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if glob.MatchAny(patterns, f) {
			out[f] = true
		}
	}
	return out, nil
}

func readSide(ctx context.Context, repo *git.Repo, ref, p string) ([]byte, error) {
	if ref != "" {
		data, ok, err := repo.FileAt(ctx, ref, p)
		if err != nil || !ok {
			return nil, err
		}
		return data, nil
	}
	clean, err := pathsafe.ResolveInRoot(repo.Root, p)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(clean)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return data, nil
}

func diffSpecFiles(ctx context.Context, repo *git.Repo, cs *ChangeSet, base, head string, opts Options) error {
	if len(opts.OpenAPI) == 0 {
		return nil
	}
	headSpecs, err := specPaths(ctx, repo, head, opts.OpenAPI)
	if err != nil {
		return err
	}
	changed := map[string]bool{}
	for _, f := range cs.Files {
		if glob.MatchAny(opts.OpenAPI, f.Path) {
			changed[f.Path] = true
		}
		if f.OldPath != nil && glob.MatchAny(opts.OpenAPI, *f.OldPath) {
			changed[*f.OldPath] = true
		}
	}
	paths := make([]string, 0, len(changed))
	for p := range changed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		before, err := readSide(ctx, repo, base, p)
		if err != nil {
			return err
		}
		var after []byte
		if headSpecs[p] || head == "" {
			after, err = readSide(ctx, repo, head, p)
			if err != nil {
				return err
			}
		}
		d := DiffOpenAPI(p, before, after)
		if d.Error != "" {
			cs.Warnings = append(cs.Warnings, Warning{Code: "openapi_unparsable", Message: fmt.Sprintf("%s: %s", p, d.Error)})
		}
		cs.OpenAPI = append(cs.OpenAPI, d)
	}
	cs.Units.Added, cs.Units.Removed = apiUnitDelta(ctx, repo, head, headSpecs, opts)
	return nil
}

func apiUnitDelta(ctx context.Context, repo *git.Repo, head string, specs map[string]bool, opts Options) ([]UnitRef, []UnitRef) {
	atHead := map[string]UnitRef{}
	for p := range specs {
		data, err := readSide(ctx, repo, head, p)
		if err != nil || data == nil {
			continue
		}
		keys, err := OperationKeys(data)
		if err != nil {
			continue
		}
		for _, k := range keys {
			key := normalize.APIUnitKey(k[0], k[1])
			atHead[key] = UnitRef{Key: key, Kind: api.UnitKindAPI, Ref: p + "#/paths/" + jsonPointerEscape(k[1]) + "/" + strings.ToLower(k[0])}
		}
	}
	known := map[string]bool{}
	removed := []UnitRef{}
	for _, u := range opts.Inventory {
		if u.Kind != api.UnitKindAPI {
			continue
		}
		for _, c := range u.Contributors {
			if !ownedBy(c, opts) {
				continue
			}
			for _, ref := range c.SourceRefs {
				p, _, _ := strings.Cut(ref, "#")
				if !glob.MatchAny(opts.OpenAPI, p) && !specs[p] {
					continue
				}
				known[u.Key] = true
				if _, ok := atHead[u.Key]; !ok {
					removed = append(removed, UnitRef{Key: u.Key, Kind: u.Kind, Ref: ref})
				}
			}
		}
	}
	added := []UnitRef{}
	for key, ref := range atHead {
		if !known[key] {
			added = append(added, ref)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Key < added[j].Key })
	sort.Slice(removed, func(i, j int) bool { return removed[i].Key < removed[j].Key })
	return added, dedupeRefs(removed)
}

func dedupeRefs(in []UnitRef) []UnitRef {
	seen := map[string]bool{}
	out := []UnitRef{}
	for _, r := range in {
		if !seen[r.Key] {
			seen[r.Key] = true
			out = append(out, r)
		}
	}
	return out
}

func jsonPointerEscape(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1")
}

// IsCodeFile reports whether a path looks like source code a symbol scan understands.
func IsCodeFile(p string) bool {
	return ruleFor(path.Base(p)) != nil
}
