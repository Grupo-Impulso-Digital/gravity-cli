package passes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

// Verbatim imports repository documents as locked pages.
type Verbatim struct{}

// Kind is verbatim.
func (Verbatim) Kind() string { return config.KindVerbatim }

// DefaultMaxAssetBytes is the upload limit when the plan does not say.
const DefaultMaxAssetBytes = 10 << 20

var imageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".svg": "image/svg+xml"}

type inputSource struct {
	ctx context.Context
	in  Input
}

func (s inputSource) Files() ([]string, error) { return s.in.Files(s.ctx) }

func (s inputSource) Read(p string) ([]byte, bool, error) { return s.in.ReadFile(s.ctx, p) }

// FileSpecs decodes options.files.
func FileSpecs(pp api.PlanPass) []verbatim.FileSpec {
	raw, _ := pp.Options["files"].([]any)
	var out []verbatim.FileSpec
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		spec := verbatim.FileSpec{}
		spec.Include, _ = m["include"].(string)
		spec.Collection, _ = m["collection"].(string)
		spec.StripPrefix, _ = m["stripPrefix"].(string)
		spec.Slug, _ = m["slug"].(string)
		spec.Title, _ = m["title"].(string)
		if list, ok := m["exclude"].([]any); ok {
			for _, e := range list {
				if s, ok := e.(string); ok {
					spec.Exclude = append(spec.Exclude, s)
				}
			}
		}
		if spec.Include != "" {
			out = append(out, spec)
		}
	}
	return out
}

