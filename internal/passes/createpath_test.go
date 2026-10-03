package passes

import (
	"strings"
	"testing"
)

func TestCreatePath(t *testing.T) {
	cases := []struct {
		prefix, planned []string
		want            string
	}{
		{[]string{"guides"}, nil, "guides"},
		{[]string{"guides"}, []string{"guides", "CI providers"}, "guides/ci-providers"},
		{[]string{"guides"}, []string{"How-to Guides"}, "guides/how-to-guides"},
		{nil, []string{"Set up", ""}, "set-up"},
		{nil, nil, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(createPath(tc.prefix, tc.planned), "/"); got != tc.want {
			t.Errorf("createPath(%v, %v) = %q, want %q", tc.prefix, tc.planned, got, tc.want)
		}
	}
}
