package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRel(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"simple", "docs/api.md", "docs/api.md", nil},
		{"dot-segments cleaned", "docs/../docs/api.md", "docs/api.md", nil},
		{"empty allowed", "", ".", nil},
		{"absolute", "/etc/passwd", "", ErrAbsolute},
		{"parent escape", "../secret", "", ErrEscape},
		{"bare parent", "..", "", ErrEscape},
		{"deep escape", "a/../../b", "", ErrEscape},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Rel(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Rel(%q) err = %v, want %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("Rel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	root := filepath.Join("home", "repo")
	got, err := Resolve(root, "docs/api.md")
	if err != nil {
		t.Fatalf("Resolve: unexpected err %v", err)
	}
	if want := filepath.Join(root, "docs", "api.md"); got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
	if _, err := Resolve(root, "../escape"); !errors.Is(err, ErrEscape) {
		t.Fatalf("Resolve escape err = %v, want ErrEscape", err)
	}
}

func TestResolveInRootRefusesSymlinksOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "in.txt"), []byte("i"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("in.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"leak.txt", "dir/secret.txt"} {
		if _, err := ResolveInRoot(root, p); !errors.Is(err, ErrEscape) {
			t.Errorf("ResolveInRoot(%q) err = %v, want ErrEscape", p, err)
		}
	}
	got, err := ResolveInRoot(root, "alias.txt")
	if err != nil || filepath.Base(got) != "in.txt" {
		t.Errorf("an in-root symlink resolves: %q, %v", got, err)
	}
	if got, err := ResolveInRoot(root, "missing.txt"); err != nil || got != filepath.Join(root, "missing.txt") {
		t.Errorf("a missing path resolves to the joined path: %q, %v", got, err)
	}
}
