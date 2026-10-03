package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ci"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/plan"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type passesFlags struct {
	trigger string
	branch  string
}

type passesData struct {
	Trigger  string        `json:"trigger"`
	Branch   string        `json:"branch"`
	Overlay  bool          `json:"overlay"`
	Repo     api.PlanRepo  `json:"repo"`
	Product  api.Product   `json:"product"`
	Passes   []plan.Pass   `json:"passes"`
	Warnings []api.Warning `json:"warnings"`
}

func newPassesCmd(a *app) *cobra.Command {
	var f passesFlags
	cmd := &cobra.Command{
		Use:   "passes",
		Short: "List this repository's passes and whether they apply (list, show, edit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.passesList(cmd.Context(), f)
		},
	}
	cmd.PersistentFlags().StringVar(&f.trigger, "trigger", "", "evaluate passes for this trigger (pr, push, release, schedule, manual)")
	cmd.PersistentFlags().StringVar(&f.branch, "branch", "", "evaluate passes for this branch (the PR target branch for pr)")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Table of passes: name, kind, source, target, triggers, applies",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return a.passesList(cmd.Context(), f)
			},
		},
		&cobra.Command{
			Use:   "show <name>",
			Short: "Everything about one pass, including its instruction layers and watermark",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.passesShow(cmd.Context(), f, args[0])
			},
		},
		&cobra.Command{
			Use:   "edit <name>",
			Short: "Open the pass editor in the app (or the manifest, for passes managed in the repository)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.passesEdit(cmd.Context(), f, args[0])
			},
		},
	)
	return cmd
}

