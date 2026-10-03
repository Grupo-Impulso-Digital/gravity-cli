package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
)

// Default loop caps.
const (
	DefaultMaxIterations = 24
	DefaultMaxToolCalls  = 80
	defaultMaxTokens     = 4096
)

// PhaseMaxTokens is the output budget of one plan or author call.
const PhaseMaxTokens = 16000

// LLM is the subset of the API client the loop needs.
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
	TokenBudget   int
	Context       *api.MessagesContext
	Log           io.Writer
}

// Result is the outcome of a completed loop.
type Result struct {
	TerminalTool  string
	TerminalInput json.RawMessage
	FinalText     string
	Stopped       bool
	StopReason    string
	Iterations    int
	ToolCalls     int
	InputTokens   int
	OutputTokens  int
}

// Tokens returns the input plus output tokens the loop consumed.
func (r *Result) Tokens() int {
	return r.InputTokens + r.OutputTokens
}

func rejectsForcedToolChoice(err error) bool {
	var ae *api.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusBadRequest || ae.Code == api.CodeProviderError
}

func withTerminalInstruction(system, terminal string) string {
	instruction := fmt.Sprintf("This is your final turn. Call the %s tool now with your final result and do not call any other tool.", terminal)
	if system == "" {
		return instruction
	}
	return system + "\n\n" + instruction
}

func sanitizeContent(parts []api.ContentPart) []api.ContentPart {
	out := make([]api.ContentPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case api.PartText:
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
		case api.PartToolUse:
			if len(bytes.TrimSpace(p.Input)) == 0 {
				p.Input = json.RawMessage("{}")
			}
		}
		out = append(out, p)
	}
	return out
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, format+"\n", args...)
	}
}

func compactLog(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

func (r *Runner) toolByName(name string) (*Tool, bool) {
	for i := range r.Tools {
		if r.Tools[i].Def.Name == name {
			return &r.Tools[i], true
		}
	}
	return nil, false
}

func (r *Runner) terminalTool() string {
	for _, t := range r.Tools {
		if t.Terminal {
			return t.Def.Name
		}
	}
	return ""
}

// Run executes the loop until a terminal tool is called or a cap is reached; the final turn forces the terminal tool.
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
	terminal := r.terminalTool()

	messages := []api.Message{api.UserText(initialUser)}
	result := &Result{}

	defer func() {
		if result.Tokens() > 0 {
			r.logf("agent: tokens: %d in / %d out over %d turn(s)", result.InputTokens, result.OutputTokens, result.Iterations)
		}
	}()

	forceNext := false
	budgetSpent := false
	forcingRejected := false
	for iter := 0; iter < maxIter; iter++ {
		result.Iterations = iter + 1

		forced := terminal != "" && (forceNext || iter == maxIter-1)
		req := api.MessagesRequest{
			Model:      r.Model,
			System:     r.System,
			Messages:   messages,
			Tools:      defs,
			ToolChoice: &api.ToolChoice{Type: api.ToolChoiceAuto},
			MaxTokens:  maxTokens,
			Context:    r.Context,
		}
		if forced {
			r.logf("agent: requiring %s on this turn", terminal)
			if forcingRejected {
				req.System = withTerminalInstruction(r.System, terminal)
			} else {
				req.ToolChoice = &api.ToolChoice{Type: api.ToolChoiceTool, Name: terminal}
			}
		}
		resp, err := r.Client.Messages(ctx, req)
		if err != nil && forced && !forcingRejected && rejectsForcedToolChoice(err) {
			r.logf("agent: the model rejected a forced tool_choice; retrying with an explicit instruction instead")
			forcingRejected = true
			req.ToolChoice = &api.ToolChoice{Type: api.ToolChoiceAuto}
			req.System = withTerminalInstruction(r.System, terminal)
			resp, err = r.Client.Messages(ctx, req)
		}
		if err != nil {
			return nil, fmt.Errorf("llm request (iteration %d): %w", iter+1, err)
		}

		messages = append(messages, api.Message{Role: api.RoleAssistant, Content: sanitizeContent(resp.Content)})
		result.FinalText = resp.TextContent()
		result.InputTokens += resp.Usage.InputTokens
		result.OutputTokens += resp.Usage.OutputTokens

		if text := strings.TrimSpace(resp.TextContent()); text != "" {
			r.logf("agent: says: %s", compactLog(text, 400))
		}

		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			if terminal != "" && !forced && iter < maxIter-1 {
				r.logf("agent: model ended its turn without %s; asking for it", terminal)
				messages = append(messages, api.UserText(fmt.Sprintf(
					"You ended your turn without calling %s. Call %s now with your final result.", terminal, terminal)))
				forceNext = true
				continue
			}
			r.logf("agent: model ended turn after %d iteration(s)", iter+1)
			return result, nil
		}

		var toolResults []api.ContentPart
		for i, use := range toolUses {
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
				if tool.Validate != nil {
					if verr := tool.Validate(use.Input); verr != nil {
						r.logf("agent: rejected %s input, asking the model to correct it: %v", use.Name, verr)
						toolResults = append(toolResults, api.ContentPart{
							Type:      api.PartToolResult,
							ToolUseID: use.ID,
							Content:   fmt.Sprintf("error: invalid %s input: %v. Correct the input and call %s again.", use.Name, verr, use.Name),
							IsError:   true,
						})
						continue
					}
				}
				r.logf("agent: model called terminal tool %q", use.Name)
				result.TerminalTool = use.Name
				result.TerminalInput = append(json.RawMessage(nil), use.Input...)
				return result, nil
			}

			result.ToolCalls++
			if result.ToolCalls > maxCalls {
				if terminal == "" || budgetSpent {
					r.logf("agent: hit tool-call cap (%d); returning partial result", maxCalls)
					result.Stopped = true
					result.StopReason = fmt.Sprintf("tool-call cap of %d reached", maxCalls)
					return result, nil
				}
				r.logf("agent: hit tool-call cap (%d); requiring %s next", maxCalls, terminal)
				budgetSpent = true
				for _, rest := range toolUses[i:] {
					toolResults = append(toolResults, api.ContentPart{
						Type:      api.PartToolResult,
						ToolUseID: rest.ID,
						Content:   fmt.Sprintf("error: the tool-call budget is exhausted. Call %s now with your final result.", terminal),
						IsError:   true,
					})
				}
				forceNext = true
				break
			}

			r.logf("agent: tool %s (%d) %s", use.Name, result.ToolCalls, compactLog(string(use.Input), 160))
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
			content := out
			if strings.TrimSpace(content) == "" {
				content = "(empty output)"
			}
			toolResults = append(toolResults, api.ContentPart{
				Type:      api.PartToolResult,
				ToolUseID: use.ID,
				Content:   content,
			})
		}

		messages = append(messages, api.ToolResult(toolResults...))
		if forced {
			forceNext = true
		}
		if r.TokenBudget > 0 && result.Tokens() >= r.TokenBudget && terminal != "" {
			if forced {
				r.logf("agent: token budget of %d spent; stopping", r.TokenBudget)
				result.Stopped = true
				result.StopReason = fmt.Sprintf("token budget of %d reached", r.TokenBudget)
				return result, nil
			}
			r.logf("agent: token budget of %d spent; requiring %s next", r.TokenBudget, terminal)
			forceNext = true
		}
	}

	r.logf("agent: hit iteration cap (%d); returning partial result", maxIter)
	result.Stopped = true
	result.StopReason = fmt.Sprintf("iteration cap of %d reached", maxIter)
	return result, nil
}
