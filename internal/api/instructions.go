package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Contract identifiers this CLI speaks.
const (
	InstructionsSchema = "gravity.instructions/1"
	ToolContract       = "gravity.tools/1"
)

// Harness phases of an instructions bundle.
const (
	PhaseGuidesPlan     = "guides.plan"
	PhaseGuidesWrite    = "guides.write"
	PhaseReachWrite     = "reach.write"
	PhaseReferenceProse = "reference.prose"
	PhaseChangelogWrite = "changelog.write"
	PhaseNucleusDistill = "nucleus.distill"
	PhaseCheckReview    = "check.review"
	PhaseUnitsMap       = "units.map"
)

// Model quality labels.
const (
	QualityRecommended    = "recommended"
	QualitySupported      = "supported"
	QualityNotRecommended = "not_recommended"
	QualityUntested       = "untested"
)

// ErrServerTooOld means the server has no instructions bundle this CLI understands.
var ErrServerTooOld = errors.New("this Gravity server is too old for gravity 1.2 (no instructions bundle); upgrade the server or use gravity 1.1")

// ErrToolContract means the bundle's prompts target a tool contract major this CLI does not implement.
var ErrToolContract = errors.New("unsupported tool contract")

// ModelLimits are a model's real output and context limits.
type ModelLimits struct {
	MaxOutputTokens int  `json:"maxOutputTokens"`
	ContextWindow   int  `json:"contextWindow"`
	Known           bool `json:"known"`
}

// ModelFallback reports that the org default replaced an unusable model.
type ModelFallback struct {
	From   string `json:"from"`
	Reason string `json:"reason"`
}

// ModelFeatures are the provider features the harness may rely on.
type ModelFeatures struct {
	ForcedToolChoice bool `json:"forcedToolChoice"`
}

// BundleModel is the model resolved for a run pass.
type BundleModel struct {
	Provider    string         `json:"provider"`
	ID          string         `json:"id"`
	Label       string         `json:"label"`
	Requested   string         `json:"requested,omitempty"`
	Fallback    *ModelFallback `json:"fallback,omitempty"`
	Limits      ModelLimits    `json:"limits"`
	Features    ModelFeatures  `json:"features"`
	Quality     string         `json:"quality"`
	QualityNote string         `json:"qualityNote,omitempty"`
}

// Language is an output language.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// BundleAudience is a pass audience with its meaning.
type BundleAudience struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Builtin     bool   `json:"builtin"`
}

// HarnessLimits are the safety caps served by the platform.
type HarnessLimits struct {
	MaxTurns          int `json:"maxTurns"`
	MaxToolCalls      int `json:"maxToolCalls"`
	StepTokens        int `json:"stepTokens"`
	MaxContinueNudges int `json:"maxContinueNudges"`
}

// BlockContract lists the block types and variants the model may author.
type BlockContract struct {
	Types           []string `json:"types"`
	CalloutVariants []string `json:"calloutVariants"`
	ListVariants    []string `json:"listVariants"`
}

// BundleChecks configures the deterministic checks.
type BundleChecks struct {
	OutputMarkers []string `json:"outputMarkers"`
}

// RenderedPrompts are the full system prompts per phase.
type RenderedPrompts struct {
	System map[string]string `json:"system"`
}

// InstructionsBundle is the gravity.instructions/1 bundle of a run pass.
type InstructionsBundle struct {
	Schema       string            `json:"schema"`
	Version      string            `json:"version"`
	ToolContract string            `json:"toolContract"`
	Model        BundleModel       `json:"model"`
	Language     Language          `json:"language"`
	Audiences    []BundleAudience  `json:"audiences"`
	Harness      HarnessLimits     `json:"harness"`
	Blocks       BlockContract     `json:"blocks"`
	Checks       BundleChecks      `json:"checks"`
	Phases       map[string]string `json:"phases"`
	Rendered     RenderedPrompts   `json:"rendered"`
	Tools        map[string]string `json:"tools"`
	Warnings     []string          `json:"warnings,omitempty"`
}

// System returns the rendered system prompt of a phase.
func (b *InstructionsBundle) System(phase string) (string, error) {
	if b != nil {
		if text, ok := b.Rendered.System[phase]; ok && strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("the instructions bundle has no system prompt for phase %s", phase)
}

// ToolDescription returns the server-owned description of a tool.
func (b *InstructionsBundle) ToolDescription(name string) (string, bool) {
	if b == nil {
		return "", false
	}
	d, ok := b.Tools[name]
	return d, ok
}

// ShortVersion is the first eight hex digits of the bundle version.
func (b *InstructionsBundle) ShortVersion() string {
	if b == nil {
		return ""
	}
	v := strings.TrimPrefix(b.Version, "sha256:")
	if len(v) > 8 {
		return v[:8]
	}
	return v
}

// Validate checks the schema and tool contract majors.
func (b *InstructionsBundle) Validate() error {
	if !sameMajor(b.Schema, InstructionsSchema) {
		return fmt.Errorf("%w (bundle schema %q)", ErrServerTooOld, b.Schema)
	}
	if !sameMajor(b.ToolContract, ToolContract) {
		return fmt.Errorf("%w: the server's instructions target %q, this CLI implements %s; upgrade gravity", ErrToolContract, b.ToolContract, ToolContract)
	}
	return nil
}

func sameMajor(got, want string) bool {
	gn, gv, ok1 := strings.Cut(strings.TrimSpace(got), "/")
	wn, wv, ok2 := strings.Cut(want, "/")
	if !ok1 || !ok2 || gn != wn {
		return false
	}
	gm, _, _ := strings.Cut(gv, ".")
	wm, _, _ := strings.Cut(wv, ".")
	g, err1 := strconv.Atoi(gm)
	w, err2 := strconv.Atoi(wm)
	return err1 == nil && err2 == nil && g == w
}

// InstructionsQuery selects the bundle of a run pass.
type InstructionsQuery struct {
	RunPassID string
	Model     string
	MaxOutput int
}

func (q InstructionsQuery) values() url.Values {
	v := url.Values{"runPassId": {q.RunPassID}}
	if q.Model != "" {
		v.Set("model", q.Model)
	}
	if q.MaxOutput > 0 {
		v.Set("maxOutput", strconv.Itoa(q.MaxOutput))
	}
	return v
}

// InstructionsBundle calls GET /api/llm/v1/instructions and checks the bundle's schema and tool contract.
func (c *Client) InstructionsBundle(ctx context.Context, q InstructionsQuery) (*InstructionsBundle, error) {
	if q.RunPassID == "" {
		return nil, errors.New("instructions: a run pass id is required")
	}
	var out InstructionsBundle
	err := c.Get(ctx, "/api/llm/v1/instructions", q.values(), &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && routeMissing(apiErr) {
		return nil, ErrServerTooOld
	}
	if err != nil {
		return nil, err
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return &out, nil
}

func routeMissing(e *APIError) bool {
	switch e.StatusCode {
	case http.StatusNotFound:
		return e.Code == ""
	case http.StatusMethodNotAllowed:
		return true
	}
	return false
}
