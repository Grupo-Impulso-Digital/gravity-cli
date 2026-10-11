package run_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func TestDebugDirIsIgnoredAndPerRunPass(t *testing.T) {
	root := t.TempDir()
	dir := run.DebugDir(root, "prun_1", "ppr_2")
	if dir != filepath.Join(root, ".gravity", "runs", "prun_1", "ppr_2") {
		t.Fatalf("dir = %s", dir)
	}
	if ignore, _ := os.ReadFile(filepath.Join(root, ".gravity", ".gitignore")); string(ignore) != "*\n" {
		t.Fatalf("ignore = %q", ignore)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run.LoadRecording(root, "latest"); err == nil {
		t.Fatal("a debug directory is not a recording")
	}
}
