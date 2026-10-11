package agent

import (
	"fmt"
	"strings"
	"unicode"
)

var stopwordLists = map[string]string{
	"en": "the and of to is in that it for with as are this be by on you your can from an or not when which will if each use returns was has have these those",
	"fr": "le la les des du et est une un pour dans que qui avec sur pas par au aux ce cette vous sont ou être peut lorsque chaque il elle ses leur nous",
	"es": "el la los las del y es una para con que por se su como al lo pero sus este esta puede cuando cada son sobre también o no",
	"de": "der die das und ist mit für nicht ein eine den dem zu auf von sie wird werden oder wenn auch bei kann nach sich im in um an es",
	"pt": "o os as do da dos das e é um uma para com não em no na por se que como ao mais pode quando cada são seu sua",
}

var languageNames = map[string]string{"en": "English", "fr": "French", "es": "Spanish", "de": "German", "pt": "Portuguese"}

var stopwords = buildStopwords()

func buildStopwords() map[string]string {
	seen := map[string][]string{}
	for lang, list := range stopwordLists {
		for _, w := range strings.Fields(list) {
			if !oneOf(lang, seen[w]) {
				seen[w] = append(seen[w], lang)
			}
		}
	}
	out := map[string]string{}
	for w, langs := range seen {
		if len(langs) == 1 {
			out[w] = langs[0]
		}
	}
	return out
}

type languageVerdict struct {
	severity string
	message  string
}

func languageScores(text string) (map[string]int, int) {
	scores := map[string]int{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	for _, w := range words {
		if lang, ok := stopwords[w]; ok {
			scores[lang]++
		}
	}
	return scores, len(words)
}

func detectLanguage(text, site string) *languageVerdict {
	site = strings.ToLower(strings.TrimSpace(site))
	if i := strings.IndexAny(site, "-_"); i > 0 {
		site = site[:i]
	}
	if _, supported := stopwordLists[site]; !supported {
		return nil
	}
	scores, words := languageScores(text)
	other, best := "", 0
	for lang, n := range scores {
		if lang != site && (n > best || n == best && lang < other) {
			other, best = lang, n
		}
	}
	own := scores[site]
	if other == "" || best < languageMinHits || best <= own {
		return nil
	}
	msg := fmt.Sprintf("the authored text (%d words) reads as %s, not %s (%d vs %d common words); write every reader-facing text in %s", words, languageNames[other], languageNames[site], best, own, languageNames[site])
	if words >= languageMinWords && best > 2*own {
		return &languageVerdict{severity: SeverityError, message: msg}
	}
	if words < languageMinWords && best <= 2*own {
		return nil
	}
	return &languageVerdict{severity: SeverityWarning, message: msg}
}
