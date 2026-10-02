package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Error codes of the platform error envelope.
const (
	CodeBadRequest           = "bad_request"
	CodeManifestInvalid      = "manifest_invalid"
	CodeAccessDenied         = "access_denied"
	CodeUnauthorized         = "unauthorized"
	CodeTokenExpired         = "token_expired"
	CodeNoProviderKey        = "no_provider_key"
	CodeBudgetExceeded       = "budget_exceeded"
	CodeForbidden            = "forbidden"
	CodeScopeMissing         = "scope_missing"
	CodeOutsideReach         = "outside_reach"
	CodeTokenKindUnsupported = "token_kind_unsupported"
	CodeModuleDisabled       = "module_disabled"
	CodeSeatLimit            = "seat_limit"
	CodeNotFound             = "not_found"
	CodeRepoNotConnected     = "repo_not_connected"
	CodeConflict             = "conflict"
	CodeLeaseHeld            = "lease_held"
	CodeLeaseLost            = "lease_lost"
	CodePlanStale            = "plan_stale"
	CodeRunNotRunning        = "run_not_running"
	CodeBranchNotAuthority   = "branch_not_authoritative"
	CodeRunIsDry             = "run_is_dry"
	CodePageLocked           = "page_locked"
	CodeLockedByOther        = "locked_by_other"
	CodeSlugTaken            = "slug_taken"
	CodeProductMismatch      = "product_mismatch"
	CodeRepoArchived         = "repo_archived"
	CodeExpiredToken         = "expired_token"
	CodePayloadTooLarge      = "payload_too_large"
	CodeUnsupportedMedia     = "unsupported_media_type"
	CodeRateLimited          = "rate_limited"
	CodeProviderError        = "provider_error"
	CodeLanguageNotEnabled   = "language_not_enabled"
	CodeSourceNotFound       = "source_not_found"
	CodeSourceNotVerbatim    = "source_not_verbatim"
	CodeSourcePending        = "source_pending_adoption"
	CodeTranslationTaken     = "translation_taken"
)

// Sentinels matched by errors.Is against an *APIError.
var (
	ErrLeaseLost     = errors.New("run lease lost")
	ErrRunNotRunning = errors.New("run not running")
	ErrLeaseHeld     = errors.New("run lease held")
	ErrPlanStale     = errors.New("plan stale")
	ErrNotConnected  = errors.New("repository not connected")
	ErrUnauthorized  = errors.New("unauthorized")
)

// ErrorDetail is one entry of the envelope's details list.
type ErrorDetail struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// LeaseHolder identifies the run holding a lease.
type LeaseHolder struct {
	RunID     string `json:"runId"`
	HeadSHA   string `json:"headSha"`
	ExpiresAt string `json:"expiresAt"`
}

// APIError is the decoded error envelope of a non-2xx response.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Details    []ErrorDetail
	Module     string
	RetryAfter int
	Holder     *LeaseHolder
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if len(e.Details) > 0 {
		parts := make([]string, 0, len(e.Details))
		for _, d := range e.Details {
			switch {
			case d.Path != "" && d.Message != "":
				parts = append(parts, d.Path+": "+d.Message)
			case d.Message != "":
				parts = append(parts, d.Message)
			}
		}
		if len(parts) > 0 && !strings.Contains(msg, parts[0]) {
			msg += " (" + strings.Join(parts, "; ") + ")"
		}
	}
	if hint := e.hint(); hint != "" {
		msg += "; " + hint
	}
	if e.Code != "" {
		return fmt.Sprintf("%s [%d %s]", msg, e.StatusCode, e.Code)
	}
	return fmt.Sprintf("%s [%d]", msg, e.StatusCode)
}

func (e *APIError) hint() string {
	switch e.Code {
	case CodeTokenExpired:
		return "run `gravity login` to get a new token"
	case CodeRepoNotConnected:
		return "run `gravity init` to connect this repository"
	}
	if e.StatusCode == http.StatusUnauthorized && e.Code == "" {
		return "check the token or run `gravity login`"
	}
	return ""
}

// Is matches the package sentinels by envelope code.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrLeaseLost:
		return e.Code == CodeLeaseLost
	case ErrRunNotRunning:
		return e.Code == CodeRunNotRunning
	case ErrLeaseHeld:
		return e.Code == CodeLeaseHeld
	case ErrPlanStale:
		return e.Code == CodePlanStale
	case ErrNotConnected:
		return e.Code == CodeRepoNotConnected
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	}
	return false
}

// IsAuth reports an authentication or authorization refusal that is not a license refusal.
func (e *APIError) IsAuth() bool {
	if e.Code == CodeModuleDisabled || e.Code == CodeSeatLimit {
		return false
	}
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// HasCode reports whether err carries an *APIError with the given envelope code.
func HasCode(err error, code string) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}

// StopsRun reports whether err means the run must stop immediately without calling finish.
func StopsRun(err error) bool {
	return errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrRunNotRunning)
}
