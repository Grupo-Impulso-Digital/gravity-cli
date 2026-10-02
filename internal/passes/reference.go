package passes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/checks"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// Page strategies of a reference pass.
const (
	StrategySingle       = "single"
	StrategyPerTag       = "per-tag"
	StrategyPerOperation = "per-operation"
)

// Reference turns OpenAPI documents into api blocks, deterministically.
type Reference struct{}

// Kind is reference.
func (Reference) Kind() string { return config.KindReference }

// SpecSource is one OpenAPI source of a reference pass.
type SpecSource struct {
	Path       string `json:"path"`
	Page       string `json:"page,omitempty"`
	Title      string `json:"title,omitempty"`
	Collection string `json:"collection,omitempty"`
}

// ReferenceSources returns the configured sources, or one per code.openapi document.
func ReferenceSources(pp api.PlanPass, m *config.Manifest) []SpecSource {
	var out []SpecSource
	if raw, ok := pp.Options["sources"].([]any); ok {
		for _, r := range raw {
			s, ok := r.(map[string]any)
			if !ok {
				continue
			}
			src := SpecSource{}
			src.Path, _ = s["path"].(string)
			src.Page, _ = s["page"].(string)
			src.Title, _ = s["title"].(string)
			src.Collection, _ = s["collection"].(string)
			if src.Path != "" {
				out = append(out, src)
			}
		}
	}
	if len(out) == 0 {
		for _, p := range m.OpenAPIFiles() {
			out = append(out, SpecSource{Path: p})
		}
	}
	return out
}

// SpecFile is one parsed OpenAPI document of a reference pass, at head and at base.
type SpecFile struct {
	SpecSource
	File  string
	Title string
	Ops   []checks.OperationDetail
	Base  []checks.OperationDetail
}

// ResolveSpecs expands the source globs against the files at the range head and parses each document.
func ResolveSpecs(ctx context.Context, in Input, sources []SpecSource) ([]SpecFile, []string, error) {
	files, err := in.Files(ctx)
	if err != nil {
		return nil, nil, err
	}
	var baseFiles []string
	if in.Range.Base != "" {
		baseFiles, _ = in.Repo.FilesAt(ctx, in.Range.Base)
	}
	atHead := map[string]bool{}
	for _, f := range files {
		atHead[f] = true
	}
	var out []SpecFile
	var warnings []string
	seen := map[string]bool{}
	baseOps := func(f string) ([]checks.OperationDetail, string) {
		if in.Range.Base == "" {
			return nil, ""
		}
		before, ok, err := in.Repo.FileAt(ctx, in.Range.Base, f)
		if err != nil || !ok {
			return nil, ""
		}
		ops, err := docs.Operations(before)
		if err != nil {
			return nil, ""
		}
		ops, _ = documentable(ops)
		return ops, specTitle(before)
	}
	for _, src := range sources {
		pattern := strings.TrimPrefix(src.Path, "./")
		for _, f := range files {
			if seen[f] || !specMatch(pattern, f) {
				continue
			}
			seen[f] = true
			data, ok, err := in.ReadFile(ctx, f)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			ops, err := docs.Operations(data)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", f, err))
				continue
			}
			ops, dropped := documentable(ops)
			for _, op := range dropped {
				warnings = append(warnings, fmt.Sprintf("%s: %s %s skipped: api blocks have no %s method", f, strings.ToUpper(op.Method), op.Path, strings.ToUpper(op.Method)))
			}
			before, _ := baseOps(f)
			out = append(out, SpecFile{SpecSource: src, File: f, Title: specTitle(data), Ops: ops, Base: before})
		}
		for _, f := range baseFiles {
			if seen[f] || atHead[f] || !specMatch(pattern, f) {
				continue
			}
			seen[f] = true
			if before, title := baseOps(f); len(before) > 0 {
				out = append(out, SpecFile{SpecSource: src, File: f, Title: title, Base: before})
			}
		}
	}
	return out, warnings, nil
}

func specMatch(pattern, f string) bool {
	return f == pattern || glob.HasMeta(pattern) && glob.Match(pattern, f)
}

var blockMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}

func documentable(ops []checks.OperationDetail) (keep, dropped []checks.OperationDetail) {
	keep = ops[:0:0]
	for _, op := range ops {
		if blockMethods[strings.ToLower(op.Method)] {
			keep = append(keep, op)
		} else {
			dropped = append(dropped, op)
		}
	}
	return keep, dropped
}

