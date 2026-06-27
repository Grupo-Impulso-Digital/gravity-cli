package agent

import (
	"path/filepath"
	"testing"
)

func TestSandboxPath(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		want    string
	}{
		{"simple", "src/main.go", false, filepath.FromSlash("src/main.go")},
		{"dot prefix", "./README.md", false, "README.md"},
		{"nested clean", "a/b/../c.txt", false, filepath.FromSlash("a/c.txt")},
		{"empty", "", true, ""},
		{"absolute", "/etc/passwd", true, ""},
		{"parent escape", "../secret", true, ""},
		{"deep escape", "a/../../etc/passwd", true, ""},
		{"bare dotdot", "..", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sandboxPath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("sandboxPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if got := truncate(short, 100, "x"); got != short {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	got := truncate(string(long), 50, "diff")
	if len(got) <= 50 {
		t.Errorf("truncated output should include the notice, len=%d", len(got))
	}
	if got[:50] != string(long[:50]) {
		t.Error("first 50 bytes should be preserved")
	}
}