// Run maps files to pages, imports changed files whole, uploads their images and proposes deleting pages whose file is gone.
func (Verbatim) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	spaceID := in.SpaceID()
	if spaceID == "" {
		return rep, fmt.Errorf("pass %s has no target space", in.Pass.Name)
	}
	m, err := verbatim.Map(inputSource{ctx: ctx, in: in}, verbatim.MapOptions{Files: FileSpecs(in.Pass), IndexFiles: in.StringsOption("indexFiles"), Detached: in.Pass.DetachedPaths})
	if err != nil {
		return rep, err
	}
	rep.Warnings = append(rep.Warnings, m.Warnings...)
	tree, err := in.Client.SpaceTree(ctx, spaceID)
	if err != nil {
		return rep, fmt.Errorf("read target %s: %w", in.Pass.Target.Ref, err)
	}
	locked := map[string]api.TreePage{}
	for _, p := range tree.Pages {
		if lockedToPass(p.Lock, in.Pass.Name, in.Info) {
			locked[p.Lock.Path] = p
		}
	}
	files, err := in.Files(ctx)
	if err != nil {
		return rep, err
	}
	exists := map[string]bool{}
	for _, f := range files {
		exists[f] = true
	}
	branch := firstOf(in.Info.Branch, in.Plan.Repo.DefaultBranch, "main")
	repo := verbatim.Repo{WebURL: firstOf(in.Info.WebURL, in.Plan.Repo.WebURL), Provider: in.Info.Provider, Branch: branch, Exists: func(p string) bool { return exists[p] }}
	head := in.HeadSHA(ctx)
	prefix := in.CollectionPrefix()
	convert := func(file string, body []byte, opts verbatim.Options) (*verbatim.Document, error) {
		var fatal error
		opts.Generator = in.Generator
		opts.Link = m.Linker(file, in.SpaceSlug(), repo)
		opts.Image = func(src string) (string, bool) {
			u, ok, err := uploadImage(ctx, in, out, &rep, file, src)
			if err != nil && fatal == nil {
				fatal = err
			}
			return u, ok
		}
		doc := verbatim.Convert(body, opts)
		if fatal != nil {
			return nil, fatal
		}
		for _, w := range doc.Warnings {
			rep.warn("%s: %s", file, w)
		}
		return doc, nil
	}
	var failures []string
	ready := map[string]bool{}
	allowEmpty, _ := in.Pass.Options["allowEmpty"].(bool)
	for _, page := range m.Pages {
		if page.Empty && !allowEmpty {
			if _, ok := locked[page.Path]; ok {
				rep.warn("%s: empty, skipped (set options.allowEmpty: true to import it); its existing page is left as is", page.Path)
			} else {
				rep.warn("%s: empty, skipped (set options.allowEmpty: true to import it)", page.Path)
			}
			continue
		}
		if cur, ok := locked[page.Path]; ok && cur.Lock.Hash == page.Hash {
			rep.Counts.Unchanged++
			ready[page.Path] = true
			continue
		}
		doc, err := convert(page.Path, page.Body, verbatim.Options{Path: page.Path, Hash: page.Hash, MDX: page.MDX, DropTitleH1: page.TitleFromH1, Title: page.Title, Audiences: page.FrontMatter.Audiences})
		if err != nil {
			return rep, err
		}
		req := api.VerbatimRequest{
			File: api.VerbatimFile{Path: page.Path, Hash: page.Hash, Branch: branch, CommitSHA: head, URL: repo.BlobURL(page.Path)},
			Page: api.VerbatimPage{
				Slug: page.Slug, Title: page.Title, Description: page.Description, Position: page.Position,
				CollectionPath: append(append([]string{}, prefix...), page.CollectionPath...), CollectionTitles: m.TitlesFor(page),
			},
			Blocks: doc.Blocks, Languages: in.Languages(),
		}
		res, err := out.Verbatim(ctx, req)
		switch {
		case err != nil && (api.StopsRun(err) || api.IsLicenseError(err)):
			return rep, err
		case err != nil && (api.HasCode(err, api.CodeLockedByOther) || api.HasCode(err, api.CodeSlugTaken) || api.HasCode(err, api.CodePageLocked)):
			failures = append(failures, fmt.Sprintf("%s: %v", page.Path, err))
			continue
		case err != nil:
			return rep, fmt.Errorf("import %s: %w", page.Path, err)
		}
		ready[page.Path] = true
		if res.Unchanged() {
			rep.Counts.Unchanged++
			continue
		}
		rep.Counts.Imported++
		rep.applied(res.Change)
		rep.impact(api.PageRef{Slug: page.Slug, Title: page.Title}, api.OpImport, page.Path+" changed")
	}
	for _, page := range m.Pages {
		if !ready[page.Path] {
			continue
		}
		for _, t := range page.Translations {
			doc, err := convert(t.Path, t.Body, verbatim.Options{Path: t.Path, Hash: t.Hash, MDX: t.MDX, DropTitleH1: t.TitleFromH1, Title: t.Title, Audiences: t.FrontMatter.Audiences})
			if err != nil {
				return rep, err
			}
			req := api.VerbatimRequest{
				File: api.VerbatimFile{Path: t.Path, Hash: t.Hash, Branch: branch, CommitSHA: head, URL: repo.BlobURL(t.Path)},
				Page: api.VerbatimPage{
					Slug: page.Slug, Title: t.Title, Description: t.Description, Position: page.Position,
					CollectionPath: append(append([]string{}, prefix...), page.CollectionPath...),
				},
				Blocks: doc.Blocks, Language: t.Lang,
			}
			res, err := out.Verbatim(ctx, req)
			switch {
			case api.HasCode(err, api.CodeLanguageNotEnabled):
				rep.warn("%s: the site lacks language %s; translation skipped", t.Path, t.Lang)
				continue
			case err != nil && (api.StopsRun(err) || api.IsLicenseError(err)):
				return rep, err
			case translationSkipped(err):
				rep.warn("%s: %s translation of %s skipped: %v", t.Path, t.Lang, page.Slug, err)
				continue
			case err != nil:
				return rep, fmt.Errorf("import %s: %w", t.Path, err)
			}
			if res.Unchanged() {
				rep.Counts.Unchanged++
				continue
			}
			rep.Counts.Imported++
			rep.applied(res.Change)
			rep.impact(api.PageRef{Slug: page.Slug, Title: page.Title}, api.OpImport, fmt.Sprintf("%s changed (%s translation)", t.Path, t.Lang))
		}
	}
	hidden := map[string]bool{}
	for _, h := range m.Hidden {
		hidden[h] = true
	}
	paths := make([]string, 0, len(locked))
	for p := range locked {
		if _, mapped := m.Lookup(p); !mapped {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		reason := fmt.Sprintf("File deleted or no longer mapped as of %s", short(head))
		if hidden[p] {
			reason = fmt.Sprintf("File marked draft or hidden in %s", short(head))
		}
		res, err := out.DeleteVerbatim(ctx, api.VerbatimDeleteRequest{Path: p, Reason: reason})
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return rep, err
			}
			if notFound(err) {
				continue
			}
			return rep, fmt.Errorf("propose deleting %s: %w", p, err)
		}
		if res.Unchanged() {
			continue
		}
		rep.Counts.Deleted++
		rep.applied(res.Change)
		page := locked[p]
		rep.impact(api.PageRef{ID: page.ID, Slug: page.Slug, Title: page.Title}, api.OpDelete, reason)
	}
	gone, err := goneTranslations(ctx, in, m, locked)
	if err != nil {
		return rep, err
	}
	for _, g := range gone {
		res, err := out.DeleteVerbatim(ctx, api.VerbatimDeleteRequest{Path: g.path, Reason: g.reason + " as of " + short(head)})
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return rep, err
			}
			if notFound(err) {
				continue
			}
			return rep, fmt.Errorf("remove translation %s: %w", g.path, err)
		}
		if res.Unchanged() {
			continue
		}
		rep.Counts.Deleted++
		rep.applied(res.Change)
		rep.impact(api.PageRef{Slug: g.slug}, api.OpDelete, fmt.Sprintf("%s translation removed: %s", firstOf(res.Language, g.lang), g.reason))
	}
	rep.Summary = summaryLine(rep.Counts, in.TargetLabel())
	if len(failures) > 0 {
		rep.Errors = failures
		return rep, fmt.Errorf("%d file(s) could not be imported: %s", len(failures), strings.Join(failures, "; "))
	}
	return rep, nil
}

