package checks_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/impulso/gravity-cli/internal/api"
	"github.com/impulso/gravity-cli/internal/checks"
)

func writeAndHash(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestVerifyBinding_Fresh(t *testing.T) {
	dir := t.TempDir()
	h := writeAndHash(t, dir, "openapi.yaml", "spec content\n")
	check := checks.VerifyBinding(dir, &api.SourceBinding{
		Kind: "file", Ref: "openapi.yaml", Hash: h, Generator: "x",
	})
	if !check.Verified {
		t.Fatal("expected verified")
	}
	if check.Stale {
		t.Errorf("expected fresh, got stale (want %s got %s)", check.Want, check.Got)
	}
}

func TestVerifyBinding_Stale(t *testing.T) {
	dir := t.TempDir()
	writeAndHash(t, dir, "openapi.yaml", "new content\n")
	check := checks.VerifyBinding(dir, &api.SourceBinding{
		Kind: "file", Ref: "openapi.yaml", Hash: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if !check.Verified {
		t.Fatal("expected verified")
	}
	if !check.Stale {
		t.Error("expected stale when recorded hash differs")
	}
}

func TestVerifyBinding_PrefixedHash(t *testing.T) {
	dir := t.TempDir()
	h := writeAndHash(t, dir, "f.txt", "abc\n")
	check := checks.VerifyBinding(dir, &api.SourceBinding{Ref: "f.txt", Hash: "sha256:" + h})
	if check.Stale {
		t.Errorf("sha256: prefix should be tolerated; got stale")
	}
}

func TestVerifyBinding_Skips(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		b    *api.SourceBinding
	}{
		{"nil binding", nil},
		{"no hash", &api.SourceBinding{Ref: "x.txt"}},
		{"no ref", &api.SourceBinding{Hash: "abc"}},
		{"missing file", &api.SourceBinding{Ref: "does-not-exist.txt", Hash: "abc"}},
		{"escape", &api.SourceBinding{Ref: "../outside.txt", Hash: "abc"}},
		{"absolute", &api.SourceBinding{Ref: "/etc/passwd", Hash: "abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := checks.VerifyBinding(dir, tc.b)
			if !check.Skipped {
				t.Errorf("expected skip, got %+v", check)
			}
			if check.SkipReason == "" {
				t.Error("skip should carry a reason")
			}
		})
	}
}
