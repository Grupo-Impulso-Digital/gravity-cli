package version

import (
	"runtime"
	"runtime/debug"
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
		{"stamped wins", "v1.0.0", info("v0.2.0", true), "1.0.0"},
		{"stamped without v", "1.0.0", nil, "1.0.0"},
		{"go install build info", "", info("v1.0.1", true), "1.0.1"},
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

func TestUserAgentFormat(t *testing.T) {
	want := "gravity-cli/" + String() + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
	if got := UserAgent(); got != want {
		t.Errorf("UserAgent = %q, want %q", got, want)
	}
}

func TestVCSInfo(t *testing.T) {
	commit, date := vcsInfo([]debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.time", Value: "2026-10-01T00:00:00Z"},
		{Key: "vcs.modified", Value: "true"},
	})
	if commit != "abc123-dirty" || date != "2026-10-01T00:00:00Z" {
		t.Fatalf("vcsInfo = %q %q", commit, date)
	}
}

func TestBuildPlatform(t *testing.T) {
	b := Build()
	if b.Platform != runtime.GOOS+"/"+runtime.GOARCH || !strings.HasPrefix(b.GoVersion, "go") || b.Version == "" {
		t.Fatalf("Build = %+v", b)
	}
}
