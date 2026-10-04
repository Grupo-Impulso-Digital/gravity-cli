package cisetup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedTemplatesMirrorInit(t *testing.T) {
	published := []struct {
		path, provider, file string
	}{
		{"ci/github/gravity.yml", GitHub, ".github/workflows/gravity.yml"},
		{"ci/gitlab/gravity.yml", GitLab, ".gitlab/gravity.yml"},
		{"ci/bitbucket/bitbucket-pipelines.yml", Bitbucket, "bitbucket-pipelines.yml"},
		{"ci/azure/azure-pipelines.gravity.yml", Azure, "azure-pipelines.gravity.yml"},
	}
	for _, p := range published {
		t.Run(p.provider, func(t *testing.T) {
			plan, err := Build(t.TempDir(), p.provider, Options{DefaultBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			for _, f := range plan.Files {
				if f.Path == p.file {
					want = f.Content
				}
			}
			if want == "" {
				t.Fatalf("init writes no %s for %s", p.file, p.provider)
			}
			path := filepath.Join("..", "..", filepath.FromSlash(p.path))
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (run go test ./internal/cisetup -update): %v", p.path, err)
			}
			if string(got) != want {
				t.Fatalf("%s drifted from what gravity ci setup writes:\n%s", p.path, got)
			}
		})
	}
}
