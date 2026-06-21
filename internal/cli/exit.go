package cli

import (
	"errors"
	"fmt"
	"os"
)

// Exit codes used across the CLI. The contract is:
//
//	0 = success / no findings
//	1 = findings were produced (drift, gaps, stale bindings, ...)
//	2 = an operational error (auth, network, bad input, ...)
const (
	CodeOK       = 0
	CodeFindings = 1
	CodeError    = 2
)

// ExitError carries an explicit process exit code alongside an error. Commands
// return it (wrapped) so the root runner can translate it into os.Exit without
// every command reaching for os.Exit directly.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// Fail builds an *ExitError with the given code wrapping err.
func Fail(code int, err error) *ExitError {
	return &ExitError{Code: code, Err: err}
}

// Failf builds an *ExitError with a formatted message.
func Failf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// CodeFor extracts the intended exit code from an error chain, defaulting to
// CodeError for any non-nil error and CodeOK for nil.
func CodeFor(err error) int {
	if err == nil {
		return CodeOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return CodeError
}

// PrintError writes an error to stderr unless it is a silent findings exit.
func PrintError(err error) {
	if err == nil {
		return
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.Err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "gravity: "+err.Error())
}
