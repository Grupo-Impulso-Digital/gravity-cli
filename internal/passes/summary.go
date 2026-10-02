package passes

import (
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/changeset"
)

const (
	maxSummaryCommits = 60
	maxSummaryFiles   = 200
)

// ChangeSummary renders a ChangeSet for a kickoff message.
func ChangeSummary(cs *changeset.ChangeSet, rng changeset.Range) string {
	var b strings.Builder
	head := rng.Head
	if head == "" {
		head = "working tree"
	}
	fmt.Fprintf(&b, "## Range\n%s: %s..%s", rng.Kind, firstOf(short(rng.Base), "root"), short(head))
	if rng.Tag != "" {
		fmt.Fprintf(&b, " (release %s, previous %s)", rng.Tag, firstOf(rng.PreviousTag, "none"))
	}
	b.WriteString("\n")
	if cs == nil {
		return b.String()
	}
	if cs.Truncated {
		b.WriteString("The change set was truncated; use git_log and git_diff for the rest.\n")
	}
	if len(cs.Commits) > 0 {
		fmt.Fprintf(&b, "\n## Commits (%d)\n", len(cs.Commits))
		for i, c := range cs.Commits {
			if i == maxSummaryCommits {
				fmt.Fprintf(&b, "… %d more (git_log)\n", len(cs.Commits)-i)
				break
			}
			pr := ""
			if c.PR != nil {
				pr = fmt.Sprintf(" (#%d)", *c.PR)
			}
			fmt.Fprintf(&b, "- %s %s%s — %s\n", short(c.SHA), c.Subject, pr, c.Author)
			if body := strings.TrimSpace(c.Body); body != "" {
				lines := strings.Split(body, "\n")
				if len(lines) > 4 {
					lines = lines[:4]
				}
				for _, l := range lines {
					fmt.Fprintf(&b, "    %s\n", l)
				}
			}
		}
	}
	if len(cs.Files) > 0 {
		fmt.Fprintf(&b, "\n## Files (%d)\n", len(cs.Files))
		for i, f := range cs.Files {
			if i == maxSummaryFiles {
				fmt.Fprintf(&b, "… %d more\n", len(cs.Files)-i)
				break
			}
			old := ""
			if f.OldPath != nil {
				old = " (from " + *f.OldPath + ")"
			}
			fmt.Fprintf(&b, "- %s %s%s +%d -%d\n", f.Status, f.Path, old, f.Additions, f.Deletions)
		}
	}
	if len(cs.Units.Touched)+len(cs.Units.Added)+len(cs.Units.Removed) > 0 {
		b.WriteString("\n## Units\n")
		for _, u := range cs.Units.Touched {
			fmt.Fprintf(&b, "- %s (%s) %s via %s\n", u.Key, u.Kind, u.Change, strings.Join(u.MatchedRefs, ", "))
		}
		for _, u := range cs.Units.Added {
			fmt.Fprintf(&b, "- %s (%s) added at %s\n", u.Key, u.Kind, u.Ref)
		}
		for _, u := range cs.Units.Removed {
			fmt.Fprintf(&b, "- %s (%s) removed\n", u.Key, u.Kind)
		}
	}
	for _, d := range cs.OpenAPI {
		fmt.Fprintf(&b, "\n## OpenAPI %s", d.Path)
		if d.Breaking {
			b.WriteString(" (breaking)")
		}
		b.WriteString("\n")
		for _, op := range d.Added {
			fmt.Fprintf(&b, "- added %s %s %s\n", op.Method, op.Path, op.OperationID)
		}
		for _, op := range d.Removed {
			fmt.Fprintf(&b, "- removed %s %s %s\n", op.Method, op.Path, op.OperationID)
		}
		for _, op := range d.Changed {
			fmt.Fprintf(&b, "- changed %s %s: %s\n", op.Method, op.Path, strings.Join(op.Changes, "; "))
		}
	}
	if len(cs.Symbols.Removed)+len(cs.Symbols.Renamed) > 0 {
		b.WriteString("\n## Symbols (best effort)\n")
		for _, s := range cs.Symbols.Removed {
			fmt.Fprintf(&b, "- removed %s in %s\n", s.Name, s.File)
		}
		for _, s := range cs.Symbols.Renamed {
			fmt.Fprintf(&b, "- renamed %s -> %s in %s\n", s.From, s.To, s.File)
		}
	}
	if len(cs.Docs.Changed) > 0 {
		fmt.Fprintf(&b, "\n## Repository documents changed\n- %s\n", strings.Join(cs.Docs.Changed, "\n- "))
	}
	return b.String()
}

// UnitDetails renders the inventory entries of the given unit keys.
func UnitDetails(p *api.Plan, keys []string) string {
	if p == nil || len(keys) == 0 {
		return ""
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var units []api.Unit
	for _, u := range p.Inventory.Units {
		if want[u.Key] {
			units = append(units, u)
		}
	}
	if len(units) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Units and their contributors\n")
	for _, u := range units {
		fmt.Fprintf(&b, "- %s (%s) %s\n", u.Key, u.Kind, u.Title)
		for _, c := range u.Contributors {
			fmt.Fprintf(&b, "    %s %s: %s\n", c.Repo.Label(), c.Role, strings.Join(c.SourceRefs, ", "))
		}
	}
	return b.String()
}

func passHeader(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s (%s)\nPass: %s (%s) → %s\n", in.Info.Name, in.Info.RemoteKey, in.Pass.Name, in.Pass.Kind, firstOf(in.Pass.Target.Ref, "no target"))
	if len(in.Pass.Audiences) > 0 {
		fmt.Fprintf(&b, "Audiences: %s\n", strings.Join(in.Pass.Audiences, ", "))
	}
	if in.Plan != nil && in.Plan.Product.Slug != "" {
		fmt.Fprintf(&b, "Product: %s (Nucleus namespace %s)\n", firstOf(in.Plan.Product.Name, in.Plan.Product.Slug), in.Plan.Product.NucleusNamespace)
	}
	return b.String()
}
