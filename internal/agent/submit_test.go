package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
)

func TestNormalizeContent(t *testing.T) {
	cases := []struct {
		typ, in, want string
	}{
		{"list", `{"text":"a","variant":"unordered"}`, `{"text":"a","variant":"bulleted"}`},
		{"list", `{"text":"a","variant":"Ordered"}`, `{"text":"a","variant":"numbered"}`},
		{"list", `{"text":"a","variant":"checklist","checked":"yes"}`, `{"text":"a","variant":"bulleted"}`},
		{"list", `{"text":"a","variant":"task","checked":true}`, `{"text":"a","variant":"bulleted"}`},
		{"list", `{"items":["a","b"]}`, `{"items":["a","b"],"variant":"bulleted"}`},
		{"list", `{"text":"a","variant":"weird"}`, `{"text":"a","variant":"bulleted"}`},
		{"heading", `{"text":"T","level":"h4"}`, `{"level":3,"text":"T"}`},
		{"heading", `{"text":"T","level":0}`, `{"level":1,"text":"T"}`},
		{"callout", `{"text":"x","variant":3}`, `{"text":"x"}`},
		{"prose", `{"text":"x"}`, `{"text":"x"}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(agent.NormalizeContent(tc.typ, json.RawMessage(tc.in)))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s %s = %s (%v), want %s", tc.typ, tc.in, got, err, tc.want)
		}
	}
}

func TestMemoryKind(t *testing.T) {
	for in, want := range map[string]string{"": "fact", "Decision": "decision", "license-rule": "other", "fact": "fact"} {
		if got := agent.MemoryKind(in); got != want {
			t.Errorf("MemoryKind(%q) = %q, want %q", in, got, want)
		}
	}
}
