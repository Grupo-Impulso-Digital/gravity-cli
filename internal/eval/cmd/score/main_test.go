package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/eval"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/passes"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/run"
)

func TestRun(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC) }
	withRecording := t.TempDir()
	rec := &run.Recording{Version: run.RecordingVersion, RunID: "prun_1", Passes: []run.RecordedPass{{
		Name: "topics-guide", Kind: "guides",
		Recorded: passes.Recorded{Changes: []api.ChangeRequest{{Op: api.OpCreate, Title: "Topics API", Blocks: []api.ChangeBlock{
			{Key: "guide:topics-api:1", Type: "heading", Content: map[string]any{"text": "GET /topics", "level": 2}},
		}}}},
		Usage: &run.RecordedUsage{Turns: 4, InputTokens: 100, OutputTokens: 50},
	}}}
	if _, err := run.SaveRecording(withRecording, rec); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		args     []string
		code     int
		stem     string
		status   string
		covered  int
		reason   string
		noOutput bool
	}{
		{name: "scores the newest recording", args: []string{"-repo", withRecording, "-model", "m-1", "-duration", "12.5"}, code: 0, stem: "m-1-20261011T090000Z", status: eval.StatusScored, covered: 1},
		{name: "explicit recording", args: []string{"-recording", run.RecordingPath(withRecording, "prun_1"), "-name", "fixed", "-max-output", "4096"}, code: 0, stem: "fixed", status: eval.StatusScored, covered: 1},
		{name: "no recording fails", args: []string{"-repo", t.TempDir(), "-model", "m-2", "-run-exit", "2"}, code: 1, stem: "m-2-20261011T090000Z", status: eval.StatusFailed, reason: "gravity run exited 2 and recorded no changes"},
		{name: "no recording, clean exit", args: []string{"-repo", t.TempDir(), "-name", "clean"}, code: 1, stem: "clean", status: eval.StatusFailed, reason: "the dry run recorded no changes"},
		{name: "needs a source", args: []string{}, code: 2, noOutput: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := t.TempDir()
			if code := score(append(tt.args, "-out", out), now); code != tt.code {
				t.Fatalf("exit %d, want %d", code, tt.code)
			}
			if tt.noOutput {
				return
			}
			data, err := os.ReadFile(filepath.Join(out, tt.stem+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(out, tt.stem+".md")); err != nil {
				t.Fatal(err)
			}
			var r eval.Report
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			if r.Status != tt.status || r.Coverage.Covered != tt.covered || r.Reason != tt.reason {
				t.Errorf("report: status %s, covered %d, reason %q", r.Status, r.Coverage.Covered, r.Reason)
			}
			if tt.status == eval.StatusScored && (r.Turns != 4 || r.TotalTokens != 150) {
				t.Errorf("usage: %d turns, %d tokens", r.Turns, r.TotalTokens)
			}
		})
	}
}
