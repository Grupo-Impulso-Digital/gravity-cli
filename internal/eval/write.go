package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var unsafeName = regexp.MustCompile(`[^a-z0-9.-]+`)

// ResultName is the file name stem of a result: <model>[-mo<maxOutput>]-<UTC time>.
func ResultName(model string, maxOutput int, at time.Time) string {
	m := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(model), "-"), "-")
	if m == "" {
		m = "configured"
	}
	if maxOutput > 0 {
		m += fmt.Sprintf("-mo%d", maxOutput)
	}
	return m + "-" + at.UTC().Format("20060102T150405Z")
}

// Write saves the report as <dir>/<name>.json and <dir>/<name>.md and returns both paths.
func Write(dir, name string, r *Report) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("encode report: %w", err)
	}
	jsonPath := filepath.Join(dir, name+".json")
	mdPath := filepath.Join(dir, name+".md")
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", jsonPath, err)
	}
	if err := os.WriteFile(mdPath, []byte(Markdown(r)), 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", mdPath, err)
	}
	return jsonPath, mdPath, nil
}
