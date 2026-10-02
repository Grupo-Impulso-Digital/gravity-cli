// Package verbatim converts repository Markdown and MDX files into Gravity's native blocks with high fidelity and maps files to locked pages.
package verbatim

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// FrontMatter holds the recognized front matter keys of a document.
type FrontMatter struct {
	Title       string         `json:"title,omitempty"`
	Slug        string         `json:"slug,omitempty"`
	Description string         `json:"description,omitempty"`
	Position    *int           `json:"position,omitempty"`
	Hidden      bool           `json:"hidden,omitempty"`
	Audiences   []string       `json:"audiences,omitempty"`
	Lang        string         `json:"lang,omitempty"`
	Raw         map[string]any `json:"-"`
}

// SplitFrontMatter separates YAML (---) or TOML (+++) front matter from the body.
func SplitFrontMatter(src []byte) (FrontMatter, []byte, error) {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.SplitAfter(text, "\n")
	fence := strings.TrimRight(lines[0], "\n")
	if fence != "---" && fence != "+++" {
		return FrontMatter{Raw: map[string]any{}}, []byte(text), nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\n") != fence {
			continue
		}
		raw := strings.Join(lines[1:i], "")
		body := strings.Join(lines[i+1:], "")
		var m map[string]any
		var err error
		if fence == "---" {
			err = yaml.Unmarshal([]byte(raw), &m)
		} else {
			m, err = parseTOML(raw)
		}
		if err != nil {
			return FrontMatter{}, nil, fmt.Errorf("front matter: %w", err)
		}
		return frontMatterFrom(m), []byte(body), nil
	}
	return FrontMatter{Raw: map[string]any{}}, []byte(text), nil
}

func frontMatterFrom(m map[string]any) FrontMatter {
	if m == nil {
		m = map[string]any{}
	}
	fm := FrontMatter{Raw: m}
	fm.Title = str(m["title"])
	fm.Slug = strings.Trim(str(m["slug"]), "/")
	if i := strings.LastIndex(fm.Slug, "/"); i >= 0 {
		fm.Slug = fm.Slug[i+1:]
	}
	fm.Description = str(m["description"])
	for _, k := range []string{"sidebar_position", "order", "weight"} {
		if n, ok := number(m[k]); ok {
			fm.Position = &n
			break
		}
	}
	fm.Hidden = truthy(m["draft"]) || truthy(m["hidden"])
	switch a := m["audiences"].(type) {
	case string:
		if a != "" {
			fm.Audiences = []string{a}
		}
	case []any:
		for _, v := range a {
			if s := str(v); s != "" {
				fm.Audiences = append(fm.Audiences, s)
			}
		}
	}
	fm.Lang = str(m["lang"])
	if fm.Lang == "" {
		fm.Lang = str(m["language"])
	}
	return fm
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int, int64, float64, bool:
		return fmt.Sprint(t)
	}
	return ""
}

func number(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	}
	return false
}

func parseTOML(raw string) (map[string]any, error) {
	out := map[string]any{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("toml line %d: expected key = value", i+1)
		}
		key := strings.Trim(strings.TrimSpace(k), `"`)
		out[key] = tomlValue(strings.TrimSpace(v))
	}
	return out, nil
}

func tomlValue(v string) any {
	switch {
	case strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) && len(v) >= 2:
		if s, err := strconv.Unquote(v); err == nil {
			return s
		}
		return v[1 : len(v)-1]
	case strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2:
		return v[1 : len(v)-1]
	case v == "true" || v == "false":
		return v == "true"
	case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
		var list []any
		for _, part := range strings.Split(v[1:len(v)-1], ",") {
			if p := strings.TrimSpace(part); p != "" {
				list = append(list, tomlValue(p))
			}
		}
		return list
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}
