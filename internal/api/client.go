// Package api implements the HTTP client for the Gravity platform REST API and LLM gateway.
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/version"
)

// RetryPolicy controls how 429 and 5xx answers are retried.
type RetryPolicy struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultRetry is three attempts with exponential backoff from one second.
var DefaultRetry = RetryPolicy{Attempts: 3, Base: time.Second, Max: 60 * time.Second}

// Request deadlines.
const (
	DefaultTimeout     = 120 * time.Second
	DefaultLLMTimeout  = 15 * time.Minute
	DefaultLLMRetryCap = 30 * time.Minute
)

// Client talks to the Gravity platform.
type Client struct {
	BaseURL     string
	Token       string
	HTTPClient  *http.Client
	UserAgent   string
	Retry       RetryPolicy
	Sleep       func(ctx context.Context, d time.Duration) error
	LLMTimeout  time.Duration
	LLMRetryCap time.Duration
	Now         func() time.Time
}

// New constructs a Client.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Token:       token,
		UserAgent:   version.UserAgent(),
		HTTPClient:  &http.Client{Timeout: DefaultTimeout},
		Retry:       DefaultRetry,
		Sleep:       sleepContext,
		LLMTimeout:  DefaultLLMTimeout,
		LLMRetryCap: DefaultLLMRetryCap,
		Now:         time.Now,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type request struct {
	method      string
	path        string
	query       url.Values
	body        any
	raw         []byte
	contentType string
	headers     map[string]string
	idempotent  bool
	llm         bool
	respHeader  *http.Header
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	if c.BaseURL == "" {
		return errors.New("no API URL configured (set --api-url or GRAVITY_API_URL, or run gravity login)")
	}
	payload := r.raw
	if r.body != nil {
		buf, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		payload = buf
	}
	if r.llm {
		return c.doLLM(ctx, r, payload, out)
	}
	attempts := max(c.Retry.Attempts, 1)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		status, data, header, err := c.send(ctx, r, payload, 0)
		if err != nil {
			lastErr = err
			if (r.method != http.MethodGet && !r.idempotent) || attempt == attempts || ctx.Err() != nil {
				return err
			}
			if serr := c.wait(ctx, c.backoff(attempt, 0)); serr != nil {
				return serr
			}
			continue
		}
		if status >= 200 && status < 300 {
			return c.decodeOK(r, header, data, out)
		}
		apiErr := decodeError(status, header, data)
		apiErr.Method, apiErr.Path = r.method, r.path
		if !retryable(status) || attempt == attempts {
			return classify(apiErr)
		}
		lastErr = apiErr
		if serr := c.wait(ctx, c.backoff(attempt, apiErr.RetryAfter)); serr != nil {
			return serr
		}
	}
	return lastErr
}

func (c *Client) doLLM(ctx context.Context, r request, payload []byte, out any) error {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	limit := c.LLMRetryCap
	if limit <= 0 {
		limit = DefaultLLMRetryCap
	}
	perCall := c.LLMTimeout
	if perCall <= 0 {
		perCall = DefaultLLMTimeout
	}
	var spent time.Duration
	for attempt := 1; ; attempt++ {
		sent := now()
		status, data, header, err := c.send(ctx, r, payload, min(perCall, max(limit-spent, time.Second)))
		spent += max(now().Sub(sent), 0)
		var failure error
		retryAfter := 0
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return err
			}
			failure = err
		case status >= 200 && status < 300:
			return c.decodeOK(r, header, data, out)
		default:
			apiErr := decodeError(status, header, data)
			apiErr.Method, apiErr.Path = r.method, r.path
			if !retryable(status) {
				return classify(apiErr)
			}
			failure, retryAfter = apiErr, apiErr.RetryAfter
		}
		wait := c.llmBackoff(attempt, retryAfter)
		if spent+wait >= limit {
			return failure
		}
		if serr := c.wait(ctx, wait); serr != nil {
			return serr
		}
		spent += wait
	}
}

