package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// AzureAnnotations renders findings as Azure Pipelines logging commands.
func AzureAnnotations(findings []api.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		level := "error"
		if f.Severity != api.SeverityError {
			level = "warning"
		}
		props := []string{"type=" + level}
		if f.File != "" {
			props = append(props, "sourcepath="+azureProp(f.File))
			if f.Line > 0 {
				props = append(props, fmt.Sprintf("linenumber=%d", f.Line))
			}
		}
		props = append(props, "code="+azureProp(f.Code))
		out = append(out, fmt.Sprintf("##vso[task.logissue %s;]%s", strings.Join(props, ";"), azureData("Gravity: "+findingMessage(f))))
	}
	return out
}

// AzureSummary is the logging command that attaches a Markdown file to the build summary.
func AzureSummary(path string) string {
	return "##vso[task.uploadsummary]" + azureData(path)
}

func azureData(s string) string {
	return strings.NewReplacer("%", "%AZP25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func azureProp(s string) string {
	return strings.NewReplacer("%", "%AZP25", "\r", "%0D", "\n", "%0A", ";", "%3B", "]", "%5D").Replace(s)
}

func findingMessage(f api.Finding) string {
	msg := f.Title
	if f.Detail != "" && f.Detail != f.Title {
		msg += ": " + f.Detail
	}
	if f.Page != nil {
		msg += " (page " + firstOf(f.Page.Slug, f.Page.ID) + ")"
	}
	return msg
}

type codeQualityIssue struct {
	Description string              `json:"description"`
	CheckName   string              `json:"check_name"`
	Fingerprint string              `json:"fingerprint"`
	Severity    string              `json:"severity"`
	Location    codeQualityLocation `json:"location"`
}

type codeQualityLocation struct {
	Path  string           `json:"path"`
	Lines codeQualityLines `json:"lines"`
}

type codeQualityLines struct {
	Begin int `json:"begin"`
}

// GitLabCodeQuality renders findings as a GitLab Code Quality report; findings without a file point at manifest.
func GitLabCodeQuality(findings []api.Finding, manifest string) ([]byte, error) {
	issues := make([]codeQualityIssue, 0, len(findings))
	for _, f := range findings {
		severity := "major"
		switch f.Severity {
		case api.SeverityWarning:
			severity = "minor"
		case api.SeverityInfo:
			severity = "info"
		}
		path, line := f.File, f.Line
		if path == "" {
			path, line = manifest, 1
		}
		if line <= 0 {
			line = 1
		}
		page := ""
		if f.Page != nil {
			page = firstOf(f.Page.ID, f.Page.Slug)
		}
		sum := sha256.Sum256([]byte(strings.Join([]string{f.Code, f.Title, path, page, f.BlockKey, f.UnitKey}, "\x00")))
		issues = append(issues, codeQualityIssue{
			Description: "Gravity: " + findingMessage(f), CheckName: "gravity/" + f.Code, Fingerprint: hex.EncodeToString(sum[:16]), Severity: severity,
			Location: codeQualityLocation{Path: path, Lines: codeQualityLines{Begin: line}},
		})
	}
	data, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode code quality report: %w", err)
	}
	return append(data, '\n'), nil
}
