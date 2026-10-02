package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"api/**", "api/openapi.yaml", true},
		{"api/**", "api/v1/x/openapi.yaml", true},
		{"api/**", "apix/openapi.yaml", false},
		{"**/*.test.ts", "a.test.ts", true},
		{"**/*.test.ts", "src/deep/a.test.ts", true},
		{"**/*.test.ts", "src/deep/a.ts", false},
		{"scripts/**", "scripts", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/x/a.go", false},
		{"docs/**/*.md", "docs/a.md", true},
		{"docs/**/*.md", "docs/x/y/a.md", true},
		{"*.{yaml,yml}", "openapi.yml", true},
		{"*.{yaml,yml}", "openapi.json", false},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},
		{"./api/**", "api/a", true},
		{"src/[ab].go", "src/b.go", true},
		{"**", "anything/at/all", true},
		{"a/**/b/**/c", "a/x/b/y/z/c", true},
	}
	for _, tc := range cases {
		if got := Match(tc.pattern, tc.name); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestRoot(t *testing.T) {
	cases := map[string]string{
		"docs/handbook/**/*.md": "docs/handbook",
		"docs/*.md":             "docs",
		"README.md":             "",
		"docs/a.md":             "docs",
		"**/*.md":               "",
	}
	for in, want := range cases {
		if got := Root(in); got != want {
			t.Errorf("Root(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchAnyAndHasMeta(t *testing.T) {
	if !MatchAny([]string{"x/**", "api/**"}, "api/a") || MatchAny(nil, "a") {
		t.Fatal("MatchAny")
	}
	if HasMeta("docs/a.md") || !HasMeta("docs/*.md") {
		t.Fatal("HasMeta")
	}
}
