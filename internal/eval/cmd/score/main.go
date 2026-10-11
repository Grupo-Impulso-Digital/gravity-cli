// Command score scores a dry run of the model quality eval fixture.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/eval"
)

func main() {
	os.Exit(score(os.Args[1:], time.Now))
}

func score(args []string, now func() time.Time) int {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	repo := fs.String("repo", "", "the fixture repository whose .gravity/runs holds the recording")
	recording := fs.String("recording", "", "the recording file to score (default: the newest under -repo written since -since)")
	since := fs.Int64("since", 0, "unix time the run started; older recordings are ignored")
	duration := fs.Float64("duration", 0, "wall time of the run in seconds")
	model := fs.String("model", "", "the model id the run was asked to use")
	maxOutput := fs.Int("max-output", 0, "the --max-output the run used")
	language := fs.String("language", "en", "the site language")
	detail := fs.Int("detail-words", eval.DefaultDetailWords, "words an endpoint section needs to count as detailed")
	runExit := fs.Int("run-exit", 0, "exit code of gravity run")
	out := fs.String("out", "testdata/eval/results", "where the JSON and Markdown results are written")
	name := fs.String("name", "", "file name stem of the results (default: <model>[-mo<n>]-<UTC time>)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := eval.Options{Model: *model, MaxOutput: *maxOutput, Language: *language, DetailWords: *detail, Duration: time.Duration(*duration * float64(time.Second))}

	path := *recording
	var findErr error
	if path == "" {
		if *repo == "" {
			fmt.Fprintln(os.Stderr, "score: pass -recording or -repo")
			return 2
		}
		path, findErr = eval.FindRecording(*repo, time.Unix(*since, 0))
	}
	var report *eval.Report
	switch {
	case findErr != nil:
		reason := "the dry run recorded no changes"
		if *runExit != 0 {
			reason = fmt.Sprintf("gravity run exited %d and recorded no changes", *runExit)
		}
		if !errors.Is(findErr, eval.ErrNoRecording) {
			reason = findErr.Error()
		}
		report = eval.Failed(opts, reason)
	default:
		opts.Recording = path
		rec, err := eval.LoadRecording(path)
		if err != nil {
			report = eval.Failed(opts, err.Error())
		} else {
			report = eval.Score(rec, opts)
		}
	}
	stem := *name
	if stem == "" {
		stem = eval.ResultName(*model, *maxOutput, now())
	}
	jsonPath, mdPath, err := eval.Write(*out, stem, report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 2
	}
	fmt.Print(eval.Markdown(report))
	fmt.Printf("\nresults: %s, %s\n", jsonPath, mdPath)
	if report.Status == eval.StatusFailed {
		return 1
	}
	return 0
}
