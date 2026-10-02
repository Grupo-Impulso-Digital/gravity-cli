package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHub posts the doc-impact comment through the GitHub REST API.
type GitHub struct {
	API   string
	Token string
	Repo  string
	HTTP  *http.Client
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    ghUser `json:"user"`
}

func (g GitHub) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (g GitHub) base() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	return "https://api.github.com"
}

func (g GitHub) do(ctx context.Context, method, url string, body, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s %s", method, url, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return nil
}

// Upsert updates the comment carrying marker, or creates one when create is true; it returns the comment URL ("" when nothing was posted).
func (g GitHub) Upsert(ctx context.Context, pr int, marker, body string, create bool) (string, error) {
	var existing *ghComment
	self, selfKnown := "", false
	ours := func(c ghComment) bool {
		if !strings.Contains(c.Body, marker) {
			return false
		}
		if c.User.Type == "Bot" {
			return true
		}
		if !selfKnown {
			selfKnown = true
			var me ghUser
			if err := g.do(ctx, http.MethodGet, g.base()+"/user", nil, &me); err == nil {
				self = me.Login
			}
		}
		return self != "" && c.User.Login == self
	}
	for page := 1; page <= 10 && existing == nil; page++ {
		var comments []ghComment
		url := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100&page=%d", g.base(), g.Repo, pr, page)
		if err := g.do(ctx, http.MethodGet, url, nil, &comments); err != nil {
			return "", err
		}
		for i := range comments {
			if ours(comments[i]) {
				existing = &comments[i]
				break
			}
		}
		if len(comments) < 100 {
			break
		}
	}
	var out ghComment
	switch {
	case existing != nil:
		if err := g.do(ctx, http.MethodPatch, fmt.Sprintf("%s/repos/%s/issues/comments/%d", g.base(), g.Repo, existing.ID), map[string]string{"body": body}, &out); err != nil {
			return "", err
		}
	case create:
		if err := g.do(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.base(), g.Repo, pr), map[string]string{"body": body}, &out); err != nil {
			return "", err
		}
	default:
		return "", nil
	}
	return out.HTMLURL, nil
}
