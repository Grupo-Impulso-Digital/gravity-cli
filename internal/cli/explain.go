package cli

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

type explainBlock struct {
	Key        string                `json:"key"`
	Type       string                `json:"type,omitempty"`
	Ownership  string                `json:"ownership,omitempty"`
	Present    bool                  `json:"present"`
	LastWriter *api.ProvenanceEntry  `json:"lastWriter"`
	History    []api.ProvenanceEntry `json:"history"`
}

type explainTranslation struct {
	Language string `json:"language"`
	Path     string `json:"path"`
}

type explainData struct {
	Page         api.ResolvedPage     `json:"page"`
	Title        string               `json:"title"`
	Lock         *api.PageLock        `json:"lock"`
	Translations []explainTranslation `json:"translations"`
	Blocks       []explainBlock       `json:"blocks"`
}

func newExplainCmd(a *app) *cobra.Command {
	var block string
	cmd := &cobra.Command{
		Use:   "explain <page>",
		Short: "Show which repository, pass, commit and run produced each block of a page",
		Long:  "Show the provenance of every block of a page: the last writer (repository, pass, commit, run), the earlier writers, and whether the page is locked to a repository file. <page> is a page id, <site>/<space>/<page>, or a viewer URL.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			creds, err := a.credentials(a.repoAPIURL(ctx))
			if err != nil {
				return err
			}
			if err := requireToken(creds); err != nil {
				return err
			}
			client := a.client(creds)
			who, err := client.WhoAmI(ctx)
			if err != nil {
				return explainAPI(err)
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
			content, err := client.Page(ctx, ref.PageID, api.PageQuery{State: "draft", Format: "json"})
			if err != nil {
				a.ui.Debugf("page content of %s: %v", ref.PageID, err)
				content = nil
			}
			data := explainData{Page: *ref, Title: prov.Page.Title, Lock: prov.Page.Lock, Blocks: mergeProvenance(prov, content)}
			if content != nil {
				data.Title = firstNonEmpty(data.Title, content.Page.Title)
				if data.Lock == nil {
					data.Lock = content.Page.Lock
				}
			}
			data.Translations = a.localTranslations(ctx, data.Lock)
			if block != "" {
				var only []explainBlock
				for _, b := range data.Blocks {
					if b.Key == block {
						only = append(only, b)
					}
				}
				if len(only) == 0 {
					return Failf(CodeError, "page %s has no block with key %q", args[0], block)
				}
				data.Blocks = only
			}
			a.printExplain(data, block != "")
			return a.ui.Result(data)
		},
	}
	cmd.Flags().StringVar(&block, "block", "", "show the full history of one block key")
	return cmd
}

func (a *app) localTranslations(ctx context.Context, lock *api.PageLock) []explainTranslation {
	out := []explainTranslation{}
	if lock == nil || lock.Path == "" {
		return out
	}
	dir, err := a.workdir()
	if err != nil {
		return out
	}
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return out
	}
	if lock.Repo != nil && lock.Repo.RemoteKey != "" {
		remote, _ := repo.RemoteURL(ctx)
		if remote == "" || git.NormalizeRemoteKey(remote) != lock.Repo.RemoteKey {
			return out
		}
	}
	files, err := repo.ListFiles(ctx, path.Join(path.Dir(lock.Path), "*"))
	if err != nil {
		return out
	}
	stem := strings.TrimSuffix(lock.Path, path.Ext(lock.Path))
	for _, f := range files {
		if s, lang, ok := verbatim.SuffixLang(f); ok && s == stem && f != lock.Path {
			out = append(out, explainTranslation{Language: lang, Path: f})
		}
	}
	return out
}

