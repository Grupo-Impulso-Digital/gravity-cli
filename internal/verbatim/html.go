package verbatim

import (
	"html"
	"regexp"
	"strings"
)

var (
	dropElementRE = regexp.MustCompile(`(?is)<(script|style|iframe)\b[^>]*>.*?</(script|style|iframe)\s*>|<(script|style|iframe)\b[^>]*/>`)
	commentRE     = regexp.MustCompile(`(?s)<!--.*?-->`)
	brRE          = regexp.MustCompile(`(?i)<br\s*/?>`)
	anchorRE      = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a\s*>`)
	imgRE         = regexp.MustCompile(`(?is)<img\b([^>]*?)/?>`)
	strongRE      = regexp.MustCompile(`(?is)<(strong|b)\b[^>]*>(.*?)</(strong|b)\s*>`)
	emRE          = regexp.MustCompile(`(?is)<(em|i)\b[^>]*>(.*?)</(em|i)\s*>`)
	codeRE        = regexp.MustCompile(`(?is)<code\b[^>]*>(.*?)</code\s*>`)
	headingTagRE  = regexp.MustCompile(`(?is)<h([1-6])\b[^>]*>(.*?)</h[1-6]\s*>`)
	liRE          = regexp.MustCompile(`(?is)<li\b[^>]*>`)
	blockEndRE    = regexp.MustCompile(`(?i)</(p|div|li|ul|ol|h[1-6]|tr|table|section|blockquote|pre)\s*>`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]+>`)
	blankRunRE    = regexp.MustCompile(`\n{3,}`)
	htmlAttrRE    = regexp.MustCompile(`(\w[\w-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

func htmlAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range htmlAttrRE.FindAllStringSubmatch(s, -1) {
		out[strings.ToLower(m[1])] = m[2] + m[3]
	}
	return out
}

func stripTags(s string) string {
	return html.UnescapeString(tagRE.ReplaceAllString(s, ""))
}

func (c *converter) htmlToMarkdown(raw string) string {
	s := commentRE.ReplaceAllString(raw, "")
	s = dropElementRE.ReplaceAllString(s, "")
	s = brRE.ReplaceAllString(s, "\n")
	s = imgRE.ReplaceAllStringFunc(s, func(m string) string {
		a := htmlAttrs(imgRE.FindStringSubmatch(m)[1])
		url, ok := c.image(a["src"])
		if !ok {
			return ""
		}
		return "![" + a["alt"] + "](" + url + ")"
	})
	s = anchorRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := anchorRE.FindStringSubmatch(m)
		href := htmlAttrs(sub[1])["href"]
		text := strings.TrimSpace(stripTags(sub[2]))
		if href == "" {
			return text
		}
		return "[" + text + "](" + c.link(href) + ")"
	})
	s = strongRE.ReplaceAllString(s, "**$2**")
	s = emRE.ReplaceAllString(s, "*$2*")
	s = codeRE.ReplaceAllString(s, "`$1`")
	s = headingTagRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := headingTagRE.FindStringSubmatch(m)
		return "\n**" + strings.TrimSpace(stripTags(sub[2])) + "**\n"
	})
	s = liRE.ReplaceAllString(s, "\n- ")
	s = blockEndRE.ReplaceAllString(s, "\n")
	s = stripTags(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(strings.TrimSpace(l), " ")
	}
	s = strings.Join(lines, "\n")
	s = blankRunRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
