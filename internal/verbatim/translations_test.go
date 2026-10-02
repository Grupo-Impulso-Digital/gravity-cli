package verbatim_test

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

func TestSuffixLang(t *testing.T) {
	cases := map[string][2]string{
		"docs/guide.fr.md":          {"docs/guide", "fr"},
		"docs/guide.pt-BR.mdx":      {"docs/guide", "pt-BR"},
		"docs/guide.pt_br.markdown": {"docs/guide", "pt-BR"},
		"docs/guide.zh-hant.md":     {"docs/guide", "zh-Hant"},
		"index.DE.md":               {"index", "de"},
	}
	for file, want := range cases {
		stem, lang, ok := verbatim.SuffixLang(file)
		if !ok || stem != want[0] || lang != want[1] {
			t.Errorf("SuffixLang(%q) = %q %q %v, want %v", file, stem, lang, ok, want)
		}
	}
	for _, file := range []string{"docs/guide.md", "docs/api.v2.md", "docs/release.notes.md", "docs/guide.fr.txt", "docs/a.b.c.json", "docs/.fr.md"} {
		if stem, lang, ok := verbatim.SuffixLang(file); ok {
			t.Errorf("SuffixLang(%q) = %q %q, want no language", file, stem, lang)
		}
	}
	if got := verbatim.NormalizeLang(" FR_ca "); got != "fr-CA" {
		t.Errorf("NormalizeLang = %q", got)
	}
}

func translationsOf(t *testing.T, m *verbatim.Mapping, page string) map[string]verbatim.Translation {
	t.Helper()
	p, ok := m.Lookup(page)
	if !ok {
		t.Fatalf("%s is not a page: %+v", page, m.Pages)
	}
	out := map[string]verbatim.Translation{}
	for _, tr := range p.Translations {
		out[tr.Lang] = tr
	}
	return out
}

