package verbatim_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/verbatim"
)

const locatedDoc = `---
title: Guide
---
import Tabs from '@theme/Tabs'

# Guide

Intro paragraph
over two lines.

- first
- second
  continued

` + "```go" + `
fmt.Println("hi")
` + "```" + `

:::note Heads up
Careful here.
:::

<Banner>
Inside a component.
</Banner>

A claim.[^1]

[^1]: The footnote.

---
`

func spans(t *testing.T, opts verbatim.Options) []string {
	t.Helper()
	fm, body, err := verbatim.SplitFrontMatter([]byte(locatedDoc))
	if err != nil {
		t.Fatal(err)
	}
	opts.LineOffset = fm.BodyLine
	doc := verbatim.Convert(body, opts)
	out := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		sb := b.SourceBinding
		out = append(out, fmt.Sprintf("%s %s:%d-%d@%s", b.Type, sb.Path, sb.LineStart, sb.LineEnd, sb.SHA))
	}
	return out
}

func TestConvertLocatesBlocksInTheOriginalFile(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	got := spans(t, verbatim.Options{Path: "docs/guide.mdx", Hash: "sha256:x", MDX: true, Locate: true, Commit: sha})
	want := []string{
		"heading docs/guide.mdx:6-6@" + sha,
		"prose docs/guide.mdx:8-9@" + sha,
		"list docs/guide.mdx:11-11@" + sha,
		"list docs/guide.mdx:12-13@" + sha,
		"code docs/guide.mdx:15-17@" + sha,
		"callout docs/guide.mdx:19-21@" + sha,
		"prose docs/guide.mdx:23-25@" + sha,
		"prose docs/guide.mdx:27-27@" + sha,
		"divider docs/guide.mdx:31-31@" + sha,
		"divider docs/guide.mdx:0-0@" + sha,
		"list docs/guide.mdx:0-0@" + sha,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("spans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestConvertLeavesBindingsUnlocatedByDefault(t *testing.T) {
	for _, s := range spans(t, verbatim.Options{Path: "docs/guide.mdx", MDX: true, Commit: "abcdef1"}) {
		if !strings.HasSuffix(s, " :0-0@") {
			t.Fatalf("unexpected location %q", s)
		}
	}
}

func TestConvertDropsACommitThatIsNotASha(t *testing.T) {
	for _, s := range spans(t, verbatim.Options{Path: "a.md", MDX: true, Locate: true, Commit: "main"}) {
		if !strings.HasSuffix(s, "@") {
			t.Fatalf("unexpected sha in %q", s)
		}
	}
}
