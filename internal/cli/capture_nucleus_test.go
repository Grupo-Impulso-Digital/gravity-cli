package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func TestMemoryTagsCarryNamespaceAndRepo(t *testing.T) {
	proj := &config.Project{Product: config.Product{Repo: "orbit-api"}}
	got := memoryTags("orbit", proj, []string{"billing", "billing", "ns:orbit"})
	want := []string{"ns:orbit", "repo:orbit-api", "billing"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}

	if got := memoryTags("orbit", nil, nil); len(got) != 1 || got[0] != "ns:orbit" {
		t.Errorf("no manifest = %v, want just the namespace tag", got)
	}

	many := make([]string, 64)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if got := memoryTags("orbit", proj, many); len(got) > maxMemoryTags {
		t.Errorf("tags = %d, want at most the platform cap of %d", len(got), maxMemoryTags)
	}
}

func TestRetiredCaptureFlagsAreRejected(t *testing.T) {
	cmd := newCaptureCmd(&globalFlags{})
	for name, replacement := range retiredCaptureFlags {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("--%s is not parsed, so passing it reports an unknown flag instead of its replacement", name)
		} else if !f.Hidden {
			t.Errorf("--%s is retired but still advertised in help", name)
		}
		if replacement == "" {
			t.Errorf("--%s has no replacement named", name)
		}
	}
	for _, live := range []string{"space", "async", "timeout", "poll-interval", "require", "format", "dry-run", "connection", "brief", "brief-file"} {
		if cmd.Flags().Lookup(live) == nil {
			t.Errorf("--%s must survive the retarget", live)
		}
	}
}

func TestResolveBriefIsSandboxed(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("focus on billing"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBrief("", "brief.md")
	if err != nil || got != "focus on billing" {
		t.Fatalf("brief = %q, %v", got, err)
	}
	if _, err := resolveBrief("", "../outside.md"); err == nil {
		t.Error("a brief escaping the repo root must be rejected")
	}
	if _, err := resolveBrief("inline", "brief.md"); err == nil {
		t.Error("--brief and --brief-file together must be rejected")
	}
	if got, err := resolveBrief("inline", ""); err != nil || got != "inline" {
		t.Errorf("inline brief = %q, %v", got, err)
	}
}

func TestDocsGenerateRejectsSinceWithFrom(t *testing.T) {
	cmd := newDocsGenerateCmd(&globalFlags{})
	cmd.SetArgs([]string{"--since", "v1.0.0", "--from", "docs.json"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want the mutual-exclusion error", err)
	}
	if CodeFor(err) != CodeError {
		t.Errorf("exit code = %d, want %d", CodeFor(err), CodeError)
	}
}
