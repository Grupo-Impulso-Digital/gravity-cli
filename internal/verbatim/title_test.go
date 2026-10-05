package verbatim_test

import (
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

func TestHumanizeTitle(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"@grupo-impulso-digital/polaris-admin", "Polaris Admin", true},
		{"@acme/core.utils", "Core Utils", true},
		{"polaris-admin", "Polaris Admin", true},
		{"my_pkg", "My Pkg", true},
		{"Polaris Admin", "Polaris Admin", false},
		{"installation", "installation", false},
		{"Getting started", "Getting started", false},
		{"@scope", "@scope", false},
	}
	for _, c := range cases {
		got, ok := verbatim.HumanizeTitle(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("HumanizeTitle(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func headings(doc *verbatim.Document) []string {
	var out []string
	for _, b := range doc.Blocks {
		if b.Type == "heading" {
			if c, ok := b.Content.(map[string]any); ok {
				out = append(out, c["text"].(string))
			}
		}
	}
	return out
}

func TestLeadingH1MatchingTheTitleIsDropped(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		opts  verbatim.Options
		heads []string
	}{
		{"front matter title", "# Installation Guide\n\nText\n\n## Steps\n", verbatim.Options{Title: "installation  guide"}, []string{"Steps"}},
		{"title override differs", "# Another\n\nText\n", verbatim.Options{Title: "Other"}, []string{"Another"}},
		{"package humanized", "# @acme/polaris-admin\n\nText\n", verbatim.Options{Title: "Polaris Admin"}, nil},
		{"not leading", "Intro\n\n# Installation\n", verbatim.Options{Title: "Installation"}, []string{"Installation"}},
		{"h1 title keeps the old rule", "Intro\n\n# Installation\n", verbatim.Options{DropTitleH1: true}, nil},
	}
	for _, c := range cases {
		c.opts.Path = "x.md"
		got := headings(verbatim.Convert([]byte(c.body), c.opts))
		if len(got) != len(c.heads) {
			t.Fatalf("%s: headings %v, want %v", c.name, got, c.heads)
		}
		for i := range got {
			if got[i] != c.heads[i] {
				t.Fatalf("%s: headings %v, want %v", c.name, got, c.heads)
			}
		}
	}
}

func TestMapHumanizesPackageTitlesAndFlagsEmptyFiles(t *testing.T) {
	src := memSource{
		"packages/admin/README.md":        "# @grupo-impulso-digital/polaris-admin\n\nAdmin app.\n",
		"packages/api/README.md":          "---\nslug: \"@acme/polaris-api\"\n---\n# API\n",
		"packages/empty/README.md":        "<!-- todo -->\n\n# Empty\n\n",
		"packages/blank/README.md":        "\n",
		"packages/web/getting-started.md": "Some text\n",
	}
	m, err := verbatim.Map(src, verbatim.MapOptions{Files: []verbatim.FileSpec{{Include: "packages/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]verbatim.Page{}
	for _, p := range m.Pages {
		by[p.Path] = p
	}
	admin := by["packages/admin/README.md"]
	if admin.Title != "Polaris Admin" || admin.PackageName != "@grupo-impulso-digital/polaris-admin" || admin.Empty {
		t.Errorf("admin: %+v", admin)
	}
	if api := by["packages/api/README.md"]; api.Slug != "polaris-api" || !api.Empty {
		t.Errorf("api: slug %q empty %v", api.Slug, api.Empty)
	}
	if !by["packages/empty/README.md"].Empty || !by["packages/blank/README.md"].Empty {
		t.Error("comment-and-heading-only files must be flagged empty")
	}
	if web := by["packages/web/getting-started.md"]; web.Title != "Getting Started" || web.PackageName != "" || web.Empty {
		t.Errorf("web: %+v", web)
	}
}
