package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Token kinds.
const (
	TokenKindOrg  = "org"
	TokenKindUser = "user"
	TokenKindRepo = "repo"
)

// Device poll statuses.
const (
	DevicePending  = "pending"
	DeviceSlowDown = "slow_down"
	DeviceApproved = "approved"
)

// DeviceStartRequest is the body of POST /api/v1/auth/device/start.
type DeviceStartRequest struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	OS            string `json:"os"`
	Hostname      string `json:"hostname,omitempty"`
	Org           string `json:"org,omitempty"`
}

// DeviceStart is the response of POST /api/v1/auth/device/start.
type DeviceStart struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// OrgRef identifies an organization.
type OrgRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// UserRef identifies a user.
type UserRef struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// DevicePoll is the response of POST /api/v1/auth/device/poll.
type DevicePoll struct {
	Status       string   `json:"status"`
	Interval     int      `json:"interval,omitempty"`
	Token        string   `json:"token,omitempty"`
	TokenKind    string   `json:"tokenKind,omitempty"`
	ExpiresAt    string   `json:"expiresAt,omitempty"`
	Organization *OrgRef  `json:"organization,omitempty"`
	User         *UserRef `json:"user,omitempty"`
	APIURL       string   `json:"apiUrl,omitempty"`
}

// ErrDeviceDenied is returned when the user denied the device login.
var ErrDeviceDenied = errors.New("the login request was denied")

// ErrDeviceExpired is returned when the device code expired or was already used.
var ErrDeviceExpired = errors.New("the login code expired; run gravity login again")

// StartDeviceLogin calls POST /api/v1/auth/device/start.
func (c *Client) StartDeviceLogin(ctx context.Context, req DeviceStartRequest) (*DeviceStart, error) {
	var out DeviceStart
	if err := c.Post(ctx, "/api/v1/auth/device/start", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollDeviceLogin calls POST /api/v1/auth/device/poll; denial and expiry map to ErrDeviceDenied and ErrDeviceExpired.
func (c *Client) PollDeviceLogin(ctx context.Context, deviceCode string) (*DevicePoll, error) {
	var out DevicePoll
	err := c.Post(ctx, "/api/v1/auth/device/poll", map[string]string{"deviceCode": deviceCode}, &out)
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Code == CodeAccessDenied:
			return nil, fmt.Errorf("%w: %w", ErrDeviceDenied, err)
		case ae.Code == CodeExpiredToken || ae.StatusCode == http.StatusGone:
			return nil, fmt.Errorf("%w: %w", ErrDeviceExpired, err)
		}
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Logout calls POST /api/v1/auth/logout, revoking the presenting user token.
func (c *Client) Logout(ctx context.Context) error {
	return c.Post(ctx, "/api/v1/auth/logout", map[string]any{}, nil)
}

// MintTokenRequest is the body of POST /api/v1/repos/{repoId}/tokens.
type MintTokenRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes,omitempty"`
	ExpiresInDays *int     `json:"expiresInDays"`
}

// KeyInfo describes a minted key without its secret.
type KeyInfo struct {
	ID        string   `json:"id"`
	KeyHint   string   `json:"keyHint"`
	Kind      string   `json:"kind"`
	RepoID    string   `json:"repoId"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expiresAt"`
	CreatedAt string   `json:"createdAt"`
}

// MintedToken is the response of POST /api/v1/repos/{repoId}/tokens.
type MintedToken struct {
	Token string  `json:"token"`
	Key   KeyInfo `json:"key"`
}

// MintRepoToken calls POST /api/v1/repos/{repoId}/tokens.
func (c *Client) MintRepoToken(ctx context.Context, repoID string, req MintTokenRequest) (*MintedToken, error) {
	var out MintedToken
	if err := c.Post(ctx, "/api/v1/repos"+pathEscape(repoID)+"/tokens", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PrincipalRepo is the repository a repo principal is bound to.
type PrincipalRepo struct {
	ID        string   `json:"id"`
	RemoteKey string   `json:"remoteKey"`
	Name      string   `json:"name"`
	Product   *Product `json:"product,omitempty"`
}

// Principal describes who the token acts as.
type Principal struct {
	Kind        string         `json:"kind"`
	User        *UserRef       `json:"user,omitempty"`
	Role        string         `json:"role,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
	Repo        *PrincipalRepo `json:"repo,omitempty"`
	CreatedBy   *UserRef       `json:"createdBy,omitempty"`
}

// TokenInfo describes the presenting token.
type TokenInfo struct {
	Kind      string   `json:"kind"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expiresAt"`
}

// WhoAmI is the response of GET /api/v1/whoami.
type WhoAmI struct {
	OrganizationID   string          `json:"organizationId"`
	OrganizationName string          `json:"organizationName"`
	DefaultSiteSlug  *string         `json:"defaultSiteSlug"`
	KeyHint          string          `json:"keyHint"`
	Features         map[string]bool `json:"features"`
	APIURL           string          `json:"apiUrl,omitempty"`
	Principal        *Principal      `json:"principal,omitempty"`
	Organization     *OrgRef         `json:"organization,omitempty"`
	Organizations    []OrgRef        `json:"organizations,omitempty"`
	Token            *TokenInfo      `json:"token,omitempty"`
	Modules          map[string]bool `json:"modules,omitempty"`
}

// HasPermission reports whether the principal holds an org-level permission key.
func (w *WhoAmI) HasPermission(key string) bool {
	if w == nil || w.Principal == nil {
		return false
	}
	for _, p := range w.Principal.Permissions {
		if p == key || p == "*" {
			return true
		}
	}
	return false
}

// WhoAmI calls GET /api/v1/whoami.
func (c *Client) WhoAmI(ctx context.Context) (*WhoAmI, error) {
	var out WhoAmI
	if err := c.Get(ctx, "/api/v1/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
