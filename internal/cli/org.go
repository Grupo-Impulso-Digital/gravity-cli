package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/auth"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/ui"
)

type orgEntry struct {
	Slug    string `json:"slug"`
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Profile string `json:"profile,omitempty"`
	Current bool   `json:"current"`
}

type orgData struct {
	Organizations []orgEntry `json:"organizations"`
	Current       string     `json:"current,omitempty"`
}

func newOrgCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "List your organizations and switch between them (list, use)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.orgList(cmd)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Organizations you belong to, and which have a stored profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.orgList(cmd)
		},
	}, &cobra.Command{
		Use:   "use <slug>",
		Short: "Make the profile of an organization current",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.orgUse(args[0])
		},
	})
	return cmd
}

func (a *app) orgList(cmd *cobra.Command) error {
	profiles, err := auth.LoadProfiles()
	if err != nil {
		return Fail(CodeError, err)
	}
	byOrg := map[string]string{}
	for _, n := range profiles.Names() {
		prof, _ := profiles.Get(n)
		if prof.Org != "" {
			byOrg[prof.Org] = n
		}
	}
	d := orgData{Organizations: []orgEntry{}}
	current := ""
	if prof, ok := profiles.Get(profiles.Current); ok {
		current = prof.Org
	}
	seen := map[string]bool{}
	creds, err := a.credentials(a.repoAPIURL(cmd.Context()))
	if err == nil && creds.Token != "" {
		if who, werr := a.client(creds).WhoAmI(cmd.Context()); werr == nil {
			if who.Organization != nil {
				current = who.Organization.Slug
			}
			for _, o := range who.Organizations {
				seen[o.Slug] = true
				d.Organizations = append(d.Organizations, orgEntry{Slug: o.Slug, Name: o.Name, Role: o.Role, Profile: byOrg[o.Slug], Current: o.Slug == current})
			}
		}
	}
	for org, name := range byOrg {
		if !seen[org] {
			d.Organizations = append(d.Organizations, orgEntry{Slug: org, Profile: name, Current: org == current})
		}
	}
	d.Current = current
	p := a.ui
	if len(d.Organizations) == 0 {
		p.Println("Not signed in. Run %s.", p.Bold("gravity login"))
		return a.ui.Result(d)
	}
	rows := make([][]string, 0, len(d.Organizations))
	for _, o := range d.Organizations {
		mark := " "
		if o.Current {
			mark = p.Paint(ui.ToneOK, p.Glyph("●", "*"))
		}
		profile := o.Profile
		if profile == "" {
			profile = p.Dim("gravity login --org " + o.Slug)
		}
		rows = append(rows, []string{mark + " " + o.Slug, firstNonEmpty(o.Name, "-"), firstNonEmpty(o.Role, "-"), profile})
	}
	p.Grid("", []string{"Organization", "Name", "Role", "Profile"}, rows)
	return a.ui.Result(d)
}

func (a *app) orgUse(slug string) error {
	profiles, err := auth.LoadProfiles()
	if err != nil {
		return Fail(CodeError, err)
	}
	for _, n := range profiles.Names() {
		prof, _ := profiles.Get(n)
		if prof.Org == slug || n == slug {
			profiles.Current = n
			if err := profiles.Save(); err != nil {
				return Fail(CodeError, err)
			}
			a.ui.Note("", ui.MarkOK, "now using %s (profile %s)", firstNonEmpty(prof.Org, slug), n)
			return a.ui.Result(orgData{Current: firstNonEmpty(prof.Org, slug)})
		}
	}
	return &ExitError{Code: CodeError, ErrCode: "profile_not_found", Err: fmt.Errorf("no stored profile for %s; sign in to it with `gravity login --org %s`", slug, slug)}
}
