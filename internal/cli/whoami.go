package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type whoamiData struct {
	Principal     *api.Principal  `json:"principal"`
	Organization  *api.OrgRef     `json:"organization"`
	Organizations []api.OrgRef    `json:"organizations"`
	Token         whoamiToken     `json:"token"`
	APIURL        string          `json:"apiUrl"`
	Profile       string          `json:"profile,omitempty"`
	Features      map[string]bool `json:"features"`
	Modules       map[string]bool `json:"modules,omitempty"`
}

type whoamiToken struct {
	Kind      string   `json:"kind"`
	Source    string   `json:"source"`
	Variable  string   `json:"variable,omitempty"`
	KeyHint   string   `json:"keyHint,omitempty"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expiresAt"`
}

func (t whoamiToken) from() string {
	if t.Variable != "" {
		return t.Source + " " + t.Variable
	}
	return t.Source
}

func profileOf(creds auth.Credentials) string {
	if creds.TokenSource == auth.SourceProfile {
		return creds.ProfileName
	}
	return ""
}

func newWhoamiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who the current token acts as, its organization, kind, scopes and expiry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds, err := a.credentials(a.repoAPIURL(cmd.Context()))
			if err != nil {
				return err
			}
			if err := a.requireToken(creds); err != nil {
				return err
			}
			who, err := a.client(creds).WhoAmI(cmd.Context())
			if err != nil {
				return explainAPI(err)
			}
			data := whoamiSummary(who, creds)
			a.printWhoami(data)
			return a.ui.Result(data)
		},
	}
}

func whoamiSummary(who *api.WhoAmI, creds auth.Credentials) whoamiData {
	org := who.Organization
	if org == nil {
		org = &api.OrgRef{ID: who.OrganizationID, Name: who.OrganizationName}
	}
	tok := whoamiToken{Kind: creds.TokenKind, Source: creds.TokenSource, Variable: creds.TokenEnv, KeyHint: who.KeyHint, Scopes: []string{}}
	if who.Token != nil {
		if who.Token.Kind != "" {
			tok.Kind = who.Token.Kind
		}
		if who.Token.Scopes != nil {
			tok.Scopes = who.Token.Scopes
		}
		tok.ExpiresAt = who.Token.ExpiresAt
	}
	orgs := who.Organizations
	if orgs == nil {
		orgs = []api.OrgRef{}
	}
	features := who.Features
	if features == nil {
		features = map[string]bool{}
	}
	return whoamiData{
		Principal: who.Principal, Organization: org, Organizations: orgs, Token: tok,
		APIURL: creds.APIURL, Profile: profileOf(creds), Features: features, Modules: who.Modules,
	}
}

func (a *app) printWhoami(d whoamiData) {
	p := a.ui
	orgName := d.Organization.Name
	if orgName == "" {
		orgName = d.Organization.Slug
	}
	switch {
	case d.Principal != nil && d.Principal.Kind == api.TokenKindRepo && d.Principal.Repo != nil:
		line := "Repository token for " + d.Principal.Repo.RemoteKey
		if d.Principal.Repo.Product != nil && d.Principal.Repo.Product.Slug != "" {
			line += " (product " + d.Principal.Repo.Product.Slug + ")"
		}
		p.Println("%s %s · %s", p.Mark(ui.MarkOK), line, orgName)
	case d.Principal != nil && d.Principal.User != nil:
		role := ""
		if d.Principal.Role != "" {
			role = " (" + d.Principal.Role + ")"
		}
		p.Println("%s Signed in as %s%s · %s", p.Mark(ui.MarkOK), d.Principal.User.Email, role, orgName)
	case d.Principal != nil && d.Principal.CreatedBy != nil:
		p.Println("%s Organization token created by %s · %s", p.Mark(ui.MarkOK), d.Principal.CreatedBy.Email, orgName)
	default:
		p.Println("%s Organization token · %s", p.Mark(ui.MarkOK), orgName)
	}
	rows := [][]string{}
	token := d.Token.Kind + " token"
	if d.Token.KeyHint != "" {
		token += " …" + d.Token.KeyHint
	}
	if d.Token.ExpiresAt != nil && *d.Token.ExpiresAt != "" {
		token += " · expires " + *d.Token.ExpiresAt
	} else {
		token += " · no expiry"
	}
	rows = append(rows, []string{"Token", token + " · from " + d.Token.from()})
	if len(d.Token.Scopes) > 0 {
		rows = append(rows, []string{"Scopes", strings.Join(d.Token.Scopes, ", ")})
	}
	if d.Profile != "" {
		rows = append(rows, []string{"Profile", d.Profile})
	}
	rows = append(rows, []string{"API", d.APIURL})
	var others []string
	for _, o := range d.Organizations {
		if d.Organization != nil && o.ID == d.Organization.ID {
			continue
		}
		label := o.Slug
		if o.Role != "" {
			label += " (" + o.Role + ")"
		}
		others = append(others, label)
	}
	if len(others) > 0 {
		rows = append(rows, []string{"Other orgs", strings.Join(others, ", ")})
	}
	p.Table("  ", rows)
}