func (a *app) loadPasses(ctx context.Context, f passesFlags) (*session, *passesData, error) {
	s, err := a.openSession(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	c, err := a.detectCI(ctx, s.info.repo)
	if err != nil {
		return nil, nil, err
	}
	trigger := f.trigger
	if trigger == "" {
		trigger = c.Trigger
		if trigger == "" || !c.IsCI() {
			trigger = ci.TriggerPush
		}
	}
	if !ci.ValidTrigger(trigger) {
		return nil, nil, Failf(CodeError, "--trigger %q must be one of pr, push, release, schedule, manual", trigger)
	}
	branch := f.branch
	if branch == "" {
		switch {
		case trigger == ci.TriggerPR && c.PR != nil && c.PR.TargetBranch != "":
			branch = c.PR.TargetBranch
		case trigger == ci.TriggerRelease:
		case c.Branch != "":
			branch = c.Branch
		default:
			branch = s.info.branch
		}
		if branch == "" && c.Detached && trigger != ci.TriggerRelease {
			return nil, nil, &ExitError{Code: CodeError, ErrCode: "branch_unknown", Err: errors.New("HEAD is detached and is not on the default branch; pass --branch (or set GRAVITY_BRANCH)")}
		}
	}
	q := api.PlanQuery{Repo: repoParam(s.who, s.info), Trigger: trigger, Branch: branch, Mode: api.ModeDry}
	if trigger == ci.TriggerManual {
		q.Mode = api.ModeWrite
	}
	if s.manifest != nil && q.Mode == api.ModeDry {
		if _, err := s.client.Connect(ctx, a.connectRequest(s.info, s.manifest, api.ContextStatus, origin(c), true)); err != nil {
			return nil, nil, Fail(CodeError, fmt.Errorf("connect: %w", err))
		}
		q.ManifestHash = s.manifest.Hash
	}
	p, err := plan.Fetch(ctx, s.client, q)
	if err != nil {
		return nil, nil, Fail(CodeError, err)
	}
	eff := plan.Merge(p, s.manifest)
	data := &passesData{Trigger: trigger, Branch: branch, Overlay: p.Overlay, Repo: p.Repo, Product: p.Product, Passes: eff.Passes, Warnings: append(append([]api.Warning{}, p.Warnings...), eff.Warnings...)}
	if data.Passes == nil {
		data.Passes = []plan.Pass{}
	}
	for _, w := range data.Warnings {
		a.ui.Warn(w.Code, w.Message)
	}
	return s, data, nil
}

func skipLabel(reason string) string {
	switch reason {
	case "":
		return ""
	case api.SkipTriggerMismatch:
		return "not on this trigger"
	case api.SkipBranchMismatch:
		return "not on this branch"
	case api.SkipTargetUnapproved:
		return "target awaits approval"
	case api.SkipTargetMissing:
		return "target missing"
	case api.SkipScopeMissing:
		return "token lacks scopes"
	case api.SkipModuleDisabled:
		return "module not licensed"
	case api.SkipNotSelected:
		return "not selected"
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func sourceLabel(p plan.Pass) string {
	switch {
	case p.Archived:
		return "repo (removed)"
	case p.Pending:
		return "repo (local)"
	case p.Source == "manifest":
		return "repo"
	}
	return "app"
}

func (a *app) passesList(ctx context.Context, f passesFlags) error {
	_, data, err := a.loadPasses(ctx, f)
	if err != nil {
		return err
	}
	p := a.ui
	where := data.Trigger
	if data.Branch != "" {
		where += " " + data.Branch
	}
	p.Println("%s %s → %s · passes for %s", p.Bold("Gravity ·"), orDash(data.Repo.Name), firstNonEmpty(data.Product.Name, data.Product.Slug, "-"), where)
	if len(data.Passes) == 0 {
		p.Println("No passes. Add them in the app (%s) or declare them in .gravity.yaml.", orDash(data.Repo.AppURL))
		return a.ui.Result(data)
	}
	rows := [][]string{{"", "NAME", "KIND", "SOURCE", "TARGET", "TRIGGERS", "APPLIES"}}
	sorted := append([]plan.Pass{}, data.Passes...)
	plan.SortByName(sorted)
	for _, ps := range sorted {
		mark, applies := ui.MarkOK, "yes"
		if !ps.Applies {
			mark, applies = ui.MarkSkip, skipLabel(ps.SkipReason)
			if ps.SkipReason == api.SkipTargetMissing {
				mark = ui.MarkFail
			}
			if ps.SkipReason == api.SkipTargetUnapproved || ps.SkipReason == api.SkipScopeMissing {
				mark = ui.MarkWarn
			}
		}
		rows = append(rows, []string{p.Mark(mark), ps.Name, ps.Kind, sourceLabel(ps), orDash(ps.Target.Ref), orDash(strings.Join(ps.Triggers, ",")), applies})
	}
	p.Table("", rows)
	return a.ui.Result(data)
}

func findPass(data *passesData, name string) (*plan.Pass, error) {
	for i := range data.Passes {
		if data.Passes[i].Name == name {
			return &data.Passes[i], nil
		}
	}
	names := make([]string, 0, len(data.Passes))
	for _, ps := range data.Passes {
		names = append(names, ps.Name)
	}
	sort.Strings(names)
	return nil, Failf(CodeError, "no pass named %q (passes: %s)", name, orDash(strings.Join(names, ", ")))
}

func (a *app) passesShow(ctx context.Context, f passesFlags, name string) error {
	_, data, err := a.loadPasses(ctx, f)
	if err != nil {
		return err
	}
	ps, err := findPass(data, name)
	if err != nil {
		return err
	}
	p := a.ui
	title := ps.Name
	if ps.Title != "" {
		title += " — " + ps.Title
	}
	p.Println("%s", p.Bold(title))
	applies := "yes"
	if !ps.Applies {
		applies = "no: " + skipLabel(ps.SkipReason)
	}
	rows := [][]string{
		{"Kind", ps.Kind + optional(" (template "+ps.Template+")", ps.Template != "")},
		{"Source", sourceLabel(*ps) + optional(" · locked", ps.Locked)},
		{"Enabled", fmt.Sprintf("%v", ps.Enabled)},
		{"Applies", applies + " (" + data.Trigger + optional(" "+data.Branch, data.Branch != "") + ")"},
		{"Target", targetLine(ps.Target)},
		{"Triggers", orDash(strings.Join(ps.Triggers, ", "))},
		{"Branches", firstNonEmpty(strings.Join(ps.Branches, ", "), "default branch")},
		{"Scope", scopeLine(ps.Scope)},
		{"Audiences", orDash(strings.Join(ps.Audiences, ", "))},
		{"Publish", orDash(ps.PublishMode) + optional(" · trusted", ps.Trusted)},
	}
	if len(ps.MissingScopes) > 0 {
		rows = append(rows, []string{"Missing scopes", strings.Join(ps.MissingScopes, ", ")})
	}
	if ps.Watermark != nil {
		rows = append(rows, []string{"Watermark", ps.Watermark.Branch + "@" + shortSHA(ps.Watermark.CommitSHA) + optional(" · "+ps.Watermark.UpdatedAt, ps.Watermark.UpdatedAt != "")})
	} else {
		rows = append(rows, []string{"Watermark", "none yet (the first run surveys recent history)"})
	}
	if ps.Prompt != nil {
		rows = append(rows, []string{"Prompt", ps.Prompt.Name + " " + shortHash(ps.Prompt.Version)})
	}
	if len(ps.Options) > 0 {
		keys := make([]string, 0, len(ps.Options))
		for k := range ps.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, ps.Options[k]))
		}
		rows = append(rows, []string{"Options", strings.Join(parts, " ")})
	}
	p.Table("  ", rows)
	if len(ps.Instructions.Layers) > 0 {
		p.Println("%s %s", p.Bold("Instructions"), p.Dim(shortHash(ps.Instructions.Hash)))
		for i, l := range ps.Instructions.Layers {
			p.Println("  %d. %s", i+1, l.Label)
			for _, line := range strings.Split(strings.TrimSpace(l.Text), "\n") {
				p.Println("     %s", line)
			}
		}
	} else if ps.Pending {
		p.Println("Instructions are composed by the app once this manifest is stored.")
	}
	for _, h := range ps.Hints {
		from := ""
		if h.RaisedBy != nil {
			from = " (from " + h.RaisedBy.Repo + ")"
		}
		p.Println("%s hint: %s%s", p.Mark(ui.MarkWarn), h.Claim, from)
	}
	return a.ui.Result(ps)
}

