package passes

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type draftSpec struct {
	mode       string
	slug       string
	keyPrefix  string
	file       string
	page       *api.PageContent
	computed   []api.ChangeBlock
	reachUnits []string
	mustCover  []agent.MustCover
}

func (in Input) draftPath(file string) string {
	if in.DebugDir == "" || file == "" {
		return ""
	}
	return filepath.Join(in.DebugDir, file+".json")
}

func newDraft(ctx context.Context, in Input, s draftSpec) (*agent.Draft, error) {
	if in.Harness == nil {
		return nil, errors.New("no AI harness for this pass")
	}
	b, err := in.Harness.LoadBundle(ctx)
	if err != nil {
		return nil, err
	}
	return agent.NewDraft(agent.DraftOptions{
		Mode: s.mode, Slug: s.slug, KeyPrefix: s.keyPrefix, Contract: b.Blocks, Audiences: in.Audiences(), ReachUnits: s.reachUnits,
		Language: b.Language.Code, Markers: b.Checks.OutputMarkers, MustCover: s.mustCover, Path: in.draftPath(s.file),
	}, s.page, s.computed), nil
}

func runDraft(ctx context.Context, in Input, d *agent.Draft, phase, kickoff string) error {
	tools := append(toolset(in), agent.DraftTools(d)...)
	_, err := in.Harness.Run(ctx, agent.Task{Phase: phase, Purpose: api.PurposeAuthor, Kickoff: kickoff, Tools: tools, Submit: agent.SubmitPageTool(d)})
	return err
}

type kickoffData struct {
	mode       string
	title      string
	slug       string
	collection []string
	mustCover  []agent.MustCover
	notes      string
	units      []string
}

func writerKickoff(in Input, d *agent.Draft, k kickoffData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<task mode=%q>\n", k.mode)
	b.WriteString(passHeader(in))
	target := in.SpaceSlug()
	if len(k.collection) > 0 {
		target += "/" + strings.Join(k.collection, "/")
	}
	fmt.Fprintf(&b, "Page: %s (slug %s) · Target: %s\n", firstOf(k.title, k.slug), k.slug, target)
	switch keys := d.EditableKeys(); {
	case d.Mode() != agent.DraftReach && d.AllEditable():
		b.WriteString("Editable: all\n")
	case len(keys) == 0:
		b.WriteString("Editable: keys: none (new blocks only)\n")
	default:
		fmt.Fprintf(&b, "Editable: keys: %s\n", strings.Join(keys, ", "))
	}
	b.WriteString("</task>\n")
	if len(k.mustCover) > 0 {
		b.WriteString("<must_cover>\n")
		for _, m := range k.mustCover {
			if m.Token != "" {
				fmt.Fprintf(&b, "- [token: %s] %s\n", m.Token, m.Text)
			} else {
				fmt.Fprintf(&b, "- %s\n", m.Text)
			}
		}
		b.WriteString("</must_cover>\n")
	}
	if strings.TrimSpace(k.notes) != "" {
		fmt.Fprintf(&b, "<planner_notes>%s</planner_notes>\n", strings.TrimSpace(k.notes))
	}
	if units := strings.TrimSpace(UnitDetails(in.Plan, k.units)); units != "" {
		fmt.Fprintf(&b, "<units>\n%s\n</units>\n", units)
	}
	fmt.Fprintf(&b, "<change_summary>\n%s</change_summary>\n", ChangeSummary(in.ChangeSet, in.Range))
	return b.String()
}

func draftBlocks(in Input, d *agent.Draft, summary string, bound map[string][]string) ([]api.ChangeBlock, []string) {
	diff := d.Diff()
	blocks := make([]api.ChangeBlock, 0, len(diff.Upserts))
	for _, u := range diff.Upserts {
		blk := u.Block
		auds := blk.Audiences
		if len(auds) == 0 {
			auds = in.Audiences()
		}
		after := u.After
		cb := api.ChangeBlock{
			Key: blk.Key, Type: blk.Type, Ownership: api.OwnershipHybrid, Content: blk.Content,
			SourceBinding: &api.SourceBinding{Kind: "ai", Ref: in.Pass.Name, Hash: "", Generator: in.Generator},
			Audiences:     auds, After: &after, Units: in.Known.Filter(append(append([]string{}, bound[blk.Key]...), blk.Units...)),
		}
		if d.Mode() == agent.DraftReach {
			cb.After = nil
		}
		if r := blk.Rationale; r != nil {
			cb.Rationale = &api.Rationale{Summary: r.Summary, Commits: r.Commits, SourceRefs: r.SourceRefs}
		}
		withProvenance(in, &cb, summary)
		blocks = append(blocks, cb)
	}
	return blocks, diff.Remove
}

func draftHints(d *agent.Draft, pageID string) []api.HintInput {
	hints := make([]api.HintInput, 0, len(d.Hints()))
	for _, h := range d.Hints() {
		hints = append(hints, api.HintInput{Kind: firstOf(h.Kind, "contradiction"), UnitKey: h.UnitKey, PageID: pageID, BlockKey: h.BlockKey, Claim: h.Claim, Detail: h.Detail, ForRepos: h.ForRepos})
	}
	return hints
}

func changeUnits(blocks []api.ChangeBlock) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.Units...)
	}
	return out
}
