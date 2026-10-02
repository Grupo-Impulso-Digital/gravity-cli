package passes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/glob"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

// Changelog writes release notes for a release, and keeps an Unreleased page on pushes.
type Changelog struct{}

// Kind is changelog.
func (Changelog) Kind() string { return config.KindChangelog }

// Touched applies the changelog skip rule: commits in range.
func (Changelog) Touched(_ context.Context, in Input) (bool, string) { return Touched(in) }

// NothingUnreleased is the Unreleased page body when every change shipped.
const NothingUnreleased = "Nothing unreleased yet."

// Run writes the release page (release trigger) or the Unreleased page (other triggers).
func (Changelog) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	if in.SpaceID() == "" {
		return rep, fmt.Errorf("pass %s has no target space", in.Pass.Name)
	}
	unreleased := in.BoolOption("unreleased", true)
	unreleasedSlug := in.StringOption("unreleasedSlug", "unreleased")
	if in.Trigger == config.TriggerRelease {
		tag := firstOf(in.Range.Tag, "release")
		slug := ReleaseSlug(in.StringOption("slugPattern", "{tag}"), tag, time.Now().UTC())
		var blocks []api.ChangeBlock
		var err error
		if in.StringOption("source", "commits") == "changelog-file" {
			blocks, err = changelogFileBlocks(ctx, in, tag)
		} else {
			blocks, err = releaseNotesBlocks(ctx, in, slug, in.ChangeSet, "release "+tag)
		}
		if err != nil {
			return rep, err
		}
		if blocks == nil {
			rep.Summary = fmt.Sprintf("would write release notes for %s (%d commits)", tag, len(in.ChangeSet.Commits))
			rep.impact(api.PageRef{Slug: slug, Title: "Release " + tag}, api.OpCreate, rep.Summary)
			return rep, nil
		}
		if err := writeNotesPage(ctx, in, out, &rep, slug, "Release "+tag, blocks, "Release notes for "+tag); err != nil {
			return rep, err
		}
		if unreleased {
			empty := []api.ChangeBlock{noteBlock(in, unreleasedSlug, "summary", NothingUnreleased, nil)}
			if err := writeNotesPage(ctx, in, out, &rep, unreleasedSlug, "Unreleased", empty, tag+" released; nothing is unreleased"); err != nil {
				return rep, err
			}
		}
		rep.Summary = summaryLine(rep.Counts, in.TargetLabel())
		return rep, nil
	}
	if !unreleased {
		rep.Summary = "nothing to do on " + in.Trigger + " (unreleased is off)"
		return rep, nil
	}
	cs, rng, err := unreleasedChanges(ctx, in)
	if err != nil {
		return rep, err
	}
	var blocks []api.ChangeBlock
	if cs == nil || len(cs.Commits) == 0 {
		blocks = []api.ChangeBlock{noteBlock(in, unreleasedSlug, "summary", NothingUnreleased, nil)}
	} else {
		blocks, err = releaseNotesBlocks(ctx, in, unreleasedSlug, cs, "unreleased changes since "+firstOf(rng.PreviousTag, "the first commit"))
		if err != nil {
			return rep, err
		}
		if blocks == nil {
			rep.Summary = fmt.Sprintf("would refresh the Unreleased page (%d commits)", len(cs.Commits))
			rep.impact(api.PageRef{Slug: unreleasedSlug, Title: "Unreleased"}, api.OpUpdate, rep.Summary)
			return rep, nil
		}
	}
	if err := writeNotesPage(ctx, in, out, &rep, unreleasedSlug, "Unreleased", blocks, "Unreleased changes"); err != nil {
		return rep, err
	}
	rep.Summary = summaryLine(rep.Counts, in.TargetLabel())
	return rep, nil
}