func optional(s string, cond bool) string {
	if cond {
		return s
	}
	return ""
}

func targetLine(t api.PassTarget) string {
	if t.Ref == "" {
		return "none"
	}
	line := t.Ref + " · " + t.Status
	if t.Approval != nil {
		line += " · approved by " + t.Approval.By
	}
	if t.ApproveURL != "" {
		line += " · approve: " + t.ApproveURL
	}
	if t.ViewerURL != "" {
		line += " · " + t.ViewerURL
	}
	return line
}

func scopeLine(s api.PassScope) string {
	var parts []string
	if len(s.Paths) > 0 {
		parts = append(parts, "paths "+strings.Join(s.Paths, ", "))
	}
	if len(s.Exclude) > 0 {
		parts = append(parts, "excluding "+strings.Join(s.Exclude, ", "))
	}
	if len(s.Units) > 0 {
		parts = append(parts, "units "+strings.Join(s.Units, ", "))
	}
	if len(parts) == 0 {
		return "whole repository"
	}
	return strings.Join(parts, " · ")
}

type editData struct {
	Pass   string `json:"pass"`
	URL    string `json:"url,omitempty"`
	Locked bool   `json:"locked"`
	File   string `json:"file,omitempty"`
	Branch string `json:"branch,omitempty"`
}

func (a *app) passesEdit(ctx context.Context, f passesFlags, name string) error {
	s, data, err := a.loadPasses(ctx, f)
	if err != nil {
		return err
	}
	ps, err := findPass(data, name)
	if err != nil {
		return err
	}
	var url string
	if ps.Locked || ps.Source == "manifest" {
		branch := firstNonEmpty(data.Repo.AuthoritativeBranch, data.Repo.DefaultBranch, s.info.defaultBranch, "main")
		path := config.ManifestFileName
		if s.manifest != nil {
			path = relPath(s.info.root, s.manifest.Path)
		}
		base, provider := s.info.webURL, s.info.provider
		if data.Repo.WebURL != "" {
			base = data.Repo.WebURL
			if _, p := webURL(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")); p != "" {
				provider = p
			}
		}
		url = fileURL(base, provider, branch, path)
		if url == "" {
			a.ui.Println("Pass %s is managed in the repository; edit it in %s on %s", name, path, branch)
			return a.ui.Result(editData{Pass: name, Locked: true, File: path, Branch: branch})
		}
		a.ui.Println("Pass %s is managed in the repository; edit it in %s", name, url)
	} else {
		if data.Repo.AppURL == "" || ps.ID == "" {
			return Failf(CodeError, "the app did not return an editor link for pass %s", name)
		}
		url = strings.TrimRight(data.Repo.AppURL, "/") + "/passes/" + ps.ID
		a.ui.Println("Edit pass %s at %s", name, url)
	}
	if a.ui.Interactive() && a.openBrowser != nil {
		_ = a.openBrowser(url)
	}
	return a.ui.Result(editData{Pass: name, URL: url, Locked: ps.Locked})
}
