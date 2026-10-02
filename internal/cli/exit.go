package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// CodeOK, CodeFindings, CodeError and CodeLicense are the exit codes used
// across the CLI. CodeLicense is distinct from CodeError so a pipeline can tell
// "the workspace's license doesn't include this" (ask an administrator) apart
// from a broken token, network or input.
const (
	CodeOK       = 0
	CodeFindings = 1
	CodeError    = 2
	CodeLicense  = 3
)

// ExitError carries an explicit process exit code alongside an error.
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

// CodeFor extracts the intended exit code from an error chain. A license
// refusal anywhere in the chain wins over the generic CodeError a command
// wrapped it in, so every command reports it the same way without per-command
// plumbing.
func CodeFor(err error) int {
	if err == nil {
		return CodeOK
	}
	if api.IsLicenseError(err) {
		return CodeLicense
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return CodeError
}

// PrintError writes an error to w unless it is a silent findings exit.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.Err == nil {
		return
	}
	fmt.Fprintln(w, "gravity: "+err.Error())
}
