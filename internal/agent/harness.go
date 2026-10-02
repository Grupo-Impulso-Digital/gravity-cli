package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

// DefaultPassTokenBudget caps the tokens one pass may spend across all its agent calls.
const DefaultPassTokenBudget = 400_000

// ErrBudgetSpent means the pass has no tokens left for another agent call.
var ErrBudgetSpent = errors.New("the pass token budget is spent")

// PromptSource resolves kind prompts.
type PromptSource interface {
	Get(ctx context.Context, name string) (prompts.Prompt, error)
}

// Harness runs the agent tasks of one pass run against the gateway with the run context.
type Harness struct {
	LLM         LLM
	Prompts     PromptSource
	RunID       string
	RunPassID   string
	TokenBudget int
	Log         io.Writer

	mu     sync.Mutex
	used   int
	calls  int
	inTok  int
	outTok int
}

// Task is one agent conversation: a kind prompt, a kickoff message, tools and the terminal tool.
type Task struct {
	Prompt        string
	Purpose       string
	Kickoff       string
	Tools         []Tool
	Submit        Tool
	MaxTokens     int
	MaxIterations int
	MaxToolCalls  int
}

// Usage reports the tokens and calls the harness spent.
type Usage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// Usage returns what the harness spent so far.
func (h *Harness) Usage() Usage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Usage{Calls: h.calls, InputTokens: h.inTok, OutputTokens: h.outTok}
}

func (h *Harness) remaining() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	budget := h.TokenBudget
	if budget <= 0 {
		budget = DefaultPassTokenBudget
	}
	return budget - h.used
}

// Run executes a task and returns its loop result; it fails when the model never submits.
func (h *Harness) Run(ctx context.Context, t Task) (*Result, error) {
	if h.LLM == nil {
		return nil, errors.New("no LLM gateway client")
	}
	left := h.remaining()
	if left <= 0 {
		return nil, ErrBudgetSpent
	}
	system := ""
	if h.Prompts != nil && t.Prompt != "" {
		p, err := h.Prompts.Get(ctx, t.Prompt)
		if err != nil {
			return nil, fmt.Errorf("prompt %s: %w", t.Prompt, err)
		}
		system = p.Text
	}
	maxTokens := t.MaxTokens
	if maxTokens <= 0 {
		maxTokens = PhaseMaxTokens
	}
	tools := append(append([]Tool{}, t.Tools...), t.Submit)
	r := &Runner{
		Client:        h.LLM,
		System:        system,
		Tools:         tools,
		MaxTokens:     maxTokens,
		MaxIterations: t.MaxIterations,
		MaxToolCalls:  t.MaxToolCalls,
		TokenBudget:   left,
		Context:       &api.MessagesContext{RunID: h.RunID, RunPassID: h.RunPassID, Purpose: t.Purpose},
		Log:           h.Log,
	}
	res, err := r.Run(ctx, t.Kickoff)
	if res != nil {
		h.mu.Lock()
		h.used += res.Tokens()
		h.calls += res.Iterations
		h.inTok += res.InputTokens
		h.outTok += res.OutputTokens
		h.mu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	if res.TerminalTool != t.Submit.Def.Name {
		reason := res.StopReason
		if reason == "" {
			reason = "it ended its turn"
		}
		return res, fmt.Errorf("%w with %s (%s)", ErrNoSubmit, t.Submit.Def.Name, reason)
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
