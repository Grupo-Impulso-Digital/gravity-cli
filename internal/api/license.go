package api

import (
	"errors"
	"fmt"
	"net/http"
)

// License refusal codes in the platform's REST error envelope. The platform
// answers 403 `{"error":{"code":"module_disabled","module":"<key>",...}}` when
// the workspace's license lacks the module that owns the route (the REST module
// gate), and `seat_limit` when an action would exceed the licensed seats.
const (
	CodeModuleDisabled = "module_disabled"
	CodeSeatLimit      = "seat_limit"
)

// ModuleDisabledError is a refusal because the workspace's license does not
// include Module. It wraps the underlying *APIError, so callers matching
// *APIError (status-code hints, retry checks) keep working.
type ModuleDisabledError struct {
	Module string
	*APIError
}

// User-facing refusal text. "licence" is the platform's own user-facing
// spelling (every Gravity screen and error uses it), so the CLI matches it.
//
//nolint:misspell // platform spelling, see above.
const (
	msgModuleDisabled        = "The %s module is not included in this workspace's licence. Ask an administrator."
	msgModuleDisabledUnnamed = "A module this command needs is not included in this workspace's licence. Ask an administrator."
	msgSeatLimit             = "This workspace has used all the seats its licence includes. Ask an administrator."
)

func (e *ModuleDisabledError) Error() string {
	if e.Module == "" {
		return msgModuleDisabledUnnamed
	}
	return fmt.Sprintf(msgModuleDisabled, e.Module)
}

// Unwrap exposes the raw *APIError.
func (e *ModuleDisabledError) Unwrap() error { return e.APIError }

// SeatLimitError is a refusal because the workspace has used every licensed
// seat. It wraps the underlying *APIError.
type SeatLimitError struct {
	*APIError
}

func (e *SeatLimitError) Error() string {
	return msgSeatLimit
}

// Unwrap exposes the raw *APIError.
func (e *SeatLimitError) Unwrap() error { return e.APIError }

// IsLicenseError reports whether err (anywhere in its chain) is a license
// refusal rather than an auth, network or input failure.
func IsLicenseError(err error) bool {
	var md *ModuleDisabledError
	var sl *SeatLimitError
	return errors.As(err, &md) || errors.As(err, &sl)
}

// classifyLicense upgrades a license refusal to its typed error. Only a 403
// counts: a stray code on another status stays a plain *APIError, so the
// existing auth/unavailable handling is untouched.
func classifyLicense(e *APIError, module string) error {
	if e.StatusCode != http.StatusForbidden {
		return e
	}
	switch e.Code {
	case CodeModuleDisabled:
		return &ModuleDisabledError{Module: module, APIError: e}
	case CodeSeatLimit:
		return &SeatLimitError{APIError: e}
	}
	return e
}
