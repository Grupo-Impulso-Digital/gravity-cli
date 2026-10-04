package verbatim

import (
	"regexp"
	"strings"
)

var (
	scopedPackage = regexp.MustCompile(`^@[a-z0-9][a-z0-9._-]*/([a-z0-9][a-z0-9._-]*)$`)
	barePackage   = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)+$`)
)

// HumanizeTitle turns a package-name title such as @scope/polaris-admin or polaris-admin into Polaris Admin.
func HumanizeTitle(s string) (string, bool) {
	t := strings.TrimSpace(s)
	name := ""
	if m := scopedPackage.FindStringSubmatch(t); m != nil {
		name = m[1]
	} else if barePackage.MatchString(t) {
		name = t
	}
	if name == "" {
		return s, false
	}
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	if len(words) == 0 {
		return s, false
	}
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " "), true
}

func isScopedPackage(s string) bool {
	return scopedPackage.MatchString(strings.TrimSpace(s))
}

func normTitle(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func sameTitle(heading, title string) bool {
	if strings.TrimSpace(title) == "" {
		return false
	}
	want := normTitle(title)
	if normTitle(heading) == want {
		return true
	}
	h, ok := HumanizeTitle(heading)
	return ok && normTitle(h) == want
}
