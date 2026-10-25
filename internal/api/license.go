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
	msgAIDisabled            = "AI is turned off for this organization. An organization admin can turn it on in Gravity › AI usage."
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

// AIDisabledError is a refusal because an organization admin turned AI off.
type AIDisabledError struct {
	*APIError
}

func (e *AIDisabledError) Error() string {
	return msgAIDisabled
}

// Unwrap exposes the raw *APIError.
func (e *AIDisabledError) Unwrap() error { return e.APIError }

// IsLicenseError reports whether err is a license or organization-policy refusal (module_disabled, seat_limit, ai_disabled, no_provider_key) rather than an auth, network or input failure.
func IsLicenseError(err error) bool {
	var md *ModuleDisabledError
	var sl *SeatLimitError
	var ad *AIDisabledError
	_, nk := AsNoProviderKey(err)
	return errors.As(err, &md) || errors.As(err, &sl) || errors.As(err, &ad) || nk
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
	case CodeAIDisabled:
		return &AIDisabledError{APIError: e}
	}
	return e
}
