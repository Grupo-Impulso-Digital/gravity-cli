// Package prompts resolves the hosted kind prompts of the passes, falling back to baked copies when the gateway is unreachable.
package prompts

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/normalize"
)

// Hosted prompt names.
const (
	PassGuides    = "pass-guides"
	PassReference = "pass-reference"
	PassChangelog = "pass-changelog"
	PassNucleus   = "pass-nucleus"
	PassCheck     = "pass-check"
	PassImpact    = "pass-impact"
	MapUnits      = "map-units"
)

// Source says where a resolved prompt came from.
const (
	SourceHosted = "hosted"
	SourceBaked  = "baked"
)

// Prompt is a resolved kind prompt.
type Prompt struct {
	Name    string `json:"name"`
	Text    string `json:"-"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

// Fetcher reads hosted prompts.
type Fetcher interface {
	PromptInfo(ctx context.Context, name string) (*api.PromptResponse, error)
}

// Resolver fetches each prompt once per process.
type Resolver struct {
	Fetcher Fetcher
	Log     func(format string, args ...any)
	mu      sync.Mutex
	cache   map[string]Prompt
}

// Baked returns the compiled-in fallback of a prompt.
func Baked(name string) (Prompt, bool) {
	text, ok := baked[name]
	if !ok {
		return Prompt{}, false
	}
	return Prompt{Name: name, Text: text, Version: normalize.SHA256([]byte(text)), Source: SourceBaked}, true
}

// Get returns the hosted prompt, or the baked fallback when the gateway does not answer.
func (r *Resolver) Get(ctx context.Context, name string) (Prompt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.cache[name]; ok {
		return p, nil
	}
	if r.cache == nil {
		r.cache = map[string]Prompt{}
	}
	var p Prompt
	if r.Fetcher != nil {
		hosted, err := r.Fetcher.PromptInfo(ctx, name)
		switch {
		case err == nil && hosted.Text != "":
			p = Prompt{Name: name, Text: hosted.Text, Version: hosted.Version, Source: SourceHosted}
		case err != nil && api.IsLicenseError(err):
			return Prompt{}, err
		case err != nil && api.StopsRun(err):
			return Prompt{}, err
		case err != nil && errors.Is(err, context.Canceled):
			return Prompt{}, err
		}
	}
	if p.Text == "" {
		b, ok := Baked(name)
		if !ok {
			return Prompt{}, fmt.Errorf("unknown prompt %q", name)
		}
		p = b
	}
	if r.Log != nil {
		r.Log("prompt %s %s (%s)", name, short(p.Version), p.Source)
	}
	r.cache[name] = p
	return p, nil
}

func short(v string) string {
	if len(v) > 15 {
		return v[:15]
	}
	return v
}
