package api

import (
	"errors"
	"net/http"
)

const msgNoProviderKey = "No AI provider is connected for this organization. An organization admin can connect Origin or add a provider key in Gravity › Settings › AI."

// NoProviderKeyError is a refusal because the organization has no AI provider connected, neither Origin nor a provider key.
type NoProviderKeyError struct {
	*APIError
}

func (e *NoProviderKeyError) Error() string {
	return msgNoProviderKey
}

// Unwrap exposes the raw *APIError.
func (e *NoProviderKeyError) Unwrap() error { return e.APIError }

// NewNoProviderKeyError builds the refusal for a caller that found out from the organization's AI configuration rather than from a 402.
func NewNoProviderKeyError() *NoProviderKeyError {
	return &NoProviderKeyError{APIError: &APIError{StatusCode: http.StatusPaymentRequired, Code: CodeNoProviderKey}}
}

// AsNoProviderKey returns the *NoProviderKeyError in err's chain.
func AsNoProviderKey(err error) (*NoProviderKeyError, bool) {
	var nk *NoProviderKeyError
	if errors.As(err, &nk) {
		return nk, true
	}
	return nil, false
}

func classifyProvider(e *APIError) *NoProviderKeyError {
	if e.StatusCode != http.StatusPaymentRequired || e.Code != CodeNoProviderKey {
		return nil
	}
	return &NoProviderKeyError{APIError: e}
}
