package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type statusData struct {
	Whoami       whoamiData          `json:"whoami"`
	Repo         api.ConnectedRepo   `json:"repo"`
	Manifest     *statusManifest     `json:"manifest"`
	Effective    api.Effective       `json:"effective"`
	Status       *api.Status         `json:"status"`
	Siblings     []api.Sibling       `json:"siblings"`
	Outcome      api.ManifestOutcome `json:"connect"`
	Capabilities []capabilityWarning `json:"capabilityWarnings"`
}

type capabilityWarning struct {
	Code    string `json:"code"`
	Pass    string `json:"pass,omitempty"`
	Message string `json:"message"`
}

type statusManifest struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	V1   bool   `json:"v1,omitempty"`
}

type session struct {
	info     *repoInfo
	manifest *config.Manifest
	v1       bool
	client   *api.Client
	who      *api.WhoAmI
	whoData  whoamiData
	creds    auth.Credentials
}

func (a *app) openSession(ctx context.Context, allowV1 bool) (*session, error) {
	info, err := a.inspectRepo(ctx)
	if err != nil {
		return nil, err
	}
	s := &session{info: info}
	m, err := a.loadManifest(info.root)
	switch {
	case errors.Is(err, config.ErrV1Manifest) && allowV1:
		s.v1 = true
		a.ui.Warn("manifest_v1", "this repository has a v1 .gravity.yaml; run `gravity init` to convert it (it is ignored until then)")
	case err != nil:
		return nil, err
	default:
		s.manifest = m
	}
	apiURL := ""
	if s.manifest != nil {
		apiURL = s.manifest.APIURL
	}
	creds, err := a.credentials(apiURL)
	if err != nil {
		return nil, err
	}
	if err := requireToken(creds); err != nil {
		return nil, err
	}
	s.client = a.client(creds)
	who, err := s.client.WhoAmI(ctx)
	if err != nil {
		return nil, explainAPI(err)
	}
	if err := requirePipelines(who.Features); err != nil {
		return nil, err
	}
	s.who = who
	s.creds = creds
	s.whoData = whoamiSummary(who, creds)
	return s, nil
}

func newStatusCmd(a *app) *cobra.Command {
	var runs int
	var check bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "One view of this repository: auth, product, passes, targets, watermarks, runs, bundles, health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.openSession(ctx, true)
			if err != nil {
				return err
			}
			c, err := a.detectCI(ctx, s.info.repo)
			if err != nil {
				return err
			}
			conn, err := s.client.Connect(ctx, a.connectRequest(s.info, s.manifest, api.ContextStatus, origin(c), true))
			if err != nil {
				return Fail(CodeError, fmt.Errorf("connect: %w", err))
			}
			st, err := s.client.Status(ctx, repoParam(s.who, s.info), runs)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("status: %w", err))
			}
			for _, w := range conn.Manifest.Warnings {
				a.ui.Warn(w.Code, w.Message)
			}
			data := statusData{Whoami: s.whoData, Repo: conn.Repo, Effective: conn.Effective, Status: st, Siblings: conn.Siblings, Outcome: conn.Manifest}
			if data.Siblings == nil {
				data.Siblings = []api.Sibling{}
			}
			data.Capabilities = capabilityWarnings(s, conn, a.statusPlan(ctx, s, conn), a.now())
			if s.manifest != nil {
				data.Manifest = &statusManifest{Path: s.manifest.Path, Hash: s.manifest.Hash}
			} else if s.v1 {
				data.Manifest = &statusManifest{Path: config.ManifestFileName, V1: true}
			}
			a.printStatus(s, conn, st, data.Capabilities)
			if check && st.Health.Status != api.HealthLive {
				reason := ""
				if len(st.Health.Reasons) > 0 {
					reason = ": " + strings.Join(st.Health.Reasons, "; ")
				}
				ee := &ExitError{Code: CodeFindings, Err: fmt.Errorf("health is %s%s", st.Health.Status, reason), ErrCode: "unhealthy"}
				if err := a.ui.Failure(ui.ErrorInfo{Code: ee.ErrCode, Message: ee.Error(), ExitCode: ee.Code}, data); err != nil {
					return err
				}
				return ee
			}
			return a.ui.Result(data)
		},
	}
	cmd.Flags().IntVar(&runs, "runs", 5, "number of recent runs to show")
	cmd.Flags().BoolVar(&check, "check", false, "exit 1 when health is not live")
	return cmd
}

func healthMark(status string) string {
	switch status {
	case api.HealthLive:
		return ui.MarkOK
	case api.HealthNeverRun, api.HealthStale, api.HealthBlocked:
		return ui.MarkWarn
	}
	return ui.MarkFail
}

func (a *app) statusPlan(ctx context.Context, s *session, conn *api.ConnectResponse) *api.Plan {
	q := api.PlanQuery{Repo: repoParam(s.who, s.info), Trigger: config.TriggerPush, Branch: firstNonEmpty(conn.Manifest.AuthoritativeBranch, s.info.defaultBranch, s.info.branch), Mode: api.ModeDry}
	if s.manifest != nil {
		q.ManifestHash = s.manifest.Hash
	}
	p, err := s.client.Plan(ctx, q)
	if err != nil {
		a.ui.Debugf("status plan: %v", err)
		return nil
	}
	return p
}

