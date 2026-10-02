package report

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Bitbucket posts the doc-impact comment on a pull request through the Bitbucket Cloud REST API.
type Bitbucket struct {
	API   string
	Token string
	Repo  string
	HTTP  *http.Client
}

type bbUser struct {
	UUID string `json:"uuid"`
}

type bbComment struct {
	ID      int64  `json:"id"`
	Deleted bool   `json:"deleted"`
	User    bbUser `json:"user"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type bbPage struct {
	Values []bbComment `json:"values"`
	Next   string      `json:"next"`
}

func (b Bitbucket) base() string {
	if b.API != "" {
		return strings.TrimRight(b.API, "/")
	}
	return "https://api.bitbucket.org/2.0"
}

func (b Bitbucket) do(ctx context.Context, method, u string, body, out any) error {
	return doJSON(ctx, b.HTTP, method, u, map[string]string{"Authorization": "Bearer " + b.Token, "Accept": "application/json"}, body, out)
}

func (b Bitbucket) commentsURL(pr int) string {
	return fmt.Sprintf("%s/repositories/%s/pullrequests/%d/comments", b.base(), b.Repo, pr)
}

// Upsert updates this repository's comment on the pull request, or creates one when create is true.
func (b Bitbucket) Upsert(ctx context.Context, pr int, marker, body string, create bool) (string, error) {
	self := ""
	var me bbUser
	if err := b.do(ctx, http.MethodGet, b.base()+"/user", nil, &me); err == nil {
		self = me.UUID
	}
	var existing *bbComment
	next := b.commentsURL(pr) + "?pagelen=100"
	for page := 0; page < 10 && next != "" && existing == nil; page++ {
		var p bbPage
		if err := b.do(ctx, http.MethodGet, next, nil, &p); err != nil {
			return "", err
		}
		for i := range p.Values {
			c := p.Values[i]
			if !c.Deleted && strings.Contains(c.Content.Raw, marker) && (self == "" || c.User.UUID == self) {
				existing = &p.Values[i]
				break
			}
		}
		next = p.Next
	}
	payload := map[string]any{"content": map[string]string{"raw": body}}
	var out bbComment
	if existing != nil {
		err := b.do(ctx, http.MethodPut, fmt.Sprintf("%s/%d", b.commentsURL(pr), existing.ID), payload, &out)
		if err == nil {
			return out.Links.HTML.Href, nil
		}
		if !create || !refused(err) {
			return "", err
		}
	}
	if !create {
		return "", nil
	}
	if err := b.do(ctx, http.MethodPost, b.commentsURL(pr), payload, &out); err != nil {
		return "", err
	}
	return out.Links.HTML.Href, nil
}
