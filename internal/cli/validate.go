package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type validateData struct {
	OK       bool              `json:"ok"`
	Errors   int               `json:"errors"`
	Warnings int               `json:"warnings"`
	Issues   []issue           `json:"issues"`
	I18n     []api.I18nOutcome `json:"i18n"`
	Server   string            `json:"server"`
}

func newValidateCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Check .gravity.yaml: schema, mapping and structure rules, and the server's conflict checks",
		Long: "Validate the manifest the way a run would: the schema, local rules (duplicate slugs, empty files, package-name titles, structure sources no pass imports) and, signed in, the platform's checks (pages that already own a slug, targets that are missing or unapproved, languages the site lacks, translations that are switched off).\n" +
			"Every issue names the pass and path it comes from and how to fix it. Exits 1 when there are errors.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.collect(cmd.Context(), modelOptions{server: true})
			if err != nil {
				return err
			}
			var i18n []api.I18nOutcome
			if m.validation != nil {
				i18n = m.validation.I18n
			}
			a.printIssues(m.issues, i18n)
			return a.validationExit(m)
		},
	}
}

func (a *app) validationData(m *model) validateData {
	d := validateData{OK: m.errors() == 0, Errors: m.errors(), Warnings: m.warnings(), Issues: m.issues, I18n: []api.I18nOutcome{}, Server: m.server}
	if d.Issues == nil {
		d.Issues = []issue{}
	}
	if m.validation != nil && m.validation.I18n != nil {
		d.I18n = m.validation.I18n
	}
	return d
}

func (a *app) validationExit(m *model) error {
	d := a.validationData(m)
	if d.OK {
		return a.ui.Result(d)
	}
	msg := fmt.Sprintf("%s in %s", plural(d.Errors, "error", "errors"), manifestRel(m.info, m.manifest))
	if err := a.ui.Failure(ui.ErrorInfo{Code: "validate_failed", Message: msg, ExitCode: CodeFindings}, d); err != nil {
		return err
	}
	return &ExitError{Code: CodeFindings, ErrCode: "validate_failed", Err: errors.New(msg)}
}

func issueMark(sev string) string {
	if sev == severityError {
		return ui.MarkFail
	}
	return ui.MarkWarn
}

func (a *app) printIssues(list []issue, i18n []api.I18nOutcome) {
	renderIssues(a.ui, list, i18n)
}

func renderIssues(p *ui.Printer, list []issue, i18n []api.I18nOutcome) {
	groups := map[string][]issue{}
	var order []string
	for _, is := range list {
		key := is.Pass
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], is)
	}
	for _, key := range order {
		title := "Manifest"
		if key != "" {
			title = "Pass " + key
		}
		items := groups[key]
		errs := 0
		for _, is := range items {
			if is.Severity == severityError {
				errs++
			}
		}
		p.Section(title, fmt.Sprintf("%s, %s", plural(errs, "error", "errors"), plural(len(items)-errs, "warning", "warnings")))
		for _, is := range items {
			tone := ui.ToneWarn
			if is.Severity == severityError {
				tone = ui.ToneFail
			}
			p.Println("  %s %s  %s", p.Mark(issueMark(is.Severity)), p.Paint(tone, is.Code), is.Message)
			if is.Path != "" {
				p.Println("      %s %s", p.Dim("at "), is.Path)
			}
			if is.Hint != "" {
				p.Println("      %s %s", p.Dim("fix"), is.Hint)
			}
		}
	}
	if len(i18n) > 0 {
		p.Section("Languages", "what each pass will really produce")
		rows := make([][]string, 0, len(i18n))
		for _, o := range i18n {
			rows = append(rows, []string{o.Pass, orDash(strings.Join(o.Languages, ",")), orDash(strings.Join(o.Effective, ",")), o.Reason})
		}
		p.Grid("  ", []string{"Pass", "Requested", "Effective", "Why"}, rows)
	}
	errs := 0
	for _, is := range list {
		if is.Severity == severityError {
			errs++
		}
	}
	p.Println("")
	switch {
	case errs > 0:
		p.Note("", ui.MarkFail, "%s, %s", plural(errs, "error", "errors"), plural(len(list)-errs, "warning", "warnings"))
	case len(list) > 0:
		p.Note("", ui.MarkWarn, "valid, with %s", plural(len(list), "warning", "warnings"))
	default:
		p.Note("", ui.MarkOK, "valid")
	}
}

func renderRunIssues(p *ui.Printer, list []issue) {
	errs := 0
	for _, is := range list {
		if is.Severity == severityError {
			errs++
		}
	}
	if errs > 0 {
		renderIssues(p, list, nil)
		return
	}
	for _, is := range list {
		if is.Code == "server_validate_unsupported" {
			p.Debugf("%s", is.Message)
			continue
		}
		p.Note("", issueMark(is.Severity), "%s  %s", p.Paint(ui.ToneWarn, is.Code), is.Message)
	}
}
