package git

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ErrNotRepository is matched by errors.Is when a command ran outside a git work tree.
var ErrNotRepository = errors.New("not a git repository — run inside your repo")

// ErrUnknownRef is matched by errors.Is when a ref or range endpoint does not resolve.
var ErrUnknownRef = errors.New("unknown git ref")

// Error is a failed git invocation with a human-readable message.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	return humanize(e)
}

// Unwrap exposes the underlying exec error.
func (e *Error) Unwrap() error { return e.Err }

// Is matches the sentinel errors this failure represents.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotRepository:
		return e.kind() == kindNotRepo
	case ErrUnknownRef:
		return e.kind() == kindUnknownRef
	}
	return false
}

const (
	kindOther = iota
	kindNoGit
	kindNotRepo
	kindNoCommits
	kindUnknownRef
	kindDubious
)

func (e *Error) kind() int {
	if errors.Is(e.Err, exec.ErrNotFound) {
		return kindNoGit
	}
	lower := strings.ToLower(e.Stderr)
	switch {
	case strings.Contains(lower, "not a git repository"):
		return kindNotRepo
	case strings.Contains(lower, "dubious ownership"):
		return kindDubious
	case strings.Contains(lower, "does not have any commits yet"),
		strings.Contains(lower, "ambiguous argument 'head'"):
		return kindNoCommits
	case strings.Contains(lower, "unknown revision"),
		strings.Contains(lower, "bad revision"),
		strings.Contains(lower, "not a valid object name"),
		strings.Contains(lower, "invalid object name"),
		strings.Contains(lower, "needed a single revision"),
		strings.Contains(lower, "bad object"):
		return kindUnknownRef
	}
	return kindOther
}

var quotedRE = regexp.MustCompile(`'([^']+)'`)

func humanize(e *Error) string {
	switch e.kind() {
	case kindNoGit:
		return "git is not installed or not on PATH — install git and retry"
	case kindNotRepo:
		return ErrNotRepository.Error()
	case kindDubious:
		return "git refuses this checkout (dubious ownership) — run `git config --global --add safe.directory '*'` in CI"
	case kindNoCommits:
		return "this repository has no commits yet — commit something first"
	case kindUnknownRef:
		ref := ""
		if m := quotedRE.FindStringSubmatch(e.Stderr); m != nil {
			ref = m[1]
		}
		if ref == "" {
			return "unknown git ref — check the ref name, or fetch full history (fetch-depth: 0 / GIT_DEPTH: 0) and tags"
		}
		return fmt.Sprintf("unknown git ref %q — check the ref name, or fetch full history (fetch-depth: 0 / GIT_DEPTH: 0) and tags", ref)
	}
	sub := "git"
	if len(e.Args) > 0 {
		sub = "git " + e.Args[0]
	}
	msg := firstLine(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return sub + ": " + msg
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "fatal: ")
		line = strings.TrimPrefix(line, "error: ")
		if line != "" {
			return line
		}
	}
	return ""
}

func stderrHas(err error, subs ...string) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	for _, s := range subs {
		if strings.Contains(ge.Stderr, s) {
			return true
		}
	}
	return false
}
