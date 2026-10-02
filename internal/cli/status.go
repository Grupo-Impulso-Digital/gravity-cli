package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type statusData struct {
	Whoami    whoamiData          `json:"whoami"`
	Repo      api.ConnectedRepo   `json:"repo"`
	Manifest  *statusManifest     `json:"manifest"`
	Effective api.Effective       `json:"effective"`
	Status    *api.Status         `json:"status"`
	Siblings  []api.Sibling       `json:"siblings"`
	Outcome   api.ManifestOutcome `json:"connect"`
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
		return nil, explainAPI(err, creds)
	}
	if err := requirePipelines(who.Features); err != nil {
		return nil, err
	}
	s.who = who
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
			if s.manifest != nil {
				data.Manifest = &statusManifest{Path: s.manifest.Path, Hash: s.manifest.Hash}
			} else if s.v1 {
				data.Manifest = &statusManifest{Path: config.ManifestFileName, V1: true}
			}
			a.printStatus(s, conn, st)
			if err := a.ui.Result(data); err != nil {
				return err
			}
			if check && st.Health.Status != api.HealthLive {
				reason := ""
				if len(st.Health.Reasons) > 0 {
					reason = ": " + strings.Join(st.Health.Reasons, "; ")
				}
				return &ExitError{Code: CodeFindings, Err: fmt.Errorf("health is %s%s", st.Health.Status, reason), ErrCode: "unhealthy"}
			}
			return nil
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

func (a *app) printStatus(s *session, conn *api.ConnectResponse, st *api.Status) {
	p := a.ui
	product := firstNonEmpty(st.Product.Name, st.Product.Slug, conn.Repo.Product.Name)
	p.Println("%s %s → %s   %s health: %s", p.Bold("Gravity ·"), firstNonEmpty(st.Repo.Name, s.info.name), product, p.Mark(healthMark(st.Health.Status)), st.Health.Status)
	for _, r := range st.Health.Reasons {
		p.Println("    %s", r)
	}
	rows := [][]string{}
	if u := s.whoData.Principal; u != nil && u.User != nil {
		rows = append(rows, []string{"Signed in", u.User.Email + " · " + firstNonEmpty(s.whoData.Organization.Name, s.whoData.Organization.Slug) + " · " + s.whoData.Token.Kind + " token"})
	} else {
		rows = append(rows, []string{"Token", s.whoData.Token.Kind + " token · " + firstNonEmpty(s.whoData.Organization.Name, s.whoData.Organization.Slug)})
	}
	rows = append(rows, []string{"Repository", s.info.remoteKey + " · " + orDash(firstNonEmpty(st.Repo.AppURL, conn.Repo.AppURL))})
	switch {
	case s.manifest != nil:
		state := "stored in Gravity"
		if !conn.Manifest.Persisted {
			state = "local changes not yet stored (lands from " + orDash(conn.Manifest.AuthoritativeBranch) + ")"
			if conn.Manifest.Hash != "" && st.Repo.ID != "" && conn.Effective.Overlay {
				state = "differs from " + orDash(conn.Manifest.AuthoritativeBranch) + "; shown as an overlay"
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
	for _, ep := range conn.Effective.Passes {
		if ep.Target.ApproveURL != "" {
			approve[ep.Name] = ep.Target.ApproveURL
		}
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
			prow = append(prow, []string{p.Mark(mark), sp.Name, sp.Kind, orDash(sp.Target.Ref), sp.Source, wm, last, note})
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
