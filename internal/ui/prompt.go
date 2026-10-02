package ui

import (
	"errors"
	"io"

	"github.com/charmbracelet/huh"
)

// ErrAborted is returned when the user cancels a prompt.
var ErrAborted = errors.New("canceled")

// Choice is one option of a prompt.
type Choice struct {
	Key   string
	Label string
}

// Prompter asks the questions of an interactive command.
type Prompter interface {
	Select(title, description string, choices []Choice, def string) (string, error)
	MultiSelect(title, description string, choices []Choice, selected []string) ([]string, error)
}

// HuhPrompter asks questions with charmbracelet/huh; Accessible switches to its line-based mode.
type HuhPrompter struct {
	In         io.Reader
	Out        io.Writer
	Accessible bool
}

func (h *HuhPrompter) run(field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithShowHelp(!h.Accessible)
	if h.Out != nil {
		form = form.WithOutput(h.Out)
	}
	if h.In != nil {
		in := h.In
		if h.Accessible {
			in = &byteReader{r: h.In}
		}
		form = form.WithInput(in)
	}
	if h.Accessible {
		form = form.WithAccessible(true)
	}
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrAborted
		}
		return err
	}
	return nil
}

// Select asks for one choice and returns its key.
func (h *HuhPrompter) Select(title, description string, choices []Choice, def string) (string, error) {
	value := def
	opts := make([]huh.Option[string], 0, len(choices))
	for _, c := range choices {
		opts = append(opts, huh.NewOption(c.Label, c.Key))
	}
	field := huh.NewSelect[string]().Title(title).Description(description).Options(opts...).Value(&value)
	if err := h.run(field); err != nil {
		return "", err
	}
	return value, nil
}

// MultiSelect asks for any number of choices and returns their keys in choice order.
func (h *HuhPrompter) MultiSelect(title, description string, choices []Choice, selected []string) ([]string, error) {
	pre := map[string]bool{}
	for _, s := range selected {
		pre[s] = true
	}
	var value []string
	opts := make([]huh.Option[string], 0, len(choices))
	for _, c := range choices {
		opts = append(opts, huh.NewOption(c.Label, c.Key).Selected(pre[c.Key]))
	}
	field := huh.NewMultiSelect[string]().Title(title).Description(description).Options(opts...).Value(&value)
	if err := h.run(field); err != nil {
		return nil, err
	}
	picked := map[string]bool{}
	for _, v := range value {
		picked[v] = true
	}
	out := []string{}
	for _, c := range choices {
		if picked[c.Key] {
			out = append(out, c.Key)
		}
	}
	return out, nil
}

type byteReader struct{ r io.Reader }

func (b *byteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.r.Read(p[:1])
}
