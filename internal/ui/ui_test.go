package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestModes(t *testing.T) {
	var out, errb bytes.Buffer
	cases := []struct {
		opts Options
		mode string
	}{
		{Options{JSON: true, Terminal: true}, ModeJSON},
		{Options{Terminal: true}, ModeTTY},
		{Options{Terminal: true, CI: true}, ModePlain},
		{Options{}, ModePlain},
	}
	for _, tc := range cases {
		if got := New(&out, &errb, tc.opts).Mode(); got != tc.mode {
			t.Errorf("%+v: mode %s, want %s", tc.opts, got, tc.mode)
		}
	}
}

func TestPlainIsASCII(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{})
	p.Println("%s developer-api · Developer Portal › API → done…", p.Mark(MarkOK))
	if out.String() != "ok developer-api - Developer Portal > API -> done...\n" {
		t.Fatalf("plain = %q", out.String())
	}
	for _, r := range out.String() {
		if r > 127 {
			t.Fatalf("non-ascii rune %q", r)
		}
	}
}

func TestTTYKeepsSymbolsAndColor(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{Terminal: true})
	p.Println("%s done", p.Mark(MarkOK))
	if !strings.Contains(out.String(), "✓") || !strings.Contains(out.String(), "\x1b[32m") {
		t.Fatalf("tty = %q", out.String())
	}
	out.Reset()
	p = New(&out, &errb, Options{Terminal: true, NoColor: true})
	p.Println("%s", p.Bold("x"))
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("no-color = %q", out.String())
	}
}

func TestJSONEnvelope(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{JSON: true})
	p.SetCommand("whoami")
	p.Println("human goes to stderr")
	p.Warn("watermark_diverged", "watermark moved")
	if err := p.Result(map[string]string{"a": "<b>"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Result("ignored second document"); err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if !env.OK || env.Command != "whoami" || env.Version == "" || env.Error != nil || len(env.Warnings) != 1 || env.Warnings[0].Code != "watermark_diverged" {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(out.String(), `"<b>"`) {
		t.Fatalf("html must not be escaped: %s", out.String())
	}
	if !strings.Contains(errb.String(), "human goes to stderr") || !strings.Contains(errb.String(), "watermark moved") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestJSONFailureEnvelope(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{JSON: true})
	p.SetCommand("status")
	if err := p.Failure(ErrorInfo{Code: "lease_held", Message: "busy", ExitCode: 2}, nil); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	e := env["error"].(map[string]any)
	if env["ok"] != false || e["code"] != "lease_held" || e["exitCode"].(float64) != 2 {
		t.Fatalf("envelope = %v", env)
	}
	if _, ok := env["warnings"].([]any); !ok {
		t.Fatal("warnings must be an array")
	}
}

func TestQuietAndVerbose(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{Quiet: true})
	p.Println("hidden")
	p.Table("", [][]string{{"a", "b"}})
	p.Always("shown")
	p.Debugf("not verbose")
	if out.String() != "shown\n" || errb.Len() != 0 {
		t.Fatalf("out=%q err=%q", out.String(), errb.String())
	}
	p = New(&out, &errb, Options{Verbose: true})
	p.Debugf("x=%d", 1)
	if errb.String() != "debug: x=1\n" {
		t.Fatalf("err = %q", errb.String())
	}
}

func TestTable(t *testing.T) {
	var out, errb bytes.Buffer
	p := New(&out, &errb, Options{})
	p.Table("  ", [][]string{{"NAME", "KIND"}, {"developer-api", "reference"}})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "  developer-api  reference") {
		t.Fatalf("table = %q", out.String())
	}
}
