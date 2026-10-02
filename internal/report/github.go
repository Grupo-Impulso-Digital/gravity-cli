package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

func (g GitHub) base() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	return "https://api.github.com"
}

func (g GitHub) do(ctx context.Context, method, url string, body, out any) error {
	return doJSON(ctx, g.HTTP, method, url, map[string]string{
		"Authorization":        "Bearer " + g.Token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}, body, out)
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