// ReleaseSlug fills a slug pattern for a tag.
func ReleaseSlug(pattern, tag string, now time.Time) string {
	core, ok := changeset.SemverCore(tag)
	major, minor, patch := "", "", ""
	if ok {
		major, minor, patch = strconv.Itoa(core[0]), strconv.Itoa(core[1]), strconv.Itoa(core[2])
	}
	s := strings.NewReplacer("{tag}", tag, "{major}", major, "{minor}", minor, "{patch}", patch, "{date}", now.Format("2006-01-02")).Replace(pattern)
	if slug := docs.Slug(s); slug != "" {
		return slug
	}
	return docs.Slug(tag)
}

func unreleasedChanges(ctx context.Context, in Input) (*changeset.ChangeSet, changeset.Range, error) {
	head := in.HeadSHA(ctx)
	pattern := in.StringOption("tagPattern", changeset.DefaultTagPattern)
	tags, err := in.Repo.Tags(ctx, pattern)
	if err != nil {
		return nil, changeset.Range{}, err
	}
	for _, t := range tags {
		if t.Commit == head {
			return nil, changeset.Range{Kind: api.RangeRelease, Base: head, Head: head, PreviousTag: t.Name}, nil
		}
	}
	rs := changeset.NewResolver(in.Repo)
	last, err := rs.LatestTag(ctx, head, pattern)
	if err != nil {
		return nil, changeset.Range{}, err
	}
	rng := changeset.Range{Kind: api.RangeRelease, Head: head}
	if last != nil {
		rng.Base, rng.PreviousTag = last.Commit, last.Name
	}
	if in.Builder == nil {
		return nil, rng, errors.New("no change set builder")
	}
	cs, err := in.Builder.For(ctx, rng)
	return cs, rng, err
}

func releaseNotesBlocks(ctx context.Context, in Input, slug string, cs *changeset.ChangeSet, what string) ([]api.ChangeBlock, error) {
	if in.Dry() && !in.Preview {
		return nil, nil
	}
	if in.Harness == nil {
		return nil, errors.New("changelog passes need the LLM gateway")
	}
	audience := in.StringOption("audience", "customer")
	kickoff := passHeader(in) + fmt.Sprintf("\nWrite the release notes for %s. Audience: %s.\n\n", what, audience) + ChangeSummary(cs, in.Range)
	notes, err := agent.Submit[agent.ReleaseNotesInput](ctx, in.Harness, agent.Task{Prompt: prompts.PassChangelog, Purpose: api.PurposeAuthor, Kickoff: kickoff, Tools: toolset(in), Submit: agent.SubmitReleaseNotesTool()})
	if err != nil {
		return nil, fmt.Errorf("release notes: %w", err)
	}
	internal := audience == "internal"
	blocks := []api.ChangeBlock{noteBlock(in, slug, "summary", firstOf(strings.TrimSpace(notes.Summary), "Changes in this release."), nil)}
	for _, section := range agent.ReleaseSections {
		var items []agent.ReleaseItem
		for _, s := range notes.Sections {
			if s.Heading == section {
				items = append(items, s.Items...)
			}
		}
		if len(items) == 0 {
			continue
		}
		key := "changelog:" + slug + ":" + docs.Slug(section)
		h := noteBlock(in, slug, docs.Slug(section), section, nil)
		h.Key, h.Type, h.Content = key, "heading", map[string]any{"text": section, "level": 2}
		blocks = append(blocks, h)
		for i, it := range items {
			text := strings.TrimSpace(it.Text)
			if internal && len(it.Commits) > 0 && !strings.Contains(text, short(it.Commits[0])) {
				refs := make([]string, 0, len(it.Commits))
				for _, c := range it.Commits {
					refs = append(refs, short(c))
				}
				text += " (" + strings.Join(refs, ", ") + ")"
			}
			b := noteBlock(in, slug, docs.Slug(section)+":"+strconv.Itoa(i+1), text, &it)
			b.Type = "list"
			b.Content = map[string]any{"text": text, "variant": "bulleted"}
			blocks = append(blocks, b)
		}
	}
	return blocks, nil
}

