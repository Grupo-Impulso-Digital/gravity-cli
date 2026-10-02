package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

const maxSuggestedSlugs = 12

func explainNotFound(ctx context.Context, e *env, err error) error {
	var ae *api.APIError
	if err == nil || e == nil || e.client == nil || !errors.As(err, &ae) || !ae.IsNotFound() || api.IsLicenseError(err) {
		return err
	}
	site := e.cfg.Site
	if site == "" {
		return err
	}
	sites, lerr := e.client.Sites(ctx)
	switch {
	case lerr == nil && !siteListed(sites, site):
		slugs := make([]string, 0, len(sites))
		for _, s := range sites {
			slugs = append(slugs, s.Slug)
		}
		return Failf(CodeError, "site '%s' not found; %s", site, availableList("sites", slugs, "no sites are visible to this token"))
	case lerr != nil && strings.Contains(strings.ToLower(ae.Message), "site not found"):
		return Failf(CodeError, "site '%s' not found (check `site:` in .gravity.yaml, GRAVITY_SITE or --site)", site)
	}
	if !strings.Contains(strings.ToLower(ae.Message), "space") {
		return Fail(CodeError, err)
	}
	tree, terr := e.client.SiteTree(ctx, site)
	if terr != nil {
		return Fail(CodeError, err)
	}
	slugs := make([]string, 0, len(tree.Spaces))
	for _, s := range tree.Spaces {
		slugs = append(slugs, s.Slug)
	}
	if target := e.targetSpace; target != "" && !contains(slugs, target) {
		return Failf(CodeError, "space '%s' not found on site '%s'; %s (`gravity sync` creates the spaces .gravity.yaml declares)",
			target, site, availableList("spaces", slugs, "the site has no spaces yet"))
	}
	return Failf(CodeError, "space not found on site '%s'; %s", site, availableList("spaces", slugs, "the site has no spaces yet"))
}

func siteListed(sites []api.SiteSummary, slug string) bool {
	for _, s := range sites {
		if s.Slug == slug {
			return true
		}
	}
	return false
}

func availableList(noun string, slugs []string, empty string) string {
	if len(slugs) == 0 {
		return empty
	}
	sorted := append([]string(nil), slugs...)
	sort.Strings(sorted)
	more := ""
	if len(sorted) > maxSuggestedSlugs {
		more = fmt.Sprintf(" (+%d more)", len(sorted)-maxSuggestedSlugs)
		sorted = sorted[:maxSuggestedSlugs]
	}
	return fmt.Sprintf("available %s: %s%s", noun, strings.Join(sorted, ", "), more)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