func TestTranslationsAttachToTheirSourcePage(t *testing.T) {
	src := memSource{
		"docs/guide.md":           "---\ntitle: Guide\n---\nSee [setup](setup.md).\n",
		"docs/guide.fr.md":        "# Le guide\n\nVoir [installation](setup.fr.md).\n",
		"docs/guide.pt-BR.mdx":    "---\ntitle: Guia\ndescription: Um guia\n---\nOi\n",
		"docs/setup.mdx":          "# Setup\n",
		"docs/setup.fr.md":        "Installer.\n",
		"docs/setup.de.md":        "---\ndraft: true\n---\nEntwurf\n",
		"docs/api.v2.md":          "# API v2\n",
		"docs/orphan.es.md":       "---\nlang: es\n---\n# Huérfano\n",
		"docs/notes.it.md":        "# Note\n",
		"docs/fr/guide-intro.md":  "---\nlang: fr\nslug: guide\n---\n# Intro du guide\n",
		"docs/de/handbook.md":     "---\nlang: de\n---\n# Handbuch\n",
		"docs/mismatch.md":        "# Mismatch\n",
		"docs/mismatch.fr.md":     "---\nlang: de\n---\n# Abgleich\n",
		"docs/hidden.md":          "---\nhidden: true\n---\n# Hidden\n",
		"docs/hidden.fr.md":       "# Caché\n",
		"docs/excluded.md":        "# Excluded\n",
		"docs/excluded.fr.md":     "# Exclu\n",
		"docs/guide.fr.markdown":  "# Doublon\n",
		"docs/img/diagram.fr.png": "PNG",
	}
	m, err := verbatim.Map(src, verbatim.MapOptions{Files: []verbatim.FileSpec{{Include: "docs/**", Exclude: []string{"docs/excluded.md"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, p := range m.Pages {
		slugs = append(slugs, p.Path+"="+p.Slug)
	}
	if got := strings.Join(slugs, " "); got != "docs/api.v2.md=api-v2 docs/excluded.fr.md=excluded-fr docs/guide.md=guide docs/mismatch.md=mismatch docs/notes.it.md=notes-it docs/setup.mdx=setup" {
		t.Fatalf("pages = %s", got)
	}
	guide := translationsOf(t, m, "docs/guide.md")
	if len(guide) != 2 || guide["fr"].Path != "docs/guide.fr.markdown" || guide["fr"].Title != "Doublon" || !guide["fr"].TitleFromH1 {
		t.Fatalf("guide translations = %+v", guide)
	}
	if br := guide["pt-BR"]; br.Path != "docs/guide.pt-BR.mdx" || br.Title != "Guia" || br.Description != "Um guia" || !br.MDX || !strings.HasPrefix(br.Hash, "sha256:") {
		t.Fatalf("pt-BR = %+v", br)
	}
	if p, _ := m.Lookup("docs/guide.md"); p.Translations[0].Lang != "fr" || p.Translations[1].Lang != "pt-BR" {
		t.Fatalf("translations are ordered by language: %+v", p.Translations)
	}
	setup := translationsOf(t, m, "docs/setup.mdx")
	if len(setup) != 1 || setup["fr"].Title != "Setup" {
		t.Fatalf("setup translations fall back to the source title: %+v", setup)
	}
	mismatch := translationsOf(t, m, "docs/mismatch.md")
	if len(mismatch) != 1 || mismatch["de"].Path != "docs/mismatch.fr.md" {
		t.Fatalf("front matter lang wins over the suffix: %+v", mismatch)
	}
	if src, ok := m.TranslationSource("docs/guide.fr.markdown"); !ok || src != "docs/guide.md" {
		t.Fatalf("TranslationSource = %q %v", src, ok)
	}
	if _, ok := m.Lookup("docs/guide.fr.md"); ok {
		t.Fatal("a translation never becomes a page")
	}
	if strings.Join(m.HiddenTrans, ",") != "docs/setup.de.md" || strings.Join(m.Hidden, ",") != "docs/hidden.md" {
		t.Fatalf("hidden = %v, hidden translations = %v", m.Hidden, m.HiddenTrans)
	}
	warnings := strings.Join(m.Warnings, "\n")
	for _, want := range []string{
		`docs/orphan.es.md: skipped (lang es); no source-language file docs/orphan.md maps in this pass`,
		`docs/de/handbook.md: skipped (lang de); no source-language page with slug "handbook" maps in this pass`,
		`docs/guide.fr.md: skipped (lang fr); docs/guide.fr.markdown already holds that language of docs/guide.md`,
		`docs/fr/guide-intro.md: skipped (lang fr); docs/guide.fr.markdown already holds that language of docs/guide.md`,
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q in\n%s", want, warnings)
		}
	}
	if strings.Contains(warnings, "hidden.fr.md") {
		t.Errorf("a translation of a hidden page is dropped quietly:\n%s", warnings)
	}
	link := m.Linker("docs/guide.fr.markdown", "handbook", verbatim.Repo{})
	if got := link("setup.fr.md"); got != "../handbook/setup" {
		t.Errorf("a link to a translation resolves to its page: %q", got)
	}
}

func TestDeclaredLanguageMapsBySlug(t *testing.T) {
	src := memSource{
		"docs/en/install.md": "---\nlang: en\n---\n# Install\n",
		"docs/en/usage.md":   "---\nlang: en\n---\n# Usage\n",
		"docs/fr/install.md": "---\nlang: fr\n---\n# Installation\n",
		"docs/en/faq.md":     "---\nlang: en\n---\n# FAQ\n",
	}
	m, err := verbatim.Map(src, verbatim.MapOptions{Files: []verbatim.FileSpec{{Include: "docs/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pages) != 3 || len(m.Warnings) != 0 {
		t.Fatalf("pages = %+v warnings = %v", m.Pages, m.Warnings)
	}
	install := translationsOf(t, m, "docs/en/install.md")
	if fr := install["fr"]; fr.Path != "docs/fr/install.md" || fr.Title != "Installation" {
		t.Fatalf("install translations = %+v", install)
	}
	for _, p := range m.Pages {
		if p.Slug == "install-2" {
			t.Fatalf("the translation took a slug: %+v", m.Pages)
		}
	}

	plain := memSource{
		"docs/install.md":     "# Install\n",
		"docs/fr/install.md":  "---\nlang: fr\n---\nInstaller.\n",
		"docs/fr/unpaired.md": "---\nlang: fr\n---\nSeul.\n",
	}
	m, err = verbatim.Map(plain, verbatim.MapOptions{Files: []verbatim.FileSpec{{Include: "docs/**"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pages) != 1 || translationsOf(t, m, "docs/install.md")["fr"].Title != "Install" {
		t.Fatalf("pages = %+v", m.Pages)
	}
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "docs/fr/unpaired.md: skipped (lang fr)") {
		t.Fatalf("warnings = %v", m.Warnings)
	}
}
