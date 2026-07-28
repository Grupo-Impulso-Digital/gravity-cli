package git_test

import (
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/git"
)

func TestNormalizeRemoteKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://GitHub.com/Acme/orbit-api.git", "github.com/Acme/orbit-api"},
		{"scp-like", "git@github.com:Acme/orbit-api.git", "github.com/Acme/orbit-api"},
		{"ssh with default port", "ssh://git@github.com:22/Acme/orbit-api", "github.com/Acme/orbit-api"},
		{"credentials and trailing slash", "https://u:p@github.com/Acme/orbit-api/", "github.com/Acme/orbit-api"},
		{"nested groups", "git@gitlab.com:group/sub/proj.git", "gitlab.com/group/sub/proj"},
		{"http default port", "http://Git.Internal:80/team/docs.git", "git.internal/team/docs"},
		{"https default port", "https://git.internal:443/team/docs", "git.internal/team/docs"},
		{"non-default port kept", "ssh://git@git.internal:2222/team/docs.git", "git.internal:2222/team/docs"},
		{"already normalized", "github.com/Acme/orbit-api", "github.com/Acme/orbit-api"},
		{"config fallback shape", "orbit/orbit-api", "orbit/orbit-api"},
		{"empty", "", ""},
		{"whitespace", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := git.NormalizeRemoteKey(tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeRemoteKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := git.NormalizeRemoteKey(got); again != got {
				t.Errorf("not idempotent: NormalizeRemoteKey(%q) = %q", got, again)
			}
		})
	}
}
