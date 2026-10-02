package passes

import (
	"context"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
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
	units := map[string]bool{}
	for _, u := range r.Units {
		units[u] = true
	}
	allowed := map[string][]string{}
	var keys []string
	for _, b := range current.Blocks {
		if b.Key == "" || b.Ownership == api.OwnershipHuman {
			continue
		}
		for _, u := range b.Units {
			if units[u] {
				allowed[b.Key] = b.Units
				keys = append(keys, b.Key)
				break
			}
		}
	}
	if len(keys) == 0 {
		rep.warn("%s: no block is bound to %s; nothing to update through unit reach", where, strings.Join(r.Units, ", "))
		rep.Counts.Unchanged++
		return nil, nil
	}
	text, err := agent.PageText(ctx, in.Client, r.PageID, "draft")
	if err != nil {
		return nil, fmt.Errorf("read page text: %w", err)
	}
	kickoff := passHeader(in) + fmt.Sprintf("\nAction: update page %s (%s) in %s/%s because %s.\n", ref.Slug, r.PageID, r.Site, r.Space, a.Reason) +
		fmt.Sprintf("This page is outside the pass target. You may change it only because this repository %s %s, which this change touches. Change only these existing blocks: %s. Add no block and remove no other block. Whichever repository wrote a block last, it is yours to correct when the change makes it untrue.\n\n## Current page (human blocks are read-only)\n%s",
			firstOf(r.Role, "contributes to"), strings.Join(r.Units, ", "), strings.Join(keys, ", "), text)
	kickoff += "\n" + UnitDetails(in.Plan, r.Units) + "\n" + ChangeSummary(in.ChangeSet, in.Range)
	changes, err := agent.Submit[agent.PageChanges](ctx, in.Harness, agent.Task{Prompt: prompts.PassGuides, Purpose: api.PurposeAuthor, Kickoff: kickoff, Tools: toolset(in), Submit: agent.SubmitPageChangesTool()})
	if err != nil {
		return nil, fmt.Errorf("author: %w", err)
	}
	summary := firstOf(changes.Summary, a.Reason)
	var blocks []api.ChangeBlock
	for _, e := range changes.Upserts {
		bound, ok := allowed[e.Key]
		if !ok {
			rep.warn("%s: block %s is not bound to %s; dropped", ref.Slug, e.Key, strings.Join(r.Units, ", "))
			continue
		}
		b := aiBlock(in, e)
		b.After = nil
		b.Units = in.Known.Filter(append(append([]string{}, bound...), b.Units...))
		withProvenance(in, &b, summary)
		blocks = append(blocks, b)
	}
	var remove []string
	for _, k := range changes.RemoveKeys {
		if _, ok := allowed[k]; !ok {
			rep.warn("%s: kept block %s, which is not bound to %s", ref.Slug, k, strings.Join(r.Units, ", "))
			continue
		}
		remove = append(remove, k)
	}
	hints := make([]api.HintInput, 0, len(changes.Hints))
	for _, h := range changes.Hints {
		hints = append(hints, api.HintInput{Kind: firstOf(h.Kind, "contradiction"), UnitKey: h.UnitKey, PageID: r.PageID, BlockKey: h.BlockKey, Claim: h.Claim, Detail: h.Detail, ForRepos: h.ForRepos})
	}
	if len(blocks) == 0 && len(remove) == 0 {
		rep.Counts.Unchanged++
		return hints, nil
	}
	req := api.ChangeRequest{Op: api.OpUpdate, Target: api.ChangeTarget{PageID: r.PageID}, Summary: summary, Blocks: blocks, RemoveBlockKeys: remove, Units: in.Known.Filter(r.Units), Languages: in.Languages()}
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
