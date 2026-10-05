package ui

import (
	"bytes"
	"testing"
)

func renderSample(p *Printer) {
	p.Section("Structure", "site polaris")
	p.Tree("  ", TreeNode{Label: "Polaris", Children: []TreeNode{
		{Label: "Docs", Children: []TreeNode{{Label: "Overview"}, {Label: "Admin/", Children: []TreeNode{{Label: "Install"}}}}},
		{Label: "API"},
	}})
	p.Grid("  ", []string{"Pass", "Kind", "AI"}, [][]string{{"docs", "verbatim", "no"}, {"user-guides", "guides", "yes"}})
	p.Note("  ", MarkWarn, "%s", "one warning")
}

func TestRenderersPlain(t *testing.T) {
	var out, errb bytes.Buffer
	renderSample(New(&out, &errb, Options{}))
	want := "\n# Structure  site polaris\n  Polaris\n  |-- Docs\n  |   |-- Overview\n  |   `-- Admin/\n  |       `-- Install\n  `-- API\n  PASS         KIND      AI\n  docs         verbatim  no\n  user-guides  guides    yes\n  ! one warning\n"
	if out.String() != want {
		t.Fatalf("plain =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestRenderersOnATerminal(t *testing.T) {
	var out, errb bytes.Buffer
	renderSample(New(&out, &errb, Options{Terminal: true, NoColor: true}))
	want := "\n▍ Structure  site polaris\n  Polaris\n  ├── Docs\n  │   ├── Overview\n  │   └── Admin/\n  │       └── Install\n  └── API\n" +
		"  ╭─────────────┬──────────┬─────╮\n  │ Pass        │ Kind     │ AI  │\n  ├─────────────┼──────────┼─────┤\n  │ docs        │ verbatim │ no  │\n  │ user-guides │ guides   │ yes │\n  ╰─────────────┴──────────┴─────╯\n  ! one warning\n"
	if out.String() != want {
		t.Fatalf("tty =\n%s\nwant\n%s", out.String(), want)
	}
}
