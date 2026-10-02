// Package normalize implements the identifiers shared with the platform: product slugs and API unit keys.
package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	maxProductSlug = 63
	maxUnitKey     = 128
	unitKeyPrefix  = 111
)

// ProductSlug derives a product slug from a free-form name.
func ProductSlug(raw string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > maxProductSlug {
		s = strings.TrimRight(s[:maxProductSlug], "-")
	}
	if s == "" {
		return "default"
	}
	return s
}

// APIUnitKey returns the canonical product unit key of an API operation.
func APIUnitKey(method, path string) string {
	m := strings.ToLower(method)
	var b strings.Builder
	inParam := false
	for _, r := range path {
		switch {
		case r == '{' && !inParam:
			inParam = true
			b.WriteByte(':')
		case r == '}' && inParam:
			inParam = false
		default:
			b.WriteRune(r)
		}
	}
	p := strings.ToLower(b.String())
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	var clean strings.Builder
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:/-", r) {
			clean.WriteRune(r)
		} else {
			clean.WriteByte('-')
		}
	}
	k := "api:" + m + ":" + clean.String()
	if len(k) > maxUnitKey {
		sum := sha256.Sum256([]byte(k))
		k = k[:unitKeyPrefix] + "." + hex.EncodeToString(sum[:])[:16]
	}
	return k
}
