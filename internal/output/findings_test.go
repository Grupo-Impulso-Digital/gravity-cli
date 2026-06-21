package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/impulso/gravity-cli/internal/output"
)

func sampleResult() output.Result {
	return output.Result{
		Command: "check api",
		Site:    "docs",
		Findings: []output.Finding{
			{Severity: output.SeverityWarning, Kind: "orphaned", Title: "orphaned: DELETE /users", Detail: "documented but not in spec", Location: "DELETE /users"},
			{Severity: output.SeverityError, Kind: "undocumented", Title: "undocumented: POST /users", Detail: "in spec but not documented", Location: "POST /users", SuggestedPage: "users"},
		},
		Skipped: 2,
	}
}

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer
	if err := output.Render(&buf, sampleResult(), output.FormatText); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 finding(s)") {
		t.Errorf("text output missing count:\n%s", out)
	}
	// Errors must sort before warnings.
	errIdx := strings.Index(out, "[ERROR]")
	warnIdx := strings.Index(out, "[WARNING]")
	if errIdx == -1 || warnIdx == -1 || errIdx > warnIdx {
		t.Errorf("expected ERROR before WARNING:\n%s", out)
	}
	if !strings.Contains(out, "skipped 2 block") {
		t.Errorf("skip note missing:\n%s", out)
	}
}

func TestRenderTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := output.Result{Command: "check docs", Site: "docs"}
	if err := output.Render(&buf, r, output.FormatText); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "no findings") {
		t.Errorf("expected 'no findings':\n%s", buf.String())
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := output.Render(&buf, sampleResult(), output.FormatJSON); err != nil {
		t.Fatalf("render: %v", err)
	}
	var decoded output.Result
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Command != "check api" || len(decoded.Findings) != 2 {
		t.Errorf("decoded result wrong: %+v", decoded)
	}
	if decoded.Skipped != 2 {
		t.Errorf("skipped not serialised: %d", decoded.Skipped)
	}
}

func TestRenderJSONEmptyFindingsArray(t *testing.T) {
	var buf bytes.Buffer
	r := output.Result{Command: "check docs", Site: "docs"}
	if err := output.Render(&buf, r, output.FormatJSON); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), `"findings": []`) {
		t.Errorf("expected empty findings array, got:\n%s", buf.String())
	}
}

func TestRenderGitHub(t *testing.T) {
	var buf bytes.Buffer
	if err := output.Render(&buf, sampleResult(), output.FormatGitHub); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "::error ") {
		t.Errorf("expected ::error annotation:\n%s", out)
	}
	if !strings.Contains(out, "::warning ") {
		t.Errorf("expected ::warning annotation:\n%s", out)
	}
	if !strings.Contains(out, "title=") {
		t.Errorf("expected title property:\n%s", out)
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]string{
		"high":     output.SeverityError,
		"critical": output.SeverityError,
		"medium":   output.SeverityWarning,
		"low":      output.SeverityInfo,
		"":         output.SeverityInfo,
		"info":     output.SeverityInfo,
		"warning":  output.SeverityWarning,
	}
	for in, want := range cases {
		if got := output.NormalizeSeverity(in); got != want {
			t.Errorf("NormalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := output.Render(&buf, sampleResult(), "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
