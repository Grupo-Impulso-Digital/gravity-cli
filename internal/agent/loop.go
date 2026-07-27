package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Grupo-Impulso-Digital/gravity-cli/internal/api"
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

// RetryBackoff is the base delay between retries of a transient gateway error;
// the nth retry waits n*RetryBackoff. Zero falls back to a 1s base (tests set a
// tiny value to stay fast).
var RetryBackoff = time.Second

// maxLLMAttempts bounds calls per model turn (1 try + up to 2 retries).
const maxLLMAttempts = 3

// callWithRetry issues one model turn, retrying transient gateway failures. The
// LLM gateway intermittently 502s (and 429/503s) on otherwise-valid requests; a
// single blip would otherwise abort a whole multi-step run, so we retry with a
// short linear backoff. Non-transient errors (and a canceled context) return
// immediately.
func (r *Runner) callWithRetry(ctx context.Context, req api.MessagesRequest) (*api.MessagesResponse, error) {
	base := RetryBackoff
	if base <= 0 {
		base = time.Second
	}
	var lastErr error
	for attempt := 1; attempt <= maxLLMAttempts; attempt++ {
		resp, err := r.Client.Messages(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == maxLLMAttempts || !transientGatewayErr(err) {
			return nil, err
		}
		r.logf("agent: transient gateway error (attempt %d/%d), retrying: %v", attempt, maxLLMAttempts, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * base):
		}
	}
	return nil, lastErr
}

// transientGatewayErr reports whether err is a gateway/provider failure worth
// retrying: a 502 (provider_error), 503, or 429.
func transientGatewayErr(err error) bool {
	var ae *api.APIError
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case 502, 503, 429:
			return true
		}
	}
	return false
}

// sanitizeContent normalizes an assistant turn so it can't serialize to an
// Anthropic block the provider rejects with a 400 when echoed back (the omitempty
// JSON tags drop empty fields, producing structurally invalid blocks):
//   - an empty/whitespace text part → dropped (would become {"type":"text"});
//   - a tool_use with empty input → input defaulted to {} (a bare tool_use with
//     no "input" field is rejected).
//
// These are the intermittent mid-run failures: a turn is only invalid when the
// model happens to emit such a part, so the loop dies several iterations in.
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

// compactLog flattens whitespace and caps length so model text and tool inputs
// fit on one readable log line.
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

	// Token accounting across the whole loop, reported on every exit path so a
	// run's cost is always visible in the log.
	var inTokens, outTokens int
	defer func() {
		if inTokens > 0 || outTokens > 0 {
			r.logf("agent: tokens: %d in / %d out over %d turn(s)", inTokens, outTokens, result.Iterations)
		}
	}()

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
		resp, err := r.callWithRetry(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("llm request (iteration %d): %w", iter+1, err)
		}

		// Record the assistant turn so the model sees its own tool_use parts.
		// Sanitize first: models often emit an empty/whitespace text block
		// alongside tool_use, and echoing it back serializes to {"type":"text"}
		// (Text is omitempty), which the provider rejects with a 400 on the next
		// turn — the intermittent mid-run failure this guards against.
		messages = append(messages, api.Message{Role: api.RoleAssistant, Content: sanitizeContent(resp.Content)})
		result.FinalText = resp.TextContent()
		inTokens += resp.Usage.InputTokens
		outTokens += resp.Usage.OutputTokens

		// Surface the model's narration: this is the run's "mind" — what it
		// concluded from the last tool results and what it intends to do next.
		if text := strings.TrimSpace(resp.TextContent()); text != "" {
			r.logf("agent: 💭 %s", compactLog(text, 400))
		}

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
				// Validate before accepting: a malformed submission (e.g. blocks
				// as a JSON-encoded string) is bounced back to the model as an
				// error tool_result so it can resubmit correctly — losing one
				// turn instead of the whole run. The iteration cap bounds retries.
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
			// A tool_result's content must be present: an empty string serializes
			// away (Content is omitempty), leaving a content-less block the provider
			// rejects. Substitute a placeholder when a tool legitimately returns "".
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
	}

	r.logf("agent: hit iteration cap (%d); returning partial result", maxIter)
	result.Stopped = true
	result.StopReason = fmt.Sprintf("iteration cap of %d reached", maxIter)
	return result, nil
}
