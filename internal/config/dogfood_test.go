package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func TestThisRepositorysManifestIsVersion2(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	m, err := config.Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", config.ManifestFileName, err)
	}
	if m.Version != 2 || len(m.Passes) == 0 {
		t.Fatalf("manifest = %+v", m)
	}
}
