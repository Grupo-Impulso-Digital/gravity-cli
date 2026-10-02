package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/config"
)

func newSpacesCmd(gf *globalFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "spaces",
		Short: "Show the site's space hierarchy and where this repo publishes",
		Long: `Read-only view of the site's content structure: top-level spaces, their
subspaces, each space's collections (page folders) and page counts, plus which
space this repo's manifest publishes into. Nothing is written.

This is the derived "repo management" view for multi-repo products: each repo
declares its mother space/subspace in .gravity.yaml, and the per-repo
collections inside a shared space show which repo feeds which pages.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := resolveEnv(*gf, "")
			if err != nil {
				return err
			}
			if err := e.requireAuth(); err != nil {
				return err
			}
			site, err := e.requireSite()
			if err != nil {
				return err
			}
			tree, err := e.client.SiteTree(cmd.Context(), site)
			if err != nil {
				return syncAPIError(err, fmt.Sprintf("read site %q", site))
			}
			view := buildSpacesView(tree, e.proj)
			if jsonOut {
				enc := json.NewEncoder(rawWriter(cmd.OutOrStdout()))
				enc.SetIndent("", "  ")
				return enc.Encode(view)
			}
			printSpacesView(cmd.OutOrStdout(), tree.Site, view)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the hierarchy as JSON")
	return cmd
}

type spacesView struct {
	Site   string      `json:"site"`
	Spaces []spaceNode `json:"spaces"`
}

type spaceNode struct {
	Slug      string           `json:"slug"`
	Name      string           `json:"name"`
	HomePage  string           `json:"homePage,omitempty"`
	Pages     int              `json:"pages"`
	ThisRepo  bool             `json:"thisRepo,omitempty"`
	Shared    bool             `json:"shared,omitempty"`
	Releases  bool             `json:"releases,omitempty"`
	Colls     []collectionNode `json:"collections,omitempty"`
	Subspaces []spaceNode      `json:"subspaces,omitempty"`
}

type collectionNode struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Pages int    `json:"pages"`
}

func buildSpacesView(tree *api.SiteTree, proj *config.Project) spacesView {
	pageByID := make(map[string]api.PageRef, len(tree.Pages))
	flatPages := map[string]int{}
	collPages := map[string]int{}
	for _, p := range tree.Pages {
		pageByID[p.ID] = p
		if p.CollectionID == nil {
			flatPages[p.SpaceID]++
		} else {
			collPages[*p.CollectionID]++
		}
	}
	collsBySpace := map[string][]collectionNode{}
	for _, c := range tree.Collections {
		collsBySpace[c.SpaceID] = append(collsBySpace[c.SpaceID], collectionNode{
			Slug:  c.Slug,
			Name:  c.Name,
			Pages: collPages[c.ID],
		})
	}

	node := func(s api.Space) spaceNode {
		n := spaceNode{
			Slug:  s.Slug,
			Name:  s.Name,
			Pages: flatPages[s.ID],
			Colls: collsBySpace[s.ID],
		}
		if s.OverviewPageID != nil {
			if pg, ok := pageByID[*s.OverviewPageID]; ok {
				n.HomePage = pg.Slug
			}
		}
		if proj != nil {
			n.ThisRepo = s.Slug == proj.Spaces.Default
			n.Shared = proj.IsShared(s.Slug)
			n.Releases = s.Slug == proj.ReleaseNotes.Space
		}
		return n
	}

	view := spacesView{Site: tree.Site.Slug}
	emitted := map[string]bool{}
	for _, s := range tree.Spaces {
		if s.ParentSpaceID != nil {
			continue
		}
		parent := node(s)
		emitted[s.ID] = true
		for _, c := range tree.Spaces {
			if c.ParentSpaceID != nil && *c.ParentSpaceID == s.ID {
				parent.Subspaces = append(parent.Subspaces, node(c))
				emitted[c.ID] = true
			}
		}
		view.Spaces = append(view.Spaces, parent)
	}
	for _, s := range tree.Spaces {
		if !emitted[s.ID] {
			view.Spaces = append(view.Spaces, node(s))
		}
	}
	return view
}

func printSpacesView(out io.Writer, site api.Site, view spacesView) {
	fmt.Fprintf(out, "%s — %s\n\n", site.Slug, site.Name)
	if len(view.Spaces) == 0 {
		fmt.Fprintln(out, "(no spaces yet — `gravity init` + `gravity sync` create them)")
		return
	}
	for _, s := range view.Spaces {
		printSpaceNode(out, s, "")
		for _, sub := range s.Subspaces {
			printSpaceNode(out, sub, "└─ ")
		}
	}
}

func printSpaceNode(out io.Writer, n spaceNode, prefix string) {
	var notes []string
	if n.HomePage != "" {
		notes = append(notes, "⌂ "+n.HomePage)
	}
	if n.ThisRepo {
		notes = append(notes, "← this repo")
	}
	if n.Shared {
		notes = append(notes, "shared")
	}
	if n.Releases {
		notes = append(notes, "release notes")
	}
	suffix := ""
	if len(notes) > 0 {
		suffix = "   [" + strings.Join(notes, ", ") + "]"
	}
	name := ""
	if n.Name != "" && n.Name != n.Slug {
		name = "  " + n.Name
	}
	fmt.Fprintf(out, "%s%s%s%s\n", prefix, n.Slug, name, suffix)

	indent := strings.Repeat(" ", len(prefix)) + "   "
	for _, c := range n.Colls {
		fmt.Fprintf(out, "%s· %s  (collection, %s)\n", indent, c.Slug, pluralPages(c.Pages))
	}
	if n.Pages > 0 {
		fmt.Fprintf(out, "%s%s\n", indent, pluralPages(n.Pages))
	}
}

func pluralPages(n int) string {
	if n == 1 {
		return "1 page"
	}
	return fmt.Sprintf("%d pages", n)
}
