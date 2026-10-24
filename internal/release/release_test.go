package release_test

import (
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/release"
)

func TestNext(t *testing.T) {
	tags := []string{"v0.3.0", "v1", "v1.0.4", "v1.1.0", "v1.10.0-rc.1", "v1.2.0-rc.1", "v1.2.0-rc.3", "v1.2.0-rc.x", "prod-2026.10.01-1"}
	cases := []struct {
		tags      []string
		bump      string
		candidate bool
		want      string
	}{
		{tags, "patch", false, "v1.1.1"},
		{tags, "minor", false, "v1.2.0"},
		{tags, "major", false, "v2.0.0"},
		{tags, "minor", true, "v1.2.0-rc.4"},
		{tags, "patch", true, "v1.1.1-rc.1"},
		{tags, "major", true, "v2.0.0-rc.1"},
		{append(tags, "v1.2.0"), "minor", true, "v1.3.0-rc.1"},
		{[]string{"v1.9.0", "v1.10.0"}, "patch", false, "v1.10.1"},
		{nil, "minor", false, release.FirstRelease},
		{[]string{"v1.0.0-rc.1"}, "patch", true, "v1.0.0-rc.2"},
	}
	for _, c := range cases {
		got, err := release.Next(c.tags, c.bump, c.candidate)
		if err != nil || got != c.want {
			t.Errorf("Next(%v, %s, rc=%v) = %q, %v; want %q", c.tags, c.bump, c.candidate, got, err, c.want)
		}
	}
	if latest := release.Latest(tags); latest != "v1.1.0" {
		t.Errorf("Latest = %q, want v1.1.0", latest)
	}
	if _, err := release.Next(tags, "pre", false); err == nil {
		t.Error("an unknown bump must fail")
	}
}