func (c *Client) llmBackoff(attempt, retryAfter int) time.Duration {
	if retryAfter > 0 {
		return time.Duration(retryAfter) * time.Second
	}
	return c.backoff(attempt, 0)
}

func (c *Client) decodeOK(r request, header http.Header, data []byte, out any) error {
	if r.respHeader != nil {
		*r.respHeader = header
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response of %s %s: %w", r.method, r.path, err)
		}
	}
	return nil
}

func (c *Client) send(ctx context.Context, r request, payload []byte, timeout time.Duration) (int, []byte, http.Header, error) {
	u := c.BaseURL + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("build request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	switch {
	case r.contentType != "":
		req.Header.Set("Content-Type", r.contentType)
	case payload != nil:
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if timeout > 0 {
		hc := *httpClient
		hc.Timeout = timeout
		httpClient = &hc
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%s %s: %w", r.method, r.path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read response of %s %s: %w", r.method, r.path, err)
	}
	return resp.StatusCode, data, resp.Header, nil
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func (c *Client) backoff(attempt, retryAfter int) time.Duration {
	limit := c.Retry.Max
	if limit <= 0 {
		limit = DefaultRetry.Max
	}
	if retryAfter > 0 {
		return min(time.Duration(retryAfter)*time.Second, limit)
	}
	base := c.Retry.Base
	if base <= 0 {
		base = DefaultRetry.Base
	}
	return min(base<<(attempt-1), limit)
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return sleep(ctx, d)
}

// Get issues a GET request and decodes the JSON response into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, request{method: http.MethodGet, path: path, query: query}, out)
}

// Post issues a POST request with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPost, path: path, body: body}, out)
}

// Put issues a PUT request with a JSON body.
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPut, path: path, body: body}, out)
}

func classify(e *APIError) error {
	if be := classifyBudget(e); be != nil {
		return be
	}
	if nk := classifyProvider(e); nk != nil {
		return nk
	}
	return classifyLicense(e)
}

func decodeError(status int, header http.Header, data []byte) *APIError {
	e := &APIError{StatusCode: status}
	var env struct {
		Error struct {
			Code       string          `json:"code"`
			Message    string          `json:"message"`
			Details    json.RawMessage `json:"details"`
			Module     string          `json:"module"`
			RetryAfter int             `json:"retryAfter"`
			Holder     *LeaseHolder    `json:"holder"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &env) == nil {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
		e.Details, e.Fields = decodeDetails(env.Error.Details)
		e.Module = env.Error.Module
		e.RetryAfter = env.Error.RetryAfter
		e.Holder = env.Error.Holder
	}
	if e.RetryAfter == 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && n > 0 {
			e.RetryAfter = n
		}
	}
	if e.Message == "" {
		e.Message = summarizeBody(status, header.Get("Content-Type"), data)
	}
	return e
}

func decodeDetails(raw json.RawMessage) ([]ErrorDetail, map[string]any) {
	if len(raw) == 0 {
		return nil, nil
	}
	var list []ErrorDetail
	if json.Unmarshal(raw, &list) == nil {
		return list, nil
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) == nil {
		return nil, fields
	}
	return nil, nil
}

const maxErrorSummary = 200

var htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func summarizeBody(status int, contentType string, data []byte) string {
	body := strings.TrimSpace(string(data))
	fallback := http.StatusText(status)
	if body == "" {
		return fallback
	}
	if strings.Contains(strings.ToLower(contentType), "html") || strings.HasPrefix(body, "<") {
		if m := htmlTitleRE.FindStringSubmatch(body); m != nil {
			if title := strings.Join(strings.Fields(m[1]), " "); title != "" {
				return clip(title, maxErrorSummary)
			}
		}
		if fallback == "" {
			return "unexpected HTML error page"
		}
		return fallback
	}
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clip(line, maxErrorSummary)
		}
	}
	return fallback
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

func pathEscape(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}