func noteBlock(in Input, slug, part, text string, item *agent.ReleaseItem) api.ChangeBlock {
	b := api.ChangeBlock{
		Key: "changelog:" + slug + ":" + part, Type: "prose", Ownership: api.OwnershipHybrid,
		Content:       map[string]any{"text": text},
		SourceBinding: &api.SourceBinding{Kind: "ai", Ref: in.Pass.Name, Hash: "", Generator: in.Generator},
		Audiences:     in.Audiences(),
	}
	if item != nil {
		b.Units = in.Known.Filter(item.Units)
		b.Rationale = &api.Rationale{Summary: "Release-notes entry", Commits: item.Commits}
	}
	return b
}

var changelogHeadingRE = regexp.MustCompile(`^##\s+\[?v?([^\]\s]+)\]?`)

func changelogFileBlocks(ctx context.Context, in Input, tag string) ([]api.ChangeBlock, error) {
	pattern := in.StringOption("changelogFile", "CHANGELOG.md")
	files, err := in.Files(ctx)
	if err != nil {
		return nil, err
	}
	file := ""
	for _, f := range files {
		if f == pattern || glob.HasMeta(pattern) && glob.Match(pattern, f) {
			file = f
			break
		}
	}
	if file == "" {
		return nil, fmt.Errorf("changelog file %s not found", pattern)
	}
	data, _, err := in.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	want := strings.TrimPrefix(tag, "v")
	var section []string
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		if m := changelogHeadingRE.FindStringSubmatch(line); m != nil {
			if inSection {
				break
			}
			inSection = strings.TrimPrefix(m[1], "v") == want
			continue
		}
		if inSection {
			section = append(section, line)
		}
	}
	if !inSection && len(section) == 0 {
		return nil, fmt.Errorf("%s has no section for %s", file, tag)
	}
	doc := verbatim.Convert([]byte(strings.Join(section, "\n")), verbatim.Options{Path: file, Hash: docs.HashBytes(data), Generator: in.Generator, Audiences: in.Audiences()})
	return doc.Blocks, nil
}

func writeNotesPage(ctx context.Context, in Input, out Sink, rep *Report, slug, title string, blocks []api.ChangeBlock, summary string) error {
	existing, err := in.Client.PageBySlug(ctx, in.SpaceID(), slug, api.PageQuery{State: "draft", Format: "json"})
	if err != nil && !notFound(err) {
		return fmt.Errorf("read page %s: %w", slug, err)
	}
	var units []string
	for _, b := range blocks {
		units = append(units, b.Units...)
	}
	ref := api.PageRef{Slug: slug, Title: title}
	if existing == nil || notFound(err) {
		c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpCreate, Target: api.ChangeTarget{SpaceID: in.SpaceID(), Slug: slug, CollectionPath: in.CollectionPrefix()}, Title: title, Summary: summary, Blocks: blocks, Units: in.Known.Filter(units), Languages: in.Languages()})
		if err != nil {
			return fmt.Errorf("create %s: %w", slug, err)
		}
		rep.Counts.Created++
		rep.applied(c)
		rep.impact(ref, api.OpCreate, summary)
		return nil
	}
	ref.ID = existing.Page.ID
	keep := map[string]bool{}
	for _, b := range blocks {
		keep[b.Key] = true
	}
	var remove []string
	for _, b := range existing.Blocks {
		if b.Ownership != api.OwnershipHuman && b.Key != "" && !keep[b.Key] && (strings.HasPrefix(b.Key, "changelog:"+slug+":") || strings.HasPrefix(b.Key, "doc:")) {
			remove = append(remove, b.Key)
		}
	}
	c, err := out.Change(ctx, api.ChangeRequest{Op: api.OpUpdate, Target: api.ChangeTarget{PageID: existing.Page.ID}, Summary: summary, Blocks: blocks, RemoveBlockKeys: remove, Units: in.Known.Filter(units), Languages: in.Languages()})
	if err != nil {
		return fmt.Errorf("update %s: %w", slug, err)
	}
	rep.Counts.Updated++
	rep.applied(c)
	rep.impact(ref, api.OpUpdate, summary)
	return nil
}
