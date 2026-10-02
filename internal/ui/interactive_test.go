package ui

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestAccessiblePrompts(t *testing.T) {
	var out bytes.Buffer
	p := &HuhPrompter{In: strings.NewReader("2\n1\n3\n0\n\n"), Out: &out, Accessible: true}
	choices := []Choice{{Key: "a", Label: "Alpha"}, {Key: "b", Label: "Beta"}, {Key: "c", Label: "Gamma"}}
	got, err := p.Select("Pick", "", choices, "a")
	if err != nil || got != "b" {
		t.Fatalf("select = %q %v", got, err)
	}
	multi, err := p.MultiSelect("Many", "", choices, []string{"a", "b"})
	if err != nil || !reflect.DeepEqual(multi, []string{"b", "c"}) {
		t.Fatalf("multi = %v %v", multi, err)
	}
	def, err := p.Select("Default", "", choices, "c")
	if err != nil || def != "c" {
		t.Fatalf("default = %q %v", def, err)
	}
	if !strings.Contains(out.String(), "1. Alpha") {
		t.Fatalf("out = %s", out.String())
	}
}

func TestProgressPlainPrintsFinishedSteps(t *testing.T) {
	var stdout, stderr bytes.Buffer
	p := New(&stdout, &stderr, Options{})
	g := p.StartProgress("Setting up", false)
	a := g.Add("Connect")
	b := g.Add("Register pass docs → docs/guides")
	c := g.Add("Mint")
	g.Begin(a)
	g.Done(a, "https://app")
	g.Fail(b, "forbidden")
	g.Skip(c, "no permission")
	g.Stop()
	want := "Setting up\n  ok Connect  https://app\n  x Register pass docs -> docs/guides  forbidden\n  - Mint  no permission\n"
	if stdout.String() != want {
		t.Fatalf("plain progress =\n%q\nwant\n%q", stdout.String(), want)
	}
}

func TestProgressTerminalRestoresOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	p := New(&stdout, &stderr, Options{Terminal: true, NoColor: true})
	g := p.StartProgress("", true)
	i := g.Add("Pass docs")
	g.Begin(i)
	p.Println("a log line while running")
	g.Done(i, "")
	g.Stop()
	g.Stop()
	p.Println("after")
	out := stdout.String()
	if !strings.Contains(out, "a log line while running") || !strings.HasSuffix(out, "after\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestCard(t *testing.T) {
	var stdout bytes.Buffer
	p := New(&stdout, &bytes.Buffer{}, Options{})
	p.Card("Connected billing-api", []string{"Product: Acme"}, []Link{{Label: "Repository", URL: "https://app/r"}, {Label: "Approve docs", URL: "https://app/a"}, {Label: "Empty"}})
	want := "Connected billing-api\n  Product: Acme\n  Repository    https://app/r\n  Approve docs  https://app/a\n"
	if stdout.String() != want {
		t.Fatalf("card = %q", stdout.String())
	}
	var tty bytes.Buffer
	New(&tty, &bytes.Buffer{}, Options{Terminal: true, NoColor: true}).Card("Title", []string{"line"}, nil)
	if !strings.Contains(tty.String(), "╭") || !strings.Contains(tty.String(), "Title") {
		t.Fatalf("tty card = %q", tty.String())
	}
}
