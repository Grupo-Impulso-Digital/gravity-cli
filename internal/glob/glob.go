// Package glob matches repository-relative paths against doublestar globs.
package glob

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern; `**` as a whole segment matches any number of segments.
func Match(pattern, name string) bool {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	for _, p := range expandBraces(pattern) {
		p = strings.TrimPrefix(p, "./")
		if matchSegments(splitSegments(p), splitSegments(name)) {
			return true
		}
	}
	return false
}

// MatchAny reports whether name matches at least one of patterns.
func MatchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

// HasMeta reports whether pattern contains glob syntax.
func HasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[]{}")
}

// Root returns the longest leading directory of pattern that contains no glob syntax.
func Root(pattern string) string {
	segs := splitSegments(pattern)
	var out []string
	for i, s := range segs {
		if HasMeta(s) || i == len(segs)-1 {
			break
		}
		out = append(out, s)
	}
	return strings.Join(out, "/")
}

func splitSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for len(rest) > 0 && rest[0] == "**" {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

func expandBraces(p string) []string {
	open := strings.IndexByte(p, '{')
	if open < 0 {
		return []string{p}
	}
	depth, end := 0, -1
	for i := open; i < len(p); i++ {
		switch p[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return []string{p}
	}
	var alts []string
	depth, start := 0, open+1
	for i := open + 1; i < end; i++ {
		switch p[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				alts = append(alts, p[start:i])
				start = i + 1
			}
		}
	}
	alts = append(alts, p[start:end])
	var out []string
	for _, a := range alts {
		out = append(out, expandBraces(p[:open]+a+p[end+1:])...)
	}
	return out
}
