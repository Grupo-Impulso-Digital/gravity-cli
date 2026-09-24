// Package api implements the HTTP client for the Gravity platform.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to the Gravity platform.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	UserAgent  string
}

// New constructs a Client.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: "gravity-cli",
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// APIError is the decoded error envelope from a non-2xx response.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("api error %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("api error %d", e.StatusCode)
}

// IsAuth reports whether the error is an authentication/authorization failure.
// A license refusal (module_disabled, seat_limit) is a 403 but not an auth
// failure: the credential is fine and "check your token" would send the user
// down the wrong path, so those surface as *ModuleDisabledError/*SeatLimitError.
func (e *APIError) IsAuth() bool {
	if e.isLicense() {
		return false
	}
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

func (e *APIError) isLicense() bool {
	return e.Code == CodeModuleDisabled || e.Code == CodeSeatLimit
}

// IsUnavailable reports that the platform does not (yet) implement this route.
func (e *APIError) IsUnavailable() bool {
	switch e.StatusCode {
	case http.StatusNotFound, http.StatusNotImplemented:
		return true
	}
	switch e.Code {
	case "not_implemented", "feature_disabled", "unknown_route":
		return true
	}
	return false
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Module  string `json:"module"`
	} `json:"error"`
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if c.BaseURL == "" {
		return errors.New("no API URL configured (set --api-url, GRAVITY_API_URL, or run gravity auth login)")
	}
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		var env errorEnvelope
		if json.Unmarshal(data, &env) == nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
		}
		if apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(data))
		}
		return classifyLicense(apiErr, env.Error.Module)
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Get issues a GET request.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// Post issues a POST request with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// Patch issues a PATCH request with a JSON body.
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, nil, body, out)
}

// Delete issues a DELETE request.
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil, out)
}
