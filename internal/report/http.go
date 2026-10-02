package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Commenter upserts the doc-impact comment of one repository on a pull request.
type Commenter interface {
	Upsert(ctx context.Context, pr int, marker, body string, create bool) (string, error)
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func doJSON(ctx context.Context, c *http.Client, method, url string, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient(c).Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return &statusError{status: resp.StatusCode, msg: fmt.Sprintf("%s %s: %s %s", method, url, resp.Status, clipBody(strings.TrimSpace(string(data))))}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return nil
}

func clipBody(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func refused(err error) bool {
	var se *statusError
	return errors.As(err, &se) && (se.status == http.StatusForbidden || se.status == http.StatusNotFound || se.status == http.StatusUnauthorized)
}
