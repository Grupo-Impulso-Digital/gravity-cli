package docs_test

import (
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/docs"
)

func TestScopeAllows(t *testing.T) {
	s := docs.Scope{
		Include: []string{"src/**", "cmd/**"},
		Exclude: []string{"**/*_test.go", "vendor/**", "src/generated"},
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"src/billing/invoice.ts", true},
		{"cmd/server/main.go", true},
		{"src/billing/invoice_test.go", false},
		{"vendor/x/y.go", false},
		{"src/generated/api.ts", false},
		{"README.md", false},
		{"../escape.go", false},
	} {
		if got := s.Allows(tc.path); got != tc.want {
			t.Errorf("Allows(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestScopeZeroAllowsEverything(t *testing.T) {
	got := docs.Scope{}.Filter([]string{"README.md", "src/a.go", "src/a.go", ""})
	if len(got) != 2 || got[0] != "README.md" || got[1] != "src/a.go" {
		t.Fatalf("Filter = %v, want the deduped, sorted input", got)
	}
}

func TestScopeIsZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    docs.Scope
		want bool
	}{
		{"empty", docs.Scope{}, true},
		{"include only", docs.Scope{Include: []string{"src/**"}}, false},
		{"exclude only", docs.Scope{Exclude: []string{"vendor/**"}}, false},
		{"both", docs.Scope{Include: []string{"src/**"}, Exclude: []string{"vendor/**"}}, false},
	} {
		if got := tc.s.IsZero(); got != tc.want {
			t.Errorf("%s: IsZero() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChangeSetContains(t *testing.T) {
	cs := docs.NewChangeSet("v1.0.0", []string{"src/billing/invoice.ts", "openapi/billing.yaml"})
	if !cs.Contains("src/billing/invoice.ts") {
		t.Error("changed file not reported as changed")
	}
	if !cs.Contains("src/billing") {
		t.Error("directory containing a changed file should count as changed")
	}
	if cs.Contains("src/auth") {
		t.Error("untouched directory reported as changed")
	}
	if cs.ContainsAny([]string{"docs/a.md", "docs/b.md"}) {
		t.Error("untouched refs reported as changed")
	}
	if !cs.ContainsAny([]string{"docs/a.md", "openapi/billing.yaml"}) {
		t.Error("one changed ref among many should count")
	}
	var nilSet *docs.ChangeSet
	if nilSet.Contains("anything") || nilSet.Len() != 0 || nilSet.Digest() != "" {
		t.Error("a nil change set (a full run) must never report a change")
	}
}

func TestChangeSetDigest(t *testing.T) {
	got := docs.NewChangeSet("v1.0.0", []string{"src/a.go", "src/b.go"}).Digest()
	for _, want := range []string{"2 file(s)", "v1.0.0", "- src/a.go", "- src/b.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}

	many := make([]string, 0, docs.MaxChangedPaths+10)
	for i := range docs.MaxChangedPaths + 10 {
		many = append(many, "src/pkg/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go")
	}
	big := docs.NewChangeSet("HEAD~50", many).Digest()
	if !strings.Contains(big, "summarized by directory") || !strings.Contains(big, "src/pkg") {
		t.Errorf("oversized digest not summarized:\n%s", big)
	}
	if strings.Count(big, "\n") > 20 {
		t.Errorf("summarized digest is still huge:\n%s", big)
	}
}
