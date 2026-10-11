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

// Platform defaults used when the bundle leaves a cap or limit unset.
const (
	DefaultMaxTurns          = 80
	DefaultMaxToolCalls      = 200
	DefaultStepTokens        = 4000
	DefaultMaxContinueNudges = 4
	DefaultMaxOutputTokens   = 8192
	DefaultContextWindow     = 128000
)

const (
	forcedTurns        = 3
	outputReserve      = 2000
	maxTruncatedStreak = 3
	elideAbovePercent  = 60
	elideToPercent     = 40
	bytesPerToken      = 3
	toolDraftView      = "draft_view"
	toolFinishUnits    = "finish_units"
	truncatedText      = "(output truncated)"
	noOutputText       = "(no output)"
)

var listPhaseTerminals = map[string]bool{
	"finish_plan":          true,
	"finish_atoms":         true,
	toolFinishUnits:        true,
	"finish_review":        true,
	"finish_release_notes": true,
}

// Task failure reasons.
var (
	ErrNoSubmit             = errors.New("no_submit")
	ErrOutputLimit          = errors.New("output_limit")
	ErrRefusal              = errors.New("refusal")
	ErrContextLimit         = errors.New("context_limit")
	ErrCapReached           = errors.New("cap_reached")
	ErrForcedSubmitRejected = errors.New("forced_submit_rejected")
)

// TaskError is a task that ended without a result; it unwraps to one of the failure reasons.
type TaskError struct {
	Reason error
	Model  string
	Detail string
}

func (e *TaskError) Error() string {
	msg := e.Reason.Error()
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Model != "" {
		msg += " (model " + e.Model + ")"
	}
	return msg
}

func (e *TaskError) Unwrap() error { return e.Reason }

// LLM is the subset of the API client the loop needs.
type LLM interface {
	Messages(ctx context.Context, req api.MessagesRequest) (*api.MessagesResponse, error)
}

// Runner drives a tool-using conversation against the LLM gateway.
type Runner struct {
	Client           LLM
	System           string
	Tools            []Tool
	Model            string
	Limits           api.ModelLimits
	Caps             api.HarnessLimits
	ForcedToolChoice bool
	Context          *api.MessagesContext
	Log              io.Writer
}

// Result is the outcome of a completed loop.
type Result struct {
	TerminalTool     string
	TerminalInput    json.RawMessage
	TerminalOutput   string
	Forced           bool
	Partial          bool
	FinalText        string
	Turns            int
	ToolCalls        int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Model            string
}

// Tokens returns the prompt plus output tokens the loop consumed.
func (r *Result) Tokens() int {
	return r.InputTokens + r.OutputTokens
}

func (r *Result) add(resp *api.MessagesResponse) {
	r.Turns++
	r.InputTokens += resp.Usage.PromptTokens()
	r.OutputTokens += resp.Usage.OutputTokens
	r.CacheReadTokens += resp.Usage.CacheReadInputTokens
	r.CacheWriteTokens += resp.Usage.CacheCreationInputTokens
	if resp.Attribution.Model != "" {
		r.Model = resp.Attribution.Model
	}
}

type loopCaps struct {
	maxTurns, maxToolCalls, stepTokens, maxNudges int
	maxOutput, contextWindow                      int
}

func (r *Runner) caps() loopCaps {
	c := loopCaps{
		maxTurns: r.Caps.MaxTurns, maxToolCalls: r.Caps.MaxToolCalls, stepTokens: r.Caps.StepTokens, maxNudges: r.Caps.MaxContinueNudges,
		maxOutput: r.Limits.MaxOutputTokens, contextWindow: r.Limits.ContextWindow,
	}
	if c.maxTurns <= 0 {
		c.maxTurns = DefaultMaxTurns
	}
	if c.maxToolCalls <= 0 {
		c.maxToolCalls = DefaultMaxToolCalls
	}
	if c.maxNudges <= 0 {
		c.maxNudges = DefaultMaxContinueNudges
	}
	if c.maxOutput <= 0 {
		c.maxOutput = DefaultMaxOutputTokens
	}
	if c.contextWindow <= 0 {
		c.contextWindow = DefaultContextWindow
	}
	if c.stepTokens <= 0 {
		c.stepTokens = min(DefaultStepTokens, c.maxOutput/2)
	}
	return c
}

func rejectsForcedToolChoice(err error) bool {
	var ae *api.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusBadRequest || ae.Code == api.CodeProviderError || ae.Code == api.CodeProviderBadRequest
}

