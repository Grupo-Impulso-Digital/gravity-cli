package cli

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

// Exit codes: success, findings, operational error, license refusal.
const (
	CodeOK       = 0
	CodeFindings = 1
	CodeError    = 2
	CodeLicense  = 3
)

// ExitError carries an explicit process exit code alongside an error.
type ExitError struct {
	Code    int
	Err     error
	ErrCode string
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

// CodeFor extracts the exit code from an error chain; a license refusal anywhere wins.
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

func errorCode(err error) string {
	var ee *ExitError
	if errors.As(err, &ee) && ee.ErrCode != "" {
		return ee.ErrCode
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return "network_error"
	}
	var me *config.ManifestError
	if errors.As(err, &me) {
		return api.CodeManifestInvalid
	}
	if errors.Is(err, config.ErrV1Manifest) {
		return "manifest_v1"
	}
	switch CodeFor(err) {
	case CodeFindings:
		return "findings"
	case CodeLicense:
		return api.CodeModuleDisabled
	}
	return "error"
}
