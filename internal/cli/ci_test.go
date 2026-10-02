package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func allCommands(c *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{c}
	for _, sub := range c.Commands() {
		out = append(out, allCommands(sub)...)
	}
	return out
}

func TestToPlainStripsEmojiAndBoxDrawing(t *testing.T) {
	got := toPlain("agent: 💭 thinking └─ connect ← this repo … ✏️ done ＋ new")
	for _, bad := range []string{"💭", "└", "←", "…", "✏", "＋", "️"} {
		if strings.Contains(got, bad) {
			t.Errorf("toPlain left %q in %q", bad, got)
		}
	}
	for _, want := range []string{"`- connect", "<- this repo", "...", "+ new"} {
		if !strings.Contains(got, want) {
			t.Errorf("toPlain(%q) missing %q", got, want)
		}
	}
	if keep := toPlain("Site:    not set — pass --site"); keep != "Site:    not set — pass --site" {
		t.Errorf("plain punctuation must survive: %q", keep)
	}
}

func TestCIModeFromFlagOrEnv(t *testing.T) {
	t.Setenv("CI", "")
	if ciMode(globalFlags{}) {
		t.Error("no flag, no env: not CI")
	}
	if !ciMode(globalFlags{ci: true}) {
		t.Error("--ci must enable CI mode")
	}
	t.Setenv("CI", "true")
	if !ciMode(globalFlags{}) {
		t.Error("CI=true must enable CI mode")
	}
}

func TestEveryCommandAcceptsCI(t *testing.T) {
	root := NewRootCommand()
	for _, c := range allCommands(root) {
		if c.Flags().Lookup("ci") == nil && c.InheritedFlags().Lookup("ci") == nil && c != root {
			t.Errorf("%s does not accept --ci", c.CommandPath())
		}
		if f := c.LocalNonPersistentFlags().Lookup("ci"); f != nil {
			t.Errorf("%s redeclares --ci locally, shadowing the global flag", c.CommandPath())
		}
	}
}

func TestCIFlagMakesOutputPlain(t *testing.T) {
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site": map[string]any{"slug": "acme", "name": "Acme"},
				"spaces": []any{
					map[string]any{"id": "s1", "slug": "platform", "name": "Platform"},
					map[string]any{"id": "s2", "slug": "connect", "name": "Connect", "parentSpaceId": "s1"},
				},
			})
		},
	}}
	srv := fp.serve(t)
	chdirTemp(t, t.TempDir())

	plain, _, err := runRoot(t, "spaces", "--ci", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(plain, "└─←") {
		t.Errorf("--ci output still carries box drawing:\n%s", plain)
	}
	fancy, _, err := runRoot(t, "spaces", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fancy, "└─") {
		t.Errorf("interactive output keeps its tree glyphs:\n%s", fancy)
	}
	var buf bytes.Buffer
	if _, err := plainWriter(&buf).Write([]byte("x 💭")); err != nil || buf.String() != "x " {
		t.Errorf("plainWriter = %q, %v", buf.String(), err)
	}
}

func TestCIModeFinalErrorLineIsPlain(t *testing.T) {
	chdirTemp(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CI", "")
	root := NewRootCommand()
	var errOut bytes.Buffer
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"release-notes", "--ci", "--api-url", "http://127.0.0.1:1", "--token", "sk_live_x"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error outside a repository")
	}
	if _, ok := root.ErrOrStderr().(plainOut); !ok {
		t.Fatalf("--ci must route stderr through the plain writer, got %T", root.ErrOrStderr())
	}
	PrintError(root.ErrOrStderr(), Failf(CodeError, "%s 💭 done", err))
	if got := errOut.String(); got != "gravity: not a git repository — run inside your repo  done\n" {
		t.Errorf("--ci final error line = %q", got)
	}
}

func TestCIModeLeavesJSONDataUntouched(t *testing.T) {
	fp := &fakePlatform{routes: map[string]http.HandlerFunc{
		"GET /api/v1/sites/acme": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site":   map[string]any{"slug": "acme", "name": "Acme"},
				"spaces": []any{map[string]any{"id": "s1", "slug": "platform", "name": "Platform → APIs ✓ 🚀…"}},
			})
		},
	}}
	srv := fp.serve(t)
	chdirTemp(t, t.TempDir())

	stdout, _, err := runRoot(t, "spaces", "--ci", "--json", "--api-url", srv.URL, "--token", "sk_live_x", "--site", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Platform → APIs ✓ 🚀…") {
		t.Errorf("--ci must not rewrite data inside --json output:\n%s", stdout)
	}

	var buf bytes.Buffer
	if rawWriter(plainWriter(&buf)) != &buf {
		t.Error("rawWriter must unwrap the plain writer")
	}
}