func contextTooLarge(err error) bool {
	var ae *api.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == api.CodeContextTooLarge || (ae.StatusCode == http.StatusRequestEntityTooLarge && ae.Code == "")
}

func sanitizeContent(parts []api.ContentPart, keepToolUses bool) []api.ContentPart {
	out := make([]api.ContentPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case api.PartText:
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
		case api.PartToolUse:
			if !keepToolUses {
				continue
			}
			if len(bytes.TrimSpace(p.Input)) == 0 {
				p.Input = json.RawMessage("{}")
			}
		default:
			continue
		}
		p.CacheControl = nil
		out = append(out, p)
	}
	return out
}

func assistantMessage(parts []api.ContentPart, empty string) api.Message {
	if len(parts) == 0 {
		parts = []api.ContentPart{{Type: api.PartText, Text: empty}}
	}
	return api.Message{Role: api.RoleAssistant, Content: parts}
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

func jsonBytes(v any) int {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(data)
}

func tokensOf(v any) int {
	return jsonBytes(v) / bytesPerToken
}

type conversation struct {
	r        *Runner
	caps     loopCaps
	messages []api.Message
	estimate int
}

func (c *conversation) appendMessage(m api.Message) {
	c.messages = append(c.messages, m)
	c.estimate += tokensOf(m)
}

func (c *conversation) appendUserText(text string) {
	last := len(c.messages) - 1
	if last >= 0 && c.messages[last].Role == api.RoleUser {
		part := api.ContentPart{Type: api.PartText, Text: text}
		c.messages[last].Content = append(c.messages[last].Content, part)
		c.estimate += tokensOf(part)
		return
	}
	c.appendMessage(api.UserText(text))
}

func (c *conversation) maxTokens() int {
	return min(c.caps.maxOutput, c.caps.contextWindow-c.estimate-outputReserve)
}

type toolCall struct {
	name  string
	input json.RawMessage
}

// elide replaces the oldest tool results, oldest first, until the estimate is below target; it returns the tokens saved.
func (c *conversation) elide(target int) int {
	calls := map[string]toolCall{}
	latestDraftView := ""
	for _, m := range c.messages {
		for _, p := range m.Content {
			switch p.Type {
			case api.PartToolUse:
				calls[p.ID] = toolCall{name: p.Name, input: p.Input}
			case api.PartToolResult:
				if calls[p.ToolUseID].name == toolDraftView {
					latestDraftView = p.ToolUseID
				}
			}
		}
	}
	saved := 0
	for i := range c.messages {
		for j := range c.messages[i].Content {
			if c.estimate < target {
				return saved
			}
			p := &c.messages[i].Content[j]
			if p.Type != api.PartToolResult || p.ToolUseID == latestDraftView || strings.HasPrefix(p.Content, "[elided: ") {
				continue
			}
			call := calls[p.ToolUseID]
			name := call.name
			if name == "" {
				name = "tool"
			}
			replacement := fmt.Sprintf("[elided: %s %s; call it again if needed]", name, compactLog(string(call.input), 120))
			gain := (len(p.Content) - len(replacement)) / bytesPerToken
			if gain <= 0 {
				continue
			}
			p.Content = replacement
			c.estimate -= gain
			saved += gain
		}
	}
	if saved > 0 {
		c.r.logf("agent: elided old tool results (~%d tokens)", saved)
	}
	return saved
}

// Run executes the loop until the terminal tool succeeds; a TaskError reports every other ending.
func (r *Runner) Run(ctx context.Context, kickoff string) (*Result, error) {
	caps := r.caps()
	defs := make([]api.Tool, 0, len(r.Tools))
	for _, t := range r.Tools {
		defs = append(defs, t.Def)
	}
	terminal := r.terminalTool()
	result := &Result{Model: r.Model}
	c := &conversation{r: r, caps: caps}
	c.appendMessage(api.UserText(kickoff))
	c.estimate += (len(r.System) + jsonBytes(defs)) / bytesPerToken

	defer func() {
		if result.Turns > 0 {
			r.logf("agent: tokens: %d in / %d out over %d turn(s)", result.InputTokens, result.OutputTokens, result.Turns)
		}
	}()

	fail := func(reason error, detail string) (*Result, error) {
		r.logf("agent: task failed: %v %s", reason, detail)
		return result, &TaskError{Reason: reason, Model: result.Model, Detail: detail}
	}

	forcedText := fmt.Sprintf("You are at the turn or tool-call limit. Call %s now with your result.", terminal)
	capForced := false
	forcingRejected := false
	retried413 := false
	truncated := 0
	nudges := 0
	for turn := 0; turn < caps.maxTurns; turn++ {
		forced := terminal != "" && (capForced || turn >= caps.maxTurns-forcedTurns)

		if c.estimate > caps.contextWindow*elideAbovePercent/100 {
			c.elide(caps.contextWindow * elideToPercent / 100)
		}
		if c.maxTokens() < caps.stepTokens {
			c.elide(min(caps.contextWindow*elideToPercent/100, caps.contextWindow-outputReserve-caps.stepTokens))
		}
		maxTokens := c.maxTokens()
		if maxTokens <= 0 {
			return fail(ErrContextLimit, fmt.Sprintf("the conversation (~%d tokens) leaves no room for a reply in a %d-token context window", c.estimate, caps.contextWindow))
		}

		toolChoice := &api.ToolChoice{Type: api.ToolChoiceAuto}
		if forced {
			r.logf("agent: requiring %s on this turn", terminal)
			if r.ForcedToolChoice && !forcingRejected {
				toolChoice = &api.ToolChoice{Type: api.ToolChoiceTool, Name: terminal}
			} else if !lastUserTextIs(c.messages, forcedText) {
				c.appendUserText(forcedText)
			}
		}
		req := api.MessagesRequest{
			Model:        r.Model,
			SystemBlocks: api.CachedSystem(r.System),
			Messages:     api.WithCacheBreakpoint(c.messages),
			Tools:        defs,
			ToolChoice:   toolChoice,
			MaxTokens:    maxTokens,
			Context:      r.Context,
		}
		resp, err := r.Client.Messages(ctx, req)
		if err != nil && toolChoice.Type == api.ToolChoiceTool && rejectsForcedToolChoice(err) {
			r.logf("agent: the model rejected a forced tool_choice; asking in a user message instead")
			forcingRejected = true
			c.appendUserText(forcedText)
			req.ToolChoice = &api.ToolChoice{Type: api.ToolChoiceAuto}
			req.Messages = api.WithCacheBreakpoint(c.messages)
			resp, err = r.Client.Messages(ctx, req)
		}
		if err != nil && contextTooLarge(err) {
			if retried413 {
				return fail(ErrContextLimit, err.Error())
			}
			retried413 = true
			target := caps.contextWindow * elideToPercent / 100
			if c.estimate <= target {
				target = c.estimate / 2
			}
			if c.elide(target) == 0 {
				return fail(ErrContextLimit, err.Error())
			}
			r.logf("agent: the context was too large; retrying with elided tool results")
			turn--
			continue
		}
		if err != nil {
			return result, fmt.Errorf("llm request (turn %d): %w", turn+1, err)
		}
		result.add(resp)
		result.FinalText = resp.TextContent()
		if text := strings.TrimSpace(resp.TextContent()); text != "" {
			r.logf("agent: says: %s", compactLog(text, 400))
		}

		prompt := resp.Usage.PromptTokens()
		switch resp.StopReason {
		case api.StopRefusal:
			return fail(ErrRefusal, strings.TrimSpace(resp.TextContent()))
		case api.StopMaxTokens:
			c.syncEstimate(prompt)
			c.appendMessage(assistantMessage(sanitizeContent(resp.Content, false), truncatedText))
			truncated++
			if truncated >= maxTruncatedStreak {
				return fail(ErrOutputLimit, fmt.Sprintf("%d consecutive replies hit the output limit of ~%d tokens", truncated, req.MaxTokens))
			}
			r.logf("agent: reply hit the output limit (%d in a row)", truncated)
			c.appendUserText(fmt.Sprintf("Your last reply hit the output limit (~%d tokens) and its tool calls were discarded. Work in smaller steps: at most ~%d tokens per tool call.", req.MaxTokens, caps.stepTokens))
			continue
		}
		truncated = 0

		content := sanitizeContent(resp.Content, true)
		c.syncEstimate(prompt)
		uses := resp.ToolUses()
		if len(uses) == 0 {
			c.appendMessage(assistantMessage(content, noOutputText))
			if terminal == "" {
				r.logf("agent: model ended turn after %d turn(s)", turn+1)
				return result, nil
			}
			nudges++
			if nudges > caps.maxNudges {
				return fail(ErrNoSubmit, fmt.Sprintf("the model ended its turn %d times without calling %s", nudges, terminal))
			}
			r.logf("agent: model ended its turn without %s; nudging (%d/%d)", terminal, nudges, caps.maxNudges)
			c.appendUserText(fmt.Sprintf("Continue. Call %s when the work is complete.", terminal))
			continue
		}
		c.appendMessage(assistantMessage(content, noOutputText))

		results, done, err := r.runTools(ctx, uses, terminal, forced, caps, result, &capForced)
		if err != nil {
			return fail(ErrForcedSubmitRejected, err.Error())
		}
		if done {
			return result, nil
		}
		c.appendMessage(api.ToolResult(results...))
	}

	return fail(ErrCapReached, fmt.Sprintf("turn cap of %d reached without %s", caps.maxTurns, terminal))
}

func (c *conversation) syncEstimate(promptTokens int) {
	if promptTokens > 0 {
		c.estimate = promptTokens
	}
}

func lastUserTextIs(messages []api.Message, text string) bool {
	if len(messages) == 0 {
		return false
	}
	m := messages[len(messages)-1]
	if m.Role != api.RoleUser || len(m.Content) == 0 {
		return false
	}
	p := m.Content[len(m.Content)-1]
	return p.Type == api.PartText && p.Text == text
}

func errorResult(id, text string) api.ContentPart {
	return api.ContentPart{Type: api.PartToolResult, ToolUseID: id, Content: "error: " + text, IsError: true}
}

// runTools executes the turn's tool calls in order with the terminal last; done reports a successful terminal call.
func (r *Runner) runTools(ctx context.Context, uses []api.ContentPart, terminal string, forced bool, caps loopCaps, result *Result, capForced *bool) ([]api.ContentPart, bool, error) {
	results := make([]api.ContentPart, len(uses))
	termIdx := -1
	failed := false
	for i, use := range uses {
		tool, ok := r.toolByName(use.Name)
		switch {
		case !ok:
			r.logf("agent: model requested unknown tool %q", use.Name)
			results[i] = errorResult(use.ID, fmt.Sprintf("unknown tool %q", use.Name))
			failed = true
			continue
		case tool.Terminal:
			if termIdx >= 0 {
				results[i] = errorResult(use.ID, fmt.Sprintf("%s may be called once per turn", use.Name))
				failed = true
				continue
			}
			termIdx = i
			continue
		case forced:
			results[i] = errorResult(use.ID, fmt.Sprintf("only %s can be called now", terminal))
			failed = true
			continue
		case result.ToolCalls >= caps.maxToolCalls:
			r.logf("agent: hit the tool-call cap (%d); requiring %s next", caps.maxToolCalls, terminal)
			results[i] = errorResult(use.ID, fmt.Sprintf("the tool-call cap of %d is reached; call %s with your result", caps.maxToolCalls, terminal))
			failed = true
			*capForced = terminal != ""
			continue
		}
		result.ToolCalls++
		r.logf("agent: tool %s (%d) %s", use.Name, result.ToolCalls, compactLog(string(use.Input), 160))
		out, err := tool.Run(ctx, use.Input)
		if err != nil {
			results[i] = errorResult(use.ID, err.Error())
			failed = true
			continue
		}
		if strings.TrimSpace(out) == "" {
			out = "(empty output)"
		}
		results[i] = api.ContentPart{Type: api.PartToolResult, ToolUseID: use.ID, Content: out}
	}
	if termIdx < 0 {
		return results, false, nil
	}
	use := uses[termIdx]
	if failed {
		results[termIdx] = errorResult(use.ID, fmt.Sprintf("%s was not run because an earlier tool call in this turn failed; fix it, then call %s again", use.Name, use.Name))
		return results, false, nil
	}
	tool, _ := r.toolByName(use.Name)
	out, err := runTerminal(ctx, tool, use.Input)
	if err != nil {
		if forced {
			return nil, false, fmt.Errorf("forced %s: %w", use.Name, err)
		}
		r.logf("agent: rejected %s input, asking the model to correct it: %v", use.Name, err)
		results[termIdx] = errorResult(use.ID, fmt.Sprintf("invalid %s input: %v. Correct the input and call %s again.", use.Name, err, use.Name))
		return results, false, nil
	}
	r.logf("agent: model called terminal tool %q", use.Name)
	result.TerminalTool = use.Name
	result.TerminalInput = append(json.RawMessage(nil), use.Input...)
	result.TerminalOutput = out
	result.Forced = forced
	if forced && listPhaseTerminals[use.Name] {
		result.Partial = true
		if use.Name == toolFinishUnits {
			result.TerminalInput = json.RawMessage(`{"complete":false}`)
		}
	}
	return results, true, nil
}

func runTerminal(ctx context.Context, tool *Tool, input json.RawMessage) (string, error) {
	if tool.Validate != nil {
		if err := tool.Validate(input); err != nil {
			return "", err
		}
	}
	if tool.Run == nil {
		return "", nil
	}
	return tool.Run(ctx, input)
}
