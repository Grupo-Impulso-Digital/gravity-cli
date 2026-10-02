package api

import (
	"errors"
	"fmt"
	"net/http"
)

// ModuleDisabledError is a refusal because the workspace's license lacks Module.
type ModuleDisabledError struct {
	Module string
	*APIError
}

//nolint:misspell // platform spelling of licence in user-facing text
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

// SeatLimitError is a refusal because the workspace has used every licensed seat.
type SeatLimitError struct {
	*APIError
}

func (e *SeatLimitError) Error() string {
	return msgSeatLimit
}

// Unwrap exposes the raw *APIError.
func (e *SeatLimitError) Unwrap() error { return e.APIError }

// IsLicenseError reports whether err is a license refusal rather than an auth, network or input failure.
func IsLicenseError(err error) bool {
	var md *ModuleDisabledError
	var sl *SeatLimitError
	return errors.As(err, &md) || errors.As(err, &sl)
}

func classifyLicense(e *APIError) error {
	if e.StatusCode != http.StatusForbidden {
		return e
	}
	switch e.Code {
	case CodeModuleDisabled:
		return &ModuleDisabledError{Module: e.Module, APIError: e}
	case CodeSeatLimit:
		return &SeatLimitError{APIError: e}
	}
	return e
}
