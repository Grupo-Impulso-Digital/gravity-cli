package docs

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func block(typ string, content any) api.ContentBlock {
	raw, _ := json.Marshal(content)
	return api.ContentBlock{Type: typ, Content: raw}
}

func TestBlockText(t *testing.T) {
	cases := []struct {
		name string
		blk  api.ContentBlock
		want string
	}{
		{"heading", block("heading", map[string]any{"text": "Install", "level": 2}), "## Install"},
		{"prose", block("prose", map[string]any{"text": "Run  the\ninstaller."}), "Run the installer."},
		{"code skips language", block("code", map[string]any{"text": "make build", "language": "sh"}), "make build"},
		{"api op", block("api", map[string]any{"method": "GET", "path": "/v1/users", "summary": "List users", "params": []any{}}), "GET /v1/users List users"},
		{"table rows", block("table", map[string]any{"rows": []any{[]any{"a", "b"}, []any{"c", "d"}}}), "a b c d"},
		{"empty", api.ContentBlock{Type: "prose"}, ""},
		{"invalid json", api.ContentBlock{Type: "prose", Content: json.RawMessage(`{`)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BlockText(tc.blk); got != tc.want {
				t.Errorf("BlockText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPageDigestIsBounded(t *testing.T) {
	blocks := []api.ContentBlock{
		block("heading", map[string]any{"text": "Billing"}),
		block("prose", map[string]any{"text": "Invoices are issued monthly."}),
	}
	full := PageDigest(blocks, 1000)
	if !strings.Contains(full, "## Billing") || !strings.Contains(full, "Invoices are issued monthly.") {
		t.Errorf("digest = %q", full)
	}
	long := []api.ContentBlock{block("prose", map[string]any{"text": strings.Repeat("é", 400)})}
	got := PageDigest(long, 100)
	if !strings.Contains(got, "truncated at 100 bytes") {
		t.Errorf("expected a truncation marker, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Error("digest cut a UTF-8 sequence")
	}
	if len(got) > 160 {
		t.Errorf("digest is %d bytes, want it bounded near 100", len(got))
	}
}
