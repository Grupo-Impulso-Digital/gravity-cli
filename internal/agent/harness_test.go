package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/prompts"
)

type fakeLLM struct {
	replies  []api.MessagesResponse
	requests []api.MessagesRequest
}

func (f *fakeLLM) Messages(_ context.Context, req api.MessagesRequest) (*api.MessagesResponse, error) {
	f.requests = append(f.requests, req)
	if len(f.replies) == 0 {
		return nil, errors.New("no scripted reply")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return &r, nil
}

type bakedOnly struct{}

func (bakedOnly) Get(_ context.Context, name string) (prompts.Prompt, error) {
	p, _ := prompts.Baked(name)
	return p, nil
}

func use(name, input string, in, out int) api.MessagesResponse {
	return api.MessagesResponse{
		StopReason: api.StopToolUse, Usage: api.Usage{InputTokens: in, OutputTokens: out},
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: "tu", Name: name, Input: json.RawMessage(input)}},
	}
}

func TestHarnessSendsRunContextAndOnlyTheKindPrompt(t *testing.T) {
	llm := &fakeLLM{replies: []api.MessagesResponse{use(agent.ToolSubmitAtoms, `{"atoms":[{"title":"Refund reasons","body":"POST /v1/refunds takes a reason."}]}`, 100, 20)}}
	h := &agent.Harness{LLM: llm, Prompts: bakedOnly{}, RunID: "prun_1", RunPassID: "ppr_1"}
	out, err := agent.Submit[agent.AtomsInput](context.Background(), h, agent.Task{Prompt: prompts.PassNucleus, Purpose: api.PurposeDistill, Kickoff: "distill", Submit: agent.SubmitAtomsTool()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Atoms) != 1 || out.Atoms[0].Title != "Refund reasons" {
		t.Fatalf("atoms = %+v", out.Atoms)
	}
	req := llm.requests[0]
	baked, _ := prompts.Baked(prompts.PassNucleus)
	if req.Context == nil || req.Context.RunID != "prun_1" || req.Context.RunPassID != "ppr_1" || req.Context.Purpose != api.PurposeDistill {
		t.Fatalf("context = %+v", req.Context)
	}
	if req.System != baked.Text || req.MaxTokens != agent.PhaseMaxTokens {
		t.Fatalf("system/max tokens = %q/%d", req.System, req.MaxTokens)
	}
	if u := h.Usage(); u.Calls != 1 || u.InputTokens != 100 || u.OutputTokens != 20 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestHarnessStopsWhenThePassBudgetIsSpent(t *testing.T) {
	llm := &fakeLLM{replies: []api.MessagesResponse{use(agent.ToolSubmitAtoms, `{"atoms":[]}`, 900, 200)}}
	h := &agent.Harness{LLM: llm, TokenBudget: 1000}
	task := agent.Task{Kickoff: "go", Submit: agent.SubmitAtomsTool()}
	if _, err := h.Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(context.Background(), task); !errors.Is(err, agent.ErrBudgetSpent) {
		t.Fatalf("second task err = %v, want ErrBudgetSpent", err)
	}
}

func TestHarnessFailsWithoutSubmit(t *testing.T) {
	llm := &fakeLLM{replies: []api.MessagesResponse{
		{StopReason: api.StopEndTurn, Content: []api.ContentPart{{Type: api.PartText, Text: "done"}}},
		{StopReason: api.StopEndTurn, Content: []api.ContentPart{{Type: api.PartText, Text: "still done"}}},
	}}
	h := &agent.Harness{LLM: llm}
	_, err := h.Run(context.Background(), agent.Task{Kickoff: "go", Submit: agent.SubmitAtomsTool(), MaxIterations: 2})
	if !errors.Is(err, agent.ErrNoSubmit) {
		t.Fatalf("err = %v", err)
	}
}
