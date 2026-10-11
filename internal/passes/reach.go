package passes

import (
	"context"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const maxProvenanceRefs = 10

func withProvenance(in Input, b *api.ChangeBlock, summary string) {
	if b.Rationale == nil {
		b.Rationale = &api.Rationale{}
	}
	r := b.Rationale
	if r.Summary == "" {
		r.Summary = summary
	}
	if len(r.Commits) == 0 && in.ChangeSet != nil {
		for _, c := range in.ChangeSet.Commits {
			if len(r.Commits) == maxProvenanceRefs {
				break
			}
			r.Commits = append(r.Commits, c.SHA)
		}
	}
	if len(r.SourceRefs) == 0 && len(b.Units) > 0 && in.Plan != nil {
		want := map[string]bool{}
		for _, u := range b.Units {
			want[u] = true
		}
		for _, u := range in.Plan.Inventory.Units {
			if !want[u.Key] {
				continue
			}
			for _, c := range u.Contributors {
				if !activeContributor(c) || !isThisRepo(c.Repo, in.Info) {
					continue
				}
				for _, ref := range c.SourceRefs {
					if len(r.SourceRefs) < maxProvenanceRefs {
						r.SourceRefs = appendUnique(r.SourceRefs, ref)
					}
				}
			}
		}
	}
}

func reachReason(r ReachPage, reason string) string {
	return fmt.Sprintf("unit reach (%s %s): %s", firstOf(r.Role, "contributes to"), strings.Join(r.Units, ", "), reason)
}

func reachSkippable(err error) bool {
	return api.HasCode(err, api.CodeOutsideReach) || api.HasCode(err, api.CodePageLocked) || api.HasCode(err, api.CodeNotFound)
}

func reachAction(ctx context.Context, in Input, out Sink, rep *Report, a agent.PageAction, r ReachPage) ([]api.HintInput, error) {
	where := r.Site + "/" + r.Space + "/" + r.Slug
	current, err := in.Client.Page(ctx, r.PageID, api.PageQuery{State: "draft", Format: "json"})
	if err != nil {
		if reachSkippable(err) {
			rep.warn("%s is not readable by this repository; unit reach skipped (%v)", where, err)
			return nil, nil
		}
		return nil, fmt.Errorf("read page: %w", err)
	}
	if current.Page.Lock != nil {
		rep.warn("%s is locked to %s; skipped", where, current.Page.Lock.Path)
		return nil, nil
	}
	ref := api.PageRef{ID: r.PageID, Slug: firstOf(current.Page.Slug, r.Slug), Title: current.Page.Title}
	d, err := newDraft(ctx, in, draftSpec{mode: agent.DraftReach, slug: ref.Slug, keyPrefix: agent.KeyPrefixGuide, file: r.PageID, page: current, reachUnits: r.Units})
	if err != nil {
		return nil, err
	}
	if len(d.EditableKeys()) == 0 {
		rep.warn("%s: no block is bound to %s; nothing to update through unit reach", where, strings.Join(r.Units, ", "))
		rep.Counts.Unchanged++
		return nil, nil
	}
	bound := map[string][]string{}
	for _, b := range current.Blocks {
		bound[b.Key] = b.Units
	}
	notes := fmt.Sprintf("%s (in %s/%s). This page is outside the pass target; this repository %s %s.", a.Why, r.Site, r.Space, firstOf(r.Role, "contributes to"), strings.Join(r.Units, ", "))
	kickoff := writerKickoff(in, d, kickoffData{mode: agent.DraftReach, title: current.Page.Title, slug: ref.Slug, collection: current.Page.CollectionPath, notes: notes, units: r.Units})
	if err := runDraft(ctx, in, d, api.PhaseReachWrite, kickoff); err != nil {
		return nil, fmt.Errorf("author: %w", err)
	}
	summary := firstOf(d.Summary(), a.Why)
	blocks, _ := draftBlocks(in, d, summary, bound)
	hints := draftHints(d, r.PageID)
	if len(blocks) == 0 {
		rep.Counts.Unchanged++
		return hints, nil
	}
	req := api.ChangeRequest{Op: api.OpUpdate, Target: api.ChangeTarget{PageID: r.PageID}, Summary: summary, Blocks: blocks, Units: in.Known.Filter(r.Units), Languages: in.Languages()}
	c, err := out.Change(ctx, req)
	if err != nil {
		if reachSkippable(err) {
			rep.warn("%s: unit reach refused by Gravity (%v)", where, err)
			return hints, nil
		}
		return nil, err
	}
	rep.Counts.Updated++
	rep.Counts.ViaUnit++
	rep.applied(c)
	rep.impact(ref, api.OpUpdate, reachReason(r, summary))
	return hints, nil
}
