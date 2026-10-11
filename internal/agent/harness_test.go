package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/agent"
	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

func use(name, input string, in, out int) api.MessagesResponse {
	return api.MessagesResponse{
		StopReason: api.StopToolUse, Usage: api.Usage{InputTokens: in, OutputTokens: out},
		Content: []api.ContentPart{{Type: api.PartToolUse, ID: "tu", Name: name, Input: json.RawMessage(input)}},
	}
}

func testBundle() *api.InstructionsBundle {
	return &api.InstructionsBundle{
		Schema: api.InstructionsSchema, Version: "sha256:0123456789", ToolContract: api.ToolContract,
		Model: api.BundleModel{
			ID: "anthropic/claude-sonnet-4-6", Provider: "anthropic",
			Limits:   api.ModelLimits{MaxOutputTokens: 64000, ContextWindow: 200000, Known: true},
			Features: api.ModelFeatures{ForcedToolChoice: true},
		},
		Harness:  api.HarnessLimits{MaxTurns: 80, MaxToolCalls: 200, StepTokens: 4000, MaxContinueNudges: 4},
		Rendered: api.RenderedPrompts{System: map[string]string{api.PhaseNucleusDistill: "distill system"}},
		Tools:    map[string]string{agent.ToolSubmitAtoms: "server description of submit_atoms"},
	}
}

func TestHarness(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *agent.Harness)
		phase string
		reply []api.MessagesResponse
		check func(t *testing.T, h *agent.Harness, llm *fakeLLM, out agent.AtomsInput, err error)
	}{
		{
			name:  "bundle system, tools and run context",
			phase: api.PhaseNucleusDistill,
			reply: []api.MessagesResponse{use(agent.ToolSubmitAtoms, `{"atoms":[{"title":"Refund reasons","body":"POST /v1/refunds takes a reason."}]}`, 100, 20)},
			check: func(t *testing.T, h *agent.Harness, llm *fakeLLM, out agent.AtomsInput, err error) {
				if err != nil || len(out.Atoms) != 1 {
					t.Fatalf("err=%v atoms=%+v", err, out.Atoms)
				}
				req := llm.requests[0]
				if req.SystemText() != "distill system" || req.SystemBlocks[0].CacheControl == nil {
					t.Fatalf("system = %+v", req.SystemBlocks)
				}
				want := api.MessagesContext{RunID: "prun_1", RunPassID: "ppr_1", Purpose: api.PurposeDistill, InstructionsVersion: "sha256:0123456789", Phase: api.PhaseNucleusDistill}
				if req.Context == nil || *req.Context != want || req.Model != "anthropic/claude-sonnet-4-6" || req.MaxTokens != 64000 {
					t.Fatalf("context=%+v model=%q max=%d", req.Context, req.Model, req.MaxTokens)
				}
				if len(req.Tools) != 1 || req.Tools[0].Description != "server description of submit_atoms" {
					t.Fatalf("tools = %+v", req.Tools)
				}
				if u := h.Usage(); u.Calls != 1 || u.InputTokens != 100 || u.OutputTokens != 20 {
					t.Fatalf("usage = %+v", u)
				}
			},
		},
		{
			name:  "missing phase",
			phase: api.PhaseGuidesWrite,
			check: func(t *testing.T, _ *agent.Harness, llm *fakeLLM, _ agent.AtomsInput, err error) {
				if err == nil || len(llm.requests) != 0 {
					t.Fatalf("err=%v requests=%d", err, len(llm.requests))
				}
			},
		},
		{
			name:  "no bundle",
			phase: api.PhaseNucleusDistill,
			setup: func(h *agent.Harness) { h.Bundle = nil },
			check: func(t *testing.T, _ *agent.Harness, _ *fakeLLM, _ agent.AtomsInput, err error) {
				if !errors.Is(err, agent.ErrNoBundle) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name:  "bundle loaded lazily once",
			phase: api.PhaseNucleusDistill,
			setup: func(h *agent.Harness) {
				h.Bundle = nil
				loads := 0
				h.Instructions = func(context.Context) (*api.InstructionsBundle, error) {
					loads++
					if loads > 1 {
						return nil, errors.New("loaded twice")
					}
					return testBundle(), nil
				}
			},
			reply: []api.MessagesResponse{use(agent.ToolSubmitAtoms, `{"atoms":[]}`, 1, 1)},
			check: func(t *testing.T, h *agent.Harness, llm *fakeLLM, _ agent.AtomsInput, err error) {
				if err != nil || h.Bundle == nil {
					t.Fatalf("err=%v bundle=%v", err, h.Bundle)
				}
			},
		},
		{
			name:  "bundle load failure",
			phase: api.PhaseNucleusDistill,
			setup: func(h *agent.Harness) {
				h.Bundle = nil
				h.Instructions = func(context.Context) (*api.InstructionsBundle, error) { return nil, api.ErrServerTooOld }
			},
			check: func(t *testing.T, _ *agent.Harness, _ *fakeLLM, _ agent.AtomsInput, err error) {
				if !errors.Is(err, api.ErrServerTooOld) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name:  "fails without submit after nudges",
			phase: api.PhaseNucleusDistill,
			setup: func(h *agent.Harness) { h.Bundle.Harness.MaxContinueNudges = 1 },
			reply: []api.MessagesResponse{
				{StopReason: api.StopEndTurn, Content: []api.ContentPart{{Type: api.PartText, Text: "done"}}, Usage: api.Usage{InputTokens: 5, OutputTokens: 1}},
				{StopReason: api.StopEndTurn, Content: []api.ContentPart{{Type: api.PartText, Text: "still done"}}, Usage: api.Usage{InputTokens: 5, OutputTokens: 1}},
			},
			check: func(t *testing.T, h *agent.Harness, _ *fakeLLM, _ agent.AtomsInput, err error) {
				if !errors.Is(err, agent.ErrNoSubmit) {
					t.Fatalf("err = %v", err)
				}
				if u := h.Usage(); u.Calls != 2 {
					t.Fatalf("usage of a failed task must still count: %+v", u)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := &fakeLLM{replies: tc.reply}
			h := &agent.Harness{LLM: llm, Bundle: testBundle(), RunID: "prun_1", RunPassID: "ppr_1"}
			if tc.setup != nil {
				tc.setup(h)
			}
			out, err := agent.Submit[agent.AtomsInput](context.Background(), h, agent.Task{Phase: tc.phase, Purpose: api.PurposeDistill, Kickoff: "distill", Submit: agent.SubmitAtomsTool()})
			tc.check(t, h, llm, out, err)
		})
	}
}

func TestHarnessRecordsPartialPhases(t *testing.T) {
	b := testBundle()
	b.Harness.MaxTurns = 2
	b.Rendered.System[api.PhaseUnitsMap] = "units system"
	llm := &fakeLLM{replies: []api.MessagesResponse{use("finish_units", `{"complete":true}`, 1, 1)}}
	h := &agent.Harness{LLM: llm, Bundle: b}
	res, err := h.Run(context.Background(), agent.Task{Phase: api.PhaseUnitsMap, Kickoff: "map", Submit: finishUnitsTool()})
	if err != nil || !res.Partial {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if p := h.Partial(); len(p) != 1 || p[0] != api.PhaseUnitsMap {
		t.Fatalf("partial = %v", p)
	}
}

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
