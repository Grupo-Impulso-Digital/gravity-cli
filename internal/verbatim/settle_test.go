package verbatim_test

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

func TestSettleKeepsLiveSlugsAndLegacyCollections(t *testing.T) {
	src := memSource{
		"docs/Dépannage/Guía rápida.md": "# Guía rápida\n\nVoir [l'état](Réseau/État.md#État-des-actifs).\n",
		"docs/Dépannage/Réseau/État.md": "# État\n\n## État des actifs\n",
		"docs/Dépannage/nouveau.md":     "# Nouveau\n",
		"docs/Sécurité/index.md":        "# Sécurité\n",
	}
	m, err := verbatim.Map(src, verbatim.MapOptions{Files: []verbatim.FileSpec{{Include: "docs/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	m.Settle(nil, []api.TreeCollection{{Slug: "d-pannage", Path: []string{"d-pannage"}}}, map[string]api.TreePage{
		"docs/Dépannage/Guía rápida.md": {Slug: "gu-a-r-pida"},
	})
	want := map[string]string{
		"docs/Dépannage/Guía rápida.md": "d-pannage/gu-a-r-pida",
		"docs/Dépannage/Réseau/État.md": "d-pannage/reseau/etat",
		"docs/Dépannage/nouveau.md":     "d-pannage/nouveau",
		"docs/Sécurité/index.md":        "securite/overview",
	}
	for _, p := range m.Pages {
		if got := strings.Join(append(append([]string{}, p.CollectionPath...), p.Slug), "/"); got != want[p.Path] {
			t.Errorf("%s = %s, want %s", p.Path, got, want[p.Path])
		}
	}
	etat, _ := m.Lookup("docs/Dépannage/Réseau/État.md")
	if titles := m.TitlesFor(*etat); titles["d-pannage"] != "Dépannage" || titles["reseau"] != "Réseau" {
		t.Errorf("titles = %v", titles)
	}
	link := m.Linker("docs/Dépannage/Guía rápida.md", "handbook", verbatim.Repo{})
	if got := link("Réseau/État.md#État-des-actifs"); got != "../handbook/etat#etat-des-actifs" {
		t.Errorf("link = %s", got)
	}
	back := m.Linker("docs/Dépannage/Réseau/État.md", "handbook", verbatim.Repo{})
	if got := back("../Guía rápida.md"); got != "../handbook/gu-a-r-pida" {
		t.Errorf("link to a managed page = %s", got)
	}
}
