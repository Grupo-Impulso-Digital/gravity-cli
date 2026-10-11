package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Check severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Check codes of check_page.
const (
	CheckLanguage      = "E1"
	CheckCodeInProse   = "E2"
	CheckMarkdownHead  = "E3"
	CheckPlanningVoice = "E4"
	CheckOutputMarker  = "E5"
	CheckCoverage      = "E7"
	CheckShortPage     = "W1"
	CheckDuplicate     = "W2"
)

// DefaultOutputMarkers are used when the bundle lists none.
var DefaultOutputMarkers = []string{"@internal", "TODO:", "FIXME", "XXX"}

const (
	languageMinWords = 150
	languageMinHits  = 5
	shortPageWords   = 120
	minJSONBytes     = 20
	minIndentedLines = 3
	headingLikeChars = 60
)

// Finding is one result of the deterministic page checks.
type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	BlockKey string `json:"blockKey,omitempty"`
	Message  string `json:"message"`
}

// String renders the finding on one line.
func (f Finding) String() string {
	where := ""
	if f.BlockKey != "" {
		where = " [" + f.BlockKey + "]"
	}
	return fmt.Sprintf("%s %s%s: %s", f.Code, f.Severity, where, f.Message)
}

// Errors keeps the error findings.
func Errors(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

func formatFindings(findings []Finding) string {
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		lines = append(lines, "- "+f.String())
	}
	return strings.Join(lines, "\n")
}

func checkStatus(findings []Finding) string {
	errs := len(Errors(findings))
	warns := len(findings) - errs
	if len(findings) == 0 {
		return "Checks: no findings.\n"
	}
	return fmt.Sprintf("Checks: %d error(s), %d warning(s); check_page lists them.\n", errs, warns)
}

var (
	readerTypes = []string{BlockProse, BlockHeading, BlockList, BlockCallout, BlockQuote, BlockTable}
	proseTypes  = []string{BlockProse, BlockList, BlockCallout, BlockQuote}

	markdownHeading = regexp.MustCompile(`(?m)^[ \t]{0,3}#{1,6}[ \t]`)
	boldOnly        = regexp.MustCompile(`^\*\*[^*]+\*\*$`)
	planningVoice   = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(this (section|page)|the (section|page)) (should|will|must|is meant to) (document|describe|cover|include|explain|list)\b`),
		regexp.MustCompile(`(?i)\b(cette|la) (section|page) (devrait|va|doit|est censée|sert à) (documenter|décrire|couvrir|inclure|expliquer|lister|présenter)`),
		regexp.MustCompile(`(?i)\bthe (planner|draft|outline)\b`),
		regexp.MustCompile(`(?i)\b(le planificateur|le brouillon|le plan de la page)\b`),
	}
	pathParam = regexp.MustCompile(`\{([^{}/\s]+)\}`)
)

func codeInText(text string) string {
	if strings.Contains(text, "```") || strings.Contains(text, "~~~") {
		return "a fenced code block"
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '{' && text[i] != '[' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var v any
		if dec.Decode(&v) != nil {
			continue
		}
		switch v.(type) {
		case map[string]any, []any:
			if dec.InputOffset() >= minJSONBytes {
				return "a JSON value"
			}
		}
	}
	run := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			run++
			if run >= minIndentedLines {
				return "indented code lines"
			}
			continue
		}
		run = 0
	}
	return ""
}

func endsSentence(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".!?:;)…", r)
}

func countWords(text string) int {
	return len(strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-' }))
}

func normalizeCoverage(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "`", ""))
	s = pathParam.ReplaceAllString(s, ":$1")
	return strings.Join(strings.Fields(s), " ")
}

// Check runs the deterministic checks on the authored and changed blocks; uncovered declares must-cover tokens left out.
func (d *Draft) Check(uncovered []Uncovered) []Finding {
	var findings []Finding
	add := func(code, severity, key, format string, args ...any) {
		findings = append(findings, Finding{Code: code, Severity: severity, BlockKey: key, Message: fmt.Sprintf(format, args...)})
	}
	var reader []string
	for _, b := range d.blocks {
		if !d.authored(b) {
			continue
		}
		text := b.Text()
		if oneOf(b.Type, readerTypes) {
			reader = append(reader, text)
		}
		if oneOf(b.Type, proseTypes) {
			if what := codeInText(text); what != "" {
				add(CheckCodeInProse, SeverityError, b.Key, "the text holds %s; put code, JSON, YAML and commands in a code block with a language", what)
			}
		}
		if oneOf(b.Type, readerTypes) && b.Type != BlockHeading {
			trimmed := strings.TrimSpace(text)
			switch {
			case markdownHeading.MatchString(text):
				add(CheckMarkdownHead, SeverityError, b.Key, "the text holds a markdown heading; use a heading block")
			case boldOnly.MatchString(trimmed):
				add(CheckMarkdownHead, SeverityError, b.Key, "the text is only bold; use a heading block")
			case b.Type == BlockProse && trimmed != "" && !strings.Contains(trimmed, "\n") && len(trimmed) <= headingLikeChars && !endsSentence(trimmed):
				add(CheckMarkdownHead, SeverityWarning, b.Key, "a short line without a period reads like a heading; use a heading block if it is one")
			}
		}
		if oneOf(b.Type, readerTypes) {
			for _, re := range planningVoice {
				if m := re.FindString(text); m != "" {
					add(CheckPlanningVoice, SeverityError, b.Key, "%q describes the page instead of the product; write for the reader", m)
					break
				}
			}
		}
		for _, m := range d.opts.Markers {
			if m != "" && strings.Contains(text, m) {
				add(CheckOutputMarker, SeverityError, b.Key, "the text holds the marker %q; leave internal notes out of the page", m)
				break
			}
		}
	}
	if verdict := detectLanguage(strings.Join(reader, "\n"), d.opts.Language); verdict != nil {
		add(CheckLanguage, verdict.severity, "", "%s", verdict.message)
	}
	if d.opts.Mode != DraftReach {
		var corpus []string
		for _, b := range d.blocks {
			corpus = append(corpus, b.Text())
		}
		page := normalizeCoverage(strings.Join(corpus, "\n"))
		declared := map[string]bool{}
		for _, u := range uncovered {
			declared[normalizeCoverage(u.Token)] = true
		}
		for _, mc := range d.opts.MustCover {
			token := normalizeCoverage(mc.Token)
			if token == "" || declared[token] || strings.Contains(page, token) {
				continue
			}
			add(CheckCoverage, SeverityError, "", "must_cover token %q is not on the page; cover it, or declare it in submit_page uncovered with the reason", mc.Token)
		}
	}
	if d.opts.Mode == DraftCreate {
		words := 0
		for _, b := range d.blocks {
			if oneOf(b.Type, readerTypes) {
				words += countWords(b.Text())
			}
		}
		if words < shortPageWords {
			add(CheckShortPage, SeverityWarning, "", "the page has %d words; a new page usually needs at least %d", words, shortPageWords)
		}
	}
	for i := 1; i < len(d.blocks); i++ {
		a, b := d.blocks[i-1], d.blocks[i]
		if a.Type != b.Type || !d.authored(a) && !d.authored(b) {
			continue
		}
		ta, tb := strings.Join(strings.Fields(a.Text()), " "), strings.Join(strings.Fields(b.Text()), " ")
		if ta != "" && strings.EqualFold(ta, tb) {
			add(CheckDuplicate, SeverityWarning, b.Key, "the block repeats the block before it (%s)", a.Key)
		}
	}
	return findings
}