func mergeProvenance(prov *api.PageProvenance, content *api.PageContent) []explainBlock {
	history := map[string][]api.ProvenanceEntry{}
	for _, b := range prov.Blocks {
		history[b.Key] = b.History
	}
	out := []explainBlock{}
	seen := map[string]bool{}
	add := func(b explainBlock) {
		if h := history[b.Key]; len(h) > 0 {
			b.History = h
			last := h[0]
			b.LastWriter = &last
		}
		if b.History == nil {
			b.History = []api.ProvenanceEntry{}
		}
		seen[b.Key] = true
		out = append(out, b)
	}
	if content != nil {
		for _, b := range content.Blocks {
			if b.Key == "" || seen[b.Key] {
				continue
			}
			add(explainBlock{Key: b.Key, Type: b.Type, Ownership: b.Ownership, Present: true})
		}
	}
	for _, b := range prov.Blocks {
		if !seen[b.Key] {
			add(explainBlock{Key: b.Key, Present: content == nil})
		}
	}
	return out
}

func writerLabel(h api.ProvenanceEntry) string {
	label := firstNonEmpty(h.Repo, "-")
	if h.Pass != "" {
		label += "/" + h.Pass
	}
	if sha := shortSHA(h.CommitSHA); sha != "" {
		label += "@" + sha
	}
	return label
}

func historyLabel(b explainBlock) string {
	switch n := len(b.History); {
	case n == 0 && b.Ownership == api.OwnershipHuman:
		return "written in Gravity"
	case n == 0:
		return "no pipeline writes"
	case n == 1:
		return "1 write"
	default:
		prev := b.History[1]
		return fmt.Sprintf("%d writes, previously %s", n, firstNonEmpty(prev.Repo, "-")+"@"+shortSHA(prev.CommitSHA))
	}
}

func lockLine(l *api.PageLock) string {
	where := l.Path
	if l.Repo != nil {
		where = l.Repo.Label() + "/" + l.Path
	}
	if l.Branch != "" {
		where += "@" + l.Branch
	}
	line := "Locked: managed in " + where
	if l.Pass != "" {
		line += " (pass " + l.Pass + ")"
	}
	line += "; edit it in the repository"
	if l.URL != "" {
		line += ": " + l.URL
	}
	return line
}

func (a *app) printExplain(d explainData, full bool) {
	p := a.ui
	p.Println("%s  %s/%s/%s", p.Bold(firstNonEmpty(d.Title, d.Page.PageSlug)), d.Page.SiteSlug, d.Page.SpaceSlug, d.Page.PageSlug)
	if d.Lock != nil {
		p.Println("%s", lockLine(d.Lock))
	}
	if len(d.Translations) > 0 {
		var parts []string
		for _, t := range d.Translations {
			parts = append(parts, t.Language+" "+t.Path)
		}
		p.Println("Translations from the repository: %s", strings.Join(parts, ", "))
	}
	if len(d.Blocks) == 0 {
		p.Println("No pipeline has written this page yet.")
		return
	}
	if full {
		for _, b := range d.Blocks {
			title := b.Key
			if b.Ownership != "" {
				title += "  (" + b.Ownership + ")"
			}
			p.Println("%s", title)
			if len(b.History) == 0 {
				p.Println("  %s", historyLabel(b))
				continue
			}
			rows := [][]string{{"AT", "ACTION", "STATE", "WRITER", "RUN", "SOURCE"}}
			for _, h := range b.History {
				rows = append(rows, historyRow(h))
			}
			p.Table("  ", rows)
		}
		return
	}
	rows := [][]string{{"BLOCK", "OWNER", "LAST WRITER", "RUN", "STATE", "AT", "HISTORY"}}
	for _, b := range d.Blocks {
		owner := orDash(b.Ownership)
		if !b.Present {
			owner = "removed"
		}
		if b.LastWriter == nil {
			rows = append(rows, []string{b.Key, owner, "-", "-", "-", "-", historyLabel(b)})
			continue
		}
		h := *b.LastWriter
		rows = append(rows, []string{b.Key, owner, writerLabel(h), orDash(h.RunID), orDash(h.State), orDash(h.At), historyLabel(b)})
	}
	p.Table("", rows)
}

func historyRow(h api.ProvenanceEntry) []string {
	return []string{h.At, h.Action, h.State, writerLabel(h), orDash(h.RunID), orDash(strings.Join(h.SourceRefs, ", "))}
}
