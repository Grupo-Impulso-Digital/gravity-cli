package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Block types of the gravity.tools/1 block contract.
const (
	BlockHeading = "heading"
	BlockProse   = "prose"
	BlockList    = "list"
	BlockCode    = "code"
	BlockTable   = "table"
	BlockCallout = "callout"
	BlockQuote   = "quote"
	BlockDivider = "divider"
)

// DefaultBlockContract is used when the bundle leaves the block contract empty.
var DefaultBlockContract = api.BlockContract{
	Types:           []string{BlockHeading, BlockProse, BlockList, BlockCode, BlockTable, BlockCallout, BlockQuote, BlockDivider},
	CalloutVariants: []string{"info", "success", "warning", "danger"},
	ListVariants:    []string{"bulleted", "numbered"},
}

func blockContract(c api.BlockContract) api.BlockContract {
	if len(c.Types) == 0 {
		c.Types = DefaultBlockContract.Types
	}
	if len(c.CalloutVariants) == 0 {
		c.CalloutVariants = DefaultBlockContract.CalloutVariants
	}
	if len(c.ListVariants) == 0 {
		c.ListVariants = DefaultBlockContract.ListVariants
	}
	return c
}

// Block is one block the model authors through the draft tools (gravity.tools/1, flat and portable).
type Block struct {
	Type      string     `json:"type"`
	Text      string     `json:"text,omitempty"`
	Level     int        `json:"level,omitempty"`
	Language  string     `json:"language,omitempty"`
	Variant   string     `json:"variant,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	Header    *bool      `json:"header,omitempty"`
	Audiences []string   `json:"audiences,omitempty"`
	Units     []string   `json:"units,omitempty"`
	Rationale *Rationale `json:"rationale,omitempty"`
}

// UnmarshalJSON accepts a level given as a string ("2", "h2") and a header given as a string.
func (b *Block) UnmarshalJSON(data []byte) error {
	type plain Block
	var raw struct {
		plain
		Level  json.RawMessage `json:"level"`
		Header json.RawMessage `json:"header"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = Block(raw.plain)
	b.Level = 0
	b.Header = nil
	if len(raw.Level) > 0 && string(raw.Level) != "null" {
		var n float64
		var s string
		switch {
		case json.Unmarshal(raw.Level, &n) == nil:
			b.Level = int(n)
		case json.Unmarshal(raw.Level, &s) == nil:
			v, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "h"))
			if err != nil {
				return fmt.Errorf("level %q must be 1, 2 or 3", s)
			}
			b.Level = v
		default:
			return errors.New("level must be 1, 2 or 3")
		}
	}
	if len(raw.Header) > 0 && string(raw.Header) != "null" {
		var v bool
		var s string
		switch {
		case json.Unmarshal(raw.Header, &v) == nil:
		case json.Unmarshal(raw.Header, &s) == nil:
			parsed, err := strconv.ParseBool(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("header %q must be a boolean", s)
			}
			v = parsed
		default:
			return errors.New("header must be a boolean")
		}
		b.Header = &v
	}
	return nil
}

var calloutAliases = map[string]string{
	"note": "info", "tip": "info", "hint": "info", "important": "warning", "caution": "warning", "warn": "warning",
	"error": "danger", "critical": "danger", "ok": "success", "check": "success",
}