func translationSkipped(err error) bool {
	for _, code := range []string{api.CodeSourceNotFound, api.CodeLockedByOther, api.CodeSourceNotVerbatim, api.CodeSourcePending, api.CodeTranslationTaken, api.CodePageLocked} {
		if api.HasCode(err, code) {
			return true
		}
	}
	return false
}

type goneTranslation struct {
	path   string
	lang   string
	slug   string
	reason string
}

func goneTranslations(ctx context.Context, in Input, m *verbatim.Mapping, locked map[string]api.TreePage) ([]goneTranslation, error) {
	specs := FileSpecs(in.Pass)
	matched := func(p string) bool {
		for _, s := range specs {
			if s.Matches(p) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []goneTranslation
	consider := func(p, reason string) error {
		if seen[p] || !matched(p) {
			return nil
		}
		seen[p] = true
		if _, ok := locked[p]; ok {
			return nil
		}
		if _, ok := m.Lookup(p); ok {
			return nil
		}
		if _, ok := m.TranslationSource(p); ok {
			return nil
		}
		if src, ok := m.SourceOf(p); ok {
			_, lang, _ := verbatim.SuffixLang(p)
			out = append(out, goneTranslation{path: p, lang: lang, slug: src.Slug, reason: reason})
			return nil
		}
		if in.Range.Base == "" || in.Repo == nil {
			return nil
		}
		data, ok, err := in.Repo.FileAt(ctx, in.Range.Base, p)
		if err != nil || !ok {
			return err
		}
		if fm, _, perr := verbatim.SplitFrontMatter(data); perr == nil && fm.Lang != "" {
			out = append(out, goneTranslation{path: p, lang: verbatim.NormalizeLang(fm.Lang), reason: reason})
		}
		return nil
	}
	for _, h := range m.HiddenTrans {
		if err := consider(h, "Translation marked draft or hidden"); err != nil {
			return nil, err
		}
	}
	if in.ChangeSet != nil {
		for _, f := range in.ChangeSet.Files {
			p := f.Path
			if f.Status == "R" && f.OldPath != nil {
				p = *f.OldPath
			} else if f.Status != "D" {
				continue
			}
			if err := consider(p, "Translation file deleted or renamed"); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func uploadImage(ctx context.Context, in Input, out Sink, rep *Report, file, src string) (string, bool, error) {
	s := strings.TrimSpace(src)
	switch {
	case s == "":
		return "", false, nil
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "//"):
		return s, true, nil
	case strings.HasPrefix(s, "data:"):
		return uploadDataURI(ctx, out, rep, file, s)
	}
	target, _, _ := strings.Cut(s, "#")
	target, _, _ = strings.Cut(target, "?")
	var candidates []string
	if strings.HasPrefix(target, "/") {
		root := strings.TrimPrefix(target, "/")
		candidates = []string{root, "static/" + root, "public/" + root}
	} else {
		candidates = []string{path.Clean(path.Join(path.Dir(file), target))}
	}
	for _, cand := range candidates {
		if cand == ".." || strings.HasPrefix(cand, "../") {
			continue
		}
		ctype, ok := imageTypes[strings.ToLower(path.Ext(cand))]
		if !ok {
			rep.warn("%s: image %s has an unsupported type; dropped", file, src)
			return "", false, nil
		}
		data, found, err := in.ReadFile(ctx, cand)
		if err != nil {
			return "", false, err
		}
		if !found {
			continue
		}
		return storeAsset(ctx, in, out, rep, file, cand, ctype, data)
	}
	return "", false, nil
}

func uploadDataURI(ctx context.Context, out Sink, rep *Report, file, uri string) (string, bool, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		rep.warn("%s: unsupported data URI image; dropped", file)
		return "", false, nil
	}
	ctype := strings.TrimSuffix(meta, ";base64")
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		rep.warn("%s: undecodable data URI image (%v); dropped", file, err)
		return "", false, nil //nolint:nilerr // a broken inline image is dropped with a warning, not a pass failure
	}
	return storeAsset(ctx, Input{}, out, rep, file, file+"#data", ctype, data)
}

func storeAsset(ctx context.Context, in Input, out Sink, rep *Report, file, repoPath, ctype string, data []byte) (string, bool, error) {
	limit := int64(DefaultMaxAssetBytes)
	if in.Plan != nil && in.Plan.Capabilities.Limits.MaxAssetBytes > 0 {
		limit = in.Plan.Capabilities.Limits.MaxAssetBytes
	}
	if int64(len(data)) > limit {
		rep.warn("%s: image %s is larger than %d bytes; dropped", file, repoPath, limit)
		return "", false, nil
	}
	a, err := out.Asset(ctx, repoPath, ctype, data)
	switch {
	case err == nil:
		return a.URL, true, nil
	case api.HasCode(err, api.CodeUnsupportedMedia) || api.HasCode(err, api.CodePayloadTooLarge):
		rep.warn("%s: image %s refused (%v); dropped", file, repoPath, err)
		return "", false, nil
	case errors.Is(err, context.Canceled):
		return "", false, err
	}
	return "", false, fmt.Errorf("upload %s: %w", repoPath, err)
}