var aiKinds = map[string]bool{config.KindGuides: true, config.KindChangelog: true, config.KindNucleus: true}

func usesLLM(p api.PlanPass) bool {
	switch {
	case aiKinds[p.Kind]:
		return true
	case p.Kind == config.KindReference:
		v, _ := p.Options["prose"].(bool)
		return v
	case p.Kind == config.KindCheck:
		v, ok := p.Options["claims"].(bool)
		return !ok || v
	}
	return false
}

func capabilityWarnings(s *session, conn *api.ConnectResponse, plan *api.Plan, now time.Time) []capabilityWarning {
	out := []capabilityWarning{}
	add := func(code, pass, msg string) {
		out = append(out, capabilityWarning{Code: code, Pass: pass, Message: msg})
	}
	if exp := s.whoData.Token.ExpiresAt; exp != nil && *exp != "" {
		if t, err := time.Parse(time.RFC3339, *exp); err == nil {
			switch left := t.Sub(now); {
			case left <= 0:
				add("token_expired", "", "the token expired on "+t.Format("2006-01-02")+"; run `gravity login` again")
			case left < 14*24*time.Hour:
				add("token_expiring", "", fmt.Sprintf("the token expires in %s (%s); run `gravity login` to renew it", plural(int(math.Ceil(left.Hours()/24)), "day", "days"), t.Format("2006-01-02")))
			}
		}
	}
	passes := conn.Effective.Passes
	if plan != nil && len(plan.Passes) > 0 {
		passes = plan.Passes
	}
	modules := s.who.Modules
	features := s.who.Features
	if plan != nil {
		if plan.Capabilities.Modules != nil {
			modules = plan.Capabilities.Modules
		}
		if plan.Capabilities.Features != nil {
			features = plan.Capabilities.Features
		}
	}
	needLLM := []string{}
	for _, p := range passes {
		if !p.Enabled {
			continue
		}
		switch {
		case p.Kind == config.KindNucleus && modules != nil && !modules["memory"]:
			add("module_disabled", p.Name, "pass "+p.Name+" needs the memory module, which this organization does not have")
		case p.Kind == config.KindCapture && modules != nil && !modules["agent"]:
			add("module_disabled", p.Name, "pass "+p.Name+" needs the agent module, which this organization does not have")
		case p.Kind == config.KindVerbatim && !features["verbatim-lock"]:
			add("feature_unavailable", p.Name, "pass "+p.Name+" imports verbatim pages, which this Gravity server does not support yet")
		}
		if p.SkipReason == api.SkipScopeMissing {
			add("scope_missing", p.Name, "pass "+p.Name+" needs token scopes "+strings.Join(p.MissingScopes, ", ")+"; mint a token with them in the app")
		}
		if usesLLM(p) {
			needLLM = append(needLLM, p.Name)
		}
	}
	if plan != nil && !plan.Capabilities.LLM.Configured && len(needLLM) > 0 {
		add("llm_not_configured", "", "no AI provider is configured for this organization; "+strings.Join(needLLM, ", ")+" cannot write until an admin adds one in the app")
	}
	return out
}

