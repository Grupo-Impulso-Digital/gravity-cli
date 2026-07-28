package git

import (
	"net/url"
	"strings"
)

var defaultPorts = []string{":22", ":80", ":443"}

// NormalizeRemoteKey reduces a git remote URL to the stable repo identity the platform keys connected_repo on.
func NormalizeRemoteKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			s = u.Host + u.Path
		}
	} else if i := strings.Index(s, "@"); i >= 0 {
		s = strings.Replace(s[i+1:], ":", "/", 1)
	}
	s = strings.TrimPrefix(s, "/")

	host, path, hasPath := strings.Cut(s, "/")
	host = strings.ToLower(host)
	for _, port := range defaultPorts {
		host = strings.TrimSuffix(host, port)
	}
	s = host
	if hasPath {
		s += "/" + path
	}

	s = strings.TrimRight(s, "/")
	if len(s) >= 4 && strings.EqualFold(s[len(s)-4:], ".git") {
		s = s[:len(s)-4]
	}
	return strings.TrimRight(s, "/")
}
