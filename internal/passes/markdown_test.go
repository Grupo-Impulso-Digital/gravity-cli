package passes

import "testing"

func TestIsMarkdown(t *testing.T) {
	for path, want := range map[string]bool{"README.md": true, "docs/a.mdx": true, "docs/b.markdown": true, "NOTES.MD": true, "src/a.ts": false, "docs/md": false} {
		if got := isMarkdown(path); got != want {
			t.Errorf("isMarkdown(%q) = %v, want %v", path, got, want)
		}
	}
}
