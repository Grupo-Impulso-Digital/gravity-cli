package version

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string, ok bool) func() (string, bool) {
		return func() (string, bool) { return v, ok }
	}
	cases := []struct {
		name    string
		stamped string
		bi      func() (string, bool)
		want    string
	}{
		{"stamped wins", "v0.3.0", info("v0.2.0", true), "0.3.0"},
		{"stamped without v", "0.3.0", nil, "0.3.0"},
		{"go install build info", "", info("v0.3.1", true), "0.3.1"},
		{"devel build", "", info("(devel)", true), "dev"},
		{"no build info", "", info("", false), "dev"},
		{"nil reader", "  ", nil, "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolve(tc.stamped, tc.bi); got != tc.want {
				t.Errorf("resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUserAgentCarriesVersion(t *testing.T) {
	ua := UserAgent()
	if !strings.HasPrefix(ua, "gravity-cli/"+String()+" (") {
		t.Errorf("UserAgent = %q, want gravity-cli/<version> (<os>/<arch>)", ua)
	}
}
