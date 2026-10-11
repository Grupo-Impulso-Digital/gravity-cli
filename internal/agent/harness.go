package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// ErrNoBundle means the harness has no instructions bundle to run a task with.
var ErrNoBundle = errors.New("no instructions bundle for this run pass")

// Harness runs the agent tasks of one run pass against the gateway with the run pass's instructions bundle.
type Harness struct {
	LLM          LLM
	Bundle       *api.InstructionsBundle
	Instructions func(ctx context.Context) (*api.InstructionsBundle, error)
	RunID        string
	RunPassID    string
	Log          io.Writer

	mu      sync.Mutex
	usage   Usage
	partial []string
}

// Task is one agent conversation: a bundle phase, a data-only kickoff message, tools and the terminal tool.
type Task struct {
	Phase   string
	Purpose string
	Kickoff string
	Tools   []Tool
	Submit  Tool
}

// Usage reports the model calls and tokens the harness spent; InputTokens include cache reads and writes.
type Usage struct {
	Calls            int `json:"calls"`
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

// Usage returns what the harness spent so far.
func (h *Harness) Usage() Usage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.usage
}

// Partial lists the phases whose list terminal was forced at a cap.
func (h *Harness) Partial() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.partial...)
}

func (h *Harness) record(phase string, res *Result) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.usage.Calls += res.Turns
	h.usage.InputTokens += res.InputTokens
	h.usage.OutputTokens += res.OutputTokens
	h.usage.CacheReadTokens += res.CacheReadTokens
	h.usage.CacheWriteTokens += res.CacheWriteTokens
	if res.Partial {
		h.partial = append(h.partial, phase)
	}
}

func (h *Harness) bundle(ctx context.Context) (*api.InstructionsBundle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Bundle == nil && h.Instructions != nil {
		b, err := h.Instructions(ctx)
		if err != nil {
			return nil, fmt.Errorf("instructions bundle: %w", err)
		}
		h.Bundle = b
	}
	if h.Bundle == nil {
		return nil, ErrNoBundle
	}
	return h.Bundle, nil
}

func (h *Harness) describe(b *api.InstructionsBundle, tools []Tool) []Tool {
	out := make([]Tool, len(tools))
	for i, t := range tools {
		if d, ok := b.ToolDescription(t.Def.Name); ok && d != "" {
			t.Def.Description = d
		} else if h.Log != nil {
			fmt.Fprintf(h.Log, "agent: the instructions bundle has no description for tool %s\n", t.Def.Name)
		}
		out[i] = t
	}
	return out
}

// Run executes a task and returns its loop result; a task that ends without its terminal tool fails with a TaskError.
func (h *Harness) Run(ctx context.Context, t Task) (*Result, error) {
	if h.LLM == nil {
		return nil, errors.New("no LLM gateway client")
	}
	b, err := h.bundle(ctx)
	if err != nil {
		return nil, err
	}
	system, err := b.System(t.Phase)
	if err != nil {
		return nil, err
	}
	r := &Runner{
		Client:           h.LLM,
		System:           system,
		Tools:            h.describe(b, append(append([]Tool{}, t.Tools...), t.Submit)),
		Model:            b.Model.ID,
		Limits:           b.Model.Limits,
		Caps:             b.Harness,
		ForcedToolChoice: b.Model.Features.ForcedToolChoice,
		Context: &api.MessagesContext{
			RunID: h.RunID, RunPassID: h.RunPassID, Purpose: t.Purpose,
			InstructionsVersion: b.Version, Phase: t.Phase,
		},
		Log: h.Log,
	}
	res, err := r.Run(ctx, t.Kickoff)
	if res != nil {
		h.record(t.Phase, res)
	}
	if err != nil {
		return res, err
	}
	if res.TerminalTool != t.Submit.Def.Name {
		return res, &TaskError{Reason: ErrNoSubmit, Model: res.Model, Detail: "the model never called " + t.Submit.Def.Name}
	}
	return res, nil
}

// Submit runs a task and decodes the terminal tool input into T.
func Submit[T any](ctx context.Context, h *Harness, t Task) (T, error) {
	var out T
	res, err := h.Run(ctx, t)
	if err != nil {
		return out, err
	}
	if err := Decode(res.TerminalInput, &out); err != nil {
		return out, fmt.Errorf("decode %s: %w", t.Submit.Def.Name, err)
	}
	return out, nil
}
