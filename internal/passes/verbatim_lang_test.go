package passes_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
)

func TestVerbatimUploadsTranslationsAfterEverySourcePage(t *testing.T) {
	r := newRepo(t)
	base := r.commit("docs", map[string]string{
		"docs/handbook/guide.md":    "---\ntitle: Guide\n---\nRead [setup](setup.md).\n",
		"docs/handbook/guide.fr.md": "---\ntitle: Le guide\n---\nLisez [installation](setup.fr.md).\n",
		"docs/handbook/guide.de.md": "# Anleitung\n\nLesen.\n",
		"docs/handbook/setup.md":    "# Setup\n\nInstall it.\n",
		"docs/handbook/setup.es.md": "# Instalación\n",
		"docs/handbook/setup.it.md": "# Installazione\n",
	})
	head := r.commit("translate", map[string]string{
		"docs/handbook/setup.es.md":    "",
		"docs/handbook/setup.it.md":    "",
		"docs/handbook/fr/setup.md":    "---\nlang: fr\n---\n# Installation\n\n![schéma](schema.png)\n",
		"docs/handbook/fr/schema.png":  "PNG",
		"docs/handbook/legacy.pt.md":   "---\nlang: pt\n---\n# Legado\n",
		"docs/handbook/guide.fr.md":    "---\ntitle: Le guide\n---\nLisez [installation](fr/setup.md).\n",
		"docs/handbook/notes.ab.md":    "# Not a translation without notes.md\n\nNotes.\n",
		"docs/handbook/unrelated.txt":  "x",
		"docs/handbook/guide.pt-BR.md": "---\ndraft: true\n---\n# Rascunho\n",
	})
	fake := newFakeAPI()
	w := &writes{fail: map[string]error{
		"docs/handbook/guide.de.md": &api.APIError{StatusCode: http.StatusUnprocessableEntity, Code: api.CodeLanguageNotEnabled, Message: "the site has no de translation"},
		"docs/handbook/setup.it.md": &api.APIError{StatusCode: http.StatusNotFound, Code: api.CodeNotFound, Message: "never imported"},
	}}
	rep, err := passes.Verbatim{}.Run(context.Background(), input(t, r, fake, verbatimPass(), manifest(), base, head, api.ModeWrite), sink(w))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	seenTranslation := false
	for _, req := range w.verbatim {
		order = append(order, req.File.Path+"@"+req.Language)
		if req.Language != "" {
			seenTranslation = true
		} else if seenTranslation {
			t.Fatalf("a source page was uploaded after a translation: %v", order)
		}
	}
	if got := strings.Join(order, " "); got != "docs/handbook/guide.md@ docs/handbook/notes.ab.md@ docs/handbook/setup.md@ docs/handbook/guide.de.md@de docs/handbook/guide.fr.md@fr docs/handbook/fr/setup.md@fr" {
		t.Fatalf("upload order = %s", got)
	}
	fr := w.verbatim[4]
	if fr.Page.Slug != "guide" || fr.Page.Title != "Le guide" || fr.Languages != nil || !strings.HasPrefix(fr.File.Hash, "sha256:") || fr.File.CommitSHA != head || !strings.HasSuffix(fr.File.URL, "/blob/main/docs/handbook/guide.fr.md") {
		t.Fatalf("fr translation = %+v", fr)
	}
	if text := fr.Blocks[0].Content.(map[string]any)["text"].(string); !strings.Contains(text, "[installation](../api/setup)") {
		t.Fatalf("a link to a translation points at its page: %q", text)
	}
	setupFR := w.verbatim[5]
	if setupFR.Page.Slug != "setup" || setupFR.Page.Title != "Installation" {
		t.Fatalf("declared translation = %+v", setupFR)
	}
	if len(w.assets) != 1 || w.assets[0] != "docs/handbook/fr/schema.png" || w.assetPass[0] != "ppr_1" {
		t.Fatalf("translation images upload from the translation's folder: %v", w.assets)
	}
	var deleted []string
	for _, d := range w.deletions {
		deleted = append(deleted, d.Path)
	}
	if got := strings.Join(deleted, " "); got != "docs/handbook/guide.pt-BR.md docs/handbook/setup.es.md docs/handbook/setup.it.md" {
		t.Fatalf("translation deletions = %s", got)
	}
	if !strings.Contains(w.deletions[1].Reason, "Translation file deleted") || !strings.Contains(w.deletions[0].Reason, "draft or hidden") {
		t.Fatalf("reasons = %+v", w.deletions)
	}
	warnings := strings.Join(rep.Warnings, "\n")
	for _, want := range []string{"docs/handbook/guide.de.md: the site lacks language de; translation skipped", "docs/handbook/legacy.pt.md: skipped (lang pt)"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q in\n%s", want, warnings)
		}
	}
	if rep.Counts.Imported != 5 || rep.Counts.Deleted != 2 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
}

func TestVerbatimSkipsTranslationsOfAFailedSourceAndRefusedTranslations(t *testing.T) {
	r := newRepo(t)
	head := r.commit("docs", map[string]string{
		"docs/handbook/a.md":    "# A\n\nText.\n",
		"docs/handbook/a.fr.md": "# A fr\n",
		"docs/handbook/b.md":    "# B\n\nText.\n",
		"docs/handbook/b.fr.md": "# B fr\n",
	})
	fake := newFakeAPI()
	w := &writes{fail: map[string]error{
		"docs/handbook/a.md":    &api.APIError{StatusCode: http.StatusConflict, Code: api.CodeLockedByOther, Message: "locked by another pass"},
		"docs/handbook/b.fr.md": &api.APIError{StatusCode: http.StatusConflict, Code: api.CodeSourcePending, Message: "adoption pending"},
	}}
	rep, err := passes.Verbatim{}.Run(context.Background(), input(t, r, fake, verbatimPass(), manifest(), "", head, api.ModeWrite), sink(w))
	if err == nil || !strings.Contains(err.Error(), "docs/handbook/a.md") {
		t.Fatalf("err = %v", err)
	}
	for _, req := range w.verbatim {
		if req.File.Path == "docs/handbook/a.fr.md" {
			t.Fatal("a translation of a page that failed to import is not sent")
		}
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "docs/handbook/b.fr.md: fr translation of b skipped") {
		t.Fatalf("warnings = %v", rep.Warnings)
	}
}

func TestVerbatimDryRunRecordsTranslations(t *testing.T) {
	r := newRepo(t)
	head := r.commit("docs", map[string]string{
		"docs/handbook/a.md":    "# A\n\nText.\n",
		"docs/handbook/a.fr.md": "# A en français\n",
	})
	rec := &passes.Recorder{}
	if _, err := (passes.Verbatim{}).Run(context.Background(), input(t, r, newFakeAPI(), verbatimPass(), manifest(), "", head, api.ModeDry), rec); err != nil {
		t.Fatal(err)
	}
	got := rec.Recorded().Verbatim
	if len(got) != 2 || got[1].Language != "fr" || got[1].Page.Slug != "a" || got[1].Page.Title != "A en français" {
		t.Fatalf("recorded = %+v", got)
	}
}