func specTitle(data []byte) string {
	var doc struct {
		Info struct {
			Title string `yaml:"title"`
		} `yaml:"info"`
	}
	if yaml.Unmarshal(data, &doc) == nil {
		return doc.Info.Title
	}
	return ""
}

type refPage struct {
	slug       string
	title      string
	collection []string
	ops        []checks.OperationDetail
	spec       string
}

func pageFor(strategy string, sf SpecFile, op checks.OperationDetail) (slug, title string) {
	docSlug := firstOf(sf.Page, docs.Slug(firstOf(sf.Title, strings.TrimSuffix(sf.File, ".yaml"))))
	docTitle := firstOf(sf.SpecSource.Title, sf.Title, sf.File)
	switch strategy {
	case StrategySingle:
		return docSlug, docTitle
	case StrategyPerOperation:
		name := op.OperationID
		if name == "" {
			name = op.Method + " " + op.Path
		}
		return docs.Slug(name), firstOf(op.Summary, strings.ToUpper(op.Method)+" "+op.Path)
	}
	if len(op.Tags) > 0 && docs.Slug(op.Tags[0]) != "" {
		return docs.Slug(op.Tags[0]), op.Tags[0]
	}
	return docSlug, docTitle
}

func groupPages(strategy string, specs []SpecFile, prefix []string) []*refPage {
	byKey := map[string]*refPage{}
	var order []string
	for _, sf := range specs {
		coll := append([]string{}, prefix...)
		for _, seg := range strings.Split(sf.Collection, "/") {
			if seg != "" {
				coll = append(coll, seg)
			}
		}
		for _, op := range sf.Ops {
			slug, title := pageFor(strategy, sf, op)
			if slug == "" {
				slug = "api"
			}
			p, ok := byKey[slug]
			if !ok {
				p = &refPage{slug: slug, title: title, collection: coll, spec: sf.File}
				byKey[slug] = p
				order = append(order, slug)
			}
			p.ops = append(p.ops, op)
		}
	}
	out := make([]*refPage, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

func notFound(err error) bool {
	var ae *api.APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// Run builds api blocks for every operation, writes the pages whose blocks changed, and removes blocks of removed operations.
func (Reference) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	spaceID := in.SpaceID()
	if spaceID == "" {
		return rep, fmt.Errorf("pass %s has no target space", in.Pass.Name)
	}
	specs, warnings, err := ResolveSpecs(ctx, in, ReferenceSources(in.Pass, in.Manifest))
	if err != nil {
		return rep, err
	}
	rep.Warnings = append(rep.Warnings, warnings...)
	if len(specs) == 0 {
		rep.Summary = "no OpenAPI document found"
		return rep, nil
	}
	strategy := in.StringOption("pageStrategy", StrategyPerTag)
	pages := groupPages(strategy, specs, in.CollectionPrefix())
	headKeys := map[string]string{}
	for _, p := range pages {
		for _, op := range p.ops {
			headKeys[docs.APIBlockKey(op.Method, op.Path)] = p.slug
		}
	}
	removedByPage := map[string][]string{}
	for _, sf := range specs {
		for _, op := range sf.Base {
			key := docs.APIBlockKey(op.Method, op.Path)
			if _, still := headKeys[key]; still {
				continue
			}
			slug, _ := pageFor(strategy, sf, op)
			removedByPage[slug] = append(removedByPage[slug], key)
		}
	}
	for _, p := range pages {
		changed, err := referencePage(ctx, in, out, &rep, p, headKeys, removedByPage[p.slug])
		if err != nil {
			return rep, err
		}
		delete(removedByPage, p.slug)
		if changed && in.BoolOption("prose", false) && (!out.Dry() || in.Preview) {
			if err := referenceProse(ctx, in, out, &rep, p); err != nil {
				rep.warn("prose for %s: %v", p.slug, err)
			}
		}
	}
	slugs := make([]string, 0, len(removedByPage))
	for slug := range removedByPage {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		if err := referenceRemovals(ctx, in, out, &rep, slug, removedByPage[slug], strategy == StrategyPerOperation); err != nil {
			return rep, err
		}
	}
	rep.UnitsTouched = touchedAPIUnits(pages)
	rep.Summary = summaryLine(rep.Counts, in.TargetLabel())
	return rep, nil
}

func touchedAPIUnits(pages []*refPage) []string {
	var out []string
	for _, p := range pages {
		for _, op := range p.ops {
			b, err := docs.APIBlock(op, "")
			if err == nil {
				out = append(out, b.Units...)
			}
		}
	}
	return out
}

func summaryLine(c Counts, target string) string {
	line := c.Line()
	if line == "" {
		line = "no changes"
	}
	return line + " in " + target
}

func referencePage(ctx context.Context, in Input, out Sink, rep *Report, p *refPage, headKeys map[string]string, removed []string) (bool, error) {
	blocks := make([]api.ChangeBlock, 0, len(p.ops))
	var pageUnits []string
	for _, op := range p.ops {
		b, err := docs.APIBlock(op, in.Generator)
		if err != nil {
			return false, err
		}
		b.Audiences = in.Audiences()
		b.Units = in.Known.Filter(b.Units)
		pageUnits = append(pageUnits, b.Units...)
		b.Rationale = &api.Rationale{Summary: "Generated from " + p.spec, SourceRefs: []string{p.spec + "#/paths/" + pointerEscape(op.Path) + "/" + strings.ToLower(op.Method)}}
		blocks = append(blocks, b)
	}
	existing, err := in.Client.PageBySlug(ctx, in.SpaceID(), p.slug, api.PageQuery{State: "draft", Format: "json"})
	if err != nil && !notFound(err) {
		return false, fmt.Errorf("read page %s: %w", p.slug, err)
	}
	if existing == nil || notFound(err) {
		req := api.ChangeRequest{
			Op: api.OpCreate, Target: api.ChangeTarget{SpaceID: in.SpaceID(), Slug: p.slug, CollectionPath: p.collection}, Title: p.title,
			Summary: fmt.Sprintf("API reference for %d operations from %s", len(blocks), p.spec), Blocks: blocks, Units: pageUnits, Languages: in.Languages(),
		}
		c, err := out.Change(ctx, req)
		if err != nil {
			return false, fmt.Errorf("create %s: %w", p.slug, err)
		}
		rep.Counts.Created++
		rep.applied(c)
		rep.impact(api.PageRef{Slug: p.slug, Title: p.title}, api.OpCreate, fmt.Sprintf("%d operations from %s", len(blocks), p.spec))
		return true, nil
	}
	if existing.Page.Lock != nil {
		rep.warn("page %s is locked to %s; skipped", p.slug, existing.Page.Lock.Path)
		return false, nil
	}
	current := map[string]api.PageBlock{}
	for _, b := range existing.Blocks {
		current[b.Key] = b
	}
	var changed []api.ChangeBlock
	for _, b := range blocks {
		cur, ok := current[b.Key]
		if ok && cur.SourceBinding != nil && cur.SourceBinding.Kind == "endpoint" && cur.SourceBinding.Hash == b.SourceBinding.Hash && containsAll(cur.Units, b.Units) {
			continue
		}
		changed = append(changed, b)
	}
	var removeKeys []string
	for _, key := range removed {
		if cur, ok := current[key]; ok && cur.Ownership != api.OwnershipHuman {
			removeKeys = append(removeKeys, key)
		}
	}
	for _, b := range existing.Blocks {
		if b.Type != "api" || b.Ownership == api.OwnershipHuman {
			continue
		}
		if other, ok := headKeys[b.Key]; ok && other != p.slug {
			removeKeys = append(removeKeys, b.Key)
		}
	}
	if len(changed) == 0 && len(removeKeys) == 0 {
		rep.Counts.Unchanged++
		return false, nil
	}
	reason := fmt.Sprintf("%d operations changed", len(changed))
	if len(removeKeys) > 0 {
		reason += fmt.Sprintf(", %d removed", len(removeKeys))
	}
	req := api.ChangeRequest{
		Op: api.OpUpdate, Target: api.ChangeTarget{PageID: existing.Page.ID}, Title: existing.Page.Title,
		Summary: reason + " in " + p.spec, Blocks: changed, RemoveBlockKeys: removeKeys, Units: pageUnits, Languages: in.Languages(),
	}
	c, err := out.Change(ctx, req)
	if err != nil {
		return false, fmt.Errorf("update %s: %w", p.slug, err)
	}
	rep.Counts.Updated++
	rep.applied(c)
	rep.impact(api.PageRef{ID: existing.Page.ID, Slug: p.slug, Title: existing.Page.Title}, api.OpUpdate, reason)
	return true, nil
}

func referenceRemovals(ctx context.Context, in Input, out Sink, rep *Report, slug string, keys []string, perOperation bool) error {
	existing, err := in.Client.PageBySlug(ctx, in.SpaceID(), slug, api.PageQuery{State: "draft", Format: "json"})
	if notFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read page %s: %w", slug, err)
	}
	if existing.Page.Lock != nil {
		return nil
	}
	ref := api.PageRef{ID: existing.Page.ID, Slug: slug, Title: existing.Page.Title}
	if perOperation {
		c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpDelete, Target: api.ChangeTarget{PageID: existing.Page.ID}, Summary: "Operation removed from the OpenAPI document: " + strings.Join(keys, ", ")})
		if err != nil {
			return fmt.Errorf("delete %s: %w", slug, err)
		}
		rep.Counts.Deleted++
		rep.applied(c)
		rep.impact(ref, api.OpDelete, "operation removed")
		return nil
	}
	present := map[string]bool{}
	for _, b := range existing.Blocks {
		if b.Ownership != api.OwnershipHuman {
			present[b.Key] = true
		}
	}
	var remove []string
	for _, k := range keys {
		if present[k] {
			remove = append(remove, k)
		}
	}
	if len(remove) == 0 {
		return nil
	}
	c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpUpdate, Target: api.ChangeTarget{PageID: existing.Page.ID}, Summary: fmt.Sprintf("%d operations removed from the OpenAPI document", len(remove)), RemoveBlockKeys: remove})
	if err != nil {
		return fmt.Errorf("update %s: %w", slug, err)
	}
	rep.Counts.Updated++
	rep.applied(c)
	rep.impact(ref, api.OpUpdate, fmt.Sprintf("%d operations removed", len(remove)))
	return nil
}

