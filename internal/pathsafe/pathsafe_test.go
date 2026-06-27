package pathsafe

import (
	"errors"
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
