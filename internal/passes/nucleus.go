package passes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// Nucleus distills repository-tagged facts into the product's Nucleus namespace.
type Nucleus struct{}

// Kind is nucleus.
func (Nucleus) Kind() string { return config.KindNucleus }

// Touched applies the nucleus skip rule.
func (Nucleus) Touched(_ context.Context, in Input) (bool, string) { return Touched(in) }

// Namespace is the Nucleus namespace a nucleus pass writes.
func Namespace(in Input) string {
	if ns := in.StringOption("namespace", ""); ns != "" {
		return ns
	}
	if in.Plan != nil {
		return in.Plan.Product.NucleusNamespace
	}
	return ""
}

// Run recalls what is known, distills new or corrected atoms from the range and writes them.
func (Nucleus) Run(ctx context.Context, in Input, out Sink) (Report, error) {
	var rep Report
	ns := Namespace(in)
	commits := 0
	if in.ChangeSet != nil {
		commits = len(in.ChangeSet.Commits)
	}
	if out.Dry() && !in.Preview {
		rep.Summary = fmt.Sprintf("would distill facts from %d commits into %s", commits, firstOf(ns, "the organization memory"))
		return rep, nil
	}
	if in.Harness == nil {
		return rep, errors.New("nucleus passes need the LLM gateway")
	}
	maxAtoms := in.IntOption("maxAtoms", 40)
	kinds := in.StringsOption("kinds")
	var query []string
	if in.ChangeSet != nil {
		for i, c := range in.ChangeSet.Commits {
			if i == 8 {
				break
			}
			query = append(query, c.Subject)
		}
	}
	known := ""
	if len(query) > 0 {
		res, err := in.Client.Recall(ctx, api.RecallRequest{Query: clip(strings.Join(query, "; "), 500), Limit: 20, Namespace: ns})
		switch {
		case err == nil:
			known = agent.FormatRecall(res.Hits)
		case api.StopsRun(err) || api.IsLicenseError(err):
			return rep, err
		default:
			rep.warn("recall: %v", err)
		}
	}
	kickoff := passHeader(in) + fmt.Sprintf("\nNamespace: %s. At most %d atoms.", firstOf(ns, "(organization)"), maxAtoms)
	if len(kinds) > 0 {
		kickoff += " Allowed kinds: " + strings.Join(kinds, ", ") + "."
	}
	kickoff += "\n\n## Already known (reuse a title to revise an atom)\n" + firstOf(known, "(nothing recalled)") + "\n\n" + ChangeSummary(in.ChangeSet, in.Range) + UnitDetails(in.Plan, touchedKeys(in))
	atoms, err := agent.Submit[agent.AtomsInput](ctx, in.Harness, agent.Task{Prompt: prompts.PassNucleus, Purpose: api.PurposeDistill, Kickoff: kickoff, Tools: toolset(in), Submit: agent.SubmitAtomsTool()})
	if err != nil {
		return rep, fmt.Errorf("distill: %w", err)
	}
	allowed := map[string]bool{}
	for _, k := range kinds {
		allowed[k] = true
	}
	written := 0
	for _, a := range atoms.Atoms {
		if written == maxAtoms {
			break
		}
		kind := firstOf(a.Kind, "fact")
		if len(allowed) > 0 && !allowed[kind] {
			continue
		}
		sources := []api.MemorySource{}
		if in.Info.ID != "" {
			sources = append(sources, api.MemorySource{Type: api.SourceRepo, ID: in.Info.ID})
		}
		for _, c := range a.Commits {
			sources = append(sources, api.MemorySource{Type: api.SourceCommit, ID: c})
		}
		for _, u := range in.Known.Filter(a.Units) {
			sources = append(sources, api.MemorySource{Type: api.SourceUnit, ID: u})
		}
		res, err := out.Memory(ctx, api.MemoryWrite{Title: a.Title, Body: a.Body, Kind: kind, Tags: a.Tags, Confidence: a.Confidence, Namespace: ns, Sources: sources})
		if err != nil {
			if api.StopsRun(err) || api.IsLicenseError(err) {
				return rep, err
			}
			return rep, fmt.Errorf("write atom %q: %w", a.Title, err)
		}
		written++
		switch res.Outcome {
		case api.OutcomeQueuedForReview:
			rep.Counts.Queued++
		case api.OutcomeUnchanged:
			rep.Counts.Unchanged++
		default:
			rep.Counts.Facts++
		}
	}
	rep.UnitsTouched = touchedKeys(in)
	rep.Summary = summaryLine(rep.Counts, firstOf(ns, "organization memory"))
	return rep, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