func referenceProse(ctx context.Context, in Input, out Sink, rep *Report, p *refPage) error {
	if in.Harness == nil {
		return errors.New("no AI harness for this pass")
	}
	var ops strings.Builder
	for _, op := range p.ops {
		fmt.Fprintf(&ops, "- %s %s %s\n", strings.ToUpper(op.Method), op.Path, op.Summary)
	}
	pageText := ""
	existing, err := in.Client.PageBySlug(ctx, in.SpaceID(), p.slug, api.PageQuery{Format: "text"})
	if err == nil {
		pageText = firstOf(existing.Text, agent.ComposePageText(existing))
	}
	kickoff := passHeader(in) + fmt.Sprintf("\nPage %s (%s) documents these operations of %s:\n%s\n", p.slug, p.title, p.spec, ops.String())
	if pageText != "" {
		kickoff += "\nCurrent page:\n" + pageText
	}
	tools := append(agent.RepoTools(in.Repo, agent.Range{Base: in.Range.Base, Head: in.Range.Head, WorkingTree: in.Range.Head == ""}), agent.DocTools(in.Client, docScope(in))...)
	changes, err := agent.Submit[agent.PageChanges](ctx, in.Harness, agent.Task{Prompt: prompts.PassReference, Purpose: api.PurposeAuthor, Kickoff: kickoff, Tools: tools, Submit: agent.SubmitPageChangesTool()})
	if err != nil {
		return err
	}
	var blocks []api.ChangeBlock
	for _, e := range changes.Upserts {
		if strings.HasPrefix(e.Key, "api:") {
			continue
		}
		blocks = append(blocks, aiBlock(in, e))
	}
	if len(blocks) == 0 {
		return nil
	}
	target := api.ChangeTarget{SpaceID: in.SpaceID(), Slug: p.slug}
	if existing != nil {
		target = api.ChangeTarget{PageID: existing.Page.ID}
	}
	c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpUpdate, Target: target, Summary: firstOf(changes.Summary, "Introduction and usage prose"), Blocks: blocks, Languages: in.Languages()})
	if err != nil {
		return err
	}
	rep.applied(c)
	return nil
}

func aiBlock(in Input, e agent.BlockEdit) api.ChangeBlock {
	var content any = e.Content
	auds := e.Audiences
	if len(auds) == 0 {
		auds = in.Audiences()
	}
	return api.ChangeBlock{
		Key: e.Key, Type: e.Type, Ownership: api.OwnershipHybrid, Content: content,
		SourceBinding: &api.SourceBinding{Kind: "ai", Ref: in.Pass.Name, Hash: "", Generator: in.Generator},
		Audiences:     auds, After: e.After, Units: in.Known.Filter(e.Units),
		Rationale: &api.Rationale{Summary: e.Rationale.Summary, Commits: e.Rationale.Commits, SourceRefs: e.Rationale.SourceRefs},
	}
}

func docScope(in Input) agent.DocScope {
	s := agent.DocScope{SpaceID: in.SpaceID()}
	if in.Pass.Target.Site != nil {
		s.SiteID = in.Pass.Target.Site.ID
	}
	s.Product = in.ProductParam
	if in.Plan != nil {
		s.Namespace = in.Plan.Product.NucleusNamespace
	}
	return s
}

func pointerEscape(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1")
}

func containsAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
