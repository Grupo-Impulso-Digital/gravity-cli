package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-bump", "minor", "-rc"}, strings.NewReader("v1.1.0\nv1.2.0-rc.1\n\nv1\n"), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got := out.String(); got != "latest=v1.1.0\nversion=v1.2.0-rc.2\n" {
		t.Fatalf("output = %q", got)
	}
	out.Reset()
	if code := run([]string{"-bump", "huge"}, strings.NewReader("v1.1.0\n"), &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatalf("an unknown bump must exit 2 with no output, got %d %q", code, out.String())
	}
}