func (b *Block) normalize() {
	b.Type = strings.ToLower(strings.TrimSpace(b.Type))
	v := strings.ToLower(strings.TrimSpace(b.Variant))
	switch b.Type {
	case BlockList:
		if mapped, ok := listVariants[v]; ok {
			v = mapped
		}
	case BlockCallout:
		if mapped, ok := calloutAliases[v]; ok {
			v = mapped
		}
	}
	b.Variant = v
	b.Language = strings.TrimSpace(b.Language)
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Validate checks a block against the block contract and the pass audiences, naming every problem.
func (b *Block) Validate(c api.BlockContract, audiences []string) error {
	b.normalize()
	c = blockContract(c)
	if !oneOf(b.Type, c.Types) {
		return fmt.Errorf("type %q must be one of %s", b.Type, strings.Join(c.Types, ", "))
	}
	var problems []string
	needText := func() {
		if strings.TrimSpace(b.Text) == "" {
			problems = append(problems, b.Type+" needs text")
		}
	}
	switch b.Type {
	case BlockHeading:
		needText()
		if b.Level < 1 || b.Level > 3 {
			problems = append(problems, "heading needs level 1, 2 or 3")
		}
	case BlockProse, BlockQuote:
		needText()
	case BlockList:
		needText()
		if !oneOf(b.Variant, c.ListVariants) {
			problems = append(problems, fmt.Sprintf("list needs variant %s", strings.Join(c.ListVariants, "|")))
		}
		if strings.Contains(strings.TrimSpace(b.Text), "\n") {
			problems = append(problems, "list holds one item per block; send one list block per item")
		}
	case BlockCode:
		needText()
		if b.Language == "" {
			problems = append(problems, "code needs language (for example json, bash, yaml, text)")
		}
	case BlockCallout:
		needText()
		if !oneOf(b.Variant, c.CalloutVariants) {
			problems = append(problems, fmt.Sprintf("callout needs variant %s", strings.Join(c.CalloutVariants, "|")))
		}
	case BlockTable:
		if len(b.Rows) == 0 {
			problems = append(problems, "table needs rows (an array of rows, each an array of cell strings)")
		}
		for i, r := range b.Rows {
			if len(r) == 0 {
				problems = append(problems, fmt.Sprintf("table rows[%d] is empty", i))
			}
		}
	case BlockDivider:
	}
	for _, a := range b.Audiences {
		if !oneOf(a, audiences) {
			if len(audiences) == 0 {
				problems = append(problems, "audiences must be empty: this pass has no audience keys")
			} else {
				problems = append(problems, fmt.Sprintf("audience %q must be one of %s", a, strings.Join(audiences, ", ")))
			}
			break
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Content converts a validated block to the server content of its type.
func (b Block) Content() map[string]any {
	switch b.Type {
	case BlockHeading:
		return map[string]any{"text": b.Text, "level": b.Level}
	case BlockCode:
		return map[string]any{"text": b.Text, "language": b.Language}
	case BlockCallout, BlockList:
		return map[string]any{"text": b.Text, "variant": b.Variant}
	case BlockTable:
		header := true
		if b.Header != nil {
			header = *b.Header
		}
		rows := make([]any, 0, len(b.Rows))
		for _, r := range b.Rows {
			cells := make([]any, 0, len(r))
			for _, c := range r {
				cells = append(cells, c)
			}
			rows = append(rows, cells)
		}
		return map[string]any{"rows": rows, "header": header}
	case BlockDivider:
		return map[string]any{}
	}
	return map[string]any{"text": b.Text}
}

func blockFromContent(typ string, content map[string]any) Block {
	b := Block{Type: typ}
	b.Text, _ = content["text"].(string)
	b.Language, _ = content["language"].(string)
	b.Variant, _ = content["variant"].(string)
	switch l := content["level"].(type) {
	case float64:
		b.Level = int(l)
	case int:
		b.Level = l
	}
	if h, ok := content["header"].(bool); ok {
		b.Header = &h
	}
	b.Rows = tableRows(content["rows"])
	return b
}

func tableRows(v any) [][]string {
	var rows [][]string
	switch rs := v.(type) {
	case []any:
		for _, r := range rs {
			var cells []string
			if cs, ok := r.([]any); ok {
				for _, c := range cs {
					cells = append(cells, fmt.Sprint(c))
				}
			}
			rows = append(rows, cells)
		}
	case [][]string:
		rows = rs
	}
	return rows
}

func blockText(typ string, content map[string]any) string {
	if typ == BlockTable {
		var lines []string
		for _, r := range tableRows(content["rows"]) {
			lines = append(lines, strings.Join(r, " | "))
		}
		return strings.Join(lines, "\n")
	}
	if t, ok := content["text"].(string); ok {
		return t
	}
	var parts []string
	if m, ok := content["method"].(string); ok {
		parts = append(parts, strings.ToUpper(m))
	}
	if p, ok := content["path"].(string); ok {
		parts = append(parts, p)
	}
	for _, k := range []string{"summary", "title", "description"} {
		if s, ok := content[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}
