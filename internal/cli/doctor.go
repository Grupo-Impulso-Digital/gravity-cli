package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type doctorData struct {
	Whoami whoamiData `json:"whoami"`
	AI     doctorAI   `json:"ai"`
	Spend  *api.Spend `json:"spend"`
}

type doctorAI struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	HasKey             bool     `json:"hasKey"`
	ConnectedProviders []string `json:"connectedProviders"`
}

func newDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that AI is ready for this organization: provider, default model and active spend caps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			creds, err := a.credentials(a.repoAPIURL(ctx))
			if err != nil {
				return err
			}
			if err := a.requireToken(creds); err != nil {
				return err
			}
			client := a.client(creds)
			who, err := client.WhoAmI(ctx)
			if err != nil {
				return explainAPI(err)
			}
			cfg, err := client.LLMConfig(ctx)
			if err != nil {
				return explainAPI(err)
			}
			data := doctorData{Whoami: whoamiSummary(who, creds), AI: doctorAIOf(cfg), Spend: cfg.Spend}
			a.printDoctor(data)
			if !cfg.HasKey {
				nk := api.NewNoProviderKeyError()
				ee := &ExitError{Code: CodeLicense, ErrCode: api.CodeNoProviderKey, Err: nk}
				if err := a.ui.Failure(ui.ErrorInfo{Code: ee.ErrCode, Message: ee.Error(), ExitCode: ee.Code}, data); err != nil {
					return err
				}
				return ee
			}
			return a.ui.Result(data)
		},
	}
}

func doctorAIOf(cfg *api.LLMConfig) doctorAI {
	out := doctorAI{Provider: cfg.Provider, Model: cfg.Model, HasKey: cfg.HasKey, ConnectedProviders: []string{}}
	if cfg.HasKey {
		out.ConnectedProviders = append(out.ConnectedProviders, cfg.AvailableProviders...)
	}
	return out
}

func (a *app) printDoctor(d doctorData) {
	p := a.ui
	p.Println("%s %s", p.Bold("Gravity · AI ·"), firstNonEmpty(d.Whoami.Organization.Name, d.Whoami.Organization.Slug))
	if !d.AI.HasKey {
		p.Note("", ui.MarkFail, "no AI provider is connected")
	} else {
		p.Note("", ui.MarkOK, "AI provider connected: %s", strings.Join(d.AI.ConnectedProviders, ", "))
		p.Table("  ", [][]string{{"Default model", d.AI.Model + " (" + d.AI.Provider + ")"}})
	}
	printSpend(p, d.Spend)
}

func printSpend(p *ui.Printer, s *api.Spend) {
	if s == nil {
		p.Note("", ui.MarkSkip, "spend caps: this Gravity server does not report them")
		return
	}
	caps := s.Caps()
	if len(caps) == 0 {
		p.Note("", ui.MarkOK, "no spend caps are set%s", spentThisMonth(s.Org.SpentUSD))
		return
	}
	title := "Spend caps"
	if !s.ResetsAt.IsZero() {
		title += "  " + p.Dim("monthly caps reset on "+s.ResetsAt.UTC().Format(time.DateOnly))
	}
	p.Println("%s", p.Bold(title))
	rows := make([][]string, 0, len(caps))
	for _, c := range caps {
		rows = append(rows, []string{spendLabel(c.Feature), amountOrDash(c.MaxUSDPerRun), amountOrDash(c.MaxUSDPerMonth), amountOrDash(c.SpentUSD)})
	}
	p.Grid("  ", []string{"Scope", "Per run", "Per month", "Spent this month"}, rows)
}

func spentThisMonth(spent *float64) string {
	if spent == nil {
		return ""
	}
	return " (" + api.USD(*spent) + " spent this month)"
}

func amountOrDash(v *float64) string {
	if v == nil {
		return "-"
	}
	return api.USD(*v)
}

func spendLabel(feature string) string {
	switch feature {
	case api.SpendOrg:
		return "Organization"
	case "ask":
		return "Ask AI"
	case "doc-agent":
		return "Doc Agent"
	case "cli-gateway":
		return "CLI"
	case "nucleus":
		return "Nucleus"
	case "translate":
		return "Translations"
	}
	return feature
}
