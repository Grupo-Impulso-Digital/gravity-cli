package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/impulso/gravity-cli/internal/api"
)

// Default caps on the loop to bound cost and latency.
const (
	DefaultMaxIterations = 24
	DefaultMaxToolCalls  = 80
	defaultMaxTokens     = 4096
)

// Author-phase caps for content-heavy, single-page authoring runs (the docs
// generator). One page's full multi-audience block set is a larger structured
// payload than the other agents emit, so it needs more output tokens; callers
// set these on the Runner explicitly.
const (
	DefaultAuthorMaxTokens     = 8192
	DefaultAuthorMaxIterations = 16
)

// LLM is the subset of the API client the loop needs (so it can be mocked).
type LLM interface {
	Messages(ctx context.Context, req api.MessagesRequest) (*api.MessagesResponse, error)
}

// Runner drives a tool-using conversation against the LLM gateway.
type Runner struct {
	Client        LLM
	System        string
	Tools         []Tool
	Model         string
	MaxTokens     int
	MaxIterations int
	MaxToolCalls  int
	// Context optionally scopes the gateway's RAG to a site/space. Optional;
	// absent context is treated as org/default-site scope by the backend.
	Context *api.MessagesContext
	// Log receives human-readable progress lines. Optional.
	Log io.Writer
}

// Result is the outcome of a completed loop.
type Result struct {
	// TerminalTool is the name of the submit tool that ended the loop, or ""
	// if the model ended with end_turn / a cap was hit.
	TerminalTool string
	// TerminalInput is the raw input the model passed to the submit tool.
	TerminalInput json.RawMessage
	// FinalText is any assistant text from the last turn.
	FinalText string
	// Stopped indicates a cap was hit before a terminal tool was called.
	Stopped bool
	// StopReason explains why (cap name) when Stopped is true.
	StopReason string
	// Iterations is the number of model turns executed before returning.
	Iterations int
	// ToolCalls is the number of non-terminal tool calls executed.
	ToolCalls int
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, format+"\n", args...)
	}
}

func (r *Runner) toolByName(name string) (*Tool, bool) {
	for i := range r.Tools {
		if r.Tools[i].Def.Name == name {
			return &r.Tools[i], true
		}
	}
	return nil, false
}

// Run executes the loop starting from the given user message until a terminal
// tool is called, the model ends its turn, or a cap is reached.
func (r *Runner) Run(ctx context.Context, initialUser string) (*Result, error) {
	maxIter := r.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}
	maxCalls := r.MaxToolCalls
	if maxCalls <= 0 {
		maxCalls = DefaultMaxToolCalls
	}
	maxTokens := r.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	defs := make([]api.Tool, 0, len(r.Tools))
	for _, t := range r.Tools {
		defs = append(defs, t.Def)
	}

	messages := []api.Message{api.UserText(initialUser)}
	result := &Result{}

	for iter := 0; iter < maxIter; iter++ {
		result.Iterations = iter + 1

		req := api.MessagesRequest{
			Model:      r.Model,
			System:     r.System,
			Messages:   messages,
			Tools:      defs,
			ToolChoice: &api.ToolChoice{Type: api.ToolChoiceAuto},
			MaxTokens:  maxTokens,
			Context:    r.Context,
		}
		resp, err := r.Client.Messages(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("llm request (iteration %d): %w", iter+1, err)
		}

		// Record the assistant turn so the model sees its own tool_use parts.
		messages = append(messages, api.Message{Role: api.RoleAssistant, Content: resp.Content})
		result.FinalText = resp.TextContent()

		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			// No tool use this turn: the model is done (end_turn) or stuck.
			r.logf("agent: model ended turn after %d iteration(s)", iter+1)
			return result, nil
		}

		// Execute each requested tool and gather results for the next turn.
		var toolResults []api.ContentPart
		for _, use := range toolUses {
			tool, ok := r.toolByName(use.Name)
			if !ok {
				r.logf("agent: model requested unknown tool %q", use.Name)
				toolResults = append(toolResults, api.ContentPart{
					Type:      api.PartToolResult,
					ToolUseID: use.ID,
					Content:   fmt.Sprintf("error: unknown tool %q", use.Name),
					IsError:   true,
				})
				continue
			}

			if tool.Terminal {
				// Terminal tool ends the loop; capture its input.
				r.logf("agent: model called terminal tool %q", use.Name)
				result.TerminalTool = use.Name
				result.TerminalInput = append(json.RawMessage(nil), use.Input...)
				return result, nil
			}

			result.ToolCalls++
			if result.ToolCalls > maxCalls {
				r.logf("agent: hit tool-call cap (%d); returning partial result", maxCalls)
				result.Stopped = true
				result.StopReason = fmt.Sprintf("tool-call cap of %d reached", maxCalls)
				return result, nil
			}

			r.logf("agent: tool %s (%d)", use.Name, result.ToolCalls)
			out, err := tool.Run(ctx, use.Input)
			if err != nil {
				toolResults = append(toolResults, api.ContentPart{
					Type:      api.PartToolResult,
					ToolUseID: use.ID,
					Content:   "error: " + err.Error(),
					IsError:   true,
				})
				continue
			}
			toolResults = append(toolResults, api.ContentPart{
				Type:      api.PartToolResult,
				ToolUseID: use.ID,
				Content:   out,
			})
		}

		messages = append(messages, api.ToolResult(toolResults...))
	}

	r.logf("agent: hit iteration cap (%d); returning partial result", maxIter)
	result.Stopped = true
	result.StopReason = fmt.Sprintf("iteration cap of %d reached", maxIter)
	return result, nil
}