func (a *app) printStatus(s *session, conn *api.ConnectResponse, st *api.Status, warnings []capabilityWarning) {
	p := a.ui
	product := firstNonEmpty(st.Product.Name, st.Product.Slug, conn.Repo.Product.Name)
	p.Println("%s %s → %s   %s health: %s", p.Bold("Gravity ·"), firstNonEmpty(st.Repo.Name, s.info.name), product, p.Mark(healthMark(st.Health.Status)), st.Health.Status)
	for _, r := range st.Health.Reasons {
		p.Println("    %s", r)
	}
	rows := [][]string{}
	org := firstNonEmpty(s.whoData.Organization.Name, s.whoData.Organization.Slug)
	signed := s.whoData.Token.Kind + " token"
	if u := s.whoData.Principal; u != nil && u.User != nil {
		signed = u.User.Email + " · " + org + " · " + signed
	} else {
		signed += " · " + org
	}
	if s.whoData.Profile != "" {
		signed += " · profile " + s.whoData.Profile
	} else {
		signed += " · from " + s.whoData.Token.Source
	}
	if exp := s.whoData.Token.ExpiresAt; exp != nil && *exp != "" {
		signed += " · expires " + strings.SplitN(*exp, "T", 2)[0]
	}
	rows = append(rows, []string{"Auth", signed})
	connection := s.info.remoteKey
	if st.Repo.LastConnectAt != "" {
		connection += " · last connect " + st.Repo.LastConnectAt
	}
	rows = append(rows, []string{"Repository", connection})
	if link := firstNonEmpty(st.Repo.AppURL, conn.Repo.AppURL); link != "" {
		rows = append(rows, []string{"App", link})
	}
	switch {
	case s.manifest != nil:
		state := "stored in Gravity"
		if !conn.Manifest.Persisted {
			authoritative := firstNonEmpty(conn.Manifest.AuthoritativeBranch, s.info.defaultBranch)
			state = "local changes not yet stored"
			if authoritative != "" {
				state += " (lands from " + authoritative + ")"
			}
			if conn.Manifest.Hash != "" && st.Repo.ID != "" && conn.Effective.Overlay {
				state = "differs from the stored manifest; shown as an overlay"
				if authoritative != "" {
					state = "differs from " + authoritative + "; shown as an overlay"
				}
			}
		}
		rows = append(rows, []string{"Manifest", relPath(s.info.root, s.manifest.Path) + " · " + shortHash(s.manifest.Hash) + " · " + state})
	case s.v1:
		rows = append(rows, []string{"Manifest", ".gravity.yaml is v1 (run gravity init to convert)"})
	default:
		rows = append(rows, []string{"Manifest", "none (passes come from the app)"})
	}
	if st.Repo.CLIVersion != "" {
		rows = append(rows, []string{"Last CLI", st.Repo.CLIVersion})
	}
	p.Table("  ", rows)

	approve := map[string]string{}
	locked := map[string]bool{}
	for _, ep := range conn.Effective.Passes {
		if ep.Target.ApproveURL != "" {
			approve[ep.Name] = ep.Target.ApproveURL
		}
		locked[ep.Name] = ep.Locked
	}
	if len(st.Passes) == 0 {
		p.Println("No passes yet. Add them in the app: %s", orDash(firstNonEmpty(st.Repo.AppURL, conn.Repo.AppURL)))
	} else {
		p.Println("%s", p.Bold("Passes"))
		prow := [][]string{}
		for _, sp := range st.Passes {
			mark := ui.MarkOK
			note := ""
			switch {
			case !sp.Enabled:
				mark, note = ui.MarkSkip, "disabled"
			case sp.Target.Status == api.TargetUnapproved:
				mark, note = ui.MarkWarn, "awaiting approval "+approve[sp.Name]
			case sp.Target.Status == api.TargetMissing:
				mark, note = ui.MarkFail, "target missing"
			case sp.LastRun != nil && sp.LastRun.Status == api.StatusFailed:
				mark, note = ui.MarkFail, "last run failed"
			}
			wm := "-"
			if len(sp.Watermarks) > 0 {
				parts := make([]string, 0, len(sp.Watermarks))
				for _, w := range sp.Watermarks {
					parts = append(parts, w.Branch+"@"+shortSHA(w.CommitSHA))
				}
				wm = strings.Join(parts, " ")
			}
			last := "never ran"
			if sp.LastRun != nil {
				last = sp.LastRun.Status + " " + sp.LastRun.At
			}
			source := "app"
			if sp.Source == "manifest" || locked[sp.Name] {
				source = "repo (locked)"
			}
			prow = append(prow, []string{p.Mark(mark), sp.Name, sp.Kind, orDash(sp.Target.Ref), source, wm, last, note})
		}
		p.Table("  ", prow)
	}
	if len(st.Runs) > 0 {
		p.Println("%s", p.Bold("Recent runs"))
		rrow := [][]string{}
		for _, r := range st.Runs {
			rrow = append(rrow, []string{r.ID, r.Trigger, orDash(r.Branch), r.Mode, r.Status, fmt.Sprintf("%d changes", r.Changes), fmt.Sprintf("%d findings", r.Findings), fmt.Sprintf("$%.2f", r.CostUSD)})
		}
		p.Table("  ", rrow)
	}
	for _, b := range st.OpenBundles {
		p.Println("Bundle awaiting review: %d changes → %s", b.Pending, b.AppURL)
	}
	if len(st.Tokens) > 0 {
		trow := [][]string{}
		for _, t := range st.Tokens {
			exp := "no expiry"
			if t.ExpiresAt != nil {
				exp = "expires " + *t.ExpiresAt
			}
			trow = append(trow, []string{"…" + t.KeyHint, t.Kind, "last used " + orDash(t.LastUsedAt), exp})
		}
		p.Println("%s", p.Bold("Tokens"))
		p.Table("  ", trow)
	}
	if len(warnings) > 0 {
		p.Println("%s", p.Bold("Capabilities"))
		for _, w := range warnings {
			p.Println("  %s %s", p.Mark(ui.MarkWarn), w.Message)
		}
	}
	if len(conn.Siblings) > 0 {
		names := make([]string, 0, len(conn.Siblings))
		for _, sib := range conn.Siblings {
			names = append(names, sib.Name+" ("+orDash(sib.Health)+")")
		}
		p.Println("Same product: %s", strings.Join(names, ", "))
	}
}

func shortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		h = h[:12]
	}
	return "sha256:" + h
}

func relPath(root, p string) string {
	if strings.HasPrefix(p, root+"/") {
		return strings.TrimPrefix(p, root+"/")
	}
	return p
}
