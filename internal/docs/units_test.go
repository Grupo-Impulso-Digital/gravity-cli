package docs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

func TestUnitSourceHash(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n")
	write("b.go", "package b\n")

	h := docs.UnitSourceHash(root, []string{"a.go", "b.go"})
	if h == "" {
		t.Fatal("hash is empty for resolvable refs")
	}
	if got := docs.UnitSourceHash(root, []string{"b.go", "a.go"}); got != h {
		t.Errorf("hash is order-dependent: %s != %s", got, h)
	}
	if got := docs.UnitSourceHash(root, []string{"a.go", "b.go", "a.go"}); got != h {
		t.Errorf("duplicate ref changed the hash: %s != %s", got, h)
	}
	write("b.go", "package b // changed\n")
	if got := docs.UnitSourceHash(root, []string{"a.go", "b.go"}); got == h {
		t.Error("hash did not change when a bound source changed")
	}
}

func TestUnitSourceHashUnresolvable(t *testing.T) {
	root := t.TempDir()
	if got := docs.UnitSourceHash(root, nil); got != "" {
		t.Errorf("no refs = %q, want empty", got)
	}
	if got := docs.UnitSourceHash(root, []string{"missing.go", "/etc/passwd", "../escape.go", " "}); got != "" {
		t.Errorf("unresolvable refs = %q, want empty", got)
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if docs.UnitSourceHash(root, []string{"missing.go", "a.go"}) != docs.UnitSourceHash(root, []string{"a.go"}) {
		t.Error("an unresolvable ref must be skipped, not poison the digest")
	}
}
