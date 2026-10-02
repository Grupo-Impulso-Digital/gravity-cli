package report

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GitLab posts the doc-impact note on a merge request through the GitLab REST API.
type GitLab struct {
	API     string
	Token   string
	Project string
	MRURL   string
	HTTP    *http.Client
}

type glUser struct {
	ID int64 `json:"id"`
}

type glNote struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	System bool   `json:"system"`
	Author glUser `json:"author"`
}

func (g GitLab) base() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	return "https://gitlab.com/api/v4"
}

func (g GitLab) do(ctx context.Context, method, u string, body, out any) error {
	return doJSON(ctx, g.HTTP, method, u, map[string]string{"PRIVATE-TOKEN": g.Token, "Accept": "application/json"}, body, out)
}

func (g GitLab) notesURL(mr int) string {
	return fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", g.base(), url.PathEscape(g.Project), mr)
}

func (g GitLab) link(id int64) string {
	if g.MRURL == "" {
		return ""
	}
	return fmt.Sprintf("%s#note_%d", g.MRURL, id)
}

// Upsert updates this repository's note on the merge request, or creates one when create is true.
func (g GitLab) Upsert(ctx context.Context, mr int, marker, body string, create bool) (string, error) {
	var self int64
	var me glUser
	if err := g.do(ctx, http.MethodGet, g.base()+"/user", nil, &me); err == nil {
		self = me.ID
	}
	var existing *glNote
	for page := 1; page <= 10 && existing == nil; page++ {
		var notes []glNote
		if err := g.do(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d&sort=asc&order_by=created_at", g.notesURL(mr), page), nil, &notes); err != nil {
			return "", err
		}
		for i := range notes {
			n := notes[i]
			if !n.System && strings.Contains(n.Body, marker) && (self == 0 || n.Author.ID == self) {
				existing = &notes[i]
				break
			}
		}
		if len(notes) < 100 {
			break
		}
	}
	var out glNote
	if existing != nil {
		err := g.do(ctx, http.MethodPut, fmt.Sprintf("%s/%d", g.notesURL(mr), existing.ID), map[string]string{"body": body}, &out)
		if err == nil {
			return g.link(out.ID), nil
		}
		if !create || !refused(err) {
			return "", err
		}
	}
	if !create {
		return "", nil
	}
	if err := g.do(ctx, http.MethodPost, g.notesURL(mr), map[string]string{"body": body}, &out); err != nil {
		return "", err
	}
	return g.link(out.ID), nil
}
