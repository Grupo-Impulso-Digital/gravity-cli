package prompts_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

type fetcher struct {
	calls int
	resp  *api.PromptResponse
	err   error
}

func (f *fetcher) PromptInfo(context.Context, string) (*api.PromptResponse, error) {
	f.calls++
	return f.resp, f.err
}

func TestEveryPassPromptHasABakedFallback(t *testing.T) {
	for _, name := range []string{prompts.PassGuides, prompts.PassReference, prompts.PassChangelog, prompts.PassNucleus, prompts.PassCheck, prompts.PassImpact, prompts.MapUnits} {
		p, ok := prompts.Baked(name)
		if !ok || strings.TrimSpace(p.Text) == "" || !strings.HasPrefix(p.Version, "sha256:") {
			t.Errorf("baked %s = %+v", name, p)
		}
	}
}

func TestResolverPrefersHostedAndCaches(t *testing.T) {
	f := &fetcher{resp: &api.PromptResponse{Name: prompts.PassGuides, Text: "hosted", Version: "sha256:abc"}}
	var logged []string
	r := &prompts.Resolver{Fetcher: f, Log: func(format string, args ...any) { logged = append(logged, format) }}
	for range 2 {
		p, err := r.Get(context.Background(), prompts.PassGuides)
		if err != nil || p.Text != "hosted" || p.Source != prompts.SourceHosted {
			t.Fatalf("Get = %+v, %v", p, err)
		}
	}
	if f.calls != 1 || len(logged) != 1 {
		t.Fatalf("calls=%d logged=%d", f.calls, len(logged))
	}
}

func TestResolverFallsBackToBaked(t *testing.T) {
	r := &prompts.Resolver{Fetcher: &fetcher{err: errors.New("offline")}}
	p, err := r.Get(context.Background(), prompts.PassCheck)
	if err != nil || p.Source != prompts.SourceBaked {
		t.Fatalf("Get = %+v, %v", p, err)
	}
	if _, err := r.Get(context.Background(), "nope"); err == nil {
		t.Fatal("unknown prompt resolved")
	}
}
