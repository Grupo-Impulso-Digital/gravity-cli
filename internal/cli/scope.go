package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

type repoScope struct {
	remoteKey string
	spaces    map[string]string
	self      string
	override  bool
}

var errNoScope = errors.New("cannot tell which pages belong to this repo: the platform attributes no page to it and there is no .gravity.yaml declaring its spaces; add a .gravity.yaml (gravity init) or pass --space")

func resolveRepoScope(myRemoteKey string, attributed bool, proj *config.Project, spaceOverride string) (repoScope, error) {
	spaces := map[string]string{}
	if spaceOverride != "" {
		spaces[spaceOverride] = ""
		return repoScope{spaces: spaces, self: myRemoteKey, override: true}, nil
	}
	if myRemoteKey != "" && attributed {
		return repoScope{remoteKey: myRemoteKey}, nil
	}
	if proj == nil {
		return repoScope{}, errNoScope
	}
	shared := map[string]bool{}
	for _, s := range proj.Spaces.Shared {
		shared[s] = true
	}
	add := func(space string) {
		if space == "" {
			space = proj.Spaces.Default
		}
		if space == "" {
			return
		}
		prefix := ""
		if shared[space] && proj.Product.Repo != "" {
			prefix = proj.Product.Repo + "/"
		}
		spaces[space] = prefix
	}
	add(proj.Spaces.Default)
	for _, d := range proj.Spaces.Declare {
		add(d.Slug)
	}
	for _, s := range proj.Sources {
		add(s.Space)
	}
	for _, d := range proj.Documents {
		if d.As == "release" {
			continue
		}
		add(d.Space)
	}
	if len(spaces) == 0 {
		return repoScope{}, errNoScope
	}
	return repoScope{spaces: spaces, self: myRemoteKey}, nil
}

func (s repoScope) includes(spaceSlug, pageSlug string, remoteKey *string) bool {
	if s.remoteKey != "" {
		return remoteKey != nil && *remoteKey == s.remoteKey
	}
	prefix, ok := s.spaces[spaceSlug]
	if !ok {
		return false
	}
	if s.self != "" && remoteKey != nil && *remoteKey != "" && *remoteKey != s.self {
		return false
	}
	return prefix == "" || strings.HasPrefix(pageSlug, prefix)
}

func attributedTo(keys []*string, myRemoteKey string) bool {
	if myRemoteKey == "" {
		return false
	}
	for _, k := range keys {
		if k != nil && *k == myRemoteKey {
			return true
		}
	}
	return false
}

func pageRemoteKeys(pages []api.Page) []*string {
	out := make([]*string, len(pages))
	for i := range pages {
		out[i] = pages[i].RepoRemoteKey
	}
	return out
}

func pageRefRemoteKeys(pages []api.PageRef) []*string {
	out := make([]*string, len(pages))
	for i := range pages {
		out[i] = pages[i].RepoRemoteKey
	}
	return out
}

func (s repoScope) bySpaces() bool {
	return s.remoteKey == ""
}

func (s repoScope) describe() string {
	if s.remoteKey != "" {
		return "pages written by " + s.remoteKey
	}
	names := make([]string, 0, len(s.spaces))
	for sp, prefix := range s.spaces {
		if prefix != "" {
			sp += " (" + prefix + "*)"
		}
		names = append(names, sp)
	}
	sort.Strings(names)
	label := "this repo's declared spaces: "
	if s.override {
		label = "--space "
	}
	label += strings.Join(names, ", ")
	if s.self != "" {
		label += " (excluding pages other repos write)"
	}
	return label
}

func (s repoScope) pages(pages []api.Page) []api.Page {
	out := make([]api.Page, 0, len(pages))
	for _, p := range pages {
		if s.includes(p.SpaceSlug, p.Slug, p.RepoRemoteKey) {
			out = append(out, p)
		}
	}
	return out
}

func scopeNote(s repoScope, kept, total int, noun string) string {
	return fmt.Sprintf("scoped to %s: %d of %d %s", s.describe(), kept, total, noun)
}
