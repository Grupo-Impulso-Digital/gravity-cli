package normalize

import (
	"encoding/json"
	"os"
	"testing"
)

func loadFixture(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

func TestProductSlugGolden(t *testing.T) {
	var cases []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	}
	loadFixture(t, "product-slug.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no fixtures")
	}
	for _, c := range cases {
		if got := ProductSlug(c.In); got != c.Out {
			t.Errorf("ProductSlug(%q) = %q, want %q", c.In, got, c.Out)
		}
	}
}

func TestAPIUnitKeyGolden(t *testing.T) {
	var cases []struct {
		In struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"in"`
		Out string `json:"out"`
	}
	loadFixture(t, "api-unit-key.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no fixtures")
	}
	for _, c := range cases {
		got := APIUnitKey(c.In.Method, c.In.Path)
		if got != c.Out {
			t.Errorf("APIUnitKey(%q, %q) = %q, want %q", c.In.Method, c.In.Path, got, c.Out)
		}
		if len(got) > maxUnitKey {
			t.Errorf("key longer than %d: %q", maxUnitKey, got)
		}
	}
}

func TestContentHashGolden(t *testing.T) {
	var cases []struct {
		In struct {
			Type    string `json:"type"`
			Content any    `json:"content"`
		} `json:"in"`
		Canonical string `json:"canonical"`
		Out       string `json:"out"`
	}
	loadFixture(t, "content-hash.json", &cases)
	for _, c := range cases {
		canon, err := CanonicalJSON(map[string]any{"type": c.In.Type, "content": c.In.Content})
		if err != nil {
			t.Fatal(err)
		}
		if string(canon) != c.Canonical {
			t.Errorf("canonical = %s, want %s", canon, c.Canonical)
		}
		got, err := ContentHash(c.In.Type, c.In.Content)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.Out {
			t.Errorf("ContentHash = %s, want %s", got, c.Out)
		}
	}
}

func TestCanonicalJSONNumbersAndEscapes(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{"b": 1.5, "a": []any{1, 1e21, "<&>\u0001"}, "c": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[1,1e+21,"<&>\u0001"],"b":1.5,"c":null}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
