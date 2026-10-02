package report

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Azure posts the doc-impact comment as a pull request thread through the Azure DevOps REST API.
type Azure struct {
	Collection string
	Project    string
	RepoID     string
	Token      string
	PRURL      string
	HTTP       *http.Client
}

type azComment struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	IsDeleted bool   `json:"isDeleted"`
}

type azThread struct {
	ID        int64       `json:"id"`
	IsDeleted bool        `json:"isDeleted"`
	Comments  []azComment `json:"comments"`
}

const azureAPIVersion = "api-version=7.1"

func (a Azure) threadsURL(pr int) string {
	return fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads", strings.TrimRight(a.Collection, "/"), url.PathEscape(a.Project), url.PathEscape(a.RepoID), pr)
}

func (a Azure) do(ctx context.Context, method, u string, body, out any) error {
	return doJSON(ctx, a.HTTP, method, u, map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}, body, out)
}

func (a Azure) link(thread int64) string {
	if a.PRURL == "" {
		return ""
	}
	return fmt.Sprintf("%s?discussionId=%d", a.PRURL, thread)
}

// Upsert updates the thread carrying marker, or opens a new thread when create is true.
func (a Azure) Upsert(ctx context.Context, pr int, marker, body string, create bool) (string, error) {
	var list struct {
		Value []azThread `json:"value"`
	}
	if err := a.do(ctx, http.MethodGet, a.threadsURL(pr)+"?"+azureAPIVersion, nil, &list); err != nil {
		return "", err
	}
	for _, t := range list.Value {
		if t.IsDeleted || len(t.Comments) == 0 || t.Comments[0].IsDeleted || !strings.Contains(t.Comments[0].Content, marker) {
			continue
		}
		err := a.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d/comments/%d?%s", a.threadsURL(pr), t.ID, t.Comments[0].ID, azureAPIVersion), map[string]string{"content": body}, nil)
		if err == nil {
			return a.link(t.ID), nil
		}
		if !create || !refused(err) {
			return "", err
		}
		break
	}
	if !create {
		return "", nil
	}
	payload := map[string]any{"comments": []map[string]any{{"parentCommentId": 0, "content": body, "commentType": 1}}, "status": 1}
	var out azThread
	if err := a.do(ctx, http.MethodPost, a.threadsURL(pr)+"?"+azureAPIVersion, payload, &out); err != nil {
		return "", err
	}
	return a.link(out.ID), nil
}
