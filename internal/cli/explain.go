package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

type explainData struct {
	Page   api.ResolvedPage   `json:"page"`
	Title  string             `json:"title"`
	Blocks []api.BlockHistory `json:"blocks"`
}

func newExplainCmd(a *app) *cobra.Command {
	var block string
	cmd := &cobra.Command{
		Use:   "explain <page>",
		Short: "Show which repository, pass and commit produced each block of a page",
		Long:  "Show the provenance of every block of a page. <page> is a page id, <site>/<space>/<page>, or a viewer URL.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			creds, err := a.credentials("")
			if err != nil {
				return err
			}
			if err := requireToken(creds); err != nil {
				return err
			}
			client := a.client(creds)
			who, err := client.WhoAmI(ctx)
			if err != nil {
				return explainAPI(err, creds)
			}
			if err := requirePipelines(who.Features); err != nil {
				return err
			}
			ref, err := client.ResolvePage(ctx, args[0])
			if err != nil {
				return Fail(CodeError, fmt.Errorf("resolve %s: %w", args[0], err))
			}
			prov, err := client.Provenance(ctx, ref.PageID)
			if err != nil {
				return Fail(CodeError, fmt.Errorf("provenance of %s: %w", args[0], err))
			}
			blocks := prov.Blocks
			if block != "" {
				blocks = nil
				for _, b := range prov.Blocks {
					if b.Key == block {
						blocks = append(blocks, b)
					}
				}
				if len(blocks) == 0 {
					return Failf(CodeError, "page %s has no block with key %q", args[0], block)
				}
			}
			if blocks == nil {
				blocks = []api.BlockHistory{}
			}
			data := explainData{Page: *ref, Title: prov.Page.Title, Blocks: blocks}
			a.printExplain(data, block != "")
			return a.ui.Result(data)
		},
	}
	cmd.Flags().StringVar(&block, "block", "", "show the full history of one block key")
	return cmd
}

func (a *app) printExplain(d explainData, full bool) {
	p := a.ui
	p.Println("%s  %s/%s/%s", p.Bold(firstNonEmpty(d.Title, d.Page.PageSlug)), d.Page.SiteSlug, d.Page.SpaceSlug, d.Page.PageSlug)
	if len(d.Blocks) == 0 {
		p.Println("No pipeline has written this page yet.")
		return
	}
	if full {
		for _, b := range d.Blocks {
			p.Println("%s", b.Key)
			rows := [][]string{}
			for _, h := range b.History {
				rows = append(rows, historyRow(h))
			}
			p.Table("  ", rows)
		}
		return
	}
	rows := [][]string{{"BLOCK", "LAST WRITER", "STATE", "AT", "HISTORY"}}
	for _, b := range d.Blocks {
		if len(b.History) == 0 {
			rows = append(rows, []string{b.Key, "-", "-", "-", "0"})
			continue
		}
		h := b.History[0]
		writer := h.Repo + "/" + h.Pass + "@" + shortSHA(h.CommitSHA)
		prev := ""
		if len(b.History) > 1 {
			prev = fmt.Sprintf("%d writes, previously %s", len(b.History), b.History[1].Repo)
		} else {
			prev = "1 write"
		}
		rows = append(rows, []string{b.Key, writer, h.State, h.At, prev})
	}
	p.Table("", rows)
}

func historyRow(h api.ProvenanceEntry) []string {
	refs := strings.Join(h.SourceRefs, ", ")
	return []string{h.At, h.Action, h.State, h.Repo + "/" + h.Pass + "@" + shortSHA(h.CommitSHA), orDash(h.RunID), orDash(refs)}
}
